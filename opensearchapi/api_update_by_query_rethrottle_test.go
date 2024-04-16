// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
)

func TestUpdateByQueryRethrottle(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient()
	require.Nil(t, err)

	testIndex := "test-updatebyquery-rethrottle-source"
	t.Cleanup(func() {
		client.Indices.Delete(
			nil,
			opensearchapi.IndicesDeleteReq{
				Indices: []string{testIndex},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
	})

	client.Indices.Create(
		nil,
		opensearchapi.IndicesCreateReq{
			Index: testIndex,
			Body:  strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
		},
	)
	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Index:   testIndex,
		Client:  client,
		Refresh: "wait_for",
	})
	for i := 1; i <= 60; i++ {
		err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       strings.NewReader(`{"foo": "bar", "counter": 1}`),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
	}
	if err := bi.Close(context.Background()); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	updatebyquery, err := client.UpdateByQuery(
		nil,
		opensearchapi.UpdateByQueryReq{
			Indices: []string{testIndex},
			Body:    strings.NewReader(`{"script":{"source":"ctx._source.counter += params.count","lang":"painless","params":{"count":4}}}`),
			Params: opensearchapi.UpdateByQueryParams{
				WaitForCompletion: opensearchapi.ToPointer(false),
				RequestsPerSecond: opensearchapi.ToPointer(1),
			},
		},
	)
	require.Nil(t, err)
	t.Run("with request", func(t *testing.T) {
		resp, err := client.UpdateByQueryRethrottle(
			nil,
			opensearchapi.UpdateByQueryRethrottleReq{
				TaskID: updatebyquery.Task,
				Params: opensearchapi.UpdateByQueryRethrottleParams{RequestsPerSecond: opensearchapi.ToPointer(40)},
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.UpdateByQueryRethrottle(nil, opensearchapi.UpdateByQueryRethrottleReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
