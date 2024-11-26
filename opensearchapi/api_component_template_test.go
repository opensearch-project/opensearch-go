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

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestComponentTemplateClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	componentTemplate := "component-template-test"

	type componentTemplateTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
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
					Results: func() (any, *opensearch.Response, error) {
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
					Results: func() (any, *opensearch.Response, error) {
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
					Results: func() (any, *opensearch.Response, error) {
						return client.ComponentTemplate.Get(nil, &opensearchapi.ComponentTemplateGetReq{ComponentTemplate: componentTemplate})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
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
					Results: func() (any, *opensearch.Response, error) {
						httpResp, err := client.ComponentTemplate.Exists(nil, opensearchapi.ComponentTemplateExistsReq{ComponentTemplate: componentTemplate})

						return nil, httpResp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						httpResp, err := failingClient.ComponentTemplate.Exists(nil, opensearchapi.ComponentTemplateExistsReq{ComponentTemplate: componentTemplate})

						return nil, httpResp, err
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []componentTemplateTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.ComponentTemplate.Delete(nil, opensearchapi.ComponentTemplateDeleteReq{ComponentTemplate: componentTemplate})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
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
					resp, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.Nil(t, resp)
						assert.NotNil(t, httpResp)
						osapitest.VerifyResponse(t, httpResp)
					} else {
						require.Nil(t, err)
						if value.Name != "Exists" {
							require.NotNil(t, resp)
							ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
						} else {
							assert.Nil(t, resp)
						}
						assert.NotNil(t, httpResp)
					}
				})
			}
		})
	}
}
