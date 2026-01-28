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

func TestTemplateClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	template := "index-template-test"

	type templateTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []templateTests
	}{
		{
			Name: "Create",
			Tests: []templateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Template.Create(
							t.Context(),
							opensearchapi.TemplateCreateReq{
								Template: template,
								Body:     strings.NewReader(`{"order":1,"index_patterns":["index-template-test"],"aliases":{"test-1234":{}},"version":1}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Template.Create(t.Context(), opensearchapi.TemplateCreateReq{Template: template})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []templateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Template.Get(t.Context(), &opensearchapi.TemplateGetReq{Templates: []string{template}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Template.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []templateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Template.Exists(t.Context(), opensearchapi.TemplateExistsReq{Template: template})
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
						resp.Response, err = failingClient.Template.Exists(t.Context(), opensearchapi.TemplateExistsReq{Template: template})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []templateTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Template.Delete(t.Context(), opensearchapi.TemplateDeleteReq{Template: template})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Template.Delete(t.Context(), opensearchapi.TemplateDeleteReq{Template: template})
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
						assert.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" && value.Name != "Exists" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			_, err := client.Template.Create(
				t.Context(),
				opensearchapi.TemplateCreateReq{
					Template: template,
					Body:     strings.NewReader(`{"order":1,"index_patterns":["index-template-test"],"aliases":{"test-1234":{}},"version":1}`),
				},
			)
			require.NoError(t, err)
			resp, err := client.Template.Get(t.Context(), &opensearchapi.TemplateGetReq{Templates: []string{template}})
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, resp.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Templates, resp.Inspect().Response)
		})
	})
}
