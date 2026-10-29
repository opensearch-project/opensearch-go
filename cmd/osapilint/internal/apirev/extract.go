// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"cmp"
	"errors"
	"go/types"
	"reflect"
	"slices"
	"sort"

	"golang.org/x/tools/go/packages"
)

// errLoad is returned when go/packages reports load errors (printed to stderr).
var errLoad = errors.New("surface: package load reported errors")

// ExtractFromDir loads every package matched by the patterns from within the
// module rooted at dir and returns their combined exported-struct surface,
// tagging each struct with its package import path. Running with dir set to a
// specific version's module root is how the generator resolves v4 vs v5
// independently, since each is its own module.
//
// It uses full type information (go/types), so field types are the checker's
// canonical strings and pointer-ness is exact. withSymbols also records the
// package-level Symbols.
func ExtractFromDir(dir, version string, withSymbols bool, patterns ...string) (*Snapshot, error) {
	cfg := &packages.Config{
		Dir:  dir,
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, errLoad
	}

	out := &Snapshot{Version: version}
	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			st, isStruct := structOf(obj)
			if isStruct {
				out.Structs = append(out.Structs, extractStruct(pkg.PkgPath, name, st))
			}
			if withSymbols && (!isStruct || !hasExportedField(st)) {
				if sym, ok := extractSymbol(pkg.Types, obj); ok {
					out.Symbols = append(out.Symbols, sym)
				}
			}
		}
	}

	// Deterministic order (pkg then name) so the committed JSON is stable.
	slices.SortFunc(out.Structs, func(a, b Struct) int {
		return cmp.Or(cmp.Compare(a.PkgPath, b.PkgPath), cmp.Compare(a.Name, b.Name))
	})
	slices.SortFunc(out.Symbols, func(a, b Symbol) int {
		return cmp.Or(cmp.Compare(a.PkgPath, b.PkgPath), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// structOf returns the struct a named type declares.
func structOf(obj types.Object) (*types.Struct, bool) {
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, false
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil, false
	}
	st, ok := named.Underlying().(*types.Struct)
	return st, ok
}

// hasExportedField reports whether st has a field a caller can name. Such a
// struct is renamed through a TypeRename against Structs; one without, such as
// a union, is recorded as a Symbol too.
func hasExportedField(st *types.Struct) bool {
	for f := range st.Fields() {
		if f.Exported() {
			return true
		}
	}
	return false
}

// extractSymbol records a package-level type, constant or function, rendering
// types relative to pkg. Variables are not recorded: the generated packages
// export none that a migration renames.
func extractSymbol(pkg *types.Package, obj types.Object) (Symbol, bool) {
	qualify := types.RelativeTo(pkg)
	sym := Symbol{PkgPath: pkg.Path(), Name: obj.Name()}
	switch o := obj.(type) {
	case *types.TypeName:
		if o.IsAlias() {
			sym.Kind = SymbolAlias
			sym.Type = types.TypeString(types.Unalias(o.Type()), qualify)
			break
		}
		sym.Kind = SymbolType
		sym.Type = types.TypeString(o.Type().Underlying(), qualify)
		if named, ok := o.Type().(*types.Named); ok {
			for m := range named.Methods() {
				_, ptr := m.Type().(*types.Signature).Recv().Type().(*types.Pointer)
				sym.Methods = append(sym.Methods, Method{
					Name:            m.Name(),
					Signature:       types.TypeString(m.Type(), qualify),
					PointerReceiver: ptr,
				})
			}
			slices.SortFunc(sym.Methods, func(a, b Method) int { return cmp.Compare(a.Name, b.Name) })
		}
	case *types.Const:
		sym.Kind = SymbolConst
		sym.Type = types.TypeString(o.Type(), qualify)
		sym.Value = o.Val().ExactString()
	case *types.Func:
		sym.Kind = SymbolFunc
		sym.Type = types.TypeString(o.Type(), qualify)
	default:
		return Symbol{}, false
	}
	return sym, true
}

// extractStruct records every exported field of st, flattening embedded structs
// so a field that moved into an embedded base type (e.g. v5's
// CommonAggregationsAggregateBase) is still seen as present rather than mis-read
// as removed. Unexported fields are skipped: the rewriter only rewrites fields a
// caller can name in a literal.
func extractStruct(pkgPath, name string, st *types.Struct) Struct {
	s := Struct{PkgPath: pkgPath, Name: name}
	s.Fields = flattenFields(st, map[string]bool{})
	sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].Name < s.Fields[j].Name })
	return s
}

// flattenFields collects exported fields of st, promoting the exported fields of
// embedded struct types (transitively), including types embedded through a type
// alias. seen guards against cyclic embeddings.
func flattenFields(st *types.Struct, seen map[string]bool) []Field {
	var out []Field
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Embedded() {
			// Promote the embedded type's own exported fields.
			et := f.Type()
			if p, ok := et.(*types.Pointer); ok {
				et = p.Elem()
			}
			named, ok := types.Unalias(et).(*types.Named)
			if !ok {
				continue
			}
			if seen[named.String()] {
				continue
			}
			seen[named.String()] = true
			if inner, ok := named.Underlying().(*types.Struct); ok {
				out = append(out, flattenFields(inner, seen)...)
			}
			continue
		}
		if !f.Exported() {
			continue
		}
		out = append(out, Field{
			Name:    f.Name(),
			Type:    f.Type().String(),
			JSONTag: reflect.StructTag(st.Tag(i)).Get("json"),
		})
	}
	return out
}
