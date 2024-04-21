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

func TestIndexTemplateClient(t *testing.T) {
	client, err := ostest.NewClient()
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
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.IndexTemplate.Exists(nil, opensearchapi.IndexTemplateExistsReq{IndexTemplate: indexTemplate})
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = failingClient.IndexTemplate.Exists(nil, opensearchapi.IndexTemplateExistsReq{IndexTemplate: indexTemplate})
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
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
}
