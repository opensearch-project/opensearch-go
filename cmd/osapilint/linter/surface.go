// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"sync"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

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

// surfaceDecoders decodes each embedded surface at most once, on first use.
//
//nolint:gochecknoglobals // immutable after init; caches the decode per major
var surfaceDecoders = func() map[major]func() (*apirev.Snapshot, error) {
	m := make(map[major]func() (*apirev.Snapshot, error), len(surfaces))
	for v := range surfaces {
		m[v] = sync.OnceValues(func() (*apirev.Snapshot, error) { return decodeSurface(v) })
	}
	return m
}()

// LookupStruct returns the fields of the exported struct named pkg.name in the
// embedded surface for major version m. It reports false if m has no embedded
// surface, the surface fails to decode, or the struct isn't in it.
func LookupStruct(m Major, pkg, name string) ([]SurfaceField, bool) {
	decode, ok := surfaceDecoders[m]
	if !ok {
		return nil, false
	}
	snap, err := decode()
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
