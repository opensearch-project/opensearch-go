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

func TestScriptClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	scriptID := "test-script"

	type scriptTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []scriptTests
	}{
		{
			Name: "Put",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.Put(
							nil,
							opensearchapi.ScriptPutReq{
								ScriptID: scriptID,
								Body:     strings.NewReader(`{"script":{"lang":"painless","source":"\n          int total = 0;\n          for (int i = 0; i < doc['ratings'].length; ++i) {\n            total += doc['ratings'][i];\n          }\n          return total;\n        "}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.Put(nil, opensearchapi.ScriptPutReq{ScriptID: scriptID})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.Get(nil, opensearchapi.ScriptGetReq{ScriptID: scriptID})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.Get(nil, opensearchapi.ScriptGetReq{ScriptID: scriptID})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.Delete(nil, opensearchapi.ScriptDeleteReq{ScriptID: scriptID})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.Delete(nil, opensearchapi.ScriptDeleteReq{ScriptID: scriptID})
					},
				},
			},
		},
		{
			Name: "Context",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.Context(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.Context(nil, nil)
					},
				},
			},
		},
		{
			Name: "Language",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.Language(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.Language(nil, nil)
					},
				},
			},
		},
		{
			Name: "PainlessExecute",
			Tests: []scriptTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Script.PainlessExecute(
							nil,
							opensearchapi.ScriptPainlessExecuteReq{Body: strings.NewReader(`{"script":{"source":"(params.x + params.y)/ 2","params":{"x":80,"y":100}}}`)},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Script.PainlessExecute(
							nil,
							opensearchapi.ScriptPainlessExecuteReq{Body: strings.NewReader(`{"script":{"source":"(params.x + params.y)/ 2","params":{"x":80,"y":100}}}`)},
						)
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			if strings.Contains(value.Name, "Language") {
				ostest.SkipIfBelowVersion(t, client, 2, 3, value.Name)
			}
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
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Put", func(t *testing.T) {
			resp, err := client.Script.Put(
				nil,
				opensearchapi.ScriptPutReq{
					ScriptID: scriptID,
					Body:     strings.NewReader(`{"script":{"lang":"painless","source":"\n          int total = 0;\n          for (int i = 0; i < doc['ratings'].length; ++i) {\n            total += doc['ratings'][i];\n          }\n          return total;\n        "}}`),
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Get", func(t *testing.T) {
			resp, err := client.Script.Get(nil, opensearchapi.ScriptGetReq{ScriptID: scriptID})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Delete", func(t *testing.T) {
			resp, err := client.Script.Delete(nil, opensearchapi.ScriptDeleteReq{ScriptID: scriptID})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Context", func(t *testing.T) {
			resp, err := client.Script.Context(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Language", func(t *testing.T) {
			ostest.SkipIfBelowVersion(t, client, 2, 3, "Language")
			resp, err := client.Script.Language(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("PainlessExecute", func(t *testing.T) {
			resp, err := client.Script.PainlessExecute(
				nil,
				opensearchapi.ScriptPainlessExecuteReq{Body: strings.NewReader(`{"script":{"source":"(params.x + params.y)/ 2","params":{"x":80,"y":100}}}`)},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	})
}
