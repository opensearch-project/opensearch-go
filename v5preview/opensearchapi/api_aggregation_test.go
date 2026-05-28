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

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/testutil"
)

func TestManual_Aggregation(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-agg")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
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
	_, err = client.Bulk(t.Context(), opensearchapi.BulkReq{
		Body:   strings.NewReader(ndjson.String()),
		Params: &opensearchapi.BulkParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name  string
		query string
		check func(t *testing.T, resp *opensearchapi.SearchResp)
	}{
		{
			name:  "terms aggregation",
			query: `{"size":0,"aggs":{"by_category":{"terms":{"field":"category"}}}}`,
			check: func(t *testing.T, resp *opensearchapi.SearchResp) {
				t.Helper()
				require.Contains(t, resp.Aggregations, "by_category")
			},
		},
		{
			name: "date histogram aggregation",
			query: `{"size":0,"aggs":{"by_month":{"date_histogram":` +
				`{"field":"timestamp","calendar_interval":"month"}}}}`,
			check: func(t *testing.T, resp *opensearchapi.SearchResp) {
				t.Helper()
				require.Contains(t, resp.Aggregations, "by_month")
			},
		},
		{
			name:  "stats aggregation",
			query: `{"size":0,"aggs":{"price_stats":{"stats":{"field":"price"}}}}`,
			check: func(t *testing.T, resp *opensearchapi.SearchResp) {
				t.Helper()
				require.Contains(t, resp.Aggregations, "price_stats")
			},
		},
		{
			name: "nested terms with stats",
			query: `{"size":0,"aggs":{"by_category":{"terms":{"field":"category"},` +
				`"aggs":{"avg_price":{"avg":{"field":"price"}}}}}}`,
			check: func(t *testing.T, resp *opensearchapi.SearchResp) {
				t.Helper()
				require.Contains(t, resp.Aggregations, "by_category")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
				Index:      []string{index},
				BodyReader: strings.NewReader(tt.query),
			})
			require.NoError(t, err)
			require.NotNil(t, resp.Aggregations)
			tt.check(t, resp)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Search(t.Context(), &opensearchapi.SearchReq{
			Index:      []string{index},
			BodyReader: strings.NewReader(`{"size":0,"aggs":{"x":{"terms":{"field":"category"}}}}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
