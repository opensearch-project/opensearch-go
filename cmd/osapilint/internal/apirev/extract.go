// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"errors"
	"go/types"
	"reflect"
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
// canonical strings and pointer-ness is exact.
func ExtractFromDir(dir, version string, patterns ...string) (*Snapshot, error) {
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
			tn, ok := obj.(*types.TypeName)
			if !ok || !obj.Exported() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			st, ok := named.Underlying().(*types.Struct)
			if !ok {
				continue
			}
			out.Structs = append(out.Structs, extractStruct(pkg.PkgPath, name, st))
		}
	}

	// Deterministic order (pkg then name) so the committed JSON is stable.
	sort.Slice(out.Structs, func(i, j int) bool {
		if out.Structs[i].PkgPath != out.Structs[j].PkgPath {
			return out.Structs[i].PkgPath < out.Structs[j].PkgPath
		}
		return out.Structs[i].Name < out.Structs[j].Name
	})
	return out, nil
}

// extractStruct records every exported field of st, flattening embedded structs
// so a field that moved into an embedded base type (e.g. v5's GetResultBase) is
// still seen as present rather than mis-read as removed. Unexported fields are
// skipped: the rewriter only rewrites fields a caller can name in a literal.
func extractStruct(pkgPath, name string, st *types.Struct) Struct {
	s := Struct{PkgPath: pkgPath, Name: name}
	s.Fields = flattenFields(st, map[string]bool{})
	sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].Name < s.Fields[j].Name })
	return s
}

// flattenFields collects exported fields of st, promoting the exported fields of
// embedded struct types (transitively). seen guards against cyclic embeddings.
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
			named, ok := et.(*types.Named)
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
		_, isPtr := f.Type().(*types.Pointer)
		out = append(out, Field{
			Name:      f.Name(),
			Type:      f.Type().String(),
			IsPointer: isPtr,
			JSONTag:   reflect.StructTag(st.Tag(i)).Get("json"),
		})
	}
	return out
}
