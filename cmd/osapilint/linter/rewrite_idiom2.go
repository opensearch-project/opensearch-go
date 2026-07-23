// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// rewrite_idiom2.go is the PURE (no type-info) call-half transform for the
// v2 -> v3 hop: it turns a recognized v2 functional-option call
// (client.Ping(client.Ping.WithContext(ctx))) into its v3 typed-Req form
// (client.Ping(ctx, &opensearchapi.PingReq{})). Where an option cannot be
// placed mechanically it plants a _OSAPILINT_RESOLVE marker in the Req-arg
// position so the build breaks exactly at the call. Recognition and receiver
// resolution are the caller's job (a later, type-aware increment); this
// function is driven only by argDetailV2toV3 + callMapV2toV3 and the parsed
// call.

// markerPrefix is the leading identifier of every compile-breaking sentinel the
// idiom-2 pass plants. The MANUAL edit is recorded when the marker is planted
// (in rewriteIdiom2Node); the marker's own role is to break the build so a human
// resolves the call the transform could not complete mechanically.
const markerPrefix = "_OSAPILINT_RESOLVE"

// markerHint is the hand-migration guidance carried in every marker comment,
// after the case-specific salvage text. It names the v3 target shape so a team
// hitting the sentinel can migrate the call by hand without cross-referencing
// the README: the root client's functional-option call collapses into a typed
// Req passed positionally, and the raw-response inspection moves to the returned
// error / typed *Resp.
const markerHint = "migrate by hand: call client.<Endpoint>(ctx, &opensearchapi.<Endpoint>Req{...}) " +
	"with the option values moved onto the Req/Params fields, and inspect the returned error " +
	"instead of the raw *Response; see the v2->v3 hop notes in cmd/osapilint/README.md"

// MarkerExpr builds the compile-breaking sentinel for a call the transform
// cannot complete mechanically. The salvage text (what could not be placed) and
// the hand-migration hint ride in the ident Name as an embedded block comment,
// which survives go/format without a CommentMap.
func MarkerExpr(salvage string) ast.Expr {
	return &ast.Ident{Name: markerPrefix + " /* OSAPILINT v2->v3 MANUAL: " + salvage + " -- " + markerHint + " */"}
}

// rewriteIdiom2Call rewrites a recognized v2 idiom-2 call into its v3 form.
// root is the receiver expression (the v2 client), chain the dotted v2 call
// path, apiPkg the local name of the v3 opensearchapi import. It returns the
// replacement call plus edit-log strings, or (nil, nil) if chain is not a
// recognized, non-removed op.
//
// apiPkg is the resolved local opensearchapi import alias, passed by the
// type-aware caller (idiom2ImportNames); the seed tests pin "opensearchapi".
//
// unsafeReuse reports whether a subexpression carried VERBATIM into the v3 Req
// (a positional, an option value, or the ctx arg) still needs a rewrite the
// pass cannot perform here - e.g. it embeds another v2 root-client call that the
// descent-stop would otherwise leave un-migrated. Such an expr routes through
// the salvage path so a marker is planted instead of emitting non-compiling
// code with no sentinel, upholding the rewrite-or-mark invariant. It is
// type-aware (the caller closes over *types.Info); nil means "never unsafe" for
// the pure unit tests.
func rewriteIdiom2Call(
	call *ast.CallExpr, root ast.Expr, chain []string, apiPkg string, unsafeReuse func(ast.Expr) bool,
) (ast.Expr, []string) {
	if unsafeReuse == nil {
		unsafeReuse = func(ast.Expr) bool { return false }
	}
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
		if unsafeReuse(arg) {
			salvage = append(salvage, fmt.Sprintf("positional %s carries an un-migrated v2 root-client reference", detail.Positionals[i].ReqField))
			continue
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
				if unsafeReuse(oc.Args[0]) {
					salvage = append(salvage, fmt.Sprintf("option %s carries an un-migrated v2 root-client reference", name))
					continue
				}
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
			if unsafeReuse(val) {
				salvage = append(salvage, fmt.Sprintf("option %s carries an un-migrated v2 root-client reference", name))
				continue
			}
			// A *bool v3 Params field (Local, FlatSettings, ...) cannot take a bare
			// bool; wrap in opensearchapi.ToPointer so the literal compiles.
			if dest.IsPtr {
				val = &ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: ast.NewIdent(apiPkg), Sel: ast.NewIdent("ToPointer")},
					Args: []ast.Expr{val},
				}
			}
			paramFields = append(paramFields, &ast.KeyValueExpr{Key: ast.NewIdent(dest.Field), Value: val})
		case destReqField:
			val, ok := optionValue(oc)
			if !ok {
				salvage = append(salvage, fmt.Sprintf("option %s (unexpected arg count)", name))
				continue
			}
			if unsafeReuse(val) {
				salvage = append(salvage, fmt.Sprintf("option %s carries an un-migrated v2 root-client reference", name))
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
			ctxArg = MarkerExpr("missing WithContext; v2 default was context.Background()")
		}
		return &ast.CallExpr{
			Fun:  fun,
			Args: []ast.Expr{ctxArg, MarkerExpr(strings.Join(salvage, "; "))},
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

// rewriteIdiom2Response rewrites a resp.<Method> call on a v2
// opensearchapi.Response receiver (the pre-bump type the walk sees; the
// import-path bump runs after). It returns the replacement node (or nil),
// whether fmt+net/http imports are needed, and a salvage marker string (empty
// = no marker needed). The caller builds any marker node; this function only
// returns the text.
//
// Dispositions:
//   - Status()         → fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
//   - String()         → marker (no faithful one-liner)
//   - Warnings()       → marker (removed in v3)
//   - HasWarnings()    → marker (removed in v3)
//   - Body, IsError, StatusCode, Header and any other selector → (nil, false, "") — left untouched
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

// reshapeConfigLiteral wraps a v2 root Config literal in the v3 opensearchapi
// Config struct. The inner literal is reused verbatim; after RewriteImports
// bumps the import path, the wrapped literal resolves to the v3 type and compiles.
func reshapeConfigLiteral(lit *ast.CompositeLit, apiName string) *ast.CompositeLit {
	return &ast.CompositeLit{
		Type: &ast.SelectorExpr{X: ast.NewIdent(apiName), Sel: ast.NewIdent("Config")},
		Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: ast.NewIdent("Client"), Value: lit},
		},
	}
}

// reshapeConfigFieldAssign rewrites sel in place from cfg.<Field> to
// cfg.Client.<Field>, inserting the Client hop for post-construction field
// accesses on a v2 Config value. The caller is responsible for gating on
// type-info (only v2 Config receivers).
func reshapeConfigFieldAssign(sel *ast.SelectorExpr) {
	sel.X = &ast.SelectorExpr{X: sel.X, Sel: ast.NewIdent("Client")}
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

// v2apiPath is the v2 opensearchapi package path. Its Response type is what the
// source still types raw responses as during the v2->v3 walk (the source has
// not yet been bumped to v3), so it is the trigger for rewriteIdiom2Response.
const v2apiPath = v2root + "/opensearchapi"

// Type-name spellings shared by the idiom-2 predicates and the synthetic nodes
// they build, named so the same identifier is not repeated as a bare literal.
const (
	typeClient   = "Client"
	typeConfig   = "Config"
	typeResponse = "Response"
)

// isV2RootClient reports whether t is the v2 ROOT opensearch.Client (the client
// whose API method fields were removed in v3), i.e. a named type "Client" in the
// v2 root module package - NOT the v2/opensearchapi sub-client. Mirrors
// isOpenSearchAPIClient but pins the exact root package path.
func isV2RootClient(t types.Type) bool {
	named := NamedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Name() == typeClient && named.Obj().Pkg().Path() == v2root
}

// isV2RootConfig reports whether t is the v2 root opensearch.Config composite
// type (named "Config" in the v2 root package). Used to recognize both the
// Config literal to wrap and the cfg receiver whose fields hop under .Client.
func isV2RootConfig(t types.Type) bool {
	named := NamedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Name() == typeConfig && named.Obj().Pkg().Path() == v2root
}

// isV2Response reports whether t is the v2 opensearchapi.Response (the raw
// response type read via .Status()/.Warnings() in v2). The source is still typed
// against v2 during the walk, so this is the trigger for rewriteIdiom2Response.
func isV2Response(t types.Type) bool {
	named := NamedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Name() == typeResponse && named.Obj().Pkg().Path() == v2apiPath
}

// containsV2RootClientRef reports whether expr contains any subexpression whose
// resolved type is the v2 root opensearch.Client - i.e. a reference the idiom-2
// call rewrite would carry verbatim into the v3 Req but the descent-stop would
// leave un-migrated. It inspects every sub-expression's resolved type (not just
// obvious call spines), so a client value reused as an arg, or nested inside
// another expression, is still caught. Used to decide whether to plant a marker
// instead of emitting a call that would not compile as v3.
func containsV2RootClientRef(expr ast.Expr, info *types.Info) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		if e, ok := n.(ast.Expr); ok && isV2RootClient(info.TypeOf(e)) {
			found = true
			return false
		}
		return true
	})
	return found
}

// v2CallChain returns the v2 call path hanging off a v2 ROOT client receiver,
// plus that receiver expression. It mirrors selectorChain but stops at the
// sub-expression whose type is the v2 root opensearch.Client - so for
// client.Indices.Exists it returns (["Indices","Exists"], <client>). Returns
// (nil, nil) if no v2 root client receiver is on the spine. Being type-gated on
// info, it never fires on a synthetic (info-less) node the walk produced.
func v2CallChain(sel *ast.SelectorExpr, info *types.Info) ([]string, ast.Expr) {
	var chain []string
	var cur ast.Expr = sel
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			return nil, nil
		}
		if isV2RootClient(info.TypeOf(s.X)) {
			chain = append(chain, s.Sel.Name)
			slices.Reverse(chain)
			return chain, s.X
		}
		chain = append(chain, s.Sel.Name)
		cur = s.X
	}
}
