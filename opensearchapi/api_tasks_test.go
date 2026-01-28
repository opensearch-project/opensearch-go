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

func TestTasksClient(t *testing.T) {
	t.Parallel()
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	// Create shared source index with test data
	sourceIndex := testutil.MustUniqueString(t, "test-tasks-source")
	t.Cleanup(func() {
		client.Indices.Delete(
			t.Context(),
			opensearchapi.IndicesDeleteReq{
				Indices: []string{sourceIndex},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
	})

	// Create source index
	ctx := t.Context()
	client.Indices.Create(
		ctx,
		opensearchapi.IndicesCreateReq{
			Index:  sourceIndex,
			Body:   strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
			Params: opensearchapi.IndicesCreateParams{WaitForActiveShards: "1"},
		},
	)

	// Populate source index with test data
	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Index:   sourceIndex,
		Client:  client,
		Refresh: "wait_for",
	})
	require.NoError(t, err)
	for i := 1; i <= 100; i++ {
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

	type tasksTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	t.Run("List", func(t *testing.T) {
		t.Parallel()

		testCases := []tasksTests{
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
		}

		for _, testCase := range testCases {
			t.Run(testCase.Name, func(t *testing.T) {
				res, err := testCase.Results()
				if testCase.Name == "inspect" {
					require.Error(t, err)
					assert.NotNil(t, res)
					osapitest.VerifyInspect(t, res.Inspect())
				} else {
					require.NoError(t, err)
					require.NotNil(t, res)
					assert.NotNil(t, res.Inspect().Response)
					testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
				}
			})
		}
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		// Create unique indices for this test
		destIndex := testutil.MustUniqueString(t, "test-tasks-dest")
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

		// Create destination index
		client.Indices.Create(
			t.Context(),
			opensearchapi.IndicesCreateReq{
				Index:  destIndex,
				Body:   strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
				Params: opensearchapi.IndicesCreateParams{WaitForActiveShards: "1"},
			},
		)

		// Create reindex task
		resp, err := client.Reindex(
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
		require.NoError(t, err)
		require.NotEmpty(t, resp)
		taskID := resp.Task

		testCases := []tasksTests{
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{TaskID: taskID})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Tasks.Get(t.Context(), opensearchapi.TasksGetReq{})
				},
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.Name, func(t *testing.T) {
				res, err := testCase.Results()
				if testCase.Name == "inspect" {
					require.Error(t, err)
					assert.NotNil(t, res)
					osapitest.VerifyInspect(t, res.Inspect())
				} else {
					require.NoError(t, err)
					require.NotNil(t, res)
					assert.NotNil(t, res.Inspect().Response)
				}
			})
		}
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()

		// Create unique indices for this test
		destIndex := testutil.MustUniqueString(t, "test-tasks-dest")
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

		// Create destination index
		client.Indices.Create(
			t.Context(),
			opensearchapi.IndicesCreateReq{
				Index:  destIndex,
				Body:   strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
				Params: opensearchapi.IndicesCreateParams{WaitForActiveShards: "1"},
			},
		)

		// Create reindex task
		resp, err := client.Reindex(
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
		require.NoError(t, err)
		require.NotEmpty(t, resp)
		taskID := resp.Task

		testCases := []tasksTests{
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{TaskID: taskID})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Tasks.Cancel(t.Context(), opensearchapi.TasksCancelReq{})
				},
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.Name, func(t *testing.T) {
				res, err := testCase.Results()
				if testCase.Name == "inspect" {
					require.Error(t, err)
					assert.NotNil(t, res)
					osapitest.VerifyInspect(t, res.Inspect())
				} else {
					require.NoError(t, err)
					require.NotNil(t, res)
					assert.NotNil(t, res.Inspect().Response)
					testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
				}
			})
		}
	})
}
