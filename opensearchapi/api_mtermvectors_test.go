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

func TestMTermvectors(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.Nil(t, err)

	testIndex := "test-mtermvectors"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	_, err = client.Indices.Create(
		nil,
		opensearchapi.IndicesCreateReq{
			Index: testIndex,
			Body: strings.NewReader(`{ "mappings": {
    "properties": {
      "text": {
        "type": "text",
        "term_vector": "with_positions_offsets_payloads",
        "store" : true,
        "analyzer" : "fulltext_analyzer"
       },
       "fullname": {
        "type": "text",
        "term_vector": "with_positions_offsets_payloads",
        "analyzer" : "fulltext_analyzer"
      }
    }
  },
  "settings" : {
    "index" : {
      "number_of_shards" : 1,
      "number_of_replicas" : 0
    },
    "analysis": {
      "analyzer": {
        "fulltext_analyzer": {
          "type": "custom",
          "tokenizer": "whitespace",
          "filter": [
            "lowercase",
            "type_as_payload"
          ]
        }
      }
    }
  }
}`),
		},
	)
	require.Nil(t, err)
	docs := []string{`{"fullname":"John Doe","text":"test test test "}`, `{"fullname":"Jane Doe","text":"Another test ..."}`}
	for i, doc := range docs {
		_, err = client.Document.Create(
			nil,
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(doc),
				DocumentID: strconv.Itoa(i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.Nil(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, err := client.MTermvectors(
			nil,
			opensearchapi.MTermvectorsReq{
				Index: testIndex,
				Body:  strings.NewReader(`{"ids":[1,2]}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.MTermvectors(nil, opensearchapi.MTermvectorsReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
