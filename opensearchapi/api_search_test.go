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

func TestSearch(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	index := "test-index-search"

	_, err = client.Index(
		nil,
		opensearchapi.IndexReq{
			Index:  index,
			Body:   strings.NewReader(`{"foo": "bar"}`),
			Params: opensearchapi.IndexParams{Refresh: "true"},
		},
	)
	require.Nil(t, err)
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.Search(nil, nil)
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
	})

	t.Run("with request", func(t *testing.T) {
		resp, err := client.Search(nil, &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader("")})
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Search(nil, nil)
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})

	t.Run("request with explain", func(t *testing.T) {
		resp, err := client.Search(nil, &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader(""), Params: opensearchapi.SearchParams{Explain: opensearchapi.ToPointer(true)}})
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.NotNil(t, resp.Hits.Hits[0].Explanation)
	})

	t.Run("request with retrieve specific fields", func(t *testing.T) {
		resp, err := client.Search(
			nil,
			&opensearchapi.SearchReq{
				Indices: []string{index},
				Body: strings.NewReader(`{
				"query": {
					"match": {
						"foo": "bar"
					}
				},
				"fields": [
					"foo"
				],
			"_source": false
			}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.NotEmpty(t, resp.Hits.Hits[0].Fields)
	})

	t.Run("url path", func(t *testing.T) {
		req := &opensearchapi.SearchReq{}
		httpReq, err := req.GetRequest()
		assert.Nil(t, err)
		require.NotNil(t, httpReq)
		assert.Equal(t, "/_search", httpReq.URL.Path)

		req = &opensearchapi.SearchReq{Indices: []string{index}}
		httpReq, err = req.GetRequest()
		assert.Nil(t, err)
		require.NotNil(t, httpReq)
		assert.Equal(t, fmt.Sprintf("/%s/_search", index), httpReq.URL.Path)
	})
}

func TestSearchWithRouting(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	index := "test-index-search"

	_, err = client.Index(
		nil,
		opensearchapi.IndexReq{
			DocumentID: "foo",
			Index:      index,
			Body:       strings.NewReader(`{"foo": "bar"}`),
			Params:     opensearchapi.IndexParams{Refresh: "true", Routing: "foo"},
		},
	)
	require.Nil(t, err)
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("request to retrieve response with routing key", func(t *testing.T) {
		resp, err := client.Search(nil, &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader(`{
		  "query": {
			"match": {
			  "foo": "bar"
			}
		  },
		  "fields": [
			"foo"
		  ],
		  "_source": false
		}`)})
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.NotEmpty(t, resp.Hits.Hits[0].Fields)
		assert.NotEmpty(t, resp.Hits.Hits[0].Routing)
		assert.Equal(t, "foo", resp.Hits.Hits[0].Routing)
	})
}
