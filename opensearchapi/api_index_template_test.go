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

func TestIndexTemplateClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	indexTemplate := "index-template-test"

	type indexTemplateTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []indexTemplateTests
	}{
		{
			Name: "Create",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.IndexTemplate.Create(
							nil,
							opensearchapi.IndexTemplateCreateReq{
								IndexTemplate: indexTemplate,
								Body:          strings.NewReader(`{"index_patterns":["index-template*"],"priority":60}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.IndexTemplate.Create(nil, opensearchapi.IndexTemplateCreateReq{IndexTemplate: indexTemplate})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.IndexTemplate.Get(nil, &opensearchapi.IndexTemplateGetReq{IndexTemplates: []string{indexTemplate}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.IndexTemplate.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = client.IndexTemplate.Exists(nil, opensearchapi.IndexTemplateExistsReq{IndexTemplate: indexTemplate})
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
						resp.response, err = failingClient.IndexTemplate.Exists(nil, opensearchapi.IndexTemplateExistsReq{IndexTemplate: indexTemplate})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Simulate",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.IndexTemplate.Simulate(
							nil,
							opensearchapi.IndexTemplateSimulateReq{
								IndexTemplate: indexTemplate,
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.IndexTemplate.Simulate(nil, opensearchapi.IndexTemplateSimulateReq{})
					},
				},
			},
		},
		{
			Name: "SimulateIndex",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.IndexTemplate.SimulateIndex(
							nil,
							opensearchapi.IndexTemplateSimulateIndexReq{
								Index: indexTemplate,
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.IndexTemplate.SimulateIndex(nil, opensearchapi.IndexTemplateSimulateIndexReq{})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []indexTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.IndexTemplate.Delete(nil, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: indexTemplate})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.IndexTemplate.Delete(nil, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: indexTemplate})
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
						if value.Name != "Exists" {
							osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
}
