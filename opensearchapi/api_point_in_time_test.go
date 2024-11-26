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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestPointInTimeClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	ostest.SkipIfBelowVersion(t, client, 2, 4, "Point_In_Time")
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	pitID := ""

	type pointInTimeTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
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
					Results: func() (any, *opensearch.Response, error) {
						keepAlive, _ := time.ParseDuration("5m")
						resp, httpResp, err := client.PointInTime.Create(
							nil,
							opensearchapi.PointInTimeCreateReq{
								Indices: []string{"*"},
								Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: keepAlive},
							},
						)
						pitID = resp.PitID
						return resp, httpResp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.PointInTime.Create(nil, opensearchapi.PointInTimeCreateReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []pointInTimeTests{
				{
					Name: "without request",
					Results: func() (any, *opensearch.Response, error) {
						return client.PointInTime.Get(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.PointInTime.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []pointInTimeTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.PointInTime.Delete(nil, opensearchapi.PointInTimeDeleteReq{PitID: []string{pitID}})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.PointInTime.Delete(nil, opensearchapi.PointInTimeDeleteReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					resp, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.Nil(t, resp)
						assert.NotNil(t, httpResp)
						osapitest.VerifyResponse(t, httpResp)
					} else {
						require.Nil(t, err)
						require.NotNil(t, resp)
						assert.NotNil(t, httpResp)
						ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
					}
				})
			}
		})
	}
}
