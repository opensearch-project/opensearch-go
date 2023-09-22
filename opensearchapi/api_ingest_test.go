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

func TestIngestClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	ingest := "ingest-test"

	type ingestTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []ingestTests
	}{
		{
			Name: "Create",
			Tests: []ingestTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Ingest.Create(
							nil,
							opensearchapi.IngestCreateReq{
								PipelineID: ingest,
								Body:       strings.NewReader(`{"description":"This pipeline processes student data","processors":[{"set":{"description":"Sets the graduation year to 2023","field":"grad_year","value":2023}},{"set":{"description":"Sets graduated to true","field":"graduated","value":true}}]}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Ingest.Create(nil, opensearchapi.IngestCreateReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []ingestTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Ingest.Get(nil, &opensearchapi.IngestGetReq{PipelineIDs: []string{ingest}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Ingest.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Grok",
			Tests: []ingestTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Ingest.Grok(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Ingest.Grok(nil, nil)
					},
				},
			},
		},
		{
			Name: "Simulate",
			Tests: []ingestTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Ingest.Simulate(
							nil,
							opensearchapi.IngestSimulateReq{
								PipelineID: ingest,
								Body:       strings.NewReader(`{"docs":[{"_index":"my-index","_id":"1","_source":{"grad_year":2024,"graduated":false,"name":"John Doe"}}]}`),
								Params:     opensearchapi.IngestSimulateParams{Verbose: opensearchapi.ToPointer(true), Pretty: true, Human: true, ErrorTrace: true},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Ingest.Simulate(nil, opensearchapi.IngestSimulateReq{})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []ingestTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Ingest.Delete(nil, opensearchapi.IngestDeleteReq{PipelineID: ingest})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Ingest.Delete(nil, opensearchapi.IngestDeleteReq{PipelineID: ingest})
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
						if value.Name != "Get" {
							osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			t.Cleanup(func() {
				failingClient.Ingest.Delete(nil, opensearchapi.IngestDeleteReq{PipelineID: ingest})
			})
			_, err := client.Ingest.Create(
				nil,
				opensearchapi.IngestCreateReq{
					PipelineID: ingest,
					Body:       strings.NewReader(`{"description":"This pipeline processes student data","processors":[{"set":{"description":"Sets the graduation year to 2023","field":"grad_year","value":2023}},{"set":{"description":"Sets graduated to true","field":"graduated","value":true}}]}`),
				},
			)
			require.Nil(t, err)

			resp, err := client.Ingest.Get(nil, nil)
			require.Nil(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, resp.Inspect().Response)
			osapitest.CompareRawJSONwithParsedJSON(t, resp.Pipelines, resp.Inspect().Response)
		})
	})
}
