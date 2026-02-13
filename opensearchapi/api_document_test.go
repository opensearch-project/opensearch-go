// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestDocumentClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	// Create unique index and document ID per test execution to avoid conflicts
	index := testutil.MustUniqueString(t, "test-document")
	documentID := testutil.MustUniqueString(t, "test-doc")

	t.Cleanup(func() { client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}}) })

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
							t.Context(),
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
							t.Context(),
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
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Document.Exists(t.Context(), opensearchapi.DocumentExistsReq{Index: index, DocumentID: documentID})
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
						resp.Response, err = failingClient.Document.Exists(t.Context(), opensearchapi.DocumentExistsReq{Index: index, DocumentID: documentID})
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
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Document.ExistsSource(
							t.Context(),
							opensearchapi.DocumentExistsSourceReq{Index: index, DocumentID: documentID},
						)
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
						resp.Response, err = failingClient.Document.ExistsSource(
							t.Context(),
							opensearchapi.DocumentExistsSourceReq{Index: index, DocumentID: documentID},
						)
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
							t.Context(),
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
						err := bi.Add(t.Context(), opensearchutil.BulkIndexerItem{
							Index:      index,
							Action:     "index",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(testCase.IndexPrepare.Body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					if err := bi.Close(t.Context()); err != nil {
						t.Errorf("Unexpected error: %s", err)
					}
				}
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if !strings.Contains(value.Name, "Exists") && value.Name != "Source" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Source", func(t *testing.T) {
			_, err := client.Document.Create(
				t.Context(),
				opensearchapi.DocumentCreateReq{
					Index:      index,
					Body:       strings.NewReader(`{"foo": "bar"}`),
					DocumentID: documentID,
				},
			)
			require.NoError(t, err)
			res, err := client.Document.Source(
				t.Context(),
				opensearchapi.DocumentSourceReq{
					Index:      index,
					DocumentID: documentID,
				},
			)
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res.Source, res.Inspect().Response)
		})
		t.Run("Fields", func(t *testing.T) {
			// Create unique document ID for this test
			storedFieldDocID := testutil.MustUniqueString(t, "test-stored-field")

			_, err := client.Indices.Mapping.Put(t.Context(),
				opensearchapi.MappingPutReq{
					Indices: []string{index},
					Body: strings.NewReader(`{
						"properties": {
							"foo-stored": {
								"type": "text",
								"store":true
							}
						}
					}`),
				})
			require.NoError(t, err)
			_, err = client.Document.Create(
				t.Context(),
				opensearchapi.DocumentCreateReq{
					Index:      index,
					Body:       strings.NewReader(`{"foo-stored": "bar"}`),
					DocumentID: storedFieldDocID,
				},
			)
			require.NoError(t, err)
			res, err := client.Document.Get(
				t.Context(),
				opensearchapi.DocumentGetReq{
					Index:      index,
					DocumentID: storedFieldDocID,
					Params: opensearchapi.DocumentGetParams{
						StoredFields: []string{"foo-stored"},
					},
				},
			)
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
			assert.NotEmpty(t, res.Fields)
		})
	})
}
