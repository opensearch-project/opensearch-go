// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//go:build integration

package opensearchapi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

func TestPointInTimeClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	osapitest.SkipIfBelowVersion(t, client, 2, 4, "Point_In_Time")
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

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
							nil,
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
					Results: func() (osapitest.Response, error) {
						return client.PointInTime.Get(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
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
					Results: func() (osapitest.Response, error) {
						return client.PointInTime.Delete(nil, opensearchapi.PointInTimeDeleteReq{PitID: []string{pitID}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
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
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
