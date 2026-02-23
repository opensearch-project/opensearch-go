// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
)

func TestNodes(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient(t)
	require.NoError(t, err)

	type nodesTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	// testCases contains non-disruptive node operations that are safe to run
	// at any time. ReloadSecurity is handled separately below because it
	// reloads TLS certificates on all nodes, dropping cluster connections.
	testCases := []struct {
		Name  string
		Tests []nodesTests
	}{
		{
			Name: "Stats",
			Tests: []nodesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Stats(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Stats(t.Context(), &opensearchapi.NodesStatsReq{
							NodeID:      []string{"*"},
							Metric:      []string{"indices"},
							IndexMetric: []string{"store"},
						})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Stats(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Info",
			Tests: []nodesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Info(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Info(t.Context(), &opensearchapi.NodesInfoReq{NodeID: []string{"*"}, Metrics: []string{"settings", "os"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Info(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "HotThreads",
			Tests: []nodesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Nodes.HotThreads(t.Context(), nil)
						return resp, err
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Nodes.HotThreads(t.Context(), &opensearchapi.NodesHotThreadsReq{NodeID: []string{"*"}})
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = failingClient.Nodes.HotThreads(t.Context(), nil)
						return resp, err
					},
				},
			},
		},
		{
			Name: "Usage",
			Tests: []nodesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Usage(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Usage(t.Context(), &opensearchapi.NodesUsageReq{NodeID: []string{"*"}, Metrics: []string{"*"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Usage(t.Context(), nil)
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
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
						if value.Name != "HotThreads" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}

	// ReloadSecurity reloads TLS certificates on all nodes, which drops
	// every in-flight connection cluster-wide. This destabilizes the shared
	// test cluster for minutes afterward (security plugin re-initialization,
	// TLS renegotiation, etc.), causing cascading failures in subsequent and
	// concurrent tests.
	//
	// TODO: re-enable once we have a custom post-reload health check that
	// confirms the security plugin has fully re-initialized (not just that
	// the HTTP endpoint responds). The standard WaitForClusterReady is
	// insufficient because the security plugin can return 503
	// "OpenSearch Security not initialized" for an extended period after
	// reload completes.
	t.Run("ReloadSecurity", func(t *testing.T) {
		t.Skip("Skipped: ReloadSecurity destabilizes the shared test cluster; needs custom post-reload health check")

		t.Run("without request", func(t *testing.T) {
			res, err := client.Nodes.ReloadSecurity(t.Context(), nil)
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		})

		t.Run("with request", func(t *testing.T) {
			res, err := client.Nodes.ReloadSecurity(t.Context(), &opensearchapi.NodesReloadSecurityReq{NodeID: []string{"*"}})
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			res, err := failingClient.Nodes.ReloadSecurity(t.Context(), nil)
			require.Error(t, err)
			assert.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})
}
