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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestMSearch(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	testIndex := testutil.MustUniqueString(t, "test-msearch")
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	// Use unique document IDs to avoid conflicts between test runs
	docIDPrefix := testutil.MustUniqueString(t, "doc")

	for i := 1; i <= 2; i++ {
		_, err = client.Document.Create(
			t.Context(),
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(`{"foo": "bar", "number": 1}`),
				DocumentID: fmt.Sprintf("%s-%d", docIDPrefix, i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.NoError(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, err := client.MSearch(
			t.Context(),
			opensearchapi.MSearchReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader("{}\n{\"query\":{\"exists\":{\"field\":\"foo\"}}}\n"),
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.MSearch(t.Context(), opensearchapi.MSearchReq{})
		require.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})

	t.Run("with aggs request", func(t *testing.T) {
		resp, err := client.MSearch(
			t.Context(),
			opensearchapi.MSearchReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader("{}\n{\"aggs\":{\"number_terms\":{\"terms\":{\"field\":\"number\"}}}}\n"),
			},
		)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})
}
