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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
)

func TestPointInTimeClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfBelowVersion(t, client, 2, 4, "Point_In_Time")
	failingClient, err := osapitest.CreateFailingClient(t)
	require.NoError(t, err)

	// Create a dedicated index so the PIT isn't affected by other parallel tests
	// creating/deleting indices on the cluster.
	index := testutil.MustUniqueString(t, "test-pit")
	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Indices.Delete(context.Background(), opensearchapi.IndicesDeleteReq{
			Indices: []string{index},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		})
	})

	pitID := ""

	type pointInTimeTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []pointInTimeTests
	}{
		{
			Name: "Create",
			Tests: []pointInTimeTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						// PIT Create requires cross-shard coordination. Retry on
						// transient inter-node failures (node_not_connected).
						keepAlive, _ := time.ParseDuration("5m")
						var resp *opensearchapi.PointInTimeCreateResp
						var lastErr error
						for attempt := range 5 {
							resp, lastErr = client.PointInTime.Create(
								t.Context(),
								opensearchapi.PointInTimeCreateReq{
									Indices: []string{index},
									Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: keepAlive},
								},
							)
							if lastErr == nil {
								pitID = resp.PitID
								return resp, nil
							}
							if !strings.Contains(lastErr.Error(), "node_not_connected") &&
								!strings.Contains(lastErr.Error(), "connection already closed") {
								break
							}
							t.Logf("PIT Create attempt %d failed (transient): %v", attempt+1, lastErr)
							time.Sleep(2 * time.Second)
						}
						return resp, lastErr
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.PointInTime.Create(t.Context(), opensearchapi.PointInTimeCreateReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []pointInTimeTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.PointInTime.Get(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.PointInTime.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []pointInTimeTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.PointInTime.Delete(t.Context(), opensearchapi.PointInTimeDeleteReq{PitID: []string{pitID}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.PointInTime.Delete(t.Context(), opensearchapi.PointInTimeDeleteReq{})
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
						testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
