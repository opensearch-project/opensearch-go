// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
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

// installSynthetic swaps hops+surfaces for a test and restores them on cleanup.
func installSynthetic(t *testing.T, synthHops map[Major]Hop, synthSurfaces map[Major][]byte) {
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
		_, err := planChain(5, 5)
		require.Error(t, err)
		_, err = planChain(6, 5)
		require.Error(t, err)
	})

	t.Run("missing hop in chain", func(t *testing.T) {
		installSynthetic(t,
			map[Major]Hop{7: {From: 7, To: 8}}, // no 8->9
			map[Major][]byte{7: mkSurface(t, "v7"), 8: mkSurface(t, "v8"), 9: mkSurface(t, "v9")},
		)
		_, err := planChain(7, 9)
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

	synthHops := map[Major]Hop{
		7: {From: 7, To: 8, FieldDispositions: []apirev.FieldDisposition{
			{
				FromPkgPath: pkgN(7), FromType: "Req", FromField: "DocID",
				Action: apirev.ActionRename, ToPkgPath: pkgN(8), ToType: "Req", ToField: "ID",
			},
		}},
		8: {From: 8, To: 9}, // no field changes v8->v9 for Req
	}
	installSynthetic(t, synthHops, map[Major][]byte{7: sv7, 8: sv8, 9: sv9})

	plans, err := planChain(7, 9)
	require.NoError(t, err)
	require.Len(t, plans, 2, "v7->v9 is two hops")

	// Hop 1 (v7->v8) renames DocID->ID; its import prefix is v7->v8.
	require.Equal(t, Major(7), plans[0].from)
	require.Equal(t, Major(8), plans[0].to)
	assertChange(t, plans[0].delta.Structs[pkgN(7)+".Req"].Changes,
		apirev.FieldChange{Kind: "rename", From: "DocID", To: "ID", NewType: "string"})
	require.Equal(t, [][2]string{{
		"github.com/opensearch-project/opensearch-go/v7",
		"github.com/opensearch-project/opensearch-go/v8",
	}}, plans[0].importPrefixes)

	// Hop 2 (v8->v9): Req gained Extra, no change to existing fields.
	require.Equal(t, Major(8), plans[1].from)
	require.Equal(t, Major(9), plans[1].to)
	require.NotContains(t, plans[1].delta.Structs, pkgN(8)+".Req",
		"v8->v9 adds a field only; no change entry expected")
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
