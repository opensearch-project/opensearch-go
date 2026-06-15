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

func TestManual_Bulk(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-bulk")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	tests := []struct {
		name      string
		body      string
		wantItems int
		check     func(t *testing.T, resp *opensearchapi.BulkResp)
	}{
		{
			name: "index and create",
			body: strings.Join([]string{
				`{"index":{"_index":"` + index + `","_id":"bulk-1"}}`,
				`{"title":"Bulk Doc 1"}`,
				`{"index":{"_index":"` + index + `","_id":"bulk-2"}}`,
				`{"title":"Bulk Doc 2"}`,
				`{"create":{"_index":"` + index + `","_id":"bulk-3"}}`,
				`{"title":"Bulk Doc 3"}`,
				"",
			}, "\n"),
			wantItems: 3,
			check: func(t *testing.T, resp *opensearchapi.BulkResp) {
				t.Helper()
				require.NotNil(t, resp.Items[0].Index)
				require.NotNil(t, resp.Items[0].Index.ID)
				require.Equal(t, "bulk-1", *resp.Items[0].Index.ID)
				require.NotNil(t, resp.Items[2].Create)
				require.NotNil(t, resp.Items[2].Create.ID)
				require.Equal(t, "bulk-3", *resp.Items[2].Create.ID)
			},
		},
		{
			name: "update and delete",
			body: strings.Join([]string{
				`{"update":{"_index":"` + index + `","_id":"bulk-1"}}`,
				`{"doc":{"title":"Updated Bulk Doc 1"}}`,
				`{"delete":{"_index":"` + index + `","_id":"bulk-2"}}`,
				"",
			}, "\n"),
			wantItems: 2,
			check: func(t *testing.T, resp *opensearchapi.BulkResp) {
				t.Helper()
				require.NotNil(t, resp.Items[0].Update)
				require.NotNil(t, resp.Items[1].Delete)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Doc.Bulk(t.Context(), opensearchapi.BulkReq{
				Body:   strings.NewReader(tt.body),
				Params: &opensearchapi.BulkParams{Refresh: "true"},
			})
			require.NoError(t, err)
			require.False(t, resp.Errors)
			require.Len(t, resp.Items, tt.wantItems)
			tt.check(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Doc.Bulk(t.Context(), opensearchapi.BulkReq{
			Body: strings.NewReader("{}\n"),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_Count(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-count")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	body := strings.Join([]string{
		`{"index":{"_index":"` + index + `","_id":"c1"}}`,
		`{"title":"alpha"}`,
		`{"index":{"_index":"` + index + `","_id":"c2"}}`,
		`{"title":"beta"}`,
		`{"index":{"_index":"` + index + `","_id":"c3"}}`,
		`{"title":"gamma"}`,
		"",
	}, "\n")
	_, err = client.Doc.Bulk(t.Context(), opensearchapi.BulkReq{
		Body:   strings.NewReader(body),
		Params: &opensearchapi.BulkParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		req       *opensearchapi.CountReq
		wantCount int64
	}{
		{
			name:      "all documents",
			req:       &opensearchapi.CountReq{Index: []string{index}},
			wantCount: 3,
		},
		{
			name: "with match query",
			req: &opensearchapi.CountReq{
				Index:      []string{index},
				BodyReader: strings.NewReader(`{"query":{"term":{"title.keyword":"alpha"}}}`),
			},
			wantCount: 1,
		},
		{
			name: "with match_all query",
			req: &opensearchapi.CountReq{
				Index:      []string{index},
				BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Count(t.Context(), tt.req)
			require.NoError(t, err)
			require.Equal(t, tt.wantCount, resp.Count)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Count(t.Context(), &opensearchapi.CountReq{Index: []string{index}})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
