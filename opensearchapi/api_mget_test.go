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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestMGet(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	testIndex := testutil.MustUniqueString(t, "test-mget")
	t.Cleanup(func() {
		client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	for i := 1; i <= 2; i++ {
		_, err = client.Document.Create(
			t.Context(),
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(`{"foo": "bar"}`),
				DocumentID: strconv.Itoa(i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.NoError(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, err := client.MGet(
			t.Context(),
			opensearchapi.MGetReq{
				Index: testIndex,
				Body:  strings.NewReader(`{"docs":[{"_id":"1"},{"_id":"2"}]}`),
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.MGet(t.Context(), opensearchapi.MGetReq{})
		require.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
