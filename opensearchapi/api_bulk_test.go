// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestBulkClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	index := "test-bulk"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	tests := []struct {
		Name    string
		Request opensearchapi.BulkReq
	}{
		{
			Name: "with index",
			Request: opensearchapi.BulkReq{
				Index: index,
				Body:  strings.NewReader("{\"index\": {}}\n{\"test\": 1234}\n{\"create\": {}}\n{\"test\": 5678}\n"),
			},
		},
		{
			Name: "without index",
			Request: opensearchapi.BulkReq{
				Body: strings.NewReader(
					fmt.Sprintf("{\"index\": {\"_index\": \"%s\"}}\n{\"test\": 1234}\n{\"create\": {\"_index\": \"%s\"}}\n{\"test\": 5678}\n", index, index),
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			res, httpResp, err := client.Bulk(
				nil,
				test.Request,
			)
			require.Nil(t, err)
			assert.NotNil(t, res)
			assert.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, res, httpResp)
		})
	}
	t.Run("with failing request", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, httpResp, err := failingClient.Bulk(nil, opensearchapi.BulkReq{Index: index})
		require.NotNil(t, err)
		require.NotNil(t, httpResp)
		require.Nil(t, res)
		osapitest.VerifyResponse(t, httpResp)
	})
}
