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
	client, err := ostest.NewClient()
	require.Nil(t, err)

	index := "test-index-test"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	t.Run("Request Empty", func(t *testing.T) {
		resp, httpResp, err := client.Index(nil, opensearchapi.IndexReq{})
		assert.NotNil(t, err)
		var osError *opensearch.StringError
		require.True(t, errors.As(err, &osError))
		assert.Equal(t, http.StatusMethodNotAllowed, osError.Status)
		assert.Contains(t, osError.Err, "Incorrect HTTP method for uri")
		assert.Nil(t, resp)
		assert.NotNil(t, httpResp)
	})

	t.Run("Request Index only", func(t *testing.T) {
		resp, httpResp, err := client.Index(nil, opensearchapi.IndexReq{Index: index})
		assert.NotNil(t, err)
		var osError *opensearch.StructError
		require.True(t, errors.As(err, &osError))
		assert.Equal(t, "parse_exception", osError.Err.Type)
		assert.Equal(t, "request body is required", osError.Err.Reason)
		assert.Nil(t, resp)
		assert.NotNil(t, httpResp)
	})

	t.Run("Request with DocID", func(t *testing.T) {
		for _, result := range []string{"created", "updated"} {
			body := strings.NewReader("{}")
			resp, httpResp, err := client.Index(nil, opensearchapi.IndexReq{Index: index, Body: body, DocumentID: "test"})
			require.Nil(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
			assert.Equal(t, result, resp.Result)
		}
	})

	t.Run("Request without DocID", func(t *testing.T) {
		body := strings.NewReader("{}")
		resp, httpResp, err := client.Index(nil, opensearchapi.IndexReq{Index: index, Body: body})
		require.Nil(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, httpResp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
		assert.Equal(t, index, resp.Index)
		assert.Equal(t, "created", resp.Result)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, httpResp, err := failingClient.Index(nil, opensearchapi.IndexReq{Index: index})
		assert.NotNil(t, err)
		assert.Nil(t, res)
		assert.NotNil(t, httpResp)
		osapitest.VerifyResponse(t, httpResp)
	})
}
