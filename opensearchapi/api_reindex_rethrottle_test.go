// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
)

func TestReindexRethrottle(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient()
	require.Nil(t, err)

	sourceIndex := "test-reindex-rethrottle-source"
	destIndex := "test-reindex-rethrottle-dest"
	testIndices := []string{sourceIndex, destIndex}
	t.Cleanup(func() {
		client.Indices.Delete(
			nil,
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
	})

	for _, index := range testIndices {
		client.Indices.Create(
			nil,
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
	for i := 1; i <= 60; i++ {
		err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       strings.NewReader(`{"title":"bar"}`),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
	}
	if err := bi.Close(context.Background()); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	reindex, _, err := client.Reindex(
		nil,
		opensearchapi.ReindexReq{
			Body: strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s","size":1},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
			Params: opensearchapi.ReindexParams{
				WaitForCompletion: opensearchapi.ToPointer(false),
				RequestsPerSecond: opensearchapi.ToPointer(1),
			},
		},
	)
	require.Nil(t, err)
	t.Run("with request", func(t *testing.T) {
		resp, httpResp, err := client.ReindexRethrottle(
			nil,
			opensearchapi.ReindexRethrottleReq{
				TaskID: reindex.Task,
				Params: opensearchapi.ReindexRethrottleParams{RequestsPerSecond: opensearchapi.ToPointer(40)},
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		assert.NotNil(t, httpResp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		resp, httpResp, err := failingClient.ReindexRethrottle(nil, opensearchapi.ReindexRethrottleReq{})
		assert.NotNil(t, err)
		assert.Nil(t, resp)
		assert.NotNil(t, httpResp)
		osapitest.VerifyResponse(t, httpResp)
	})
}
