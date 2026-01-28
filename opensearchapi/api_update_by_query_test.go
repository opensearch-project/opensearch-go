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
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestUpdateByQuery(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)

	testIndex := fmt.Sprintf("test-update-by-query-%d", rand.Int63())
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
				Body:       strings.NewReader(`{"foo": "bar", "counter": 1}`),
				DocumentID: fmt.Sprintf("%s-%d", docIDPrefix, i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.NoError(t, err)
	}

	t.Run("with request", func(t *testing.T) {
		resp, err := client.UpdateByQuery(
			t.Context(),
			opensearchapi.UpdateByQueryReq{
				Indices: []string{testIndex},
				Body:    strings.NewReader(`{"script":{"source":"ctx._source.counter += params.count","lang":"painless","params":{"count":4}}}`),
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.UpdateByQuery(t.Context(), opensearchapi.UpdateByQueryReq{})
		assert.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
