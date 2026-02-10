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
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestSearch(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient(t)
	require.Nil(t, err)

	index := testutil.MustUniqueString(t, "test-index-search")

	_, err = client.Indices.Create(
		t.Context(),
		opensearchapi.IndicesCreateReq{
			Index: index,
			Body: strings.NewReader(`{
				"mappings": {
					"properties": {
						"baz": {
							"type": "nested"
						},
						"foo": {
							"type": "text",
							"fields": {
								"suggestions": {
									"type": "completion"
								}
							}
						}
					}
				}
			}`),
		},
	)
	require.Nil(t, err)
	_, err = client.Index(
		t.Context(),
		opensearchapi.IndexReq{
			DocumentID: "foo",
			Index:      index,
			Body:       strings.NewReader(`{"foo": "bar", "baz": [{"foo": "test"}]}`),
			Params:     opensearchapi.IndexParams{Refresh: "true", Routing: "foo"},
		},
	)
	require.Nil(t, err)
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.Search(t.Context(), nil)
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
	})

	t.Run("with request", func(t *testing.T) {
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader("")})
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Search(t.Context(), nil)
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})

	t.Run("request with explain", func(t *testing.T) {
		resp, err := client.Search(
			t.Context(),
			&opensearchapi.SearchReq{
				Indices: []string{index},
				Body:    strings.NewReader(""),
				Params:  opensearchapi.SearchParams{Explain: opensearchapi.ToPointer(true)},
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.NotNil(t, resp.Hits.Hits[0].Explanation)
	})

	t.Run("request with retrieve specific fields", func(t *testing.T) {
		resp, err := client.Search(
			t.Context(),
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
	t.Run("request to retrieve response with routing key", func(t *testing.T) {
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader(`{
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

	t.Run("with seq_no and primary_term", func(t *testing.T) {
		seqNoPrimaryTerm := true
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
			Indices: []string{index},
			Body:    strings.NewReader(""),
			Params: opensearchapi.SearchParams{
				SeqNoPrimaryTerm: &seqNoPrimaryTerm,
			},
		})
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
		for _, hit := range resp.Hits.Hits {
			assert.NotNil(t, hit.SeqNo)
			assert.NotNil(t, hit.PrimaryTerm)
		}
	})

	t.Run("without seq_no and primary_term", func(t *testing.T) {
		seqNoPrimaryTerm := false
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
			Indices: []string{index},
			Body:    strings.NewReader(""),
			Params: opensearchapi.SearchParams{
				SeqNoPrimaryTerm: &seqNoPrimaryTerm,
			},
		})
		require.Nil(t, err)
		assert.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.NotEmpty(t, resp.Hits.Hits)
		for _, hit := range resp.Hits.Hits {
			assert.Nil(t, hit.SeqNo)
			assert.Nil(t, hit.PrimaryTerm)
		}
	})

	t.Run("request with suggest", func(t *testing.T) {
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader(`{
			"suggest": {
			  "text": "bar",
			  "my-suggest": {
			    "term": {
				  "field": "foo"
				}
			  }
			}
		  }`)})
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Suggest)
	})

	t.Run("request with completion suggest", func(t *testing.T) {
		resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{Indices: []string{index}, Body: strings.NewReader(`{
			"suggest": {
			  "my-suggest": {
			  	"text": "bar",
				"completion": {
					"field": "foo.suggestions",
					"skip_duplicates": true
				} 
			  }
			}
		  }`)})
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Suggest)
		assert.NotEmpty(t, resp.Suggest["my-suggest"])
		for _, suggestion := range resp.Suggest["my-suggest"] {
			assert.Equal(t, suggestion.Text, "bar")
			assert.NotEmpty(t, suggestion.Options)
			assert.Equal(t, suggestion.Options[0].Text, "bar")
		}
	})

	t.Run("request with highlight", func(t *testing.T) {
		resp, err := client.Search(
			t.Context(),
			&opensearchapi.SearchReq{
				Indices: []string{index},
				Body: strings.NewReader(`{
					"query": {
						"match": {
							"foo": "bar"
						}
					},
					"highlight": {
						"fields": {
							"foo": {}
						}
					}
				}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.Equal(t, map[string][]string{"foo": {"<em>bar</em>"}}, resp.Hits.Hits[0].Highlight)
	})

	t.Run("request with matched queries", func(t *testing.T) {
		resp, err := client.Search(
			t.Context(),
			&opensearchapi.SearchReq{
				Indices: []string{index},
				Body: strings.NewReader(`{
					"query": {
						"match": {
							"foo": {
								"query": "bar",
								"_name": "test"
							}
						}
					}
				}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.Equal(t, []string{"test"}, resp.Hits.Hits[0].MatchedQueries)
	})

	t.Run("request with inner hits", func(t *testing.T) {
		resp, err := client.Search(
			t.Context(),
			&opensearchapi.SearchReq{
				Indices: []string{index},
				Body: strings.NewReader(`{
					"query": {
						"nested": {
							"path": "baz",
							"query": {
								"match": {
									"baz.foo": "test"
								}
							},
							"inner_hits": {}
						}
					}
				}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp.Hits.Hits)
		assert.NotEmpty(t, resp.Hits.Hits[0].InnerHits)
		assert.NotNil(t, resp.Hits.Hits[0].InnerHits["baz"])
		assert.NotEmpty(t, resp.Hits.Hits[0].InnerHits["baz"].Hits.Hits)
	})

	t.Run("request with phase took", func(t *testing.T) {
		ostest.SkipIfBelowVersion(t, client, 2, 12, "request with phase took")
		resp, err := client.Search(
			t.Context(),
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
				Params: opensearchapi.SearchParams{
					PhaseTook: true,
				},
			},
		)
		require.Nil(t, err)
		assert.NotNil(t, resp.PhaseTook)
	})
}
