// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
)

// hop_v4_to_v5_test.go pins the concrete v4->v5 facts the rewriter depends on.
// Unlike the generic drift guards in delta_test.go, these assertions ARE
// version-specific - they encode the exact changes the osv4 consumer corpus must
// undergo - so they live beside the v4->v5 data and are named for that hop.

// planV4toV5 returns the single hop plan for v4->v5.
func planV4toV5(t *testing.T) hopPlan {
	t.Helper()
	plans, err := planChain(4, 5)
	require.NoError(t, err)
	require.Len(t, plans, 1, "v4->v5 is a single hop")
	return plans[0]
}

// TestHopV4toV5_KnownChanges verifies the v4->v5 delta carries the specific type
// renames, field renames, pointer-wraps, and removals we rely on.
func TestHopV4toV5_KnownChanges(t *testing.T) {
	d := planV4toV5(t).delta

	// DocumentGetReq -> GetReq (type rename) carrying a field rename + pointer-wrap.
	getReq := d.Structs[v4api+".DocumentGetReq"]
	require.Equalf(t, v5api+".GetReq", getReq.To, "DocumentGetReq should map to %s.GetReq", v5api)
	assertChange(t, getReq.Changes, apirev.FieldChange{Kind: "rename", From: "DocumentID", To: "ID", NewType: "string"})
	assertChangeKind(t, getReq.Changes, "Params", "pointerWrap")

	// Field rename proven by a shared JSON tag: SearchResp.Timeout -> TimedOut.
	assertChangeKind(t, d.Structs[v4api+".SearchResp"].Changes, "Timeout", "rename")

	// Field rename proven by v4 source assembly: UpdateReq.DocumentID -> ID.
	assertChange(t, d.Structs[v4api+".UpdateReq"].Changes,
		apirev.FieldChange{Kind: "rename", From: "DocumentID", To: "ID", NewType: "string"})

	// EnableMetrics fan-in: removed from BOTH Config structs, as distinct keys.
	root := "github.com/opensearch-project/opensearch-go/v4.Config"
	transport := "github.com/opensearch-project/opensearch-go/v4/opensearchtransport.Config"
	assertChangeKind(t, d.Structs[root].Changes, "EnableMetrics", "remove")
	assertChangeKind(t, d.Structs[transport].Changes, "EnableMetrics", "remove")
}

// TestHopV4toV5_NoUnclassifiedInCorpus asserts that none of the field changes the
// v4->v5 delta produces for the referenced types are "unclassified" for the
// fields our own osv4 wrapper actually sets/reads. A stray unclassified here
// would fail a real rewrite loudly (by design); this catches a missing
// disposition at unit-test time instead. It is intentionally scoped to the
// request/response types the wrapper touches, not the entire apirev.
func TestHopV4toV5_NoUnclassifiedForKnownFields(t *testing.T) {
	d := planV4toV5(t).delta
	// Fields the osv4 wrapper is known to set; each must resolve to a concrete
	// action (rename/remove/pointerWrap), never "unclassified".
	knownlySet := map[string][]string{
		v4api + ".SearchResp":     {"Timeout"},
		v4api + ".UpdateReq":      {"DocumentID"},
		v4api + ".IndexReq":       {"DocumentID"},
		v4api + ".SnapshotGetReq": {"Repo", "Snapshots"},
	}
	for typ, fields := range knownlySet {
		sd := d.Structs[typ]
		for _, f := range fields {
			for _, ch := range sd.Changes {
				if ch.From == f {
					require.NotEqualf(t, "unclassified", ch.Kind,
						"%s#%s is unclassified - add a FieldDisposition", typ, f)
				}
			}
		}
	}
}
