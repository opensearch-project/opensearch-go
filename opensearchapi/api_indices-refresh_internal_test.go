// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndicesRefreshReq_GetRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      IndicesRefreshReq
		wantPath string
	}{
		{
			name:     "empty refreshes all indices",
			req:      IndicesRefreshReq{},
			wantPath: "/_refresh",
		},
		{
			name:     "Indices builds the path",
			req:      IndicesRefreshReq{Indices: []string{"a", "b"}},
			wantPath: "/a,b/_refresh",
		},
		{
			name:     "deprecated Index builds the path when Indices is empty",
			req:      IndicesRefreshReq{Index: []string{"legacy"}},
			wantPath: "/legacy/_refresh",
		},
		{
			name: "Indices wins when both are set",
			req: IndicesRefreshReq{
				Indices: []string{"preferred"},
				Index:   []string{"ignored"},
			},
			wantPath: "/preferred/_refresh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			httpReq, err := tt.req.GetRequest(http.MethodPost)
			require.NoError(t, err)
			require.Equal(t, http.MethodPost, httpReq.Method)
			require.Equal(t, tt.wantPath, httpReq.URL.Path)
		})
	}
}

func TestIndicesRefreshParams_get(t *testing.T) {
	t.Parallel()

	params := IndicesRefreshParams{
		AllowNoIndices:    ToPointer(true),
		ExpandWildcards:   "open,closed",
		IgnoreUnavailable: ToPointer(false),
		Pretty:            true,
		FilterPath:        []string{"_shards"},
	}
	got := params.get()
	require.Equal(t, map[string]string{
		"allow_no_indices":   "true",
		"expand_wildcards":   "open,closed",
		"ignore_unavailable": "false",
		"pretty":             "true",
		"filter_path":        "_shards",
	}, got)
}

func TestIndicesRefreshResp_ShardsTyped(t *testing.T) {
	t.Parallel()

	const body = `{"_shards":{"total":2,"successful":2,"failed":0}}`
	var resp IndicesRefreshResp
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	require.Equal(t, 2, resp.Shards.Total)
	require.Equal(t, 2, resp.Shards.Successful)
	require.Equal(t, 0, resp.Shards.Failed)
}
