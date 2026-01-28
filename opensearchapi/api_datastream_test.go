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

func TestDataStreamClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	dataStream := "data-stream-test"

	_, err = client.IndexTemplate.Create(
		t.Context(),
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: dataStream,
			Body: strings.NewReader(`{"index_patterns":["data-stream-test"],"template":{"settings":` +
				`{"index":{"number_of_replicas":"0"}}},"priority":60,"data_stream":{}}`),
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.IndexTemplate.Delete(t.Context(), opensearchapi.IndexTemplateDeleteReq{IndexTemplate: dataStream})
	})

	type dataStreamTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []dataStreamTests
	}{
		{
			Name: "Create",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Create(t.Context(), opensearchapi.DataStreamCreateReq{DataStream: dataStream})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Create(t.Context(), opensearchapi.DataStreamCreateReq{DataStream: dataStream})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Get(t.Context(), &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Stats",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Stats(t.Context(), &opensearchapi.DataStreamStatsReq{DataStreams: []string{dataStream}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Stats(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []dataStreamTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.DataStream.Delete(t.Context(), opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.DataStream.Delete(t.Context(), opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
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
						ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
