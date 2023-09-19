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

func TestComponentTemplateClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	componentTemplate := "component-template-test"

	type componentTemplateTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []componentTemplateTests
	}{
		{
			Name: "Create",
			Tests: []componentTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.ComponentTemplate.Create(
							nil,
							opensearchapi.ComponentTemplateCreateReq{
								ComponentTemplate: componentTemplate,
								Body:              strings.NewReader(`{"template":{"settings":{"index":{"number_of_shards":"2","number_of_replicas":"0"}}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.ComponentTemplate.Create(nil, opensearchapi.ComponentTemplateCreateReq{ComponentTemplate: componentTemplate})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []componentTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.ComponentTemplate.Get(nil, &opensearchapi.ComponentTemplateGetReq{ComponentTemplate: componentTemplate})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.ComponentTemplate.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []componentTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = client.ComponentTemplate.Exists(nil, opensearchapi.ComponentTemplateExistsReq{ComponentTemplate: componentTemplate})
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
						resp.response, err = failingClient.ComponentTemplate.Exists(nil, opensearchapi.ComponentTemplateExistsReq{ComponentTemplate: componentTemplate})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []componentTemplateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.ComponentTemplate.Delete(nil, opensearchapi.ComponentTemplateDeleteReq{ComponentTemplate: componentTemplate})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.ComponentTemplate.Delete(nil, opensearchapi.ComponentTemplateDeleteReq{ComponentTemplate: componentTemplate})
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
