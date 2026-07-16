// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_Aggregation(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-agg")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{
		Index: index,
		BodyReader: strings.NewReader(
			`{"mappings":{"properties":{"category":{"type":"keyword"},` +
				`"price":{"type":"integer"},"timestamp":{"type":"date"}}}}`,
		),
	})
	require.NoError(t, err)

	docs := []string{
		`{"category":"electronics","price":100,"timestamp":"2024-01-01"}`,
		`{"category":"electronics","price":200,"timestamp":"2024-01-15"}`,
		`{"category":"books","price":15,"timestamp":"2024-02-01"}`,
		`{"category":"books","price":25,"timestamp":"2024-02-15"}`,
		`{"category":"clothing","price":50,"timestamp":"2024-03-01"}`,
	}

	var ndjson strings.Builder
	for _, doc := range docs {
		ndjson.WriteString(`{"index":{"_index":"` + index + `"}}` + "\n")
		ndjson.WriteString(doc + "\n")
	}
	_, err = client.Doc.Bulk(t.Context(), opensearchapi.BulkReq{
		Body:   strings.NewReader(ndjson.String()),
		Params: &opensearchapi.BulkParams{Refresh: "true"},
	})
	require.NoError(t, err)

	// Each case drives a real aggregation against the cluster and decodes the
	// result through its As<T>() accessor, asserting the decoded shape. This
	// exercises the SearchResultAggregationsValue union surface (the response
	// half, which a request can't cover) and validates that the running server
	// version returns what the generated client can decode.
	tests := []struct {
		name  string
		key   string
		query string
		check func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue)
	}{
		{
			name:  "terms (string) decodes via AsSTerms",
			key:   "by_category",
			query: `{"size":0,"aggs":{"by_category":{"terms":{"field":"category"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsSTerms()
				require.NoError(t, err)
				require.Len(t, v.Buckets, 3)
				got := map[string]int64{}
				for _, b := range v.Buckets {
					got[b.Key] = b.DocCount
				}
				require.Equal(t, int64(2), got["electronics"])
				require.Equal(t, int64(2), got["books"])
				require.Equal(t, int64(1), got["clothing"])
			},
		},
		{
			name: "date_histogram decodes via AsDateHistogram",
			key:  "by_month",
			query: `{"size":0,"aggs":{"by_month":{"date_histogram":` +
				`{"field":"timestamp","calendar_interval":"month"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsDateHistogram()
				require.NoError(t, err)
				require.NotEmpty(t, v.Buckets)
			},
		},
		{
			name:  "stats decodes via AsStats",
			key:   "price_stats",
			query: `{"size":0,"aggs":{"price_stats":{"stats":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsStats()
				require.NoError(t, err)
				require.Equal(t, int64(5), v.Count)
				require.InDelta(t, 390, v.Sum, 1e-9)
				require.NotNil(t, v.Min)
				require.InDelta(t, 15, *v.Min, 1e-9)
				require.NotNil(t, v.Max)
				require.InDelta(t, 200, *v.Max, 1e-9)
			},
		},
		{
			name:  "avg decodes via AsAvg",
			key:   "price_avg",
			query: `{"size":0,"aggs":{"price_avg":{"avg":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsAvg()
				require.NoError(t, err)
				require.NotNil(t, v.Value)
				require.InDelta(t, 78, *v.Value, 1e-9)
			},
		},
		{
			name:  "sum decodes via AsSum",
			key:   "price_sum",
			query: `{"size":0,"aggs":{"price_sum":{"sum":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsSum()
				require.NoError(t, err)
				require.NotNil(t, v.Value)
				require.InDelta(t, 390, *v.Value, 1e-9)
			},
		},
		{
			name:  "min decodes via AsMin",
			key:   "price_min",
			query: `{"size":0,"aggs":{"price_min":{"min":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsMin()
				require.NoError(t, err)
				require.NotNil(t, v.Value)
				require.InDelta(t, 15, *v.Value, 1e-9)
			},
		},
		{
			name:  "max decodes via AsMax",
			key:   "price_max",
			query: `{"size":0,"aggs":{"price_max":{"max":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsMax()
				require.NoError(t, err)
				require.NotNil(t, v.Value)
				require.InDelta(t, 200, *v.Value, 1e-9)
			},
		},
		{
			name:  "value_count decodes via AsValueCount",
			key:   "price_count",
			query: `{"size":0,"aggs":{"price_count":{"value_count":{"field":"price"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsValueCount()
				require.NoError(t, err)
				require.NotNil(t, v.Value)
				require.InDelta(t, 5, *v.Value, 1e-9)
			},
		},
		{
			name:  "cardinality decodes via AsCardinality",
			key:   "distinct_categories",
			query: `{"size":0,"aggs":{"distinct_categories":{"cardinality":{"field":"category"}}}}`,
			check: func(t *testing.T, agg opensearchapi.SearchResultAggregationsValue) {
				t.Helper()
				v, err := agg.AsCardinality()
				require.NoError(t, err)
				require.Equal(t, int64(3), v.Value) // exact for small cardinalities
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
				Indices:    []string{index},
				BodyReader: strings.NewReader(tt.query),
			})
			require.NoError(t, err)
			require.Contains(t, resp.Aggregations, tt.key)
			tt.check(t, resp.Aggregations[tt.key])
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Search(t.Context(), &opensearchapi.SearchReq{
			Indices:    []string{index},
			BodyReader: strings.NewReader(`{"size":0,"aggs":{"x":{"terms":{"field":"category"}}}}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
