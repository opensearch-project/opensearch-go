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
	client, err := ostest.NewClient(t)
	require.NoError(t, err)

	index := "test-bulk"
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
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
					fmt.Sprintf(
						"{\"index\": {\"_index\": \"%s\"}}\n{\"test\": 1234}\n"+
							"{\"create\": {\"_index\": \"%s\"}}\n{\"test\": 5678}\n",
						index,
						index,
					),
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			res, err := client.Bulk(
				t.Context(),
				test.Request,
			)
			require.NoError(t, err)
			assert.NotEmpty(t, res)
			ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		})
	}
	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.Bulk(t.Context(), opensearchapi.BulkReq{Index: index})
		assert.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
