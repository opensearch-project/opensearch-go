// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
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

// rewriteConfig bundles everything a rewrite needs. The delta, renames,
// regroups, removedHelpers, and importPrefixes are all composed for the resolved
// source->target chain (see compose.go); the engine itself is version-agnostic.
type rewriteConfig struct {
	dir            string       // consumer module directory
	patterns       []string     // go/packages patterns (default ./...)
	delta          apirev.Delta // composed field-level delta, keyed by qualified source type
	renames        []apirev.TypeRename
	regroups       []methodRegroup
	removedHelpers map[string]string
	importPrefixes [][2]string // source module prefix -> target module prefix
	write          bool
}

// rewriteResult reports per-file edits and any unclassified-field references
// (a field that vanished on the target with no FieldDisposition - a defect in
// the tool's field table, since it cannot know rename-vs-remove).
type rewriteResult struct {
	path         string
	edits        []string
	unclassified []string
}

// rewriteRules is the per-file bundle the AST walk consumes: the composed delta
// plus the derived rename index and the call-site/import rules. Passed by value
// down the walk so no engine function reaches for a package global.
type rewriteRules struct {
	delta          apirev.Delta
	renameByFrom   map[string]apirev.TypeRename
	regroups       []methodRegroup
	removedHelpers map[string]string
	importPrefixes [][2]string
}

// runTypeAwareRewrite loads the consumer packages against their current (source)
// deps, rewrites each file, and returns the results.
func runTypeAwareRewrite(cfg rewriteConfig) ([]rewriteResult, error) {
	loadCfg := &packages.Config{
		Dir: cfg.dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Tests: true,
	}
	pkgs, err := packages.Load(loadCfg, cfg.patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("consumer must compile against the source version before rewriting; load reported errors")
	}

	// Build quick lookups from the type map: qualified source type -> target
	// name, so a type reference (opensearchapi.DocumentGetReq) can be renamed to
	// its target spelling.
	renameByFrom := map[string]apirev.TypeRename{}
	for _, r := range cfg.renames {
		renameByFrom[r.FromPkgPath+"."+r.FromName] = r
	}
	rules := rewriteRules{
		delta:          cfg.delta,
		renameByFrom:   renameByFrom,
		regroups:       cfg.regroups,
		removedHelpers: cfg.removedHelpers,
		importPrefixes: cfg.importPrefixes,
	}

	seen := map[string]bool{} // dedupe files shared across test/non-test package variants
	var results []rewriteResult

	// pending holds files whose AST was mutated, to be flushed only after the
	// whole module is checked: an unclassified-field reference anywhere aborts the
	// run before any file is written, so a bug in the field table can never leave
	// the module half-rewritten.
	type pendingWrite struct {
		path string
		fset *token.FileSet
		file *ast.File
	}
	var pending []pendingWrite
	var unclassified []string

	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			// Resolve the file's path from the token position, not by indexing a
			// parallel slice - Syntax and CompiledGoFiles are not guaranteed to
			// be the same length across test/non-test package variants.
			path := pkg.Fset.Position(file.Pos()).Filename
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true

			r := rewriteFileTyped(pkg, file, rules)
			unclassified = append(unclassified, r.unclassified...)
			if len(r.edits) == 0 {
				continue
			}
			r.path = path
			results = append(results, r)
			pending = append(pending, pendingWrite{path: path, fset: pkg.Fset, file: file})
		}
	}

	// A referenced unclassified field is an osapifix bug - the field-disposition
	// table is incomplete. Fail loudly and write nothing rather than guess or
	// silently drop a caller's value.
	if len(unclassified) > 0 {
		return results, fmt.Errorf("osapifix bug: %d field(s) vanished on the target with no disposition "+
			"(cannot know rename vs remove); classify each in the hop's FieldDispositions table:\n  %s",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}

	if !cfg.write {
		return results, nil
	}

	// Sandbox all writes to the consumer module directory: os.Root refuses any
	// path that escapes root (via .., absolute paths, or symlinks), so a
	// misresolved file position can never overwrite something outside the module
	// being migrated.
	abs, err := filepath.Abs(cfg.dir)
	if err != nil {
		return results, fmt.Errorf("resolve module dir: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return results, fmt.Errorf("open module dir as sandbox root: %w", err)
	}
	defer root.Close()

	for _, w := range pending {
		if err := writeFormatted(root, cfg.dir, w.path, w.fset, w.file); err != nil {
			return results, err
		}
	}
	return results, nil
}

// rewriteFileTyped rewrites a single file's AST using resolved type information.
func rewriteFileTyped(pkg *packages.Package, file *ast.File, rules rewriteRules) rewriteResult {
	var res rewriteResult
	info := pkg.TypesInfo

	// isV2Hop gates the v2->v3 idiom-2 pass: the source module prefix being the v2
	// root module is the cheap, explicit hop marker (see plan.go importPrefixes).
	isV2Hop := len(rules.importPrefixes) > 0 && rules.importPrefixes[0][0] == v2root
	// Root import bookkeeping, captured before rewriteImports bumps the path: the
	// idiom-2 pass repoints *opensearch.Client (root) -> *opensearchapi.Client, so
	// a file whose ONLY use of the root package was that type ends up with the
	// bumped root import unreferenced. rootSpecName is the spec's literal name (""
	// when unnamed, needed to delete it); rootEffectiveName is the name references
	// actually use ("opensearch" when unnamed, needed to scan for surviving uses).
	var apiName, apiImportPath, rootSpecName, rootEffectiveName, rootBumpedPath string
	if isV2Hop {
		apiName = idiom2ImportNames(file)
		apiImportPath = rules.importPrefixes[0][1] + "/opensearchapi"
		rootBumpedPath = rules.importPrefixes[0][1]
		rootSpecName, rootEffectiveName = rootImportName(file, pkg)
	}
	var needImports []string // v3 import paths to inject after the walk

	astutil.Apply(file, func(c *astutil.Cursor) bool {
		if isV2Hop {
			if edits, imports, handled := rewriteIdiom2Node(c, info, apiName, apiImportPath); handled {
				res.edits = append(res.edits, edits...)
				needImports = append(needImports, imports...)
				// Stop descending: the rewritten call's stale selector (or the
				// synthetic subtree) must not be re-walked, or flagFieldAccess would
				// double-report the same op. Synthetic nodes also carry no
				// info.Selections, so the type gates are inert on them regardless.
				return false
			}
		}
		switch n := c.Node().(type) {
		case *ast.CallExpr:
			res.edits = append(res.edits, rewriteCall(c, n, info, rules)...)
		case *ast.CompositeLit:
			edits, unclassified := rewriteCompositeLit(n, info, rules.delta, rules.renameByFrom)
			res.edits = append(res.edits, edits...)
			res.unclassified = append(res.unclassified, unclassified...)
		case *ast.SelectorExpr:
			// Type reference rename: opensearchapi.DocumentGetReq -> .GetReq.
			if e := rewriteTypeRef(n, info, rules.renameByFrom); e != "" {
				res.edits = append(res.edits, e)
			}
			// Reference to a type removed outright on the target (idiom 1's
			// opensearchapi.*Request family): report as a manual worklist item.
			if e := flagRemovedTypeRef(n, info, rules.delta.RemovedTypes); e != "" {
				res.edits = append(res.edits, e)
			}
			// Field access into a collapsed/removed field: flag as MANUAL, or as an
			// unclassified-field bug if the field vanished with no disposition.
			if edit, unclassified := flagFieldAccess(n, info, rules.delta); unclassified != "" {
				res.unclassified = append(res.unclassified, unclassified)
			} else if edit != "" {
				res.edits = append(res.edits, edit)
			}
		}
		return true
	}, nil)

	// Import-path bump: source module paths -> target. Purely textual on the
	// import spec, but scoped to the known opensearch-go module paths so
	// unrelated imports are untouched. Done after the AST walk so type resolution
	// above still saw the source paths.
	res.edits = append(res.edits, rewriteImports(file, rules.importPrefixes)...)

	// Inject imports the idiom-2 pass introduced (v3 opensearchapi for the reshaped
	// Config/Req, fmt+net/http for the raw-response Status rewrite). Done after the
	// walk and rewriteImports, like the import bump, so type resolution saw the
	// original source.
	//
	// Guard on the resolved import PATH, not astutil's own dedup: astutil matches
	// on (name, path), so AddImport injecting an unnamed spec for a path the file
	// already imports under an alias (osapi ".../opensearchapi", which rewriteImports
	// just bumped to v3 in place) would NOT dedupe - it would add a second, unnamed,
	// unused import and the output would fail with "imported and not used". The
	// synthetic nodes reference the file's existing alias (apiName, resolved by
	// idiom2ImportNames from that same spec), so a path already imported under any
	// name needs no injection at all; only a genuinely new path is added.
	for _, p := range dedupe(needImports) {
		if fileImportsPath(file, p) {
			continue
		}
		astutil.AddImport(pkg.Fset, file, p)
	}

	// Prune the root import if the idiom-2 pass rendered it dead. When a file's
	// only use of the root package was the *opensearch.Client type, that reference
	// was repointed to *opensearchapi.Client, and rewriteImports still bumped the
	// root spec v2->v3 in place - leaving a bumped-but-unreferenced import that
	// fails to compile with "imported and not used". Delete it once no
	// <rootName>.X reference survives in the file. Uses the spec's literal name so
	// an aliased root import is matched exactly.
	if isV2Hop && rootEffectiveName != "" && !usesPkgIdent(file, rootEffectiveName) {
		if astutil.DeleteNamedImport(pkg.Fset, file, rootSpecName, rootBumpedPath) {
			res.edits = append(res.edits, fmt.Sprintf("drop now-unused root import %q (idiom2)", rootBumpedPath))
		}
	}
	return res
}

// rootImportName returns the file's import of the v2 root package as
// (specName, effectiveName): specName is the spec's literal local name ("" when
// unnamed, which DeleteNamedImport needs to match the spec), and effectiveName
// is the identifier references actually use - the spec name, or the package's
// real name (from pkg.Imports) when the spec is unnamed. Both are "" when the
// file does not import the root package. The pkg.Imports lookup keys on the
// pre-bump v2root path, so it must run before rewriteImports mutates the spec.
func rootImportName(file *ast.File, pkg *packages.Package) (string, string) {
	for _, imp := range file.Imports {
		if imp.Path == nil || strings.Trim(imp.Path.Value, `"`) != v2root {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, imp.Name.Name
		}
		if dep := pkg.Imports[v2root]; dep != nil {
			return "", dep.Name
		}
		return "", "" // unnamed and unresolved: cannot scan for uses, leave it
	}
	return "", ""
}

// fileImportsPath reports whether file already imports path, regardless of the
// spec's local name (alias or unnamed). This is the by-path presence check the
// idiom-2 import injection needs, since astutil's own dedup keys on (name, path)
// and so misses an aliased spec bumped to the same path.
func fileImportsPath(file *ast.File, path string) bool {
	for _, imp := range file.Imports {
		if imp.Path != nil && strings.Trim(imp.Path.Value, `"`) == path {
			return true
		}
	}
	return false
}

// rewriteIdiom2Node applies the v2->v3 idiom-2 transforms to a single node,
// gated entirely on resolved type info so it never fires on the synthetic
// (info-less) nodes it produces. It returns the edit-log lines, the v3 import
// paths the rewrite introduced, and whether it handled the node (the caller then
// stops descending). apiName is the file's local opensearchapi import alias;
// apiImportPath is the v3 opensearchapi package path to inject.
func rewriteIdiom2Node(
	c *astutil.Cursor, info *types.Info, apiName, apiImportPath string,
) ([]string, []string, bool) {
	switch n := c.Node().(type) {
	case *ast.CallExpr:
		sel, ok := n.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil, nil, false
		}
		// v2-root constructor opensearchv2.NewClient(cfg) -> opensearchapi.NewClient(cfg).
		// After the import bump the opensearchv2 alias points at the v3 ROOT package,
		// whose NewClient takes opensearch.Config; the reshaped cfg is an
		// opensearchapi.Config, so the constructor must move to opensearchapi too. Gated
		// on the package ident resolving to the exact v2 root path so no other NewClient
		// is touched.
		if pkgIdent, ok := sel.X.(*ast.Ident); ok && sel.Sel.Name == "NewClient" {
			if pn, ok := info.Uses[pkgIdent].(*types.PkgName); ok && pn.Imported().Path() == v2root {
				sel.X = ast.NewIdent(apiName)
				edits := []string{"repoint opensearchv2.NewClient -> opensearchapi.NewClient (idiom2)"}
				// Inline form NewClient(Config{...}): handling this node prunes the
				// walk, so the Config-literal arg is never reached by the CompositeLit
				// branch below - reshape it here. The split form (cfg := Config{}; ...;
				// NewClient(cfg)) needs nothing extra: the literal is a standalone
				// assignment the walk reshapes on its own.
				for i, arg := range n.Args {
					if lit, ok := arg.(*ast.CompositeLit); ok && isV2RootConfig(info.TypeOf(lit)) {
						n.Args[i] = reshapeConfigLiteral(lit, apiName)
						edits = append(edits, "reshape v2 Config{...} -> opensearchapi.Config{Client: ...} (idiom2)")
					}
				}
				return edits, []string{apiImportPath}, true
			}
		}
		// Raw-response method on a v2 opensearchapi.Response receiver (resp.Status()
		// etc). The source is still v2-typed during the walk, so the v2 Response
		// type is the trigger. Replaces the whole call node. This fires on ANY v2
		// opensearchapi.Response receiver, not only the seed ops, but that is safe:
		// both seed ops (Ping, Indices.Exists) are raw-response in v2 and v3, and a
		// non-seed op is never call-rewritten, so its file won't compile as v3
		// anyway (the response rewrite there is moot).
		if isV2Response(info.TypeOf(sel.X)) {
			if node, needIO, marker := rewriteIdiom2Response(sel); node != nil {
				c.Replace(node)
				var imports []string
				if needIO {
					imports = []string{"fmt", "net/http"}
				}
				return []string{fmt.Sprintf("rewrite resp.%s() -> fmt.Sprintf/http.StatusText (idiom2)", sel.Sel.Name)}, imports, true
			} else if marker != "" {
				c.Replace(markerExpr(marker))
				return []string{fmt.Sprintf("MANUAL resp.%s() - %s", sel.Sel.Name, marker)}, nil, true
			}
			return nil, nil, false
		}
		// Idiom-2 client call: client.<v2path>(opts...) -> client.<v3path>(ctx, Req).
		if chain, root := v2CallChain(sel, info); root != nil {
			// A subexpression carried verbatim into the v3 Req is unsafe to reuse if
			// it still contains a v2 root-client construct: the descent-stop below
			// would leave it un-migrated. Flag it so rewriteIdiom2Call plants a
			// marker rather than emitting non-compiling code with no sentinel.
			unsafeReuse := func(e ast.Expr) bool { return containsV2RootClientRef(e, info) }
			if newNode, edits := rewriteIdiom2Call(n, root, chain, apiName, unsafeReuse); newNode != nil {
				c.Replace(newNode)
				var imports []string
				if usesPkgIdent(newNode, apiName) {
					imports = []string{apiImportPath}
				}
				return edits, imports, true
			}
		}
		return nil, nil, false
	case *ast.CompositeLit:
		if isV2RootConfig(info.TypeOf(n)) {
			c.Replace(reshapeConfigLiteral(n, apiName))
			return []string{"reshape v2 Config{...} -> opensearchapi.Config{Client: ...} (idiom2)"}, []string{apiImportPath}, true
		}
		return nil, nil, false
	case *ast.SelectorExpr:
		// Type reference opensearchv2.Client in type position (the struct field
		// `client *opensearchv2.Client`) -> opensearchapi.Client. After the import
		// bump the opensearchv2 alias points at the v3 ROOT package, whose Client has
		// no API methods (.Ping/.Indices live on opensearchapi.Client), so the field's
		// package qualifier must move to opensearchapi. Gated on the resolved TypeName
		// being the exact v2 root Client so no other type ref is touched.
		if tn, ok := info.Uses[n.Sel].(*types.TypeName); ok &&
			tn.Pkg() != nil && tn.Name() == typeClient && tn.Pkg().Path() == v2root {
			n.X = ast.NewIdent(apiName)
			return []string{"repoint opensearchv2.Client -> opensearchapi.Client (idiom2)"}, []string{apiImportPath}, true
		}
		// Post-construction field access on a v2 Config value: cfg.Username ->
		// cfg.Client.Username, matching the reshaped Config wrapper.
		if isV2RootConfig(info.TypeOf(n.X)) {
			reshapeConfigFieldAssign(n)
			return []string{fmt.Sprintf("reshape cfg.%s -> cfg.Client.%s (idiom2)", n.Sel.Name, n.Sel.Name)}, nil, true
		}
		return nil, nil, false
	default:
		return nil, nil, false
	}
}

// usesPkgIdent reports whether node references a package-qualified identifier
// name (a selector whose base is the ident name), used to decide whether a
// rewritten call actually needs its opensearchapi import injected (the salvage
// marker path does not).
func usesPkgIdent(node ast.Node, name string) bool {
	if name == "" {
		return false
	}
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// idiom2ImportNames resolves the file's local import alias for the v3
// opensearchapi package, falling back to the package's default name when
// unaliased. Both v2 and v3 spellings of the opensearchapi path are accepted,
// since the walk runs on v2-typed source before the import bump.
func idiom2ImportNames(file *ast.File) string {
	apiName := "opensearchapi"
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		path := strings.Trim(imp.Path.Value, `"`)
		if isOpenSearchAPIPath(path) && imp.Name != nil {
			apiName = imp.Name.Name
		}
	}
	return apiName
}

// rewriteImports rewrites source opensearch-go import paths to their target
// prefix in-place. A prefix match covers every sub-package (opensearchapi,
// opensearchtransport, plugins/*, ...) without enumerating each.
func rewriteImports(file *ast.File, importPrefixes [][2]string) []string {
	var edits []string
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		val := strings.Trim(imp.Path.Value, `"`)
		for _, m := range importPrefixes {
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
// a collapsed raw Body, or whose type changed incompatibly) or "unclassified" (a
// vanished field with no disposition) on a source type, e.g. resp.Deleted or
// sr.Aggregations. It resolves the field through the SELECTION against two type
// keys - the declaring type (following embedding, e.g. a consumer's wrapper
// embedding *opensearchapi.SearchResp maps to the opensearchapi type) and the
// receiver type (the type the field is accessed through, which matches the
// root-client dispositions after gensurface flattens promoted fields) - so an
// access via either shape is caught. It reports (does not rewrite) - the
// conversion is semantic. It returns (manualEdit, unclassifiedMsg): at most one is
// non-empty.
func flagFieldAccess(sel *ast.SelectorExpr, info *types.Info, delta apirev.Delta) (string, string) {
	selection, ok := info.Selections[sel]
	if !ok {
		return "", ""
	}
	fieldVar, ok := selection.Obj().(*types.Var)
	if !ok || !fieldVar.IsField() || fieldVar.Pkg() == nil {
		return "", ""
	}
	// Resolve the struct the delta rules this field on. Two shapes must both work:
	//
	//   - declaringType follows embedding to the type that literally declares the
	//     field (e.g. a consumer's wrapper embedding *opensearchapi.SearchResp maps
	//     to the opensearchapi type).
	//   - the receiver type is the type the field is accessed THROUGH. gensurface
	//     flattens promoted fields onto the embedding struct, so a field declared on
	//     an embedded (and possibly removed) type like opensearchapi.API appears in
	//     the surface on the receiver v2.Client. The v2->v3 root-client dispositions
	//     are keyed on Client, so the receiver type is what matches there.
	//
	// Try the declaring type first (the established wrapper case), then the receiver
	// type. They never both carry a rule for the same field, so first-match is safe.
	for _, qual := range []string{declaringType(selection), qualifiedType(selection.Recv())} {
		if qual == "" {
			continue
		}
		if manual, unclassified, matched := flagFieldChange(delta, qual, sel.Sel.Name); matched {
			return manual, unclassified
		}
	}
	return "", ""
}

// flagFieldChange looks up field on the delta struct qual and reports it if it is
// a manual/unclassified change. The bool reports whether a change entry for the
// field was found (so the caller can stop trying alternative type keys); at most
// one of the two strings is non-empty.
func flagFieldChange(delta apirev.Delta, qual, field string) (string, string, bool) {
	sd, ok := delta.Structs[qual]
	if !ok {
		return "", "", false
	}
	for _, ch := range sd.Changes {
		if ch.From != field {
			continue
		}
		switch ch.Kind {
		case apirev.KindManual:
			return fmt.Sprintf("MANUAL %q: access .%s - %s", sd.From, ch.From, ch.Note), "", true
		case apirev.KindUnclassified:
			return "", fmt.Sprintf("%s#%s (read at a call site) - %s", sd.From, ch.From, ch.Note), true
		}
		return "", "", true // a non-reportable change kind still counts as matched
	}
	return "", "", false
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
// composite literal, keyed by the literal's resolved qualified type. It returns
// (edits, unclassified): a key naming a field that vanished with no disposition
// is reported as unclassified (a bug) rather than rewritten or dropped.
func rewriteCompositeLit(
	lit *ast.CompositeLit, info *types.Info, delta apirev.Delta, renameByFrom map[string]apirev.TypeRename,
) ([]string, []string) {
	qual := qualifiedType(info.TypeOf(lit))
	if qual == "" {
		return nil, nil
	}
	sd, hasDelta := delta.Structs[qual]

	// Index field changes by source field name (may be empty if no delta for this type).
	byField := map[string]apirev.FieldChange{}
	if hasDelta {
		for _, ch := range sd.Changes {
			byField[ch.From] = ch
		}
	}

	label := qual
	if hasDelta {
		label = sd.From
	}

	var edits, unclassified []string
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
		if e := renameEmbeddedKey(key, info, renameByFrom); e != "" {
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
		case apirev.KindRename:
			edits = append(edits, fmt.Sprintf("%s: field %s -> %s", label, ch.From, ch.To))
			key.Name = ch.To
			kept = append(kept, kv)
		case apirev.KindPointerWrap:
			if inner, ok := kv.Value.(*ast.CompositeLit); ok {
				kv.Value = &ast.UnaryExpr{Op: token.AND, X: inner}
				edits = append(edits, fmt.Sprintf("%s: field %s wrapped in & (now pointer)", label, ch.From))
			}
			kept = append(kept, kv)
		case apirev.KindRemove:
			// Safe only for a literal key: the field is a knob that no longer
			// exists (e.g. EnableMetrics). Dropping the key is correct.
			edits = append(edits, fmt.Sprintf("%s: field %s removed", label, ch.From))
			// drop it (don't append)
		case apirev.KindManual:
			// The field's data relocated (raw Body collapse); we must NOT drop or
			// rewrite it mechanically. Leave it in place and flag for a human.
			edits = append(edits, fmt.Sprintf("MANUAL %q: field %s - %s", label, ch.From, ch.Note))
			kept = append(kept, kv)
		case apirev.KindUnclassified:
			// The field vanished on the target and no disposition covers it. We do
			// NOT drop the key (that would silently lose the caller's value) - we
			// record a bug and leave the literal intact so the run aborts.
			unclassified = append(unclassified, fmt.Sprintf("%s#%s (set in a literal) - %s", label, ch.From, ch.Note))
			kept = append(kept, kv)
		default:
			kept = append(kept, kv)
		}
	}
	lit.Elts = kept
	return edits, unclassified
}

// renameEmbeddedKey handles a composite-literal key that names an embedded
// field whose type was renamed v4->v5. For an embedded field, the key IS the
// type's base name (e.g. `IndicesCountResp: apiResp` embedding
// *opensearchapi.IndicesCountResp); when that type becomes CountResp the key
// must follow. Resolved via the field object so only real embedded fields of a
// renamed type are touched. Returns "" if not applicable.
func renameEmbeddedKey(key *ast.Ident, info *types.Info, renameByFrom map[string]apirev.TypeRename) string {
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
	r, ok := renameByFrom[qk]
	if !ok {
		return ""
	}
	old := key.Name
	key.Name = r.ToName
	return fmt.Sprintf("embedded key %s -> %s", old, r.ToName)
}

// rewriteCall handles call-site rules: removed opensearchapi helpers and client
// method regrouping onto target sub-clients. It uses the cursor so it can
// replace the whole call node (e.g. ToPointer(x) -> &x).
func rewriteCall(c *astutil.Cursor, call *ast.CallExpr, info *types.Info, rules rewriteRules) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// (a) opensearchapi.<Helper>(...) removals.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		if pn, ok := info.Uses[pkgIdent].(*types.PkgName); ok && isOpenSearchAPIPath(pn.Imported().Path()) {
			switch rules.removedHelpers[sel.Sel.Name] {
			case "addressOf":
				// ToPointer(x) -> &x (target methods take *Req directly).
				if len(call.Args) == 1 {
					c.Replace(&ast.UnaryExpr{Op: token.AND, X: call.Args[0]})
					return []string{"opensearchapi.ToPointer(x) -> &x"}
				}
			case apirev.KindManual:
				return []string{fmt.Sprintf("MANUAL opensearchapi.%s removed - replace by hand (no mechanical target equivalent)", sel.Sel.Name)}
			}
		}
	}

	// (b) client method regrouping.
	if e := regroupMethod(call, sel, info, rules.regroups); e != "" {
		return []string{e}
	}
	return nil
}

// isOpenSearchAPIPath reports whether path is a v4 or v5 opensearchapi package.
func isOpenSearchAPIPath(path string) bool {
	return strings.HasSuffix(path, "/opensearchapi")
}

// regroupMethod rewrites a client method call whose sub-client path changed in
// the target (e.g. client.Indices.Count -> client.Count, client.Index ->
// client.Doc.Index). It matches the trailing selector chain against a
// methodRegroup.FromPath, rebuilds it as ToPath rooted at the same receiver, and
// wraps the sole request arg in & when the target method takes a pointer.
func regroupMethod(call *ast.CallExpr, sel *ast.SelectorExpr, info *types.Info, regroups []methodRegroup) string {
	chain, root := selectorChain(sel, info)
	if root == nil {
		return ""
	}

	for _, rg := range regroups {
		if !slices.Equal(chain, rg.FromPath) {
			continue
		}
		call.Fun = buildSelector(root, rg.ToPath)
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
			strings.Join(rg.FromPath, "."), strings.Join(rg.ToPath, "."))
	}
	return ""
}

// selectorChain returns the method/sub-client name chain that hangs off the
// opensearchapi client receiver, plus that receiver expression. It walks the
// selector spine outward and STOPS at the sub-expression whose type is the
// opensearchapi Client - so for `e.Client.Indices.Count` it returns
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
func rewriteTypeRef(sel *ast.SelectorExpr, info *types.Info, renameByFrom map[string]apirev.TypeRename) string {
	obj := info.Uses[sel.Sel]
	tn, ok := obj.(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return ""
	}
	key := tn.Pkg().Path() + "." + tn.Name()
	r, ok := renameByFrom[key]
	if !ok {
		return ""
	}
	old := sel.Sel.Name
	sel.Sel = ast.NewIdent(r.ToName)
	return fmt.Sprintf("type %s -> %s", old, r.ToName)
}

// flagRemovedTypeRef reports a reference to a source type that was removed
// outright on the target (delta.RemovedTypes) - e.g. the v2
// opensearchapi.*Request family deleted in v3's client redesign. Such a type has
// no mechanical target equivalent (the migration is a call/response shape change,
// not a rename), so the engine reports it as a MANUAL worklist item rather than
// rewriting it or leaving the consumer a bare "undefined" compile error. Matches
// on the resolved TypeName so only genuine references to the removed type are
// flagged. Report-only: it never mutates the AST. Returns "" if not applicable.
func flagRemovedTypeRef(sel *ast.SelectorExpr, info *types.Info, removed map[string]bool) string {
	if len(removed) == 0 {
		return ""
	}
	tn, ok := info.Uses[sel.Sel].(*types.TypeName)
	if !ok || tn.Pkg() == nil {
		return ""
	}
	key := tn.Pkg().Path() + "." + tn.Name()
	if !removed[key] {
		return ""
	}
	return fmt.Sprintf("MANUAL %q removed on the target - no mechanical equivalent; "+
		"migrate this reference by hand (see the hop's follow-ups)", key)
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

// writeFormatted prints the (mutated) AST back to disk, gofmt-formatted, through
// the sandbox root. path is the file's absolute path (from the token position);
// it is made relative to dir so the write goes through root, which rejects any
// target that escapes the module directory.
func writeFormatted(root *os.Root, dir, path string, fset *token.FileSet, file *ast.File) error {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absDir, path)
	if err != nil {
		return fmt.Errorf("relativize %q against module dir: %w", path, err)
	}
	return root.WriteFile(rel, buf.Bytes(), 0o644)
}
