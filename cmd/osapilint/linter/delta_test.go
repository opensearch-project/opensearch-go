// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/internal/apirev"
)

// TestHopRenamesAgainstSurfaces is the generic drift guard: for EVERY registered
// hop, each hand-authored type rename must be real against that hop's own
// surfaces - the source name present in the source surface, the target name
// present in the target surface, and the source name NOT surviving in the target
// (else it isn't a rename). This is version-agnostic: adding a v3->v4 hop is
// automatically covered. It is the guard that keeps a hop's TypeRenames from
// drifting from the actual package types (the failure mode that produced a
// spurious Indices->Index rule).
func TestHopRenamesAgainstSurfaces(t *testing.T) {
	has := func(s *apirev.Snapshot, pkg, name string) bool {
		for _, st := range s.Structs {
			if st.PkgPath == pkg && st.Name == name {
				return true
			}
		}
		return false
	}

	for from, h := range hops {
		require.Equal(t, from, h.From, "hop registered under key v%d but has From v%d", from, h.From)

		fromSnap, err := decodeSurface(h.From)
		require.NoErrorf(t, err, "hop v%d->v%d source surface", h.From, h.To)
		toSnap, err := decodeSurface(h.To)
		require.NoErrorf(t, err, "hop v%d->v%d target surface", h.From, h.To)

		for _, r := range h.TypeRenames {
			require.Truef(t, has(fromSnap, r.FromPkgPath, r.FromName),
				"hop v%d->v%d: source type %s.%s not found in v%d surface (stale entry?)",
				h.From, h.To, r.FromPkgPath, r.FromName, h.From)
			require.Truef(t, has(toSnap, r.ToPkgPath, r.ToName),
				"hop v%d->v%d: target %s.%s not found in v%d surface (wrong rename?)",
				h.From, h.To, r.ToPkgPath, r.ToName, h.To)
			require.Falsef(t, has(toSnap, r.ToPkgPath, r.FromName),
				"hop v%d->v%d: %s still exists in v%d under its source name; not a rename",
				h.From, h.To, r.FromName, h.To)
		}
	}
}

func assertChange(t *testing.T, changes []apirev.FieldChange, want apirev.FieldChange) {
	t.Helper()
	require.Containsf(t, changes, want, "missing expected change %+v", want)
}

func assertChangeKind(t *testing.T, changes []apirev.FieldChange, field, kind string) {
	t.Helper()
	for _, c := range changes {
		if c.From == field && c.Kind == kind {
			return
		}
	}
	require.Failf(t, "missing change", "want %s change for field %q in %+v", kind, field, changes)
}

// TestHopFieldDispositionsAgainstSurfaces is the field-level drift guard: for
// EVERY registered hop, each disposition must be real against that hop's
// surfaces. The source (type, field) must exist in the source surface; for a
// rename, the target (type, field) must exist in the target surface AND the
// source field must be gone from the target type (else it isn't a rename). This
// is what keeps the authoritative field-rename table from drifting from the real
// package types - the field-level analog of the type-rename guard.
func TestHopFieldDispositionsAgainstSurfaces(t *testing.T) {
	hasField := func(s *apirev.Snapshot, pkg, typ, field string) bool {
		for _, st := range s.Structs {
			if st.PkgPath == pkg && st.Name == typ {
				for _, f := range st.Fields {
					if f.Name == field {
						return true
					}
				}
			}
		}
		return false
	}

	for _, h := range hops {
		fromSnap, err := decodeSurface(h.From)
		require.NoErrorf(t, err, "hop v%d->v%d source surface", h.From, h.To)
		toSnap, err := decodeSurface(h.To)
		require.NoErrorf(t, err, "hop v%d->v%d target surface", h.From, h.To)

		for _, d := range h.FieldDispositions {
			require.Truef(t, hasField(fromSnap, d.FromPkgPath, d.FromType, d.FromField),
				"hop v%d->v%d: source field %s.%s#%s not found in v%d surface (stale entry?)",
				h.From, h.To, d.FromPkgPath, d.FromType, d.FromField, h.From)

			if d.Action != apirev.ActionRename {
				continue
			}
			require.Truef(t, hasField(toSnap, d.ToPkgPath, d.ToType, d.ToField),
				"hop v%d->v%d: rename target %s.%s#%s not found in v%d surface (wrong rename?)",
				h.From, h.To, d.ToPkgPath, d.ToType, d.ToField, h.To)
			require.Falsef(t, hasField(toSnap, d.ToPkgPath, d.ToType, d.FromField),
				"hop v%d->v%d: field %s still exists on %s in v%d under its source name; not a rename",
				h.From, h.To, d.FromField, d.ToType, h.To)
		}
	}
}
