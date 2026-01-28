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

const (
	willErrorPrefix = "WILL_ERROR: "
)

func TestClusterClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-cluster-indices")
	indexWithUnassigned := testutil.MustUniqueString(t, "test-cluster-unassigned")
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index, indexWithUnassigned}})
	})

	// Create a regular index
	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	// Create an index with more replicas than available nodes to ensure unassigned shards
	// This will create unassigned replica shards that AllocationExplain can analyze
	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{
		Index: indexWithUnassigned,
		Body: strings.NewReader(`{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 10
			}
		}`),
	})
	require.NoError(t, err)

	type clusterTest struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := map[string][]clusterTest{
		"AllocationExplain": {
			{
				Name:    "with nil request - finds unassigned shards",
				Results: func() (osapitest.Response, error) { return client.Cluster.AllocationExplain(t.Context(), nil) },
			},
			{
				Name: "with request - explains specific unassigned replica shard",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{Body: &opensearchapi.ClusterAllocationExplainBody{
							Index: indexWithUnassigned, Shard: 0, Primary: false,
						}})
				},
			},
			{
				Name: willErrorPrefix + "with request for non-existent index",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{Body: &opensearchapi.ClusterAllocationExplainBody{
							Index: "non-existent-index-12345", Shard: 0, Primary: true,
						}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.AllocationExplain(t.Context(), nil) },
			},
		},
		"Health": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.Health(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.Health(t.Context(), &opensearchapi.ClusterHealthReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.Health(t.Context(), nil) },
			},
		},
		"PendingTasks": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.PendingTasks(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.PendingTasks(t.Context(), &opensearchapi.ClusterPendingTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.PendingTasks(t.Context(), nil) },
			},
		},
		"GetSettings": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.GetSettings(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.GetSettings(t.Context(), &opensearchapi.ClusterGetSettingsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.GetSettings(t.Context(), nil) },
			},
		},
		"PutSettings": {
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.PutSettings(t.Context(),
						opensearchapi.ClusterPutSettingsReq{Body: strings.NewReader(
							`{"transient":{"indices":{"recovery":{"max_bytes_per_sec":null}}}}`)})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.PutSettings(t.Context(), opensearchapi.ClusterPutSettingsReq{})
				},
			},
		},
		"Reroute": {
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.Reroute(t.Context(), opensearchapi.ClusterRerouteReq{Body: strings.NewReader(`{}`)})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.Reroute(t.Context(), opensearchapi.ClusterRerouteReq{Body: strings.NewReader(`{}`)})
				},
			},
		},
		"State": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.State(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.State(t.Context(), &opensearchapi.ClusterStateReq{Metrics: []string{"_all"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.State(t.Context(), nil) },
			},
		},
		"Stats": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.Stats(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.Stats(t.Context(), &opensearchapi.ClusterStatsReq{NodeFilters: []string{"data:true"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.Stats(t.Context(), nil) },
			},
		},
		"PutDecommission": {
			/* Needs node awarness attr to work
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.PutDecommission(t.Context(),
						opensearchapi.ClusterPutDecommissionReq{AwarenessAttrName: "test", AwarenessAttrValue: "test"})
				},
			},
			*/
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.PutDecommission(t.Context(),
						opensearchapi.ClusterPutDecommissionReq{AwarenessAttrName: "test", AwarenessAttrValue: "test"})
				},
			},
		},
		"GetDecommission": {
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.GetDecommission(t.Context(), opensearchapi.ClusterGetDecommissionReq{AwarenessAttrName: "test"})
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.GetDecommission(t.Context(), opensearchapi.ClusterGetDecommissionReq{AwarenessAttrName: "test"})
				},
			},
		},
		"DeleteDecommission": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.DeleteDecommission(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.DeleteDecommission(t.Context(), &opensearchapi.ClusterDeleteDecommissionReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.DeleteDecommission(t.Context(), nil) },
			},
		},
		"RemoteInfo": {
			{
				Name:    "with nil request",
				Results: func() (osapitest.Response, error) { return client.Cluster.RemoteInfo(t.Context(), nil) },
			},
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.RemoteInfo(t.Context(), &opensearchapi.ClusterRemoteInfoReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (osapitest.Response, error) { return failingClient.Cluster.RemoteInfo(t.Context(), nil) },
			},
		},
	}
	for catType, value := range testCases {
		t.Run(catType, func(t *testing.T) {
			if strings.Contains(catType, "Decommission") {
				testutil.SkipIfBelowVersion(t, client, 2, 4, catType)
			}
			for _, testCase := range value {
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results()
					switch {
					case testCase.Name == "inspect":
						require.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					case strings.HasPrefix(testCase.Name, willErrorPrefix):
						// Test cases that are expected to fail
						require.Error(t, err)
						assert.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
					default:
						// Test cases that are expected to succeed
						require.NoError(t, err)
						assert.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
