// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestSearchShards(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-index-search-shards")

	_, err = client.Indices.Create(
		t.Context(),
		opensearchapi.IndicesCreateReq{
			Index: index,
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.SearchShards(t.Context(), nil)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("with request", func(t *testing.T) {
		resp, err := client.SearchShards(t.Context(), &opensearchapi.SearchShardsReq{Indices: []string{index}})
		require.NoError(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.SearchShards(t.Context(), nil)
		assert.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
