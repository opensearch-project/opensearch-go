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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestReindex(t *testing.T) {
	t.Parallel()
	client, err := ostest.NewClient(t)
	require.Nil(t, err)

	sourceIndex := testutil.MustUniqueString(t, "test-reindex-source")
	destIndex := testutil.MustUniqueString(t, "test-reindex-dest")
	testIndices := []string{sourceIndex, destIndex}
	t.Cleanup(func() {
		client.Indices.Delete(
			t.Context(),
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
	})

	for _, index := range testIndices {
		client.Indices.Create(
			t.Context(),
			opensearchapi.IndicesCreateReq{
				Index: index,
				Body:  strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
			},
		)
	}

	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Index:   sourceIndex,
		Client:  client,
		Refresh: "wait_for",
	})
	require.Nil(t, err)
	for i := 1; i <= 100; i++ {
		err := bi.Add(t.Context(), opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       strings.NewReader(`{"title":"bar"}`),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
	}
	if err := bi.Close(t.Context()); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	t.Run("with request", func(t *testing.T) {
		t.Parallel()
		resp, err := client.Reindex(
			t.Context(),
			opensearchapi.ReindexReq{
				Body: strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s"},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("with request but dont wait", func(t *testing.T) {
		t.Parallel()
		resp, err := client.Reindex(
			t.Context(),
			opensearchapi.ReindexReq{
				Body:   strings.NewReader(fmt.Sprintf(`{"source":{"index":"%s"},"dest":{"index":"%s"}}`, sourceIndex, destIndex)),
				Params: opensearchapi.ReindexParams{WaitForCompletion: opensearchapi.ToPointer(false)},
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		t.Parallel()
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Reindex(t.Context(), opensearchapi.ReindexReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
