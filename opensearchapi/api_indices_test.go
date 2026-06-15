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
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_IndicesCreate(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-indices-create")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	t.Run("create and verify", func(t *testing.T) {
		resp, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)
		require.True(t, resp.Acknowledged)
		require.True(t, resp.ShardsAcknowledged)
		require.Equal(t, index, resp.Index)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_IndicesExists(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-indices-exists")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	tests := []struct {
		name   string
		index  []string
		exists bool
	}{
		{name: "existing index", index: []string{index}, exists: true},
		{name: "non-existing index", index: []string{testutil.MustUniqueString(t, "no-such-index")}, exists: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Indices.Exists(t.Context(), &opensearchapi.IndicesExistsReq{Index: tt.index})
			if tt.exists {
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestManual_IndicesDelete(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-indices-delete")

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	t.Run("delete existing", func(t *testing.T) {
		resp, err := client.Indices.Delete(t.Context(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
		require.NoError(t, err)
		require.True(t, resp.Acknowledged)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.Delete(t.Context(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_Index(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-index-doc")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	tests := []struct {
		name string
		req  opensearchapi.IndexReq
	}{
		{
			name: "auto-generated id",
			req: opensearchapi.IndexReq{
				Index: index,
				Body:  strings.NewReader(`{"title":"Test Document","count":42}`),
			},
		},
		{
			name: "explicit id",
			req: opensearchapi.IndexReq{
				Index: index,
				ID:    "doc-1",
				Body:  strings.NewReader(`{"title":"Explicit ID","count":7}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Doc.Index(t.Context(), tt.req)
			require.NoError(t, err)
			require.NotEmpty(t, resp.ID)
			require.Equal(t, index, resp.Index)
			require.NotEmpty(t, resp.Result)
			require.Positive(t, resp.Version)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Doc.Index(t.Context(), opensearchapi.IndexReq{
			Index: index,
			Body:  strings.NewReader(`{}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_Search(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-search")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Doc.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "doc-1",
		Body:   strings.NewReader(`{"title":"Hello World","count":1}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		body     string
		wantHits bool
	}{
		{name: "match all", body: `{"query":{"match_all":{}}}`, wantHits: true},
		{name: "size zero", body: `{"query":{"match_all":{}},"size":0}`, wantHits: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
				Index:      []string{index},
				BodyReader: strings.NewReader(tt.body),
			})
			require.NoError(t, err)
			require.NotNil(t, resp.Hits.Total)
			if tt.wantHits {
				require.NotEmpty(t, resp.Hits.Hits)
			} else {
				require.Empty(t, resp.Hits.Hits)
			}
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Search(t.Context(), &opensearchapi.SearchReq{
			BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
