// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearch_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestBuildRequest(t *testing.T) {
	t.Run("basic request without body", func(t *testing.T) {
		req, err := opensearch.BuildRequest("GET", "/test", nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "GET", req.Method)
		assert.Equal(t, "/test", req.URL.Path)
		assert.Empty(t, req.Header.Get("Content-Type"))
	})

	t.Run("request with body sets content-type", func(t *testing.T) {
		body := bytes.NewReader([]byte(`{"test": "data"}`))
		req, err := opensearch.BuildRequest("POST", "/test", body, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "POST", req.Method)
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	})

	t.Run("request with params", func(t *testing.T) {
		params := map[string]string{
			"filter_path": "took,hits.hits._source",
			"pretty":      "true",
		}
		req, err := opensearch.BuildRequest("GET", "/test", nil, params, nil)
		require.NoError(t, err)

		query := req.URL.Query()
		assert.Equal(t, "took,hits.hits._source", query.Get("filter_path"))
		assert.Equal(t, "true", query.Get("pretty"))
	})

	t.Run("request with headers", func(t *testing.T) {
		headers := http.Header{
			"X-Custom-Header": []string{"custom-value"},
			"Authorization":   []string{"Bearer token123"},
		}
		req, err := opensearch.BuildRequest("GET", "/test", nil, nil, headers)
		require.NoError(t, err)

		assert.Equal(t, "custom-value", req.Header.Get("X-Custom-Header"))
		assert.Equal(t, "Bearer token123", req.Header.Get("Authorization"))
	})

	t.Run("request with body and headers merges headers", func(t *testing.T) {
		body := bytes.NewReader([]byte(`{"test": "data"}`))
		headers := http.Header{
			"X-Custom-Header": []string{"custom-value"},
		}
		req, err := opensearch.BuildRequest("POST", "/test", body, nil, headers)
		require.NoError(t, err)

		// Body sets Content-Type
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
		// Custom header should also be present
		assert.Equal(t, "custom-value", req.Header.Get("X-Custom-Header"))
	})

	t.Run("request with headers but no body", func(t *testing.T) {
		headers := http.Header{
			"X-Custom-Header": []string{"custom-value"},
		}
		req, err := opensearch.BuildRequest("GET", "/test", nil, nil, headers)
		require.NoError(t, err)

		// Headers should be copied directly
		assert.Equal(t, "custom-value", req.Header.Get("X-Custom-Header"))
		assert.Empty(t, req.Header.Get("Content-Type"))
	})

	t.Run("request with multiple header values", func(t *testing.T) {
		headers := http.Header{
			"X-Custom-Header": []string{"value1", "value2"},
		}
		req, err := opensearch.BuildRequest("GET", "/test", nil, nil, headers)
		require.NoError(t, err)

		values := req.Header.Values("X-Custom-Header")
		assert.Len(t, values, 2)
		assert.Contains(t, values, "value1")
		assert.Contains(t, values, "value2")
	})

	t.Run("request with all options", func(t *testing.T) {
		body := bytes.NewReader([]byte(`{"test": "data"}`))
		params := map[string]string{
			"timeout": "30s",
		}
		headers := http.Header{
			"X-Request-ID": []string{"req-123"},
		}

		req, err := opensearch.BuildRequest("POST", "/test/_search", body, params, headers)
		require.NoError(t, err)

		assert.Equal(t, "POST", req.Method)
		assert.Equal(t, "/test/_search", req.URL.Path)
		assert.Equal(t, "30s", req.URL.Query().Get("timeout"))
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
		assert.Equal(t, "req-123", req.Header.Get("X-Request-ID"))
	})

	t.Run("invalid method returns error", func(t *testing.T) {
		// Invalid HTTP method should cause NewRequest to fail
		req, err := opensearch.BuildRequest("INVALID\nMETHOD", "/test", nil, nil, nil)
		assert.Error(t, err)
		assert.Nil(t, req)
	})
}
