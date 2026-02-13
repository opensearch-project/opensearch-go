// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestCatClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	// snapshotRepo := "test-snapshot-repo"

	index := testutil.MustUniqueString(t, "test-cat-indices")
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(),
		opensearchapi.IndicesCreateReq{Index: index, Body: strings.NewReader(`{"aliases":{"TEST_CAT_ALIAS":{}}}`)})
	require.NoError(t, err)

	type catTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := map[string][]catTests{
		"Aliases": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Aliases(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Aliases(t.Context(), &opensearchapi.CatAliasesReq{Aliases: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Aliases(t.Context(), nil) },
			},
		},
		"Allocation": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Allocation(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Allocation(t.Context(), &opensearchapi.CatAllocationReq{NodeIDs: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Allocation(t.Context(), nil) },
			},
		},
		"ClusterManager": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.ClusterManager(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.ClusterManager(t.Context(), &opensearchapi.CatClusterManagerReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.ClusterManager(t.Context(), nil) },
			},
		},
		"Count": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Count(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Count(t.Context(), &opensearchapi.CatCountReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Count(t.Context(), nil) },
			},
		},
		"FieldData": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.FieldData(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.FieldData(t.Context(), &opensearchapi.CatFieldDataReq{FieldData: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.FieldData(t.Context(), nil) },
			},
		},
		"Health": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Health(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Health(t.Context(), &opensearchapi.CatHealthReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Health(t.Context(), nil) },
			},
		},
		"Indices": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Indices(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Indices(t.Context(), &opensearchapi.CatIndicesReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Indices(t.Context(), nil) },
			},
		},
		"Master": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Master(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Master(t.Context(), &opensearchapi.CatMasterReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Master(t.Context(), nil) },
			},
		},
		"NodeAttrs": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.NodeAttrs(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.NodeAttrs(t.Context(), &opensearchapi.CatNodeAttrsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.NodeAttrs(t.Context(), nil) },
			},
		},
		"Nodes": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Nodes(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Nodes(t.Context(), &opensearchapi.CatNodesReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Nodes(t.Context(), nil) },
			},
		},
		"PendingTasks": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.PendingTasks(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.PendingTasks(t.Context(), &opensearchapi.CatPendingTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.PendingTasks(t.Context(), nil) },
			},
		},
		"Plugins": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Plugins(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Plugins(t.Context(), &opensearchapi.CatPluginsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Plugins(t.Context(), nil) },
			},
		},
		"Recovery": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Recovery(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Recovery(t.Context(), &opensearchapi.CatRecoveryReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Recovery(t.Context(), nil) },
			},
		},
		"Repositories": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Repositories(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Repositories(t.Context(), &opensearchapi.CatRepositoriesReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Repositories(t.Context(), nil) },
			},
		},
		"Segments": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Segments(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Segments(t.Context(), &opensearchapi.CatSegmentsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Segments(t.Context(), nil) },
			},
		},
		"Shards": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Shards(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Shards(t.Context(), &opensearchapi.CatShardsReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Shards(t.Context(), nil) },
			},
		},
		/* Need to create snapshot + repo
		"Shards": []catTests{
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Snapshots(t.Context(), opensearchapi.CatSnapshotsReq{Repository: snapshotRepo})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cat.Snapshots(t.Context(), opensearchapi.CatSnapshotsReq{Repository: snapshotRepo})
				},
			},
		},
		*/
		"Tasks": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Tasks(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Tasks(t.Context(), &opensearchapi.CatTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Tasks(t.Context(), nil) },
			},
		},
		"Templates": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.Templates(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.Templates(t.Context(), &opensearchapi.CatTemplatesReq{Templates: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.Templates(t.Context(), nil) },
			},
		},
		"ThreadPool": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cat.ThreadPool(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cat.ThreadPool(t.Context(), &opensearchapi.CatThreadPoolReq{Pools: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cat.ThreadPool(t.Context(), nil) },
			},
		},
	}
	for catType, value := range testCases {
		t.Run(catType, func(t *testing.T) {
			if catType == "ClusterManager" {
				testutil.SkipIfBelowVersion(t, client, 2, 0, catType)
			}
			for _, testCase := range value {
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						assert.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Aliases", func(t *testing.T) {
			resp, err := client.Cat.Aliases(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Aliases, resp.Inspect().Response)
		})
		t.Run("Allocation", func(t *testing.T) {
			resp, err := client.Cat.Allocation(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Allocations, resp.Inspect().Response)
		})
		t.Run("ClusterManager", func(t *testing.T) {
			testutil.SkipIfBelowVersion(t, client, 2, 0, "ClusterManager")
			resp, err := client.Cat.ClusterManager(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.ClusterManagers, resp.Inspect().Response)
		})
		t.Run("Count", func(t *testing.T) {
			resp, err := client.Cat.Count(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Counts, resp.Inspect().Response)
		})
		t.Run("FieldData", func(t *testing.T) {
			resp, err := client.Cat.FieldData(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.FieldData, resp.Inspect().Response)
		})
		t.Run("Health", func(t *testing.T) {
			resp, err := client.Cat.Health(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Health, resp.Inspect().Response)
		})
		t.Run("Indices", func(t *testing.T) {
			resp, err := client.Cat.Indices(t.Context(), &opensearchapi.CatIndicesReq{Params: opensearchapi.CatIndicesParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Master", func(t *testing.T) {
			resp, err := client.Cat.Master(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Master, resp.Inspect().Response)
		})
		t.Run("NodeAttrs", func(t *testing.T) {
			resp, err := client.Cat.NodeAttrs(t.Context(),
				&opensearchapi.CatNodeAttrsReq{Params: opensearchapi.CatNodeAttrsParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.NodeAttrs, resp.Inspect().Response)
		})
		t.Run("Nodes", func(t *testing.T) {
			resp, err := client.Cat.Nodes(t.Context(), &opensearchapi.CatNodesReq{Params: opensearchapi.CatNodesParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Nodes, resp.Inspect().Response)
		})
		t.Run("PendingTasks", func(t *testing.T) {
			resp, err := client.Cat.PendingTasks(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.PendingTasks, resp.Inspect().Response)
		})
		t.Run("Plugins", func(t *testing.T) {
			resp, err := client.Cat.Plugins(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Plugins, resp.Inspect().Response)
		})
		t.Run("Recovery", func(t *testing.T) {
			resp, err := client.Cat.Recovery(t.Context(), &opensearchapi.CatRecoveryReq{Params: opensearchapi.CatRecoveryParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Recovery, resp.Inspect().Response)
		})
		t.Run("Repositories", func(t *testing.T) {
			resp, err := client.Cat.Repositories(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Repositories, resp.Inspect().Response)
		})
		t.Run("Segments", func(t *testing.T) {
			resp, err := client.Cat.Segments(t.Context(), &opensearchapi.CatSegmentsReq{Params: opensearchapi.CatSegmentsParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Segments, resp.Inspect().Response)
		})
		t.Run("Shards", func(t *testing.T) {
			resp, err := client.Cat.Shards(t.Context(), &opensearchapi.CatShardsReq{Params: opensearchapi.CatShardsParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Shards, resp.Inspect().Response)
		})
		/* Need to create Snapshot + Repo
		t.Run("Snapshots", func(t *testing.T) {
			resp, err := client.Cat.Snapshots(t.Context(),
				&opensearchapi.CatSnapshotsReq{Repository: snapshotRepo, Params: opensearchapi.CatSnapshotsParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Snapshots, resp.Inspect().Response)
		})
		*/
		t.Run("Tasks", func(t *testing.T) {
			resp, err := client.Cat.Tasks(t.Context(), &opensearchapi.CatTasksReq{Params: opensearchapi.CatTasksParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Tasks, resp.Inspect().Response)
		})
		t.Run("Templates", func(t *testing.T) {
			resp, err := client.Cat.Templates(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Templates, resp.Inspect().Response)
		})
		t.Run("ThreadPool", func(t *testing.T) {
			resp, err := client.Cat.ThreadPool(t.Context(),
				&opensearchapi.CatThreadPoolReq{Params: opensearchapi.CatThreadPoolParams{H: []string{"*"}}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.ThreadPool, resp.Inspect().Response)
		})
	})
}
