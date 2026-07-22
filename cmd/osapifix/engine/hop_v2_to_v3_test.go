// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
)

// hop_v2_to_v3_test.go pins the concrete v2->v3 facts the rewriter depends on.
// v2->v3 is the project's largest boundary: the opensearchapi package was
// redesigned from a function-based API to a typed sub-client API, so 166 of 182
// v2 structs are removed outright and only 16 survive by name. This hop therefore
// authors NO type/field renames; it rules the root client's removed methods
// MANUAL (idiom 2) and relies on the removed-type diagnostic for the deleted
// *Request family (idiom 1). These assertions pin exactly that.

const (
	v2rootPkg = "github.com/opensearch-project/opensearch-go/v2"
	v2apiPkg  = "github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// planV2toV3 returns the single hop plan for v2->v3.
func planV2toV3(t *testing.T) hopPlan {
	t.Helper()
	plans, err := planChain(2, 3)
	require.NoError(t, err)
	require.Len(t, plans, 1, "v2->v3 is a single hop")
	return plans[0]
}

// TestHopV2toV3_NoRenames asserts the hop authors no type/method renames: the
// redesign is a shape change, surfaced via MANUAL dispositions and the
// removed-type diagnostic, never mis-encoded as a 1:1 rename.
func TestHopV2toV3_NoRenames(t *testing.T) {
	p := planV2toV3(t)
	require.Empty(t, p.renames, "v2->v3 declares no type renames")
	require.Empty(t, p.regroups, "v2->v3 declares no method regroups")
	require.Empty(t, p.removedHelpers, "v2->v3 declares no removed-helper rules")
}

// TestHopV2toV3_RootClientMethodsManual asserts every removed root-client method
// field is ruled MANUAL (idiom 2), so a consumer constructing and calling the
// root opensearch.Client gets an actionable worklist line, not a bare
// "unclassified" bug. Spot-checks the endpoints config actually uses (Ping) plus
// representative others.
func TestHopV2toV3_RootClientMethodsManual(t *testing.T) {
	changes := planV2toV3(t).delta.Structs[v2rootPkg+".Client"].Changes
	for _, method := range []string{"Ping", "Info", "Bulk", "Search", "Indices", "Cat"} {
		assertChangeKind(t, changes, method, apirev.KindManual)
	}

	// Transport survives on the root Client in v3, so it must NOT be flagged.
	for _, ch := range changes {
		require.NotEqual(t, "Transport", ch.From, "Transport survives on the root Client and must not be a change")
	}
}

// TestHopV2toV3_RemovedRequestTypes asserts the deleted function-API *Request
// types (idiom 1) are recorded in the delta's RemovedTypes set, so the engine
// reports a reference to one as a manual worklist item rather than silently
// leaving the consumer an "undefined" compile error.
func TestHopV2toV3_RemovedRequestTypes(t *testing.T) {
	removed := planV2toV3(t).delta.RemovedTypes
	require.NotEmpty(t, removed, "v2->v3 must record removed types")
	for _, typ := range []string{"BulkRequest", "SearchRequest", "IndicesGetRequest", "PingRequest"} {
		require.Truef(t, removed[v2apiPkg+"."+typ],
			"removed function-API type %s must be recorded in RemovedTypes", typ)
	}
}

// TestHopV2toV3_Survivors verifies the non-Client survivors are field-identical
// v2->v3 (no change entry), confirming the hop needs no field rulings for them.
func TestHopV2toV3_Survivors(t *testing.T) {
	structs := planV2toV3(t).delta.Structs
	// A field-identical survivor produces no StructDelta entry at all.
	require.NotContains(t, structs, v2apiPkg+".InfoResp",
		"InfoResp is field-identical v2->v3; no change entry expected")
	require.NotContains(t, structs, v2rootPkg+".Config",
		"root Config is field-identical v2->v3; no change entry expected")
}

// TestHopV2toV3_Followups asserts the two idiom transforms and the response-model
// change are reported as operator follow-ups.
func TestHopV2toV3_Followups(t *testing.T) {
	f := planV2toV3(t).followups
	require.True(t, containsSubstr(f, "function-based API to a typed sub-client API"),
		"must report the API redesign")
	require.True(t, containsSubstr(f, "opensearchapi.NewClient"),
		"must tell the operator how to construct the v3 typed client")
}

// TestHopV2toV3Idiom2FollowupTrimmed asserts the idiom-2 followup no longer
// claims the seed ops are wholly MANUAL: with the idiom-2 pass wired in, Ping
// and Indices.Exists are now rewritten best-effort (call + raw-response +
// client lifecycle), and the followup says so.
func TestHopV2toV3Idiom2FollowupTrimmed(t *testing.T) {
	joined := strings.Join(hopV2toV3.SemanticFollowups, "\n")
	require.Contains(t, joined, "Ping") // seed ops named as best-effort rewritten
	require.Contains(t, joined, "best-effort")
}

// TestHopV2toV3_ChainsToV5 verifies the registered hop composes: a v2->v5 request
// yields the three adjacent hops in order.
func TestHopV2toV3_ChainsToV5(t *testing.T) {
	plans, err := planChain(2, 5)
	require.NoError(t, err)
	require.Len(t, plans, 3, "v2->v5 chains v2->v3, v3->v4, v4->v5")
	require.Equal(t, [2]major{2, 3}, [2]major{plans[0].from, plans[0].to})
	require.Equal(t, [2]major{3, 4}, [2]major{plans[1].from, plans[1].to})
	require.Equal(t, [2]major{4, 5}, [2]major{plans[2].from, plans[2].to})
}
