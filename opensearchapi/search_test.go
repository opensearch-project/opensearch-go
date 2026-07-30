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
// This is the expression a consumer writes. It previously compiled and returned an
// empty slice, because a redundant `hits` redeclaration on the enclosing struct
// shadowed the declaration that carries the envelope.
func TestSearchHitEnvelopeIsDecoded(t *testing.T) {
	t.Parallel()

	var resp opensearchapi.SearchResult
	require.NoError(t, json.Unmarshal([]byte(searchHitEnvelopeBody), &resp))

	hits := resp.Hits.Hits
	require.Len(t, hits, 1, "the per-hit envelope must be populated, not shadowed away")

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
	require.InDelta(t, float64(1), hit.Sort[0].Float64(), 1e-9)
	require.InDelta(t, float64(2), hit.Sort[1].Float64(), 1e-9)

	require.JSONEq(t, `{"a":1}`, string(hit.Source))
}

// TestSearchHitEnvelopeSiblingsAreDecoded pins the sibling fields of `hits` as a
// control. These share the embedded struct with the shadowed slice but carry no
// duplicate tag, so they decode correctly both before and after the fix. If one
// of these ever regresses alongside the envelope, the cause is broader than tag
// shadowing.
func TestSearchHitEnvelopeSiblingsAreDecoded(t *testing.T) {
	t.Parallel()

	var resp opensearchapi.SearchResult
	require.NoError(t, json.Unmarshal([]byte(searchHitEnvelopeBody), &resp))

	require.NotNil(t, resp.Hits.MaxScore)
	require.InDelta(t, float32(1.5), *resp.Hits.MaxScore, 1e-6)

	require.NotNil(t, resp.Hits.Total)
	require.Equal(t, int64(1), resp.Hits.Total.SearchTotalHits().Value)
}
