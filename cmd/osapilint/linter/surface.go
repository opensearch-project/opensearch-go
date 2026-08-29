// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

// surface.go exports the embedded surface lookup for callers outside this
// module (e.g. drift guards in another repo) that cannot reach
// internal/apirev directly - Go's internal-package rule stops at this
// module's boundary. Field and Struct mirror apirev's shapes narrowed to
// what such a guard needs: a field's name and its type as apirev recorded
// it (types.Type.String()).

// Field is one exported struct field's name and type.
type Field struct {
	Name string
	Type string
}

// Struct is the field-level shape of one exported struct type.
type Struct struct {
	Fields []Field
}

// LookupStruct returns the field shape of the exported struct named pkg.name
// in the embedded surface for major version m. It reports false if m has no
// embedded surface or the struct isn't in it.
func LookupStruct(m Major, pkg, name string) (Struct, bool) {
	snap, err := decodeSurface(m)
	if err != nil {
		return Struct{}, false
	}

	for _, st := range snap.Structs {
		if st.PkgPath != pkg || st.Name != name {
			continue
		}
		fields := make([]Field, len(st.Fields))
		for i, f := range st.Fields {
			fields[i] = Field{Name: f.Name, Type: f.Type}
		}
		return Struct{Fields: fields}, true
	}
	return Struct{}, false
}
