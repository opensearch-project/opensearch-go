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

// TestManual_MGet drives a real mget against the cluster and asserts the
// decoded shape of each per-item union branch. mget responses intermix
// success items ({_index,_id,found,_source,...}) and error items
// ({_index,_id,error:{...}}) as sibling array elements; this exercises the
// MGetRespBodyDocsItem merged single-pass decode (the success|error fan-in)
// and validates that the running server version returns what the generated
// client can decode.
func TestManual_MGet(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-mget")
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

	missingIndex := testutil.MustUniqueString(t, "test-mget-missing")

	resp, err := client.MGet(t.Context(), opensearchapi.MGetReq{
		Body: &opensearchapi.MGetBody{
			Docs: []opensearchapi.MGetOperation{
				{ID: "1", Index: &index},        // success, found
				{ID: "404", Index: &index},      // success, not found
				{ID: "1", Index: &missingIndex}, // per-item error: index missing
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.Docs, 3)

	// Each case asserts the decoded branch of one docs[] element, exercising the
	// MGetRespBodyDocsItem success|error fan-in: GetResult for found/not-found
	// documents, MGetMultiGetError for a per-item index-missing error.
	tests := []struct {
		name  string
		idx   int
		check func(t *testing.T, item opensearchapi.MGetRespBodyDocsItem)
	}{
		{
			name: "found document decodes via GetResult branch",
			idx:  0,
			check: func(t *testing.T, item opensearchapi.MGetRespBodyDocsItem) {
				t.Helper()
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemGetResultType, item.Type())
				v := item.GetResult()
				require.Equal(t, "1", v.ID)
				require.Equal(t, index, v.Index)
				require.True(t, v.Found)
				require.JSONEq(t, `{"title":"present"}`, string(v.Source))
			},
		},
		{
			name: "missing document decodes via GetResult branch with found=false",
			idx:  1,
			check: func(t *testing.T, item opensearchapi.MGetRespBodyDocsItem) {
				t.Helper()
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemGetResultType, item.Type())
				v := item.GetResult()
				require.Equal(t, "404", v.ID)
				require.Equal(t, index, v.Index)
				require.False(t, v.Found)
			},
		},
		{
			name: "missing index decodes via MGetMultiGetError branch",
			idx:  2,
			check: func(t *testing.T, item opensearchapi.MGetRespBodyDocsItem) {
				t.Helper()
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemMGetMultiGetErrorType, item.Type())
				v := item.MGetMultiGetError()
				require.Equal(t, "1", v.ID)
				require.Equal(t, missingIndex, v.Index)
				require.NotEmpty(t, v.Error.Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, resp.Docs[tt.idx])
		})
	}
}
