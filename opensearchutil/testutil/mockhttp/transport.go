// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package mockhttp provides HTTP mocking utilities for testing OpenSearch Go client functionality.
package mockhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// HandlerMap is a type alias for route handlers
type HandlerMap map[string]func(http.ResponseWriter, *http.Request)

// OpenSearchInfo represents the basic OpenSearch cluster info response structure
type OpenSearchInfo struct {
	Name        string `json:"name"`
	ClusterName string `json:"cluster_name"`
	ClusterUUID string `json:"cluster_uuid"`
	Tagline     string `json:"tagline"`
	Version     struct {
		Number                           string  `json:"number"`
		BuildType                        string  `json:"build_type"`
		BuildHash                        string  `json:"build_hash"`
		BuildDate                        string  `json:"build_date"`
		BuildSnapshot                    bool    `json:"build_snapshot"`
		LuceneVersion                    string  `json:"lucene_version"`
		MinimumWireCompatibilityVersion  string  `json:"minimum_wire_compatibility_version"`
		MinimumIndexCompatibilityVersion string  `json:"minimum_index_compatibility_version"`
		Distribution                     *string `json:"distribution,omitempty"`
	} `json:"version"`
}

// NodeInfo represents the structure returned by /_nodes endpoint
type NodeInfo struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Roles      []string       `json:"roles"`
	Attributes map[string]any `json:"attributes"`
	HTTP       struct {
		PublishAddress string `json:"publish_address"`
	} `json:"http"`
}

// GetDefaultHandlers returns the standard set of mock handlers for OpenSearch client testing.
// These handlers cover the most common endpoints used in discovery and health checks.
func GetDefaultHandlers(t *testing.T) HandlerMap {
	t.Helper()
	return HandlerMap{
		// Health check endpoint - returns basic cluster info
		"/{$}": func(w http.ResponseWriter, _ *http.Request) {
			healthInfo := OpenSearchInfo{
				Name:        "test-node",
				ClusterName: "test-cluster",
				ClusterUUID: "test-uuid",
				Tagline:     "The OpenSearch Project: https://opensearch.org/",
				Version: struct {
					Number                           string  `json:"number"`
					BuildType                        string  `json:"build_type"`
					BuildHash                        string  `json:"build_hash"`
					BuildDate                        string  `json:"build_date"`
					BuildSnapshot                    bool    `json:"build_snapshot"`
					LuceneVersion                    string  `json:"lucene_version"`
					MinimumWireCompatibilityVersion  string  `json:"minimum_wire_compatibility_version"`
					MinimumIndexCompatibilityVersion string  `json:"minimum_index_compatibility_version"`
					Distribution                     *string `json:"distribution,omitempty"`
				}{
					Number:                           "3.0.0",
					BuildType:                        "docker",
					BuildHash:                        "abc123",
					BuildDate:                        "2023-01-01T00:00:00.000000Z",
					BuildSnapshot:                    false,
					LuceneVersion:                    "9.5.0",
					MinimumWireCompatibilityVersion:  "7.10.0",
					MinimumIndexCompatibilityVersion: "7.0.0",
					Distribution:                     nil,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(healthInfo); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		},
		// Default 404 handler for unmatched routes (must be last due to "/" catch-all behavior)
		"/": func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		},
	}
}

// GetDefaultHandlersWithNodes returns default handlers plus a node discovery endpoint
// populated with the provided node configuration.
func GetDefaultHandlersWithNodes(t *testing.T, nodes map[string][]string) HandlerMap {
	t.Helper()
	handlers := GetDefaultHandlers(t)

	// Add node discovery endpoint with custom node data
	handlers["/_nodes/http"] = func(w http.ResponseWriter, _ *http.Request) {
		nodeMap := make(map[string]map[string]NodeInfo)
		nodeMap["nodes"] = make(map[string]NodeInfo)

		for name, roles := range nodes {
			nodeMap["nodes"][name] = NodeInfo{
				ID:    name + "-id",
				Name:  name,
				Roles: roles,
				HTTP: struct {
					PublishAddress string `json:"publish_address"`
				}{
					PublishAddress: "127.0.0.1:9200",
				},
				Attributes: make(map[string]any),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(nodeMap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}

	return handlers
}

// NewTransportFromRoutes creates a mock HTTP transport from a map of route handlers.
// This allows tests to easily customize endpoints while inheriting sensible defaults.
//
// Example usage:
//
//	routes := mockhttp.GetDefaultHandlers(t)
//	routes["/custom/endpoint"] = func(w http.ResponseWriter, r *http.Request) {
//		w.WriteHeader(http.StatusOK)
//		w.Write([]byte(`{"custom": "response"}`))
//	}
//	transport := mockhttp.NewTransportFromRoutes(t, routes)
func NewTransportFromRoutes(t *testing.T, routes HandlerMap) http.RoundTripper {
	t.Helper()
	mux := http.NewServeMux()

	for pattern, handler := range routes {
		mux.HandleFunc(pattern, handler)
	}

	return NewTransport(t, mux)
}

// Transport provides a mock HTTP transport for testing that wraps an http.Handler
type Transport struct {
	handler http.Handler
}

// RoundTrip implements http.RoundTripper interface
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a response recorder to capture the handler's response
	recorder := &responseRecorder{
		header: make(http.Header),
		body:   &bytes.Buffer{},
		code:   http.StatusOK,
	}

	// Serve the request using the handler
	t.handler.ServeHTTP(recorder, req)

	// Convert the recorded response to an http.Response
	resp := &http.Response{
		StatusCode:    recorder.code,
		Header:        recorder.header,
		Body:          io.NopCloser(bytes.NewReader(recorder.body.Bytes())),
		ContentLength: int64(recorder.body.Len()),
		Request:       req,
		ProtoMajor:    1,
		ProtoMinor:    1,
	}

	return resp, nil
}

// responseRecorder is a simple implementation of http.ResponseWriter for testing
type responseRecorder struct {
	header http.Header
	body   *bytes.Buffer
	code   int
}

// Header returns the HTTP response headers
func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

// WriteHeader sets the HTTP response status code
func (r *responseRecorder) WriteHeader(statusCode int) {
	r.code = statusCode
}

// TransportWithFunc provides a mock HTTP transport using a function for compatibility
type TransportWithFunc struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

// RoundTrip executes the HTTP request using the configured handler function
func (t *TransportWithFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.RoundTripFunc(req)
}

// NewTransport creates a mock HTTP transport that uses the provided handler for routing.
// This allows tests to use http.ServeMux or any other http.Handler for realistic request routing.
//
// Example usage:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//	    w.WriteHeader(http.StatusOK)
//	    w.Write([]byte(`{"version": {"number": "2.0.0"}}`))
//	})
//	transport := mockhttp.NewTransport(t, mux)
func NewTransport(t *testing.T, handler http.Handler) http.RoundTripper {
	t.Helper()
	return &Transport{
		handler: handler,
	}
}

// NewTransportWithRoundTripFunc creates a mock HTTP transport using a custom RoundTrip function.
// This is provided for compatibility with existing tests that need fine-grained control over the
// HTTP request/response cycle.
//
// Example usage:
//
//	transport := mockhttp.NewTransportWithRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
//	    return &http.Response{
//	        StatusCode: http.StatusOK,
//	        Body: io.NopCloser(strings.NewReader(`{"status": "ok"}`)),
//	    }, nil
//	})
func NewTransportWithRoundTripFunc(t *testing.T, roundTripFunc func(req *http.Request) (*http.Response, error)) http.RoundTripper {
	t.Helper()
	return &TransportWithFunc{
		RoundTripFunc: roundTripFunc,
	}
}

// NewRequest creates a new mock request for testing
func NewRequest(t *testing.T, method, path string, headers http.Header) interface {
	GetMethod() string
	GetPath() string
	GetHeaders() http.Header
} {
	t.Helper()
	return &request{
		method:  method,
		path:    path,
		headers: headers,
	}
}

// NewRoundTripFunc creates a new mock transport for testing
func NewRoundTripFunc(t *testing.T, fn func(req *http.Request) (*http.Response, error)) http.RoundTripper {
	t.Helper()
	return &mockTransport{fn: fn}
}

// request provides a mock implementation for testing opensearchtransport.Request interface
type request struct {
	method  string
	path    string
	headers http.Header
}

// GetMethod implements opensearchtransport.Request
func (r *request) GetMethod() string { return r.method }

// GetPath implements opensearchtransport.Request
func (r *request) GetPath() string { return r.path }

// GetHeaders implements opensearchtransport.Request
func (r *request) GetHeaders() http.Header {
	if r.headers == nil {
		return make(http.Header)
	}
	return r.headers
}

// mockTransport implements http.RoundTripper
type mockTransport struct {
	fn func(req *http.Request) (*http.Response, error)
}

// RoundTrip implements http.RoundTripper interface
func (mt *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return mt.fn(req)
}
