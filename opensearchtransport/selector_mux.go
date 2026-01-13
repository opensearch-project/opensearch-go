// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import (
	"fmt"
	"net/http"
)

//nolint:gochecknoglobals // Shared empty header to avoid allocations in selectorResponseWriter
var emptyHeader = make(http.Header)

// SelectorMux is a connection selector multiplexer that routes requests to different selectors
// based on HTTP patterns, leveraging http.ServeMux for pattern matching.
type SelectorMux struct {
	mux *http.ServeMux // Underlying HTTP multiplexer for pattern matching
}

// Route represents a pattern-to-selector mapping.
type Route struct {
	Pattern  string   // HTTP pattern (e.g., "POST /_bulk", "GET /_search", "/")
	Selector Selector // Selector to use for matching requests
}

// NewSelectorMux creates a new selector multiplexer with the given routes.
func NewSelectorMux(routes []Route) *SelectorMux {
	mux := http.NewServeMux()

	// Register each route with the ServeMux
	for _, route := range routes {
		// Capture the selector for this route in the closure
		selector := route.Selector
		mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
			// Store the selector in the request context or use a different mechanism
			// For now, we'll use a custom response writer to pass back the selector
			if sw, ok := w.(*selectorResponseWriter); ok {
				sw.selector = selector
			}
		})
	}

	return &SelectorMux{
		mux: mux,
	}
}

// selectorResponseWriter is a custom ResponseWriter that captures the selector.
type selectorResponseWriter struct {
	selector Selector
}

// Header returns an empty header map for the selector response writer.
func (w *selectorResponseWriter) Header() http.Header { return emptyHeader }

// Write is a no-op for the selector response writer.
func (w *selectorResponseWriter) Write([]byte) (int, error) { return 0, nil }

// WriteHeader is a no-op for the selector response writer.
func (w *selectorResponseWriter) WriteHeader(statusCode int) {}

// Select implements Selector interface but always returns an error since
// SelectorMux requires an HTTP request for routing decisions.
func (m *SelectorMux) Select(connections []*Connection) (*Connection, error) {
	return nil, ErrSelectNotImplemented
}

// SelectForRequest implements RequestAwareSelector to route based on HTTP patterns.
func (m *SelectorMux) SelectForRequest(connections []*Connection, req *http.Request) (*Connection, error) {
	if len(connections) == 0 {
		return nil, ErrNoConnections
	}

	// Use a custom response writer to capture the selector
	sw := &selectorResponseWriter{}

	// Let ServeMux find and call the appropriate handler
	m.mux.ServeHTTP(sw, req)

	// If we got a selector, use it
	if sw.selector != nil {
		return sw.selector.Select(connections)
	}

	// No matching route found
	return nil, fmt.Errorf("no route matched request: %s %s", req.Method, req.URL.Path)
}
