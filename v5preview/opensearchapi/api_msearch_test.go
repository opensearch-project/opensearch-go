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
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/testutil"
)

// TestManual_MSearch drives a real msearch against the cluster and asserts the
// decoded shape of each per-item union branch. An msearch responses[] element
// is either a search result ({hits,took,...}) or a per-search error
// ({error:{...},status}); this exercises the
// MSearchMultiSearchResultResponsesItem merged single-pass decode (the
// success|error fan-in) and validates that the running server version returns
// what the generated client can decode.
func TestManual_MSearch(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-msearch")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{
		Index:      index,
		BodyReader: strings.NewReader(`{"mappings":{"properties":{"title":{"type":"keyword"}}}}`),
	})
	require.NoError(t, err)

	_, err = client.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"present"}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	missingIndex := testutil.MustUniqueString(t, "test-msearch-missing")

	// NDJSON: a header line (which index to target) followed by a query line,
	// per sub-search. The first targets the populated index (success); the
	// second targets a non-existent index (per-item error).
	body := `{"index":"` + index + `"}` + "\n" +
		`{"query":{"match_all":{}}}` + "\n" +
		`{"index":"` + missingIndex + `"}` + "\n" +
		`{"query":{"match_all":{}}}` + "\n"

	resp, err := client.MSearch(t.Context(), &opensearchapi.MSearchReq{
		Body: strings.NewReader(body),
	})
	// A per-sub-query error surfaces as a partial-failure error (the response is
	// still fully populated). With a single failed sub-query the per-op
	// aggregate collapses to a bare *MultiSearchItemError.
	require.Error(t, err)
	var itemErr *opensearchapi.MultiSearchItemError
	require.ErrorAs(t, err, &itemErr)
	require.Equal(t, 1, itemErr.SucceededCount)
	require.Len(t, itemErr.Items, 1)
	require.Equal(t, 1, itemErr.Items[0].Index)
	require.Equal(t, 404, itemErr.Items[0].Status)

	require.Len(t, resp.Responses, 2)

	// Each case asserts the decoded branch of one responses[] element,
	// exercising the success|error fan-in: MSearchMultiSearchItem for a
	// successful sub-search, ErrorRespBase for the index-missing one.
	tests := []struct {
		name  string
		idx   int
		check func(t *testing.T, item opensearchapi.MSearchMultiSearchResultResponsesItem)
	}{
		{
			name: "successful sub-search decodes via MSearchMultiSearchItem branch",
			idx:  0,
			check: func(t *testing.T, item opensearchapi.MSearchMultiSearchResultResponsesItem) {
				t.Helper()
				require.Equal(t, opensearchapi.MSearchMultiSearchResultResponsesItemMSearchMultiSearchItemType, item.Type())
				v := item.MSearchMultiSearchItem()
				require.False(t, v.TimedOut)
				require.NotNil(t, v.Hits.Total)
				require.Equal(t, opensearchapi.SearchHitsMetadataTotalSearchTotalHitsType, v.Hits.Total.Type())
				require.Equal(t, int64(1), v.Hits.Total.SearchTotalHits().Value)
				require.Len(t, v.Hits.Hits, 1)
			},
		},
		{
			name: "missing index decodes via ErrorRespBase branch",
			idx:  1,
			check: func(t *testing.T, item opensearchapi.MSearchMultiSearchResultResponsesItem) {
				t.Helper()
				require.Equal(t, opensearchapi.MSearchMultiSearchResultResponsesItemErrorRespBaseType, item.Type())
				v := item.ErrorRespBase()
				require.Equal(t, 404, v.Status)
				require.NotEmpty(t, v.Error.Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, resp.Responses[tt.idx])
		})
	}
}
