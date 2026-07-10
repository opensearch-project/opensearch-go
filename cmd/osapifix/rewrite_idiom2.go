// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

// rewrite_idiom2.go is the PURE (no type-info) call-half transform for the
// v2 -> v3 hop: it turns a recognized v2 functional-option call
// (client.Ping(client.Ping.WithContext(ctx))) into its v3 typed-Req form
// (client.Ping(ctx, &opensearchapi.PingReq{})). Where an option cannot be
// placed mechanically it plants a _OSAPIFIX_RESOLVE marker in the Req-arg
// position so the build breaks exactly at the call. Recognition and receiver
// resolution are the caller's job (a later, type-aware increment); this
// function is driven only by argDetailV2toV3 + callMapV2toV3 and the parsed
// call.

// markerExpr builds the compile-breaking sentinel for a call the transform
// cannot complete mechanically. The salvage text rides in the ident Name as an
// embedded block comment, which survives go/format without a CommentMap.
func markerExpr(salvage string) ast.Expr {
	return &ast.Ident{Name: "_OSAPIFIX_RESOLVE /* OSAPIFIX v2->v3 MANUAL: " + salvage + " */"}
}

// rewriteIdiom2Call rewrites a recognized v2 idiom-2 call into its v3 form.
// root is the receiver expression (the v2 client), chain the dotted v2 call
// path, apiPkg the local name of the v3 opensearchapi import. It returns the
// replacement call plus edit-log strings, or (nil, nil) if chain is not a
// recognized, non-removed op.
//
// apiPkg is the resolved local opensearchapi import alias; the type-aware
// caller (a later increment) passes the real name, only the seed tests pin
// "opensearchapi".
//
//nolint:unparam // apiPkg varies once the type-aware caller lands; see doc above
func rewriteIdiom2Call(call *ast.CallExpr, root ast.Expr, chain []string, apiPkg string) (ast.Expr, []string) {
	detail, ok := argDetailV2toV3[strings.Join(chain, ".")]
	if !ok {
		return nil, nil
	}
	var entry callMapEntry
	found := false
	for _, e := range callMapV2toV3 {
		if slices.Equal(e.V2Path, chain) {
			entry, found = e, true
			break
		}
	}
	if !found || entry.Removed {
		return nil, nil
	}

	// Split args syntactically: leading non-option args are positionals, the
	// trailing WithX(...) calls are options. In v2 idiom-2 positionals always
	// precede options.
	var positionals []ast.Expr
	var options []*ast.CallExpr
	for _, arg := range call.Args {
		if oc := optionCall(arg); oc != nil {
			options = append(options, oc)
			continue
		}
		positionals = append(positionals, arg)
	}

	var salvage []string
	var reqFields []ast.Expr // positional + destReqField KeyValueExprs
	var paramFields []ast.Expr

	// Positionals map by index to their v3 Req field.
	for i, arg := range positionals {
		if i >= len(detail.Positionals) {
			salvage = append(salvage, "unexpected positional arg count")
			break
		}
		reqFields = append(reqFields, &ast.KeyValueExpr{
			Key:   ast.NewIdent(detail.Positionals[i].ReqField),
			Value: arg,
		})
	}

	var ctxArg ast.Expr
	for _, oc := range options {
		name := oc.Fun.(*ast.SelectorExpr).Sel.Name
		dest, known := detail.Options[name]
		if !known {
			salvage = append(salvage, fmt.Sprintf("unknown option %s", name))
			continue
		}
		switch dest.Kind {
		case destContext:
			if len(oc.Args) == 1 {
				ctxArg = oc.Args[0]
			} else {
				salvage = append(salvage, fmt.Sprintf("option %s (expected one arg)", name))
			}
		case destParams:
			val, ok := optionValue(oc)
			if !ok {
				salvage = append(salvage, fmt.Sprintf("option %s (unexpected arg count)", name))
				continue
			}
			paramFields = append(paramFields, &ast.KeyValueExpr{Key: ast.NewIdent(dest.Field), Value: val})
		case destReqField:
			val, ok := optionValue(oc)
			if !ok {
				salvage = append(salvage, fmt.Sprintf("option %s (unexpected arg count)", name))
				continue
			}
			reqFields = append(reqFields, &ast.KeyValueExpr{Key: ast.NewIdent(dest.Field), Value: val})
		case destDropped:
			salvage = append(salvage, fmt.Sprintf("option %s (v3 field dropped)", name))
		case destMarker:
			salvage = append(salvage, fmt.Sprintf("option %s (semantic shape change; not mechanical)", name))
		}
	}

	if ctxArg == nil {
		salvage = append(salvage, "missing WithContext; v2 default was context.Background()")
	}

	edits := []string{fmt.Sprintf("rewrite client.%s -> client.%s (idiom2)",
		strings.Join(chain, "."), strings.Join(entry.V3Path, "."))}

	fun := buildSelector(root, entry.V3Path)

	if len(salvage) > 0 {
		if ctxArg == nil {
			ctxArg = markerExpr("missing WithContext; v2 default was context.Background()")
		}
		return &ast.CallExpr{
			Fun:  fun,
			Args: []ast.Expr{ctxArg, markerExpr(strings.Join(salvage, "; "))},
		}, edits
	}

	// Emit the collected params as a single nested Params field.
	if len(paramFields) > 0 {
		paramsType := strings.TrimSuffix(entry.V3Req, "Req") + "Params"
		reqFields = append(reqFields, &ast.KeyValueExpr{
			Key: ast.NewIdent("Params"),
			Value: &ast.CompositeLit{
				Type: &ast.SelectorExpr{X: ast.NewIdent(apiPkg), Sel: ast.NewIdent(paramsType)},
				Elts: paramFields,
			},
		})
	}

	var req ast.Expr = &ast.CompositeLit{
		Type: &ast.SelectorExpr{X: ast.NewIdent(apiPkg), Sel: ast.NewIdent(entry.V3Req)},
		Elts: reqFields,
	}
	if entry.ReqPtr {
		req = &ast.UnaryExpr{Op: token.AND, X: req}
	}

	return &ast.CallExpr{Fun: fun, Args: []ast.Expr{ctxArg, req}}, edits
}

// optionCall reports whether arg is a v2 functional-option call — an
// *ast.CallExpr whose Fun is a selector with a "With"-prefixed method — and
// returns it, else nil.
func optionCall(arg ast.Expr) *ast.CallExpr {
	oc, ok := arg.(*ast.CallExpr)
	if !ok {
		return nil
	}
	sel, ok := oc.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "With") {
		return nil
	}
	return oc
}

// rewriteIdiom2Response rewrites a resp.<Method> selector on a v3 raw
// *opensearch.Response. It returns the replacement node (or nil), whether
// fmt+net/http imports are needed, and a salvage marker string (empty = no
// marker needed). The caller builds any marker node; this function only
// returns the text.
//
// Dispositions:
//   - Status()         → fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
//   - Warnings()       → marker (removed in v3)
//   - HasWarnings()    → marker (removed in v3)
//   - String()         → marker (format changed, no faithful one-liner)
//   - everything else  → (nil, false, "") — left untouched
func rewriteIdiom2Response(sel *ast.SelectorExpr) (ast.Node, bool, string) {
	switch sel.Sel.Name {
	case "Status": //nolint:goconst // API method name; matching literals live in the suppressed callmap data table
		node := &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Sprintf")},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: `"%d %s"`},
				&ast.SelectorExpr{X: sel.X, Sel: ast.NewIdent("StatusCode")},
				&ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: ast.NewIdent("http"), Sel: ast.NewIdent("StatusText")},
					Args: []ast.Expr{&ast.SelectorExpr{X: sel.X, Sel: ast.NewIdent("StatusCode")}},
				},
			},
		}
		return node, true, ""
	case "Warnings":
		return nil, false, "Warnings() removed in v3"
	case "HasWarnings":
		return nil, false, "HasWarnings() removed in v3"
	case "String":
		return nil, false, "String() has no faithful one-liner v3 equivalent"
	default:
		return nil, false, ""
	}
}

// optionValue yields the v3 field value for a non-context option: a 0-arg
// option (WithPretty()) sets the field to true; a 1-arg option (WithLocal(v))
// sets it to that arg. More than one arg is not mechanical (ok=false).
func optionValue(oc *ast.CallExpr) (ast.Expr, bool) {
	switch len(oc.Args) {
	case 0:
		return ast.NewIdent("true"), true
	case 1:
		return oc.Args[0], true
	default:
		return nil, false
	}
}
