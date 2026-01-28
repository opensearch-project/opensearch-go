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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestClusterClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-cluster-indices")
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	// Default validation function for most test cases
	validateDefault := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.Nil(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
	}

	// Validation function for "inspect" test cases (failing client)
	validateInspect := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.NotNil(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	}

	// Validation function for AllocationExplain with nil request
	validateAllocationExplainNil := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// AllocationExplain with nil request in a healthy cluster should return
		// "unable to find any unassigned shards" error - this is expected behavior
		if err != nil {
			// Check if it's the expected "no unassigned shards" error
			expectedErr := "unable to find any unassigned shards to explain"
			if strings.Contains(err.Error(), expectedErr) {
				// This is expected for a healthy cluster - treat as success
				require.NotNil(t, res)
				require.NotNil(t, res.Inspect().Response)
			} else {
				t.Errorf("Unexpected error for AllocationExplain: %v", err)
			}
		} else {
			// No error means there were unassigned shards - also valid
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		}
	}

	// Validation function for AllocationExplain with specific shard request (positive case)
	validateAllocationExplainPositive := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// This should succeed and return explanation data for the assigned shard
		require.Nil(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)

		// Verify we got a successful HTTP response
		response := res.Inspect().Response
		require.Equal(t, 200, response.StatusCode, "Expected 200 OK for successful allocation explanation")
	}

	// Validation function for AllocationExplain with unassigned shard request (tests target_node field)
	validateAllocationExplainUnassigned := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		// The unassigned shard request tests our target_node field functionality
		if err != nil {
			// Check if it's the expected "no unassigned shards" error
			expectedErr := "unable to find any unassigned shards to explain"
			if strings.Contains(err.Error(), expectedErr) {
				// This is expected for a healthy cluster - treat as success
				require.NotNil(t, res)
				require.NotNil(t, res.Inspect().Response)
				t.Logf("Expected 'no unassigned shards' error in healthy cluster for unassigned shard test: %v", err)
			} else {
				// Some other unexpected error
				t.Errorf("Unexpected error for AllocationExplain unassigned shard test: %v", err)
			}
		} else {
			// No error means there were unassigned shards - test target_node field
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		}
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
				Name: "with request",
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
				Name: "with unassigned shard request",
				Results: func() (osapitest.Response, error) {
					return client.Cluster.AllocationExplain(
						t.Context(),
						&opensearchapi.ClusterAllocationExplainReq{
							Body: &opensearchapi.ClusterAllocationExplainBody{
								Index:   index,
								Shard:   0,
								Primary: false,
							},
						},
					)
				},
				Validate: validateAllocationExplainUnassigned,
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
				ostest.SkipIfBelowVersion(t, client, 2, 4, catType)
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
