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

func TestClusterClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	index := "test-cluster-indices"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, _, err = client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
	require.Nil(t, err)

	type clusterTest struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
	}

	testCases := map[string][]clusterTest{
		"AllocationExplain": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.AllocationExplain(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.AllocationExplain(nil, &opensearchapi.ClusterAllocationExplainReq{Body: &opensearchapi.ClusterAllocationExplainBody{Index: index, Shard: 0, Primary: true}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.AllocationExplain(nil, nil) },
			},
		},
		"Health": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.Health(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.Health(nil, &opensearchapi.ClusterHealthReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.Health(nil, nil) },
			},
		},
		"PendingTasks": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.PendingTasks(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.PendingTasks(nil, &opensearchapi.ClusterPendingTasksReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.PendingTasks(nil, nil) },
			},
		},
		"GetSettings": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.GetSettings(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.GetSettings(nil, &opensearchapi.ClusterGetSettingsReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.GetSettings(nil, nil) },
			},
		},
		"PutSettings": []clusterTest{
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.PutSettings(nil, opensearchapi.ClusterPutSettingsReq{Body: strings.NewReader(`{"transient":{"indices":{"recovery":{"max_bytes_per_sec":null}}}}`)})
				},
			},
			{
				Name: "inspect",
				Results: func() (any, *opensearch.Response, error) {
					return failingClient.Cluster.PutSettings(nil, opensearchapi.ClusterPutSettingsReq{})
				},
			},
		},
		"Reroute": []clusterTest{
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.Reroute(nil, opensearchapi.ClusterRerouteReq{Body: strings.NewReader(`{}`)})
				},
			},
			{
				Name: "inspect",
				Results: func() (any, *opensearch.Response, error) {
					return failingClient.Cluster.Reroute(nil, opensearchapi.ClusterRerouteReq{Body: strings.NewReader(`{}`)})
				},
			},
		},
		"State": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.State(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.State(nil, &opensearchapi.ClusterStateReq{Metrics: []string{"_all"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.State(nil, nil) },
			},
		},
		"Stats": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.Stats(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.Stats(nil, &opensearchapi.ClusterStatsReq{NodeFilters: []string{"data:true"}})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.Stats(nil, nil) },
			},
		},
		"PutDecommission": []clusterTest{
			/* Needs node awarness attr to work
			{
				Name: "with request",
				Results: func() (any,opensearch.Response, error) {
					return client.Cluster.PutDecommission(nil, opensearchapi.ClusterPutDecommissionReq{AwarenessAttrName: "test", AwarenessAttrValue: "test"})
				},
			},
			*/
			{
				Name: "inspect",
				Results: func() (any, *opensearch.Response, error) {
					return failingClient.Cluster.PutDecommission(nil, opensearchapi.ClusterPutDecommissionReq{AwarenessAttrName: "test", AwarenessAttrValue: "test"})
				},
			},
		},
		"GetDecommission": []clusterTest{
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.GetDecommission(nil, opensearchapi.ClusterGetDecommissionReq{AwarenessAttrName: "test"})
				},
			},
			{
				Name: "inspect",
				Results: func() (any, *opensearch.Response, error) {
					return failingClient.Cluster.GetDecommission(nil, opensearchapi.ClusterGetDecommissionReq{AwarenessAttrName: "test"})
				},
			},
		},
		"DeleteDecommission": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.DeleteDecommission(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.DeleteDecommission(nil, &opensearchapi.ClusterDeleteDecommissionReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.DeleteDecommission(nil, nil) },
			},
		},
		"RemoteInfo": []clusterTest{
			{
				Name:    "with nil request",
				Results: func() (any, *opensearch.Response, error) { return client.Cluster.RemoteInfo(nil, nil) },
			},
			{
				Name: "with request",
				Results: func() (any, *opensearch.Response, error) {
					return client.Cluster.RemoteInfo(nil, &opensearchapi.ClusterRemoteInfoReq{})
				},
			},
			{
				Name:    "inspect",
				Results: func() (any, *opensearch.Response, error) { return failingClient.Cluster.RemoteInfo(nil, nil) },
			},
		},
	}
	for catType, value := range testCases {
		t.Run(catType, func(t *testing.T) {
			if strings.Contains(catType, "Decommission") {
				ostest.SkipIfBelowVersion(t, client, 2, 4, catType)
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
						ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
					}
				})
			}
		})
	}
}
