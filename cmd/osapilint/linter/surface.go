// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

// surface.go exports the embedded surface lookup for callers outside this
// module (e.g. a drift guard in another module) that cannot reach
// internal/apirev directly - Go's internal-package rule stops at this
// module's boundary. SurfaceField mirrors apirev's Field narrowed to what such
// a guard needs: a field's name and its type as apirev recorded it
// (types.Type.String()).

// SurfaceField is one exported struct field's name and type.
type SurfaceField struct {
	Name string
	Type string
}

// LookupStruct returns the fields of the exported struct named pkg.name in the
// embedded surface for major version m. It reports false if m has no embedded
// surface, the surface fails to decode, or the struct isn't in it.
func LookupStruct(m Major, pkg, name string) ([]SurfaceField, bool) {
	snap, err := decodeSurface(m)
	if err != nil {
		return nil, false
	}
	st, ok := snap.Lookup(pkg, name)
	if !ok {
		return nil, false
	}

	fields := make([]SurfaceField, len(st.Fields))
	for i, f := range st.Fields {
		fields[i] = SurfaceField{Name: f.Name, Type: f.Type}
	}
	return fields, true
}
