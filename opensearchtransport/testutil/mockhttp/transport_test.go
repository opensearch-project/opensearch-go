// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mockhttp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

func TestNewTransport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})

	transport := mockhttp.NewTransport(t, mux)
	require.NotNil(t, transport)

	req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "test response", string(body))
}

func TestNewTransportWithRoundTripFunc(t *testing.T) {
	roundTripFunc := func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("custom response")),
		}, nil
	}

	transport := mockhttp.NewTransportWithRoundTripFunc(t, roundTripFunc)
	require.NotNil(t, transport)

	req, err := http.NewRequest(http.MethodPost, "http://example.com/custom", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "custom response", string(body))
}

func TestNewTransportFromRoutes(t *testing.T) {
	routes := mockhttp.HandlerMap{
		"/health": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"healthy"}`))
		},
		"/error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal"}`))
		},
	}

	transport := mockhttp.NewTransportFromRoutes(t, routes)
	require.NotNil(t, transport)

	t.Run("Health endpoint", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com/health", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"status":"healthy"}`, string(body))
	})

	t.Run("Error endpoint", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com/error", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestGetDefaultHandlers(t *testing.T) {
	handlers := mockhttp.GetDefaultHandlers(t)
	require.NotNil(t, handlers)
	require.Contains(t, handlers, "/{$}")
	require.Contains(t, handlers, "/")

	transport := mockhttp.NewTransportFromRoutes(t, handlers)

	t.Run("Root returns cluster info JSON", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://localhost:9200/", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var info mockhttp.OpenSearchInfo
		err = json.NewDecoder(resp.Body).Decode(&info)
		require.NoError(t, err)

		require.Equal(t, "test-cluster", info.ClusterName)
		require.Equal(t, "test-node", info.Name)
		require.Equal(t, "3.0.0", info.Version.Number)
		require.Equal(t, "The OpenSearch Project: https://opensearch.org/", info.Tagline)
	})

	t.Run("Unmatched routes return 404", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://localhost:9200/nonexistent", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestGetDefaultHandlersWithNodes(t *testing.T) {
	nodes := map[string][]string{
		"node1": {"cluster_manager", "data"},
		"node2": {"data", "ingest"},
		"node3": {"ingest"},
	}

	handlers := mockhttp.GetDefaultHandlersWithNodes(t, nodes)
	require.NotNil(t, handlers)
	require.Contains(t, handlers, "/_nodes/http")

	transport := mockhttp.NewTransportFromRoutes(t, handlers)

	req, err := http.NewRequest(http.MethodGet, "http://localhost:9200/_nodes/http", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	require.Contains(t, bodyStr, "nodes")
	require.Contains(t, bodyStr, "node1")
	require.Contains(t, bodyStr, "node2")
	require.Contains(t, bodyStr, "node3")
	require.Contains(t, bodyStr, "cluster_manager")
	require.Contains(t, bodyStr, "ingest")
}

func TestNewRequest(t *testing.T) {
	t.Run("With headers", func(t *testing.T) {
		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")
		headers.Set("Authorization", "Bearer token")

		req := mockhttp.NewRequest(t, "POST", "/test", headers)
		require.NotNil(t, req)
		require.Equal(t, "POST", req.GetMethod())
		require.Equal(t, "/test", req.GetPath())
		require.Equal(t, "application/json", req.GetHeaders().Get("Content-Type"))
		require.Equal(t, "Bearer token", req.GetHeaders().Get("Authorization"))
	})

	t.Run("Nil headers returns empty header map", func(t *testing.T) {
		req := mockhttp.NewRequest(t, "GET", "/", nil)
		require.NotNil(t, req)
		require.Equal(t, "GET", req.GetMethod())
		require.Equal(t, "/", req.GetPath())

		headers := req.GetHeaders()
		require.NotNil(t, headers)
		require.Empty(t, headers)
	})
}

func TestNewRoundTripFunc(t *testing.T) {
	called := false
	transport := mockhttp.NewRoundTripFunc(t, func(_ *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
		}, nil
	})
	require.NotNil(t, transport)

	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	require.True(t, called)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTransportRoundTripPreservesRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Method", r.Method)
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	transport := mockhttp.NewTransport(t, mux)
	req, err := http.NewRequest(http.MethodPut, "http://localhost:9200/my-index", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.MethodPut, resp.Header.Get("X-Echo-Method"))
	require.Equal(t, "/my-index", resp.Header.Get("X-Echo-Path"))
	require.Equal(t, req, resp.Request)
}

func TestCreateMockOpenSearchResponse(t *testing.T) {
	body := mockhttp.CreateMockOpenSearchResponse()
	require.NotEmpty(t, body)

	var info mockhttp.OpenSearchInfo
	err := json.Unmarshal([]byte(body), &info)
	require.NoError(t, err)

	require.Equal(t, "test-cluster", info.ClusterName)
	require.Equal(t, "test-node", info.Name)
	require.Equal(t, "3.4.0", info.Version.Number)
	require.NotNil(t, info.Version.Distribution)
	require.Equal(t, "opensearch", *info.Version.Distribution)
}
