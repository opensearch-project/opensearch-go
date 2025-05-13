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

func TestMSearch(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	testIndex := "test-msearch"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	for i := 1; i <= 2; i++ {
		_, err = client.Document.Create(
			nil,
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(`{"foo": "bar", "number": 1}`),
				DocumentID: strconv.Itoa(i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.Nil(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, err := client.MSearch(
			nil,
			opensearchapi.MSearchReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader("{}\n{\"query\":{\"exists\":{\"field\":\"foo\"}}}\n"),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.MSearch(nil, opensearchapi.MSearchReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})

	t.Run("with aggs request", func(t *testing.T) {
		resp, err := client.MSearch(
			nil,
			opensearchapi.MSearchReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader("{}\n{\"aggs\":{\"number_terms\":{\"terms\":{\"field\":\"number\"}}}}\n"),
			},
		)
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})
}
