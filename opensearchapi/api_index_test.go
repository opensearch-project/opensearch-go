// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestIndexClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.Nil(t, err)

	index := "test-index-test"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("Request Empty", func(t *testing.T) {
		resp, err := client.Index(nil, opensearchapi.IndexReq{})
		assert.NotNil(t, err)
		var osError *opensearch.StringError
		require.True(t, errors.As(err, &osError))
		assert.Equal(t, http.StatusMethodNotAllowed, osError.Status)
		assert.Contains(t, osError.Err, "Incorrect HTTP method for uri")
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.Inspect())
	})

	t.Run("Request Index only", func(t *testing.T) {
		resp, err := client.Index(nil, opensearchapi.IndexReq{Index: index})
		assert.NotNil(t, err)
		var osError *opensearch.StructError
		require.True(t, errors.As(err, &osError))
		assert.Equal(t, "parse_exception", osError.Err.Type)
		assert.Equal(t, "request body is required", osError.Err.Reason)
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.Inspect())
	})

	t.Run("Request with DocID", func(t *testing.T) {
		for _, result := range []string{"created", "updated"} {
			body := strings.NewReader("{}")
			resp, err := client.Index(nil, opensearchapi.IndexReq{Index: index, Body: body, DocumentID: "test"})
			require.Nil(t, err)
			require.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
			assert.Equal(t, result, resp.Result)
		}
	})

	t.Run("Request without DocID", func(t *testing.T) {
		body := strings.NewReader("{}")
		resp, err := client.Index(nil, opensearchapi.IndexReq{Index: index, Body: body})
		require.Nil(t, err)
		require.NotNil(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		assert.Equal(t, index, resp.Index)
		assert.Equal(t, "created", resp.Result)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Index(nil, opensearchapi.IndexReq{Index: index})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}
