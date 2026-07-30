// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// searchHitEnvelopeBody is a minimal search response carrying the full per-hit
// envelope: the identity fields (`_index`, `_id`), the scoring field (`_score`),
// the optimistic-concurrency pair (`_seq_no`, `_primary_term`), the routing
// value, and the sort cursor used for search_after pagination.
const searchHitEnvelopeBody = `{"hits":{"total":{"value":1,"relation":"eq"},"max_score":1.5,` +
	`"hits":[{"_index":"idx","_id":"doc1","_score":1.5,"_seq_no":7,"_primary_term":3,` +
	`"_routing":"r1","sort":[1,2],"_source":{"a":1}}]}}`

// TestSearchHitEnvelopeIsDecoded asserts the per-hit envelope survives decoding
// into the typed response. Every field here is transport metadata that lives
// outside `_source`, so a consumer cannot recover it by re-parsing the document
// body: `_id` is needed to address the hit at all, `_seq_no`/`_primary_term`
// gate optimistic-concurrency writes, and `sort` is the search_after cursor.
//
// What this pins is the public API surface, not the structural rule. Against the
// pre-fix types the body below decoded without complaint and the length
// assertion passed; the test failed to COMPILE, because `hit.ID` and its
// siblings did not exist on the shadowed hit type. So it guards the shape a
// consumer writes: these accessors exist, on these types, carrying these values.
//
// The structural rule - a struct must not redeclare a JSON tag its embed already
// carries, which is what made these fields unreachable - is enforced at
// generation time instead, by the allowlist guard in
// cmd/osgen/tagshadow_guard.go. That catches the shape everywhere in the
// generated surface; this catches the one place a consumer would notice.
func TestSearchHitEnvelopeIsDecoded(t *testing.T) {
	t.Parallel()

	var resp opensearchapi.SearchResult
	require.NoError(t, json.Unmarshal([]byte(searchHitEnvelopeBody), &resp))

	hits := resp.Hits.Hits
	require.Len(t, hits, 1, "the response body carries exactly one hit")

	hit := hits[0]

	require.NotNil(t, hit.ID, "_id must be reachable from the typed response")
	require.Equal(t, "doc1", *hit.ID)

	require.NotNil(t, hit.Index)
	require.Equal(t, "idx", *hit.Index)

	require.NotNil(t, hit.Score)
	require.InDelta(t, 1.5, *hit.Score, 1e-9)

	require.NotNil(t, hit.SeqNo, "_seq_no must be reachable for optimistic concurrency")
	require.Equal(t, int64(7), *hit.SeqNo)

	require.NotNil(t, hit.PrimaryTerm, "_primary_term must be reachable for optimistic concurrency")
	require.Equal(t, int64(3), *hit.PrimaryTerm)

	require.NotNil(t, hit.Routing)
	require.Equal(t, "r1", *hit.Routing)

	require.Len(t, hit.Sort, 2, "sort must be reachable for search_after pagination")
	require.Equal(t, opensearchapi.SortResultsItemFloat64Type, hit.Sort[0].Type())
	sort0, err := hit.Sort[0].Float64()
	require.NoError(t, err)
	require.InDelta(t, float64(1), sort0, 1e-9)
	sort1, err := hit.Sort[1].Float64()
	require.NoError(t, err)
	require.InDelta(t, float64(2), sort1, 1e-9)

	require.JSONEq(t, `{"a":1}`, string(hit.Source))
}
