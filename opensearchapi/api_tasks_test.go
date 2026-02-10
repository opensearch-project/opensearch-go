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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
)

func TestTasksClient(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient(t)
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	sourceIndex := "test-tasks-source"
	destIndex := "test-tasks-dest"
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

	ctx := t.Context()
	for _, index := range testIndices {
		client.Indices.Create(
			ctx,
			opensearchapi.IndicesCreateReq{
				Index:  index,
				Body:   strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
				Params: opensearchapi.IndicesCreateParams{WaitForActiveShards: "1"},
			},
		)
	}
	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Index:   sourceIndex,
		Client:  client,
		Refresh: "wait_for",
	})
	require.Nil(t, err)
	for i := 1; i <= 60; i++ {
		err := bi.Add(ctx, opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       strings.NewReader(`{"title":"bar"}`),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
	}
	if err := bi.Close(ctx); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	// Helper to create a reindex task for subtests that need one
	createReindexTask := func(t *testing.T) string {
		t.Helper()
		respReindex, err := client.Reindex(
			t.Context(),
			opensearchapi.ReindexReq{
				Body: strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s","size":1},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
				Params: opensearchapi.ReindexParams{
					WaitForCompletion: opensearchapi.ToPointer(false),
					RequestsPerSecond: opensearchapi.ToPointer(1),
					Refresh:           opensearchapi.ToPointer(true),
				},
			},
		)
		require.Nil(t, err)
		require.NotEmpty(t, respReindex)
		return respReindex.Task
	}

	t.Run("List", func(t *testing.T) {
		t.Parallel()
		t.Run("with request", func(t *testing.T) {
			t.Parallel()
			resp, err := client.Tasks.List(t.Context(), nil)
			require.Nil(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Tasks.List(t.Context(), nil)
			assert.NotNil(t, err)
			assert.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()
		taskID := createReindexTask(t)

		t.Run("with request", func(t *testing.T) {
			resp, err := client.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{TaskID: taskID})
			require.Nil(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{})
			assert.NotNil(t, err)
			assert.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()
		taskID := createReindexTask(t)

		t.Run("with request", func(t *testing.T) {
			resp, err := client.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{TaskID: taskID})
			require.Nil(t, err)
			require.NotNil(t, resp)
			assert.NotNil(t, resp.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{})
			assert.NotNil(t, err)
			assert.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})
}
