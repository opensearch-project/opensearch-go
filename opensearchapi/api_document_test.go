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
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v2/opensearchutil"
)

func TestDocumentClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	index := "test-document"
	documentID := "test"

	t.Cleanup(func() { client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}}) })

	type docIndexPrep struct {
		DocCount int
		Body     string
	}

	type documentTests struct {
		Name         string
		IndexPrepare *docIndexPrep
		Results      func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []documentTests
	}{
		{
			Name: "Create",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Document.Create(
							nil,
							opensearchapi.DocumentCreateReq{
								Index:      index,
								Body:       strings.NewReader(`{"foo": "bar"}`),
								DocumentID: documentID,
								Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.Create(
							nil,
							opensearchapi.DocumentCreateReq{
								Index:      index,
								Body:       strings.NewReader("{}"),
								DocumentID: documentID,
							},
						)
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = client.Document.Exists(nil, opensearchapi.DocumentExistsReq{Index: index, DocumentID: documentID})
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
						resp.response, err = failingClient.Document.Exists(nil, opensearchapi.DocumentExistsReq{Index: index, DocumentID: documentID})
						return resp, err
					},
				},
			},
		},
		{
			Name: "ExistsSource",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp dummyInspect
							err  error
						)
						resp.response, err = client.Document.ExistsSource(nil, opensearchapi.DocumentExistsSourceReq{Index: index, DocumentID: documentID})
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
						resp.response, err = failingClient.Document.ExistsSource(nil, opensearchapi.DocumentExistsSourceReq{Index: index, DocumentID: documentID})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Document.Get(
							nil,
							opensearchapi.DocumentGetReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.Get(
							nil,
							opensearchapi.DocumentGetReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
			},
		},
		{
			Name: "Explain",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Document.Explain(
							nil,
							opensearchapi.DocumentExplainReq{
								Index:      index,
								DocumentID: documentID,
								Body:       strings.NewReader(`{"query":{"term":{"foo":{"value":"bar"}}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.Explain(
							nil,
							opensearchapi.DocumentExplainReq{
								Index:      index,
								DocumentID: documentID,
								Body:       strings.NewReader(``),
							},
						)
					},
				},
			},
		},
		{
			Name: "Source",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Document.Source(
							nil,
							opensearchapi.DocumentSourceReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.Source(
							nil,
							opensearchapi.DocumentSourceReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []documentTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Document.Delete(
							nil,
							opensearchapi.DocumentDeleteReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.Delete(
							nil,
							opensearchapi.DocumentDeleteReq{
								Index:      index,
								DocumentID: documentID,
							},
						)
					},
				},
			},
		},
		{
			Name: "DeleteByQuery",
			Tests: []documentTests{
				{
					Name:         "with request",
					IndexPrepare: &docIndexPrep{DocCount: 100, Body: `{"title":"bar"}`},
					Results: func() (osapitest.Response, error) {
						return client.Document.DeleteByQuery(
							nil,
							opensearchapi.DocumentDeleteByQueryReq{
								Indices: []string{index},
								Body:    strings.NewReader(`{"query":{"match":{"title":"bar"}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.DeleteByQuery(
							nil,
							opensearchapi.DocumentDeleteByQueryReq{
								Indices: []string{index},
								Body:    strings.NewReader(`{"query":{"match":{"title":"bar"}}}`),
							},
						)
					},
				},
			},
		},
		{
			Name: "DeleteByQueryRethrottle",
			Tests: []documentTests{
				{
					Name:         "with request",
					IndexPrepare: &docIndexPrep{DocCount: 10000, Body: `{"title":"foo"}`},
					Results: func() (osapitest.Response, error) {
						delResp, err := client.Document.DeleteByQuery(
							nil,
							opensearchapi.DocumentDeleteByQueryReq{
								Indices: []string{index},
								Body:    strings.NewReader(`{"query":{"match":{"title":"foo"}}}`),
								Params:  opensearchapi.DocumentDeleteByQueryParams{WaitForCompletion: opensearchapi.ToPointer(false)},
							},
						)
						if err != nil {
							return delResp, err
						}
						return client.Document.DeleteByQueryRethrottle(
							nil,
							opensearchapi.DocumentDeleteByQueryRethrottleReq{
								TaskID: delResp.Task,
								Params: opensearchapi.DocumentDeleteByQueryRethrottleParams{RequestsPerSecond: opensearchapi.ToPointer(50)},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Document.DeleteByQueryRethrottle(
							nil,
							opensearchapi.DocumentDeleteByQueryRethrottleReq{
								TaskID: "some-task-id",
								Params: opensearchapi.DocumentDeleteByQueryRethrottleParams{RequestsPerSecond: opensearchapi.ToPointer(50)},
							},
						)
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				if testCase.IndexPrepare != nil {
					bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
						Index:      index,
						Client:     client,
						ErrorTrace: true,
						Human:      true,
						Pretty:     true,
					})
					for i := 1; i <= testCase.IndexPrepare.DocCount; i++ {
						err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
							Index:      index,
							Action:     "index",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(testCase.IndexPrepare.Body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					if err := bi.Close(context.Background()); err != nil {
						t.Errorf("Unexpected error: %s", err)
					}
				}
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
						if !strings.Contains(value.Name, "Exists") && value.Name != "Source" {
							osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Source", func(t *testing.T) {
			_, err := client.Document.Create(
				nil,
				opensearchapi.DocumentCreateReq{
					Index:      index,
					Body:       strings.NewReader(`{"foo": "bar"}`),
					DocumentID: documentID,
				},
			)
			require.Nil(t, err)
			res, err := client.Document.Source(
				nil,
				opensearchapi.DocumentSourceReq{
					Index:      index,
					DocumentID: documentID,
				},
			)
			require.Nil(t, err)
			require.NotNil(t, res)
			assert.NotNil(t, res.Inspect().Response)
			osapitest.CompareRawJSONwithParsedJSON(t, res.Source, res.Inspect().Response)
		})
	})
}
