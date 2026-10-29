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

// TestMLSearchResponseDecodesHitsMetadata guards the hits narrowing on ML search
// responses: it must keep the total and max_score of the hits it narrows, and
// the typed _source of each hit.
func TestMLSearchResponseDecodesHitsMetadata(t *testing.T) {
	t.Parallel()

	body := `{
		"took": 3, "timed_out": false,
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 2, "relation": "eq"},
			"max_score": 1.5,
			"hits": [{"_id": "m1", "_index": ".plugins-ml-model", "_score": 1.5, "model_id": "m1", "_source": {"algorithm": "TEXT_EMBEDDING"}}]
		}
	}`

	var resp opensearchapi.MLSearchResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	require.NotNil(t, resp.Hits)

	total, err := resp.Hits.Total.TotalHits()
	require.NoError(t, err)
	require.Equal(t, int64(2), total.Value)
	require.Equal(t, new(float32(1.5)), resp.Hits.MaxScore)

	require.Len(t, resp.Hits.Hits, 1)
	hit := resp.Hits.Hits[0]
	require.Equal(t, new("m1"), hit.ID)
	require.Equal(t, new("m1"), hit.ModelID)
	require.NotNil(t, hit.Source)
	require.Equal(t, new("TEXT_EMBEDDING"), hit.Source.Algorithm)
}
