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

func TestManual_DocumentGet(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-doc-get")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "doc-1",
		Body:   strings.NewReader(`{"title":"Hello","count":42}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		id      string
		wantErr bool
		check   func(t *testing.T, resp *opensearchapi.GetResp)
	}{
		{
			name: "existing document",
			id:   "doc-1",
			check: func(t *testing.T, resp *opensearchapi.GetResp) {
				t.Helper()
				require.True(t, resp.Found)
				require.Equal(t, "doc-1", resp.ID)
				require.Equal(t, index, resp.Index)
				require.NotNil(t, resp.Version)
				require.Positive(t, *resp.Version)
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
			},
		},
		{
			name:    "missing document",
			id:      "no-such-doc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(t.Context(), opensearchapi.GetReq{Index: index, ID: tt.id})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, resp)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Get(t.Context(), opensearchapi.GetReq{Index: index, ID: "doc-1"})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_DocumentMget(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-doc-mget")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	for _, id := range []string{"doc-1", "doc-2", "doc-3"} {
		_, err = client.Index(t.Context(), opensearchapi.IndexReq{
			Index:  index,
			ID:     id,
			Body:   strings.NewReader(`{"title":"Doc ` + id + `"}`),
			Params: &opensearchapi.IndexParams{Refresh: "true"},
		})
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		body     string
		wantDocs int
	}{
		{
			name:     "two documents",
			body:     `{"docs":[{"_index":"` + index + `","_id":"doc-1"},{"_index":"` + index + `","_id":"doc-2"}]}`,
			wantDocs: 2,
		},
		{
			name: "three documents",
			body: `{"docs":[` +
				`{"_index":"` + index + `","_id":"doc-1"},` +
				`{"_index":"` + index + `","_id":"doc-2"},` +
				`{"_index":"` + index + `","_id":"doc-3"}]}`,
			wantDocs: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.MGet(t.Context(), opensearchapi.MGetReq{
				BodyReader: strings.NewReader(tt.body),
			})
			require.NoError(t, err)
			require.Len(t, resp.Docs, tt.wantDocs)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.MGet(t.Context(), opensearchapi.MGetReq{
			BodyReader: strings.NewReader(`{"docs":[]}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_DocumentUpdate(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-doc-update")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	for _, id := range []string{"doc-1", "doc-2"} {
		_, err = client.Index(t.Context(), opensearchapi.IndexReq{
			Index:  index,
			ID:     id,
			Body:   strings.NewReader(`{"title":"Original","count":1}`),
			Params: &opensearchapi.IndexParams{Refresh: "true"},
		})
		require.NoError(t, err)
	}

	tests := []struct {
		name string
		id   string
		body string
	}{
		{
			name: "partial update title",
			id:   "doc-1",
			body: `{"doc":{"title":"Updated"}}`,
		},
		{
			name: "partial update count",
			id:   "doc-2",
			body: `{"doc":{"count":99}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Update(t.Context(), opensearchapi.UpdateReq{
				Index:      index,
				ID:         tt.id,
				BodyReader: strings.NewReader(tt.body),
			})
			require.NoError(t, err)
			require.Equal(t, "updated", resp.Result)
			require.Equal(t, index, resp.Index)
			require.Equal(t, tt.id, resp.ID)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Update(t.Context(), opensearchapi.UpdateReq{
			Index:      index,
			ID:         "doc-1",
			BodyReader: strings.NewReader(`{"doc":{}}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_DeleteByQuery(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-del-by-query")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	for i := range 5 {
		_, err = client.Index(t.Context(), opensearchapi.IndexReq{
			Index:  index,
			Body:   strings.NewReader(`{"title":"deleteme","seq":` + strings.Repeat("1", i+1) + `}`),
			Params: &opensearchapi.IndexParams{Refresh: "true"},
		})
		require.NoError(t, err)
	}

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "delete all matching",
			query: `{"query":{"match_all":{}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.DeleteByQuery(t.Context(), &opensearchapi.DeleteByQueryReq{
				Index:      []string{index},
				BodyReader: strings.NewReader(tt.query),
			})
			require.NoError(t, err)
			require.NotNil(t, resp.Body)
			require.Contains(t, string(resp.Body), `"deleted"`)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.DeleteByQuery(t.Context(), &opensearchapi.DeleteByQueryReq{
			Index:      []string{index},
			BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
