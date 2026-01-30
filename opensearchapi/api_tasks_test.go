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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestTasksClient(t *testing.T) {
	t.Parallel()
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

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

	ctx := context.Background()
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
	require.NoError(t, err)
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

	respReindex, err := client.Reindex(
		ctx,
		opensearchapi.ReindexReq{
			Body: strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s","size":1},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
			Params: opensearchapi.ReindexParams{
				WaitForCompletion: opensearchapi.ToPointer(false),
				RequestsPerSecond: opensearchapi.ToPointer(1),
				Refresh:           opensearchapi.ToPointer(true),
			},
		},
	)
	require.NoError(t, err)
	assert.NotEmpty(t, respReindex)

	// Helper function to wait for task to be available
	waitForTask := func(taskID string) error {
		for range 50 { // Max 5 seconds (50 * 100ms)
			_, err := client.Tasks.Get(ctx, opensearchapi.TasksGetReq{TaskID: taskID})
			if err == nil {
				return nil // Task is available
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("task %s not available after polling", taskID)
	}

	type tasksTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []tasksTests
	}{
		{
			Name: "List",
			Tests: []tasksTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Tasks.List(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Tasks.List(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []tasksTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						// Wait for the task to be available before trying to get it
						if err := waitForTask(respReindex.Task); err != nil {
							return nil, err
						}
						return client.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{TaskID: respReindex.Task})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{})
					},
				},
			},
		},
		{
			Name: "Cancel",
			Tests: []tasksTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{TaskID: respReindex.Task})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range value.Tests {
				// Tasks tests must run sequentially to avoid race conditions with task cancellation
				t.Run(testCase.Name, func(t *testing.T) { //nolint:paralleltest // sequential execution required
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" && value.Name != "Exists" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
}
