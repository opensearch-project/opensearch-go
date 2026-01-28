// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestReindexRethrottle(t *testing.T) {
	t.Parallel()
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	sourceIndex := testutil.MustUniqueString(t, "test-reindex-rethrottle-source")
	destIndex := testutil.MustUniqueString(t, "test-reindex-rethrottle-dest")
	testIndices := []string{sourceIndex, destIndex}
	t.Cleanup(func() {
		client.Indices.Delete(
			t.Context(),
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
	})

	for _, index := range testIndices {
		client.Indices.Create(
			t.Context(),
			opensearchapi.IndicesCreateReq{
				Index: index,
				Body:  strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
			},
		)
	}
	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Index:   sourceIndex,
		Client:  client,
		Refresh: "wait_for",
	})
	require.NoError(t, err)
	for i := 1; i <= 60; i++ {
		err := bi.Add(t.Context(), opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       strings.NewReader(`{"title":"bar"}`),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
	}
	if err := bi.Close(t.Context()); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	reindex, err := client.Reindex(
		t.Context(),
		opensearchapi.ReindexReq{
			Body: strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s","size":1},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
			Params: opensearchapi.ReindexParams{
				WaitForCompletion: opensearchapi.ToPointer(false),
				RequestsPerSecond: opensearchapi.ToPointer(1),
			},
		},
	)
	require.NoError(t, err)
	t.Run("with request", func(t *testing.T) {
		t.Parallel()
		resp, err := client.ReindexRethrottle(
			t.Context(),
			opensearchapi.ReindexRethrottleReq{
				TaskID: reindex.Task,
				Params: opensearchapi.ReindexRethrottleParams{RequestsPerSecond: opensearchapi.ToPointer(40)},
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		t.Parallel()
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.ReindexRethrottle(t.Context(), opensearchapi.ReindexRethrottleReq{})
		require.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
