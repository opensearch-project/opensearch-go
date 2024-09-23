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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestUpdateByQuery(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	testIndex := "test-update_by_query"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	for i := 1; i <= 2; i++ {
		_, _, err = client.Document.Create(
			nil,
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(`{"foo": "bar", "counter": 1}`),
				DocumentID: strconv.Itoa(i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.Nil(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, httpResp, err := client.UpdateByQuery(
			nil,
			opensearchapi.UpdateByQueryReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader(`{"script":{"source":"ctx._source.counter += params.count","lang":"painless","params":{"count":4}}}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		assert.NotNil(t, httpResp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, httpResp, err := failingClient.UpdateByQuery(nil, opensearchapi.UpdateByQueryReq{})
		assert.NotNil(t, err)
		assert.Nil(t, res)
		assert.NotNil(t, httpResp)
		osapitest.VerifyResponse(t, httpResp)
	})
}
