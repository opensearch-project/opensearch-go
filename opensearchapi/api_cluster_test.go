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

	// Default validation function for most test cases
	validateDefault := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
	}

	// Validation function for "inspect" test cases (failing client)
	validateInspect := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	}

	// Validation function for AllocationExplain with nil request
	validateAllocationExplainNil := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// AllocationExplain with nil request should succeed now that we have indexWithUnassigned
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
	}

	// Validation function for AllocationExplain with specific shard request (positive case)
	validateAllocationExplainPositive := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// This should succeed and return explanation data for the shard
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)

		// Verify we got a successful HTTP response
		response := res.Inspect().Response
		require.Equal(t, 200, response.StatusCode, "Expected 200 OK for successful allocation explanation")
	}

	// Validation function for expected error cases
	validateExpectedError := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// Test cases that are expected to fail
		require.Error(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
	}

	type clusterTest struct {
		Name     string
		Results  func() (osapitest.Response, error)
		Validate func(*testing.T, osapitest.Response, error) // Custom validation function
	}

	testCases := map[string][]clusterTest{
		"AllocationExplain": {
			{
				Name:     "with nil request",
				Results:  func() (osapitest.Response, error) { return client.Cluster.AllocationExplain(t.Context(), nil) },
				Validate: validateAllocationExplainNil,
			},
			{
				Name: "with request - explains specific unassigned replica shard",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(
						t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{
							Body: &opensearchapi.ClusterAllocationExplainBody{
								Index:   index,
								Shard:   0,
								Primary: true,
							},
						},
					)
				},
				Validate: validateAllocationExplainPositive,
			},
			{
				Name: willErrorPrefix + "with request for non-existent index",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{Body: &opensearchapi.ClusterAllocationExplainBody{
							Index: "non-existent-index-12345", Shard: 0, Primary: true,
						}})
				},
				Validate: validateExpectedError,
			},
			{
				Name: "with unassigned shard request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(
						t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{
							Body: &opensearchapi.ClusterAllocationExplainBody{
								Index:   indexWithUnassigned,
								Shard:   0,
								Primary: false,
							},
						},
					)
				},
				Validate: validateAllocationExplainPositive,
			},
			{
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.AllocationExplain(t.Context(), nil) },
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.Health(t.Context(), nil) },
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.PendingTasks(t.Context(), nil) },
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.GetSettings(t.Context(), nil) },
				Validate: validateInspect,
			},
		},
		"PutSettings": {
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.PutSettings(
						t.Context(),
						opensearchapi.ClusterPutSettingsReq{
							Body: strings.NewReader(
								`{"transient":{"indices":{"recovery":{"max_bytes_per_sec":null}}}}`,
							),
						},
					)
				},
			},
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.PutSettings(t.Context(), opensearchapi.ClusterPutSettingsReq{})
				},
				Validate: validateInspect,
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
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.State(t.Context(), nil) },
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.Stats(t.Context(), nil) },
				Validate: validateInspect,
			},
		},
		"PutDecommission": {
			/* Needs node awarness attr to work
			{
				Name: "with request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.PutDecommission(
						t.Context(),
						opensearchapi.ClusterPutDecommissionReq{
							AwarenessAttrName:  "test",
							AwarenessAttrValue: "test",
						},
					)
				},
			},
			*/
			{
				Name: "inspect",
				Results: func() (osapitest.Response, error) {
					return failingClient.Cluster.PutDecommission(
						t.Context(),
						opensearchapi.ClusterPutDecommissionReq{
							AwarenessAttrName:  "test",
							AwarenessAttrValue: "test",
						},
					)
				},
				Validate: validateInspect,
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
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.DeleteDecommission(t.Context(), nil) },
				Validate: validateInspect,
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
				Name:     "inspect",
				Results:  func() (osapitest.Response, error) { return failingClient.Cluster.RemoteInfo(t.Context(), nil) },
				Validate: validateInspect,
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
					if testCase.Validate != nil {
						testCase.Validate(t, res, err)
					} else {
						validateDefault(t, res, err)
					}
				})
			}
		})
	}
}
