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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestPointInTimeClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)
	ostest.SkipIfBelowVersion(t, client, 2, 4, "Point_In_Time")
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

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
						keepAlive, _ := time.ParseDuration("5m")
						resp, err := client.PointInTime.Create(
							t.Context(),
							opensearchapi.PointInTimeCreateReq{
								Indices: []string{"*"},
								Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: keepAlive},
							},
						)
						pitID = resp.PitID
						return resp, err
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
						assert.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
