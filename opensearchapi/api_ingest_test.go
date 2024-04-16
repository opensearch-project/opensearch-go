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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestIngestClient(t *testing.T) {
	client, err := ostest.NewClient()
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
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
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
			ostest.CompareRawJSONwithParsedJSON(t, resp.Pipelines, resp.Inspect().Response)
		})
	})
}
