// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// majorSeg matches a module-major-version path segment, for comparing package
// paths across majors.
var majorSeg = regexp.MustCompile(`/v[0-9]+(/|$)`)

// TestHopSymbolRenamesAgainstSurfaces is the drift guard for SymbolRenames: for
// every hop, each source symbol must exist in the source surface and be gone from
// the target, each target must exist in the target surface, and the two must be
// equivalent under the hop's renames (apirev.EquivalentSymbol). The target may
// already exist in the source surface: a release can add the new name beside the
// old one before removing it.
func TestHopSymbolRenamesAgainstSurfaces(t *testing.T) {
	t.Parallel()

	for from, h := range hops {
		require.Equal(t, from, h.From)
		if len(h.SymbolRenames) == 0 {
			continue
		}
		t.Run(h.From.String()+"->"+h.To.String(), func(t *testing.T) {
			t.Parallel()

			fromSnap, err := decodeSurface(h.From)
			require.NoError(t, err)
			toSnap, err := decodeSurface(h.To)
			require.NoError(t, err)

			// Names are mapped per package, so collect them before comparing.
			// A FromName listed twice, within or across the two tables, would
			// silently keep only the last mapping.
			seen := make(map[string]struct{})
			renamed := make(map[string]map[string]string)
			for _, r := range append(slices.Clone(h.TypeRenames), h.SymbolRenames...) {
				key := r.FromPkgPath + "." + r.FromName
				_, dup := seen[key]
				require.Falsef(t, dup, "%s is renamed more than once", key)
				seen[key] = struct{}{}
				if renamed[r.FromPkgPath] == nil {
					renamed[r.FromPkgPath] = make(map[string]string)
				}
				renamed[r.FromPkgPath][r.FromName] = r.ToName
			}

			for _, r := range h.SymbolRenames {
				// The guard maps only own-package names, so a cross-package
				// rename cannot be verified. A major-version bump of the same
				// package is not a move.
				require.Equalf(t, majorSeg.ReplaceAllString(r.FromPkgPath, "$1"), majorSeg.ReplaceAllString(r.ToPkgPath, "$1"),
					"%s moves packages; SymbolRenames are same-package only", r.FromName)
				src, ok := fromSnap.LookupSymbol(r.FromPkgPath, r.FromName)
				require.Truef(t, ok, "source symbol %s.%s not in the %s surface", r.FromPkgPath, r.FromName, h.From)
				_, ok = toSnap.LookupSymbol(r.FromPkgPath, r.FromName)
				require.Falsef(t, ok, "%s.%s still in the %s surface; not a rename", r.FromPkgPath, r.FromName, h.To)
				dst, ok := toSnap.LookupSymbol(r.ToPkgPath, r.ToName)
				require.Truef(t, ok, "target symbol %s.%s not in the %s surface", r.ToPkgPath, r.ToName, h.To)

				require.NoErrorf(t, apirev.EquivalentSymbol(src, dst, renamed[r.FromPkgPath]),
					"%s -> %s is not an equivalent rename", r.FromName, r.ToName)
			}
		})
	}
}

// TestEquivalentSymbolRejectsDifferentUnionBranches pins the real-surface case
// the guard exists for: the v5 wrapper options union has an Item-typed Array
// branch that SearchCompletionSuggestOptions types differently, so mapping one
// onto the other is refused even with the enum renamed.
func TestEquivalentSymbolRejectsDifferentUnionBranches(t *testing.T) {
	t.Parallel()

	fromSnap, err := decodeSurface(version{major: 5})
	require.NoError(t, err)
	toSnap, err := decodeSurface(version{major: 5, minor: 2})
	require.NoError(t, err)

	src, ok := fromSnap.LookupSymbol(v5api, "SearchResultJSONValueSuggestValueItemCompletionOptions")
	require.True(t, ok)
	dst, ok := toSnap.LookupSymbol(v5api, "SearchCompletionSuggestOptions")
	require.True(t, ok)

	renamed := map[string]string{
		"SearchResultJSONValueSuggestValueItemCompletionOptions":     "SearchCompletionSuggestOptions",
		"SearchResultJSONValueSuggestValueItemCompletionOptionsType": "SearchCompletionSuggestOptionsType",
	}
	err = apirev.EquivalentSymbol(src, dst, renamed)
	require.Error(t, err)
	require.Contains(t, err.Error(), `method "Array" differs`)
}
