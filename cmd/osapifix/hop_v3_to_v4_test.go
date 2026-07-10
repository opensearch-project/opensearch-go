// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hop_v3_to_v4_test.go pins the concrete v3->v4 facts the rewriter depends on.
// The v3->v4 hop carries no type/field/method tables (the surface diff plus the
// fail-loud default cover it), so these assertions pin the diff-derived behavior
// instead: the pointer-wraps the diff produces, the error-model followup, the
// deliberate fail-loud handling of the redesigned response fields, and the fact
// that the hop chains cleanly onto v4->v5.

// planV3toV4 returns the single hop plan for v3->v4.
func planV3toV4(t *testing.T) hopPlan {
	t.Helper()
	plans, err := planChain(3, 4)
	require.NoError(t, err)
	require.Len(t, plans, 1, "v3->v4 is a single hop")
	return plans[0]
}

// TestHopV3toV4_KnownChanges verifies the v3->v4 delta carries the pointer-wraps
// the diff derives and the error-model followup, and that no type/field tables
// were hand-authored for this hop (all changes are diff-derived or fail-loud).
func TestHopV3toV4_KnownChanges(t *testing.T) {
	p := planV3toV4(t)

	// No hand-authored renames or dispositions: the diff plus the fail-loud
	// default carry this hop.
	require.Empty(t, p.renames, "v3->v4 declares no type renames")
	require.Empty(t, p.regroups, "v3->v4 declares no method regroups")

	// A field that became a pointer in v4 is auto-detected as a pointerWrap by the
	// surface diff (no table entry needed): CatNodesItemResp.CPU int -> *int.
	assertChangeKind(t, p.delta.Structs[v3api+".CatNodesItemResp"].Changes, "CPU", "pointerWrap")

	// The error-model move is reported as a semantic followup, never rewritten.
	require.True(t, containsSubstr(p.followups, "opensearchapi.{Error,Err,RootCause,StringError}"),
		"v3->v4 must report the error-package move as a followup")
	require.True(t, containsSubstr(p.followups, "opensearch.StructError"),
		"v3->v4 must flag the Error -> StructError shape change")
}

// TestHopV3toV4_FailLoudForRedesignedFields asserts that the response fields
// redesigned away in v4 (opensearchapi.*Resp.Indices, replaced by an unexported
// field + GetIndices() accessor) are reported as "unclassified" rather than
// silently dropped or wrongly renamed. This is deliberate: no disposition is
// authored up front, so the fail-loud default surfaces the field only if a real
// consumer actually reads it, at which point a proven ruling is added.
func TestHopV3toV4_FailLoudForRedesignedFields(t *testing.T) {
	d := planV3toV4(t).delta

	// Indices vanished on all six *Resp types; each must be unclassified.
	for _, typ := range []string{
		"AliasGetResp", "IndicesGetResp", "IndicesRecoveryResp",
		"MappingFieldResp", "MappingGetResp", "SettingsGetResp",
	} {
		assertChangeKind(t, d.Structs[v3api+"."+typ].Changes, "Indices", "unclassified")
	}

	// opensearchtransport.Connection dropped its liveness fields; also fail-loud.
	conn := d.Structs[v3transport+".Connection"].Changes
	for _, f := range []string{"DeadSince", "Failures", "IsDead"} {
		assertChangeKind(t, conn, f, "unclassified")
	}
}

// TestHopV3toV4_ChainsToV5 verifies the registered hop composes: a v3->v5 request
// yields the two adjacent hops in order, applied serially by the driver.
func TestHopV3toV4_ChainsToV5(t *testing.T) {
	plans, err := planChain(3, 5)
	require.NoError(t, err)
	require.Len(t, plans, 2, "v3->v5 chains v3->v4 then v4->v5")
	require.Equal(t, [2]Major{3, 4}, [2]Major{plans[0].from, plans[0].to})
	require.Equal(t, [2]Major{4, 5}, [2]Major{plans[1].from, plans[1].to})
}

// containsSubstr reports whether any element of s contains sub.
func containsSubstr(s []string, sub string) bool {
	for _, v := range s {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}
