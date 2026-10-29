// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// plan_test.go exercises planChain and the field-disposition behavior of
// DeriveDelta with SYNTHETIC surfaces and hops for fictitious majors
// (v7/v8/v9), so multi-hop chaining and every disposition action are covered
// independently of the real v4->v5 data.

func pkgN(major int) string {
	switch major {
	case 7:
		return "github.com/opensearch-project/opensearch-go/v7/opensearchapi"
	case 8:
		return "github.com/opensearch-project/opensearch-go/v8/opensearchapi"
	case 9:
		return "github.com/opensearch-project/opensearch-go/v9/opensearchapi"
	}
	return "github.com/opensearch-project/opensearch-go/opensearchapi"
}

// majorLine is the first release line of major m.
func majorLine(m major) version { return version{major: m} }

// installSynthetic swaps hops+surfaces for a test and restores them on cleanup.
func installSynthetic(t *testing.T, synthHops map[version]hop, synthSurfaces map[version][]byte) {
	t.Helper()
	origHops, origSurfaces := hops, surfaces
	hops, surfaces = synthHops, synthSurfaces
	t.Cleanup(func() { hops, surfaces = origHops, origSurfaces })
}

func mkSurface(t *testing.T, version string, structs ...apirev.Struct) []byte {
	t.Helper()
	b, err := json.Marshal(apirev.Snapshot{Version: version, Structs: structs})
	require.NoError(t, err)
	return b
}

// TestPlanChain_Errors covers the chain-validation failures.
func TestPlanChain_Errors(t *testing.T) {
	t.Run("src not older than dst", func(t *testing.T) {
		_, err := planChain(majorLine(5), majorLine(5))
		require.Error(t, err)
		_, err = planChain(majorLine(6), majorLine(5))
		require.Error(t, err)
	})

	t.Run("missing hop in chain", func(t *testing.T) {
		installSynthetic(t,
			map[version]hop{majorLine(7): {From: majorLine(7), To: majorLine(8)}}, // no 8->9
			map[version][]byte{majorLine(7): mkSurface(t, "v7"), majorLine(8): mkSurface(t, "v8"), majorLine(9): mkSurface(t, "v9")},
		)
		_, err := planChain(majorLine(7), majorLine(9))
		require.Error(t, err, "missing v8->v9 hop must error")
	})
}

// TestPlanChain_SerialHops verifies planChain yields one self-contained plan per
// adjacent hop, each with its own endpoint-diffed delta and import prefix.
func TestPlanChain_SerialHops(t *testing.T) {
	sv7 := mkSurface(t, "v7", apirev.Struct{PkgPath: pkgN(7), Name: "Req", Fields: []apirev.Field{
		{Name: "DocID", Type: "string"},
	}})
	sv8 := mkSurface(t, "v8", apirev.Struct{PkgPath: pkgN(8), Name: "Req", Fields: []apirev.Field{
		{Name: "ID", Type: "string"},
	}})
	sv9 := mkSurface(t, "v9", apirev.Struct{PkgPath: pkgN(9), Name: "Req", Fields: []apirev.Field{
		{Name: "ID", Type: "string"},
		{Name: "Extra", Type: "string"},
	}})

	synthHops := map[version]hop{
		majorLine(7): {From: majorLine(7), To: majorLine(8), FieldDispositions: []apirev.FieldDisposition{
			{
				FromPkgPath: pkgN(7), FromType: "Req", FromField: "DocID",
				Action: apirev.ActionRename, ToPkgPath: pkgN(8), ToType: "Req", ToField: "ID",
			},
		}},
		majorLine(8): {From: majorLine(8), To: majorLine(9)}, // no field changes v8->v9 for Req
	}
	installSynthetic(t, synthHops, map[version][]byte{majorLine(7): sv7, majorLine(8): sv8, majorLine(9): sv9})

	plans, err := planChain(majorLine(7), majorLine(9))
	require.NoError(t, err)
	require.Len(t, plans, 2, "v7->v9 is two hops")

	// Hop 1 (v7->v8) renames DocID->ID; its import prefix is v7->v8.
	require.Equal(t, majorLine(7), plans[0].from)
	require.Equal(t, majorLine(8), plans[0].to)
	assertChange(t, plans[0].delta.Structs[pkgN(7)+".Req"].Changes,
		apirev.FieldChange{Kind: "rename", From: "DocID", To: "ID", NewType: "string"})
	require.Equal(t, [][2]string{{
		"github.com/opensearch-project/opensearch-go/v7",
		"github.com/opensearch-project/opensearch-go/v8",
	}}, plans[0].importPrefixes)

	// Hop 2 (v8->v9): Req gained Extra, no change to existing fields.
	require.Equal(t, majorLine(8), plans[1].from)
	require.Equal(t, majorLine(9), plans[1].to)
	require.NotContains(t, plans[1].delta.Structs, pkgN(8)+".Req",
		"v8->v9 adds a field only; no change entry expected")
}

// TestPlanChain_MinorLine verifies that a release line within a major chains
// like a major: v7 -> v7.3 -> v8 is two hops, and the hop within v7 keeps the
// import path.
func TestPlanChain_MinorLine(t *testing.T) {
	v73 := version{major: 7, minor: 3}
	installSynthetic(t,
		map[version]hop{
			majorLine(7): {From: majorLine(7), To: v73},
			v73:          {From: v73, To: majorLine(8)},
		},
		map[version][]byte{majorLine(7): mkSurface(t, "v7"), v73: mkSurface(t, "v7.3"), majorLine(8): mkSurface(t, "v8")},
	)

	plans, err := planChain(majorLine(7), majorLine(8))
	require.NoError(t, err)
	require.Len(t, plans, 2)
	require.Equal(t, [2]version{majorLine(7), v73}, [2]version{plans[0].from, plans[0].to})
	require.Equal(t, [2]version{v73, majorLine(8)}, [2]version{plans[1].from, plans[1].to})
	require.Equal(t, [][2]string{{"github.com/opensearch-project/opensearch-go/v7", "github.com/opensearch-project/opensearch-go/v7"}},
		plans[0].importPrefixes)
}

// TestReleaseLines pins how a release maps to the line whose surface it uses.
func TestReleaseLines(t *testing.T) {
	t.Parallel()

	v52 := version{major: 5, minor: 2}
	tests := []struct {
		name   string
		major  major
		minor  int
		want   version
		wantOK bool
	}{
		{name: "first release of a major", major: 5, minor: 0, want: majorLine(5), wantOK: true},
		{name: "a compatible minor stays on the line", major: 5, minor: 1, want: majorLine(5), wantOK: true},
		{name: "the minor a line starts at", major: 5, minor: 2, want: v52, wantOK: true},
		{name: "a later minor of the newest line", major: 5, minor: 7, want: v52, wantOK: true},
		{name: "older major", major: 4, minor: 9, want: majorLine(4), wantOK: true},
		{name: "unknown major", major: 9, minor: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := lineOf(tt.major, tt.minor)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestLineNeighbors pins the lookups that walk the lines of one major.
func TestLineNeighbors(t *testing.T) {
	t.Parallel()

	v52 := version{major: 5, minor: 2}
	tests := []struct {
		name   string
		lookup func() (version, bool)
		want   version
		wantOK bool
	}{
		{name: "next line after v5", lookup: func() (version, bool) { return nextLineOf(majorLine(5)) }, want: v52, wantOK: true},
		{name: "no line after the newest", lookup: func() (version, bool) { return nextLineOf(v52) }},
		{name: "newest line of v5", lookup: func() (version, bool) { return newestLineOf(5) }, want: v52, wantOK: true},
		{name: "first line of v5", lookup: func() (version, bool) { return firstLineOf(5) }, want: majorLine(5), wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tt.lookup()
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestVersionString pins the labels versions print as.
func TestVersionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   version
		want string
	}{
		{in: majorLine(5), want: "v5"},
		{in: version{major: 5, minor: 2}, want: "v5.2"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.in.String())
		})
	}
}

// TestDeriveDelta_FieldDispositions covers each disposition action plus the
// unclassified fallback, on a single synthetic hop.
func TestDeriveDelta_FieldDispositions(t *testing.T) {
	from := &apirev.Snapshot{Version: "v7", Structs: []apirev.Struct{{
		PkgPath: pkgN(7), Name: "Req", Fields: []apirev.Field{
			{Name: "Renamed", Type: "string"},
			{Name: "Removed", Type: "bool"},
			{Name: "Relocated", Type: "string"},
			{Name: "Mystery", Type: "string"}, // vanishes, no disposition
			{Name: "Kept", Type: "string"},
		},
	}}}
	to := &apirev.Snapshot{Version: "v8", Structs: []apirev.Struct{{
		PkgPath: pkgN(8), Name: "Req", Fields: []apirev.Field{
			{Name: "NewName", Type: "string"},
			{Name: "Kept", Type: "string"},
		},
	}}}
	disps := []apirev.FieldDisposition{
		{
			FromPkgPath: pkgN(7), FromType: "Req", FromField: "Renamed",
			Action: apirev.ActionRename, ToPkgPath: pkgN(8), ToType: "Req", ToField: "NewName",
		},
		{FromPkgPath: pkgN(7), FromType: "Req", FromField: "Removed", Action: apirev.ActionRemove},
		{
			FromPkgPath: pkgN(7), FromType: "Req", FromField: "Relocated",
			Action: apirev.ActionManual, Note: "moved behind Body",
		},
	}

	d := apirev.DeriveDelta(from, to, nil, disps)
	changes := d.Structs[pkgN(7)+".Req"].Changes

	assertChange(t, changes, apirev.FieldChange{Kind: "rename", From: "Renamed", To: "NewName", NewType: "string"})
	assertChangeKind(t, changes, "Removed", "remove")
	assertChangeKind(t, changes, "Relocated", "manual")
	assertChangeKind(t, changes, "Mystery", "unclassified") // no disposition -> unclassified, not silent drop

	// Kept survived unchanged: no change entry.
	for _, ch := range changes {
		require.NotEqual(t, "Kept", ch.From, "unchanged field must not appear in the delta")
	}
}
