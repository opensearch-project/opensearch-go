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

func TestUpdate(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	testIndex := "test-update"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	for i := 1; i <= 2; i++ {
		_, err = client.Document.Create(
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
		resp, err := client.Update(
			nil,
			opensearchapi.UpdateReq{
				Index:      testIndex,
				DocumentID: "1",
				Body:       strings.NewReader(`{"script":{"source":"ctx._source.counter += params.count","lang":"painless","params":{"count":4}}}`),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Update(nil, opensearchapi.UpdateReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestUpdateResp_WithSource(t *testing.T) {
    data := `{
        "_index": "test",
        "_id": "1",
        "_version": 1,
        "result": "noop",
        "_shards": {"total":1,"successful":1,"failed":0},
        "_seq_no": 1,
        "_primary_term": 1,
        "get": {
            "_index": "test",
            "_id": "1",
            "_version": 1,
            "_seq_no": 1,
            "_primary_term": 1,
            "found": true,
            "_source": {"foo":"bar"}
        }
    }`

    var resp UpdateResp
    err := json.Unmarshal([]byte(data), &resp)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if resp.Get == nil || !resp.Get.Found {
        t.Fatalf("expected get field to be present and found=true")
    }

    expected := `{"foo":"bar"}`
    if string(resp.Get.Source) != expected {
        t.Errorf("expected %s, got %s", expected, string(resp.Get.Source))
    }
}
