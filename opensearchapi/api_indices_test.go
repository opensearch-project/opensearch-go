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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

type dummyInspect struct {
	response *opensearch.Response
}

func (r dummyInspect) Inspect() opensearchapi.Inspect {
	return opensearchapi.Inspect{Response: r.response}
}

func TestIndicesClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	index := "test-indices-create"
	_, err = client.Indices.Delete(
		nil,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{index},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	require.Nil(t, err)

	type indicesTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []indicesTests
	}{
		{
			Name: "Create",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = client.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = failingClient.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Block",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
					},
				},
			},
		},
		{
			Name: "Analyze",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
					},
				},
			},
		},
		{
			Name: "ClearCache",
			Tests: []indicesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ClearCache(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ClearCache(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
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
						assert.Nil(t, err)
						assert.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		index := "test-indices-validate"
		t.Run("Create", func(t *testing.T) {
			resp, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Block", func(t *testing.T) {
			resp, err := client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Analyze", func(t *testing.T) {
			resp, err := client.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ClearCache", func(t *testing.T) {
			resp, err := client.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Delete", func(t *testing.T) {
			resp, err := client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	})
}
