package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"slices"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osupgrade-v4-to-v5/internal/surface"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// applyDelta.go is the type-aware rewriter. Unlike a name-based pass, it loads
// the consumer module WITH v4 type information (go/packages + go/types), so it
// can resolve every composite-literal type and field-access receiver to its
// exact qualified type and apply only that type's delta. This is what prevents
// a bare field name (Indices, EnableMetrics) from being rewritten on the wrong
// struct.
//
// Precondition: the consumer must still compile against v4 (that is its current
// state before the bump). The rewriter produces v5-shaped source; the operator
// then bumps the dependency and builds.

// rewriteConfig bundles everything a rewrite needs.
type rewriteConfig struct {
	dir      string           // consumer module directory
	patterns []string         // go/packages patterns (default ./...)
	delta    surface.Delta    // v4->v5 field-level delta, keyed by qualified v4 type
	renames  []surface.TypeRename
	write    bool
}

// rewriteResult reports per-file edits.
type rewriteResult struct {
	path  string
	edits []string
}

// runTypeAwareRewrite loads the consumer packages against their current (v4)
// deps, rewrites each file, and returns the results.
func runTypeAwareRewrite(cfg rewriteConfig) ([]rewriteResult, error) {
	loadCfg := &packages.Config{
		Dir:  cfg.dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Tests: true,
	}
	pkgs, err := packages.Load(loadCfg, cfg.patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("consumer must compile against v4 before rewriting; load reported errors")
	}

	// Build quick lookups from the type map: qualified v4 type -> v5 name, so a
	// type reference (opensearchapi.DocumentGetReq) can be renamed to its v5
	// spelling.
	renameByV4 := map[string]surface.TypeRename{}
	for _, r := range cfg.renames {
		renameByV4[r.V4PkgPath+"."+r.V4Name] = r
	}

	seen := map[string]bool{} // dedupe files shared across test/non-test package variants
	var results []rewriteResult

	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			// Resolve the file's path from the token position, not by indexing a
			// parallel slice — Syntax and CompiledGoFiles are not guaranteed to
			// be the same length across test/non-test package variants.
			path := pkg.Fset.Position(file.Pos()).Filename
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true

			r := rewriteFileTyped(pkg, file, cfg.delta, renameByV4)
			if len(r.edits) == 0 {
				continue
			}
			r.path = path
			results = append(results, r)

			if cfg.write {
				if err := writeFormatted(path, pkg.Fset, file); err != nil {
					return results, err
				}
			}
		}
	}
	return results, nil
}

// rewriteFileTyped rewrites a single file's AST using resolved type information.
func rewriteFileTyped(pkg *packages.Package, file *ast.File, delta surface.Delta, renameByV4 map[string]surface.TypeRename) rewriteResult {
	var res rewriteResult
	info := pkg.TypesInfo

	astutil.Apply(file, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.CallExpr:
			res.edits = append(res.edits, rewriteCall(c, n, info)...)
		case *ast.CompositeLit:
			res.edits = append(res.edits, rewriteCompositeLit(n, info, delta, renameByV4)...)
		case *ast.SelectorExpr:
			// Type reference rename: opensearchapi.DocumentGetReq -> .GetReq.
			if e := rewriteTypeRef(n, info, renameByV4); e != "" {
				res.edits = append(res.edits, e)
			}
			// Field access into a collapsed/removed field: flag as MANUAL.
			if e := flagFieldAccess(n, info, delta); e != "" {
				res.edits = append(res.edits, e)
			}
		}
		return true
	}, nil)

	// Import-path bump: v4 module paths -> v5. Purely textual on the import
	// spec, but scoped to the known opensearch-go module paths so unrelated
	// imports are untouched. Done after the AST walk so type resolution above
	// still saw the v4 paths.
	res.edits = append(res.edits, rewriteImports(file)...)
	return res
}

// v4ToV5ImportPrefixes maps v4 module import prefixes to their v5 equivalents.
// A prefix match covers every sub-package (opensearchapi, opensearchtransport,
// plugins/*, …) without enumerating each.
var v4ToV5ImportPrefixes = [][2]string{
	{"github.com/opensearch-project/opensearch-go/v4", "github.com/opensearch-project/opensearch-go/v5"},
}

// rewriteImports rewrites v4 opensearch-go import paths to v5 in-place.
func rewriteImports(file *ast.File) []string {
	var edits []string
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		val := strings.Trim(imp.Path.Value, `"`)
		for _, m := range v4ToV5ImportPrefixes {
			if val == m[0] || strings.HasPrefix(val, m[0]+"/") {
				newVal := m[1] + strings.TrimPrefix(val, m[0])
				imp.Path.Value = `"` + newVal + `"`
				edits = append(edits, fmt.Sprintf("import %s -> %s", val, newVal))
				break
			}
		}
	}
	return edits
}

// flagFieldAccess detects a read of a field that became "manual" (relocated into
// a collapsed raw Body, or whose type changed incompatibly) on a v4 type, e.g.
// resp.Deleted or sr.Aggregations. It resolves the field through the SELECTION,
// so an access via an embedded field (osv4's own SearchResp wrapper embedding
// *opensearchapi.SearchResp) still maps to the declaring opensearchapi type. It
// reports (does not rewrite) — the conversion is semantic.
func flagFieldAccess(sel *ast.SelectorExpr, info *types.Info, delta surface.Delta) string {
	selection, ok := info.Selections[sel]
	if !ok {
		return ""
	}
	fieldVar, ok := selection.Obj().(*types.Var)
	if !ok || !fieldVar.IsField() || fieldVar.Pkg() == nil {
		return ""
	}
	// The declaring struct is the field object's "container": recover it by
	// walking the selection's receiver to the type that actually declares the
	// field. types.Selection.Recv is the receiver expression's type, which for an
	// embedded access is the outer type; instead key on where the field is
	// declared, available via fieldVar's position resolved against the delta by
	// its declaring named type. We approximate that by scanning the delta for a
	// struct whose qualified name matches the field's declaring package + the
	// receiver chain — simplest robust route: match on field name + package.
	qual := declaringType(selection)
	if qual == "" {
		return ""
	}
	sd, ok := delta.Structs[qual]
	if !ok {
		return ""
	}
	for _, ch := range sd.Changes {
		if ch.From == sel.Sel.Name && ch.Kind == "manual" {
			return fmt.Sprintf("MANUAL %s: access .%s — %s", sd.V4, ch.From, ch.Note)
		}
	}
	return ""
}

// declaringType returns the qualified type ("<pkgPath>.<Name>") that actually
// declares the selected field, following embedding. It uses the selection's
// index path: Recv() is the outermost receiver type, and the last index step
// lands in the struct that declares the field.
func declaringType(sel *types.Selection) string {
	t := sel.Recv()
	idx := sel.Index() // path of field indices; last is the field itself
	for _, i := range idx[:len(idx)-1] {
		// descend through embedded fields
		st := underlyingStruct(t)
		if st == nil {
			return ""
		}
		t = st.Field(i).Type()
	}
	st := underlyingStruct(t)
	if st == nil {
		return ""
	}
	// t is now the struct that declares the field; recover its named type name.
	named := namedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
}

func underlyingStruct(t types.Type) *types.Struct {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if st, ok := t.Underlying().(*types.Struct); ok {
		return st
	}
	return nil
}

func namedOf(t types.Type) *types.Named {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n
	}
	return nil
}

// rewriteCompositeLit applies field renames, pointer-wraps, and removals to a
// composite literal, keyed by the literal's resolved qualified type.
func rewriteCompositeLit(lit *ast.CompositeLit, info *types.Info, delta surface.Delta, renameByV4 map[string]surface.TypeRename) []string {
	qual := qualifiedType(info.TypeOf(lit))
	if qual == "" {
		return nil
	}
	sd, hasDelta := delta.Structs[qual]

	// Index field changes by v4 field name (may be empty if no delta for this type).
	byField := map[string]surface.FieldChange{}
	if hasDelta {
		for _, ch := range sd.Changes {
			byField[ch.From] = ch
		}
	}

	label := qual
	if hasDelta {
		label = sd.V4
	}

	var edits []string
	kept := lit.Elts[:0]
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			kept = append(kept, elt)
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			kept = append(kept, elt)
			continue
		}

		// Embedded-field key rename: if the key names an embedded field whose
		// type was renamed v4->v5 (e.g. `IndicesCountResp: x` embedding
		// *opensearchapi.IndicesCountResp, now CountResp), rename the key to the
		// v5 type's base name. Resolved via the field object, so only genuine
		// embedded fields of a renamed type are touched.
		if e := renameEmbeddedKey(key, kv, info, renameByV4); e != "" {
			edits = append(edits, e)
			kept = append(kept, kv)
			continue
		}

		ch, has := byField[key.Name]
		if !has {
			kept = append(kept, elt)
			continue
		}
		switch ch.Kind {
		case "rename":
			edits = append(edits, fmt.Sprintf("%s: field %s -> %s", label, ch.From, ch.To))
			key.Name = ch.To
			kept = append(kept, kv)
		case "pointerWrap":
			if inner, ok := kv.Value.(*ast.CompositeLit); ok {
				kv.Value = &ast.UnaryExpr{Op: token.AND, X: inner}
				edits = append(edits, fmt.Sprintf("%s: field %s wrapped in & (now pointer)", label, ch.From))
			}
			kept = append(kept, kv)
		case "remove":
			// Safe only for a literal key: the field is a knob that no longer
			// exists (e.g. EnableMetrics). Dropping the key is correct.
			edits = append(edits, fmt.Sprintf("%s: field %s removed", label, ch.From))
			// drop it (don't append)
		case "manual":
			// The field's data relocated (raw Body collapse); we must NOT drop or
			// rewrite it mechanically. Leave it in place and flag for a human.
			edits = append(edits, fmt.Sprintf("MANUAL %s: field %s — %s", label, ch.From, ch.Note))
			kept = append(kept, kv)
		default:
			kept = append(kept, kv)
		}
	}
	lit.Elts = kept
	return edits
}

// renameEmbeddedKey handles a composite-literal key that names an embedded
// field whose type was renamed v4->v5. For an embedded field, the key IS the
// type's base name (e.g. `IndicesCountResp: apiResp` embedding
// *opensearchapi.IndicesCountResp); when that type becomes CountResp the key
// must follow. Resolved via the field object so only real embedded fields of a
// renamed type are touched. Returns "" if not applicable.
func renameEmbeddedKey(key *ast.Ident, kv *ast.KeyValueExpr, info *types.Info, renameByV4 map[string]surface.TypeRename) string {
	obj := info.Uses[key]
	v, ok := obj.(*types.Var)
	if !ok || !v.Embedded() {
		return ""
	}
	// The embedded field's type gives the qualified v4 type key.
	ft := v.Type()
	if p, ok := ft.(*types.Pointer); ok {
		ft = p.Elem()
	}
	named, ok := ft.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	qk := named.Obj().Pkg().Path() + "." + named.Obj().Name()
	r, ok := renameByV4[qk]
	if !ok {
		return ""
	}
	old := key.Name
	key.Name = r.V5Name
	return fmt.Sprintf("embedded key %s -> %s", old, r.V5Name)
}

// rewriteCall handles call-site rules: removed opensearchapi helpers and client
// method regrouping onto v5 sub-clients. It uses the cursor so it can replace
// the whole call node (e.g. ToPointer(x) -> &x).
func rewriteCall(c *astutil.Cursor, call *ast.CallExpr, info *types.Info) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// (a) opensearchapi.<Helper>(...) removals.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		if pn, ok := info.Uses[pkgIdent].(*types.PkgName); ok && isOpenSearchAPIPath(pn.Imported().Path()) {
			switch removedHelpers[sel.Sel.Name] {
			case "addressOf":
				// ToPointer(x) -> &x (v5 methods take *Req directly).
				if len(call.Args) == 1 {
					c.Replace(&ast.UnaryExpr{Op: token.AND, X: call.Args[0]})
					return []string{"opensearchapi.ToPointer(x) -> &x"}
				}
			case "manual":
				return []string{fmt.Sprintf("MANUAL opensearchapi.%s removed — replace by hand (no mechanical v5 equivalent)", sel.Sel.Name)}
			}
		}
	}

	// (b) client method regrouping.
	if e := regroupMethod(call, sel, info); e != "" {
		return []string{e}
	}
	return nil
}

// isOpenSearchAPIPath reports whether path is a v4 or v5 opensearchapi package.
func isOpenSearchAPIPath(path string) bool {
	return strings.HasSuffix(path, "/opensearchapi")
}

// regroupMethod rewrites a client method call whose sub-client path changed in
// v5 (e.g. client.Indices.Count -> client.Count, client.Index -> client.Doc.Index).
// It matches the trailing selector chain against a methodRegroup.V4Path, rebuilds
// it as V5Path rooted at the same receiver, and wraps the sole request arg in &
// when the v5 method takes a pointer.
func regroupMethod(call *ast.CallExpr, sel *ast.SelectorExpr, info *types.Info) string {
	chain, root := selectorChain(sel, info)
	if root == nil {
		return ""
	}

	for _, rg := range methodRegroups {
		if !slices.Equal(chain, rg.V4Path) {
			continue
		}
		call.Fun = buildSelector(root, rg.V5Path)
		if rg.PtrArg && len(call.Args) >= 1 {
			last := len(call.Args) - 1
			// Only wrap if the argument is not ALREADY a pointer. This covers two
			// cases without double-wrapping: an argument that was
			// opensearchapi.ToPointer(x) (already rewritten, or typed *Req), and a
			// plain value Req. Checking the resolved type is robust to traversal
			// order (the inner ToPointer call may be rewritten before or after
			// this outer call).
			if _, isPtr := info.TypeOf(call.Args[last]).(*types.Pointer); !isPtr {
				call.Args[last] = &ast.UnaryExpr{Op: token.AND, X: call.Args[last]}
			}
		}
		return fmt.Sprintf("call client.%s(...) -> client.%s(...)",
			strings.Join(rg.V4Path, "."), strings.Join(rg.V5Path, "."))
	}
	return ""
}

// selectorChain returns the method/sub-client name chain that hangs off the
// opensearchapi client receiver, plus that receiver expression. It walks the
// selector spine outward and STOPS at the sub-expression whose type is the
// opensearchapi Client — so for `e.Client.Indices.Count` it returns
// (["Indices","Count"], <e.Client>), not (["Client","Indices","Count"], <e>).
// Returns (nil, nil) if no opensearchapi client receiver is found on the spine.
func selectorChain(sel *ast.SelectorExpr, info *types.Info) ([]string, ast.Expr) {
	var chain []string
	var cur ast.Expr = sel
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			return nil, nil // reached a non-selector without finding the client
		}
		// If the receiver of this selector is the client, the chain is complete.
		if isOpenSearchAPIClient(info.TypeOf(s.X)) {
			chain = append(chain, s.Sel.Name)
			slices.Reverse(chain)
			return chain, s.X
		}
		chain = append(chain, s.Sel.Name)
		cur = s.X
	}
}

// buildSelector constructs root.path[0].path[1]... as nested SelectorExprs.
func buildSelector(root ast.Expr, path []string) ast.Expr {
	e := root
	for _, p := range path {
		e = &ast.SelectorExpr{X: e, Sel: ast.NewIdent(p)}
	}
	return e
}

// isOpenSearchAPIClient reports whether t is *opensearchapi.Client.
func isOpenSearchAPIClient(t types.Type) bool {
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Name() == "Client" && isOpenSearchAPIPath(named.Obj().Pkg().Path())
}

// rewriteTypeRef renames a qualified type reference whose v4 type was renamed in
// v5 (opensearchapi.DocumentGetReq -> opensearchapi.GetReq). It matches on the
// resolved object so only the intended type is touched.
func rewriteTypeRef(sel *ast.SelectorExpr, info *types.Info, renameByV4 map[string]surface.TypeRename) string {
	obj := info.Uses[sel.Sel]
	tn, ok := obj.(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return ""
	}
	key := tn.Pkg().Path() + "." + tn.Name()
	r, ok := renameByV4[key]
	if !ok {
		return ""
	}
	old := sel.Sel.Name
	sel.Sel = ast.NewIdent(r.V5Name)
	return fmt.Sprintf("type %s -> %s", old, r.V5Name)
}

// qualifiedType returns "<pkgPath>.<Name>" for a named struct type (dereferencing
// a pointer), or "" if t is not a named type from a package.
func qualifiedType(t types.Type) string {
	if t == nil {
		return ""
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
}

// writeFormatted prints the (mutated) AST back to disk, gofmt-formatted.
func writeFormatted(path string, fset *token.FileSet, file *ast.File) error {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
