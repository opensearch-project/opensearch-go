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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

func TestDataStreamClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	dataStream := "data-stream-test"

	_, err = client.IndexTemplate.Create(
		nil,
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: dataStream,
			Body:          strings.NewReader(`{"index_patterns":["data-stream-test"],"template":{"settings":{"index":{"number_of_replicas":"0"}}},"priority":60,"data_stream":{}}`),
		},
	)
	require.Nil(t, err)

	type dataStreamTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []dataStreamTests
	}{
		{
			Name: "Create",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Create(nil, opensearchapi.DataStreamCreateReq{DataStream: dataStream})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Create(nil, opensearchapi.DataStreamCreateReq{DataStream: dataStream})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Get(nil, &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Stats",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Stats(nil, &opensearchapi.DataStreamStatsReq{DataStreams: []string{dataStream}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Stats(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Delete(nil, opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Delete(nil, opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
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
