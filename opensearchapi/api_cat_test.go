// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"github.com/opensearch-project/opensearch-go/v4"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestCatClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	// snapshotRepo := "test-snapshot-repo"

	index := "test-cat-indices"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, _, err = client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index, Body: strings.NewReader(`{"aliases":{"TEST_CAT_ALIAS":{}}}`)})
	require.Nil(t, err)

	type catTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
	}

	testCases := map[string][]catTests{
		"Aliases": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Aliases(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Aliases(nil, &opensearchapi.CatAliasesReq{Aliases: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Aliases(nil, nil) },
			},
		},
		"Allocation": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Allocation(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Allocation(nil, &opensearchapi.CatAllocationReq{NodeIDs: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Allocation(nil, nil) },
			},
		},
		"ClusterManager": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.ClusterManager(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.ClusterManager(nil, &opensearchapi.CatClusterManagerReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.ClusterManager(nil, nil) },
			},
		},
		"Count": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Count(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Count(nil, &opensearchapi.CatCountReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Count(nil, nil) },
			},
		},
		"FieldData": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.FieldData(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.FieldData(nil, &opensearchapi.CatFieldDataReq{FieldData: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.FieldData(nil, nil) },
			},
		},
		"Health": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Health(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Health(nil, &opensearchapi.CatHealthReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Health(nil, nil) },
			},
		},
		"Indices": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Indices(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Indices(nil, &opensearchapi.CatIndicesReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Indices(nil, nil) },
			},
		},
		"Master": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Master(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Master(nil, &opensearchapi.CatMasterReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Master(nil, nil) },
			},
		},
		"NodeAttrs": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.NodeAttrs(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.NodeAttrs(nil, &opensearchapi.CatNodeAttrsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.NodeAttrs(nil, nil) },
			},
		},
		"Nodes": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Nodes(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Nodes(nil, &opensearchapi.CatNodesReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Nodes(nil, nil) },
			},
		},
		"PendingTasks": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.PendingTasks(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.PendingTasks(nil, &opensearchapi.CatPendingTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.PendingTasks(nil, nil) },
			},
		},
		"Plugins": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Plugins(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Plugins(nil, &opensearchapi.CatPluginsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Plugins(nil, nil) },
			},
		},
		"Recovery": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Recovery(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Recovery(nil, &opensearchapi.CatRecoveryReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Recovery(nil, nil) },
			},
		},
		"Repositories": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Repositories(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Repositories(nil, &opensearchapi.CatRepositoriesReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Repositories(nil, nil) },
			},
		},
		"Segments": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Segments(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Segments(nil, &opensearchapi.CatSegmentsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Segments(nil, nil) },
			},
		},
		"Shards": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Shards(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Shards(nil, &opensearchapi.CatShardsReq{Indices: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Shards(nil, nil) },
			},
		},
		/* Need to create snapshot + repo
		"Shards": []catTests{
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Snapshots(nil, opensearchapi.CatSnapshotsReq{Repository: snapshotRepo})
				},
			},
			{
				Name: "inspect",
				Results: func() (any, *opensearch.Response, error) {
					return failingClient.Cat.Snapshots(nil, opensearchapi.CatSnapshotsReq{Repository: snapshotRepo})
				},
			},
		},
		*/
		"Tasks": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Tasks(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Tasks(nil, &opensearchapi.CatTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Tasks(nil, nil) },
			},
		},
		"Templates": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.Templates(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.Templates(nil, &opensearchapi.CatTemplatesReq{Templates: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.Templates(nil, nil) },
			},
		},
		"ThreadPool": []catTests{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cat.ThreadPool(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cat.ThreadPool(nil, &opensearchapi.CatThreadPoolReq{Pools: []string{"*"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cat.ThreadPool(nil, nil) },
			},
		},
	}
	for catType, value := range testCases {
		t.Run(catType, func(t *testing.T) {
			if catType == "ClusterManager" {
				ostest.SkipIfBelowVersion(t, client, 2, 0, catType)
			}
			for _, testCase := range value {
				t.Run(testCase.Name, func(t *testing.T) {
					resp, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.Nil(t, resp)
						assert.NotNil(t, httpResp)
						osapitest.VerifyResponse(t, httpResp)
					} else {
						assert.Nil(t, err)
						assert.NotNil(t, resp)
						assert.NotNil(t, httpResp)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Aliases", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Aliases(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Aliases, httpResp)
		})
		t.Run("Allocation", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Allocation(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Allocations, httpResp)
		})
		t.Run("ClusterManager", func(t *testing.T) {
			ostest.SkipIfBelowVersion(t, client, 2, 0, "ClusterManager")
			resp, httpResp, err := client.Cat.ClusterManager(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.ClusterManagers, httpResp)
		})
		t.Run("Count", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Count(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Counts, httpResp)
		})
		t.Run("FieldData", func(t *testing.T) {
			resp, httpResp, err := client.Cat.FieldData(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.FieldData, httpResp)
		})
		t.Run("Health", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Health(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Health, httpResp)
		})
		t.Run("Indices", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Indices(nil, &opensearchapi.CatIndicesReq{Params: opensearchapi.CatIndicesParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, httpResp)
		})
		t.Run("Master", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Master(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Master, httpResp)
		})
		t.Run("NodeAttrs", func(t *testing.T) {
			resp, httpResp, err := client.Cat.NodeAttrs(nil, &opensearchapi.CatNodeAttrsReq{Params: opensearchapi.CatNodeAttrsParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.NodeAttrs, httpResp)
		})
		t.Run("Nodes", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Nodes(nil, &opensearchapi.CatNodesReq{Params: opensearchapi.CatNodesParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Nodes, httpResp)
		})
		t.Run("PendingTasks", func(t *testing.T) {
			resp, httpResp, err := client.Cat.PendingTasks(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.PendingTasks, httpResp)
		})
		t.Run("Plugins", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Plugins(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Plugins, httpResp)
		})
		t.Run("Recovery", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Recovery(nil, &opensearchapi.CatRecoveryReq{Params: opensearchapi.CatRecoveryParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Recovery, httpResp)
		})
		t.Run("Repositories", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Repositories(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Repositories, httpResp)
		})
		t.Run("Segments", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Segments(nil, &opensearchapi.CatSegmentsReq{Params: opensearchapi.CatSegmentsParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Segments, httpResp)
		})
		t.Run("Shards", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Shards(nil, &opensearchapi.CatShardsReq{Params: opensearchapi.CatShardsParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Shards, httpResp)
		})
		/* Need to create Snapshot + Repo
		t.Run("Snapshots", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Snapshots(nil, &opensearchapi.CatSnapshotsReq{Repository: snapshotRepo, Params: opensearchapi.CatSnapshotsParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Snapshots, httpResp)
		})
		*/
		t.Run("Tasks", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Tasks(nil, &opensearchapi.CatTasksReq{Params: opensearchapi.CatTasksParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Tasks, httpResp)
		})
		t.Run("Templates", func(t *testing.T) {
			resp, httpResp, err := client.Cat.Templates(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Templates, httpResp)
		})
		t.Run("ThreadPool", func(t *testing.T) {
			resp, httpResp, err := client.Cat.ThreadPool(nil, &opensearchapi.CatThreadPoolReq{Params: opensearchapi.CatThreadPoolParams{H: []string{"*"}}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.ThreadPool, httpResp)
		})
	})
}
