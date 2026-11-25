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

func decodeAggs(t *testing.T, body string) map[string]opensearchapi.CommonAggregationsAggregate {
	t.Helper()
	var resp opensearchapi.SearchResp
	doc := `{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[]},` +
		`"aggregations":` + body + `}`
	require.NoError(t, json.Unmarshal([]byte(doc), &resp))
	return resp.Aggregations
}

func TestAggregationSubAggregations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		body  string
		check func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate)
	}{
		{
			name: "date_histogram bucket keeps its top_hits child",
			body: `{"h":{"buckets":[{"key_as_string":"2026-01-01","key":1,"doc_count":2,` +
				`"sample":{"hits":{"total":{"value":2,"relation":"eq"},"max_score":null,"hits":[]}}}]}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["h"]
				h, err := agg.AsDateHistogram()
				require.NoError(t, err)
				buckets, err := h.Buckets.Array()
				require.NoError(t, err)
				require.Len(t, buckets, 1)
				require.Equal(t, int64(1), buckets[0].Key)
				require.Equal(t, "2026-01-01", *buckets[0].KeyAsString)
				require.Equal(t, int64(2), buckets[0].DocCount)
				require.Len(t, buckets[0].Aggregations, 1)
				sample := buckets[0].Aggregations["sample"]
				top, err := sample.AsTopHits()
				require.NoError(t, err)
				require.NotNil(t, top.Hits.Hits)
			},
		},
		{
			name: "string terms bucket keeps key, error bound and avg child",
			body: `{"t":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"a","doc_count":3,"avg_price":{"value":2.5}}]}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["t"]
				terms, err := agg.AsSTerms()
				require.NoError(t, err)
				require.Len(t, terms.Buckets, 1)
				b := terms.Buckets[0]
				require.Equal(t, "a", b.Key)
				require.Equal(t, int64(3), b.DocCount)
				require.Len(t, b.Aggregations, 1)
				avgAgg := b.Aggregations["avg_price"]
				avg, err := avgAgg.AsAvg()
				require.NoError(t, err)
				require.InDelta(t, 2.5, *avg.Value, 0.0001)
			},
		},
		{
			name: "filter aggregate exposes sub-aggregation and keeps doc_count and meta",
			body: `{"f":{"doc_count":4,"meta":{"k":"v"},"inner":{"value":1.5}}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["f"]
				f, err := agg.AsFilter()
				require.NoError(t, err)
				require.Equal(t, int64(4), f.DocCount)
				require.NotNil(t, f.Meta)
				require.Len(t, f.Aggregations, 1)
				require.Contains(t, f.Aggregations, "inner")
			},
		},
		{
			name: "filters bucket base exposes sub-aggregation",
			body: `{"fs":{"buckets":[{"doc_count":1,"inner":{"value":7}}]}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["fs"]
				fs, err := agg.AsFilters()
				require.NoError(t, err)
				buckets, err := fs.Buckets.Array()
				require.NoError(t, err)
				require.Len(t, buckets, 1)
				require.Equal(t, int64(1), buckets[0].DocCount)
				require.Contains(t, buckets[0].Aggregations, "inner")
			},
		},
		{
			name: "keyed filters bucket decodes from Map() with its sub-aggregation",
			body: `{"fs":{"buckets":{"errors":{"doc_count":1,"inner":{"value":7}}}}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["fs"]
				fs, err := agg.AsFilters()
				require.NoError(t, err)
				keyed, err := fs.Buckets.Map()
				require.NoError(t, err)
				var bucket opensearchapi.CommonAggregationsMultiBucketBase
				require.NoError(t, json.Unmarshal(keyed["errors"], &bucket))
				require.Equal(t, int64(1), bucket.DocCount)
				require.Contains(t, bucket.Aggregations, "inner")
			},
		},
		{
			name: "terms bucket re-encodes its key, doc_count and sub-aggregation",
			body: `{"t":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"a","doc_count":3,"avg_price":{"value":2.5}}]}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["t"]
				terms, err := agg.AsSTerms()
				require.NoError(t, err)
				out, err := json.Marshal(terms.Buckets[0])
				require.NoError(t, err)
				require.JSONEq(t, `{"key":"a","doc_count":3,"avg_price":{"value":2.5}}`, string(out))
			},
		},
		{
			name: "bucket without sub-aggregations has nil Aggregations",
			body: `{"h":{"buckets":[{"key":1,"doc_count":2}]}}`,
			check: func(t *testing.T, aggs map[string]opensearchapi.CommonAggregationsAggregate) {
				t.Helper()
				agg := aggs["h"]
				h, err := agg.AsDateHistogram()
				require.NoError(t, err)
				buckets, err := h.Buckets.Array()
				require.NoError(t, err)
				require.Nil(t, buckets[0].Aggregations)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t, decodeAggs(t, tt.body))
		})
	}
}
