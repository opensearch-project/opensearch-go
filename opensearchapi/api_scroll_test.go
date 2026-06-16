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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_Scroll(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-scroll")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	ndjson := strings.Join([]string{
		`{"index":{"_index":"` + index + `","_id":"s1"}}`,
		`{"title":"Scroll Doc 1"}`,
		`{"index":{"_index":"` + index + `","_id":"s2"}}`,
		`{"title":"Scroll Doc 2"}`,
		`{"index":{"_index":"` + index + `","_id":"s3"}}`,
		`{"title":"Scroll Doc 3"}`,
		"",
	}, "\n")
	_, err = client.Doc.Bulk(t.Context(), opensearchapi.BulkReq{
		Body:   strings.NewReader(ndjson),
		Params: &opensearchapi.BulkParams{Refresh: "true"},
	})
	require.NoError(t, err)

	t.Run("scroll lifecycle", func(t *testing.T) {
		searchResp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
			Indices:    []string{index},
			BodyReader: strings.NewReader(`{"query":{"match_all":{}},"size":1}`),
			Params:     &opensearchapi.SearchParams{Scroll: 1 * time.Minute},
		})
		require.NoError(t, err)
		require.NotNil(t, searchResp.ScrollID)
		require.Len(t, searchResp.Hits.Hits, 1)
		testutil.CompareRawJSONwithParsedJSON(t, searchResp, searchResp.Inspect().Response)

		scrollID := *searchResp.ScrollID

		tests := []struct {
			name    string
			wantHit bool
		}{
			{name: "page 2", wantHit: true},
			{name: "page 3", wantHit: true},
			{name: "page 4 (empty)", wantHit: false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				scrollResp, err := client.Scroll.Get(t.Context(), opensearchapi.ScrollReq{
					BodyReader: strings.NewReader(`{"scroll_id":"` + scrollID + `","scroll":"1m"}`),
				})
				require.NoError(t, err)
				if tt.wantHit {
					require.NotEmpty(t, scrollResp.Hits.Hits)
				} else {
					require.Empty(t, scrollResp.Hits.Hits)
				}
				if scrollResp.ScrollID != nil {
					scrollID = *scrollResp.ScrollID
				}
				testutil.CompareRawJSONwithParsedJSON(t, scrollResp, scrollResp.Inspect().Response)
			})
		}

		t.Run("clear scroll", func(t *testing.T) {
			clearResp, err := client.Scroll.Delete(t.Context(), &opensearchapi.ClearScrollReq{
				ScrollID: []string{scrollID},
			})
			require.NoError(t, err)
			require.True(t, clearResp.Succeeded)
			require.Positive(t, clearResp.NumFreed)
			testutil.CompareRawJSONwithParsedJSON(t, clearResp, clearResp.Inspect().Response)
		})
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Scroll.Get(t.Context(), opensearchapi.ScrollReq{
			BodyReader: strings.NewReader(`{"scroll_id":"fake","scroll":"1m"}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
