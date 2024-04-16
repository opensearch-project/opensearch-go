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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestNodes(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	type nodesTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

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
						return client.Nodes.Stats(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Stats(nil, &opensearchapi.NodesStatsReq{NodeID: []string{"*"}, Metric: []string{"indices"}, IndexMetric: []string{"store"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Stats(nil, nil)
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
						return client.Nodes.Info(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Info(nil, &opensearchapi.NodesInfoReq{NodeID: []string{"*"}, Metrics: []string{"settings", "os"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Info(nil, nil)
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
						resp.Response, err = client.Nodes.HotThreads(nil, nil)
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
						resp.Response, err = client.Nodes.HotThreads(nil, &opensearchapi.NodesHotThreadsReq{NodeID: []string{"*"}})
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
						resp.Response, err = failingClient.Nodes.HotThreads(nil, nil)
						return resp, err
					},
				},
			},
		},
		{
			Name: "ReloadSecurity",
			Tests: []nodesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.ReloadSecurity(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.ReloadSecurity(nil, &opensearchapi.NodesReloadSecurityReq{NodeID: []string{"*"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.ReloadSecurity(nil, nil)
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
						return client.Nodes.Usage(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Nodes.Usage(nil, &opensearchapi.NodesUsageReq{NodeID: []string{"*"}, Metrics: []string{"*"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Nodes.Usage(nil, nil)
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
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "HotThreads" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
}
