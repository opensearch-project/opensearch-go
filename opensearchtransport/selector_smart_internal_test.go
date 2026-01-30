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
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// Compile-time interface compliance checks
var (
	_ RequestAwareSelector = (NewSmartSelector())
)

func TestNewSmartSelector(t *testing.T) {
	selector := NewSmartSelector()

	if selector == nil {
		t.Errorf("Expected NewSmartSelector() to return a non-nil selector")
	}

	// Should return a ChainSelector
	_, ok := selector.(*ChainSelector)
	if !ok {
		t.Errorf("Expected NewSmartSelector() to return a ChainSelector, got %T", selector)
	}
}

func TestSmartSelectorBulkOperations(t *testing.T) {
	selector := NewSmartSelector()

	// Create connections with different roles
	ingestConn := &Connection{
		URL:   &url.URL{Host: "ingest:9200"},
		Roles: newRoleSet([]string{RoleIngest}),
	}
	dataConn := &Connection{
		URL:   &url.URL{Host: "data:9200"},
		Roles: newRoleSet([]string{RoleData}),
	}
	connections := []*Connection{ingestConn, dataConn}

	testCases := []struct {
		method string
		path   string
		desc   string
	}{
		{http.MethodPost, "/_bulk", "root bulk endpoint"},
		{http.MethodPut, "/_bulk", "root bulk endpoint with PUT"},
		{http.MethodPost, "/my-index/_bulk", "index-specific bulk endpoint"},
		{http.MethodPut, "/my-index/_bulk", "index-specific bulk endpoint with PUT"},
		{http.MethodPost, "/_bulk/stream", "streaming bulk endpoint"},
		{http.MethodPut, "/_bulk/stream", "streaming bulk endpoint with PUT"},
		{http.MethodPost, "/my-index/_bulk/stream", "index-specific streaming bulk endpoint"},
		{http.MethodPut, "/my-index/_bulk/stream", "index-specific streaming bulk endpoint with PUT"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &http.Request{
				Method: tc.method,
				URL:    &url.URL{Path: tc.path},
			}

			conn, err := selector.SelectForRequest(connections, req)
			if err != nil {
				t.Errorf("Expected SelectForRequest to succeed for %s, got error: %v", tc.desc, err)
			}

			// Should select ingest node for bulk operations
			if conn != ingestConn {
				t.Errorf("Expected bulk operation to select ingest node, got %v", conn)
			}
		})
	}
}

func TestSmartSelectorSearchOperations(t *testing.T) {
	selector := NewSmartSelector()

	// Create connections with different roles
	ingestConn := &Connection{
		URL:   &url.URL{Host: "ingest:9200"},
		Roles: newRoleSet([]string{RoleIngest}),
	}
	dataConn := &Connection{
		URL:   &url.URL{Host: "data:9200"},
		Roles: newRoleSet([]string{RoleData}),
	}
	connections := []*Connection{ingestConn, dataConn}

	testCases := []struct {
		method string
		path   string
		desc   string
	}{
		{http.MethodGet, "/_search", "root search endpoint"},
		{http.MethodPost, "/_search", "root search endpoint with POST"},
		{http.MethodGet, "/my-index/_search", "index-specific search endpoint"},
		{http.MethodPost, "/my-index/_search", "index-specific search endpoint with POST"},
		{http.MethodGet, "/_msearch", "multi-search endpoint"},
		{http.MethodPost, "/_msearch", "multi-search endpoint with POST"},
		{http.MethodGet, "/my-index/_msearch", "index-specific multi-search"},
		{http.MethodPost, "/my-index/_msearch", "index-specific multi-search with POST"},
		{http.MethodGet, "/_count", "count queries"},
		{http.MethodPost, "/_count", "count queries with POST"},
		{http.MethodGet, "/my-index/_count", "index-specific count"},
		{http.MethodPost, "/my-index/_count", "index-specific count with POST"},
		{http.MethodPost, "/my-index/_delete_by_query", "delete by query"},
		{http.MethodPost, "/my-index/_update_by_query", "update by query"},
		{http.MethodGet, "/my-index/_explain/123", "explain query"},
		{http.MethodPost, "/my-index/_explain/123", "explain query with POST"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &http.Request{
				Method: tc.method,
				URL:    &url.URL{Path: tc.path},
			}

			conn, err := selector.SelectForRequest(connections, req)
			if err != nil {
				t.Errorf("Expected SelectForRequest to succeed for %s, got error: %v", tc.desc, err)
			}

			// Should select data node for search operations
			if conn != dataConn {
				t.Errorf("Expected search operation to select data node, got %v", conn)
			}
		})
	}
}

func TestSmartSelectorDocumentRetrieval(t *testing.T) {
	selector := NewSmartSelector()

	// Create connections with different roles
	ingestConn := &Connection{
		URL:   &url.URL{Host: "ingest:9200"},
		Roles: newRoleSet([]string{RoleIngest}),
	}
	dataConn := &Connection{
		URL:   &url.URL{Host: "data:9200"},
		Roles: newRoleSet([]string{RoleData}),
	}
	connections := []*Connection{ingestConn, dataConn}

	testCases := []struct {
		method string
		path   string
		desc   string
	}{
		{http.MethodGet, "/my-index/_doc/123", "get document by ID"},
		{http.MethodHead, "/my-index/_doc/123", "check document existence"},
		{http.MethodGet, "/my-index/_source/123", "get document source"},
		{http.MethodHead, "/my-index/_source/123", "check document source"},
		{http.MethodGet, "/_mget", "multi-get documents"},
		{http.MethodPost, "/_mget", "multi-get documents with POST"},
		{http.MethodGet, "/my-index/_mget", "index-specific multi-get"},
		{http.MethodPost, "/my-index/_mget", "index-specific multi-get with POST"},
		{http.MethodGet, "/my-index/_termvectors", "term vectors"},
		{http.MethodPost, "/my-index/_termvectors", "term vectors with POST"},
		{http.MethodGet, "/my-index/_termvectors/123", "term vectors for document"},
		{http.MethodPost, "/my-index/_termvectors/123", "term vectors for document with POST"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &http.Request{
				Method: tc.method,
				URL:    &url.URL{Path: tc.path},
			}

			conn, err := selector.SelectForRequest(connections, req)
			if err != nil {
				t.Errorf("Expected SelectForRequest to succeed for %s, got error: %v", tc.desc, err)
			}

			// Should select data node for document retrieval
			if conn != dataConn {
				t.Errorf("Expected document retrieval to select data node, got %v", conn)
			}
		})
	}
}

func TestSmartSelectorIngestPipelineOperations(t *testing.T) {
	selector := NewSmartSelector()

	// Create connections with different roles
	ingestConn := &Connection{
		URL:   &url.URL{Host: "ingest:9200"},
		Roles: newRoleSet([]string{RoleIngest}),
	}
	dataConn := &Connection{
		URL:   &url.URL{Host: "data:9200"},
		Roles: newRoleSet([]string{RoleData}),
	}
	connections := []*Connection{ingestConn, dataConn}

	testCases := []struct {
		method string
		path   string
		desc   string
	}{
		{http.MethodGet, "/_ingest/pipeline", "list ingest pipelines"},
		{http.MethodPost, "/_ingest/pipeline/my-pipeline", "create ingest pipeline"},
		{http.MethodPut, "/_ingest/pipeline/my-pipeline", "create/update ingest pipeline"},
		{http.MethodDelete, "/_ingest/pipeline/my-pipeline", "delete ingest pipeline"},
		{http.MethodGet, "/_ingest/pipeline/my-pipeline", "get specific pipeline"},
		{http.MethodGet, "/_ingest/pipeline/my-pipeline/_simulate", "simulate pipeline with ID"},
		{http.MethodPost, "/_ingest/pipeline/my-pipeline/_simulate", "simulate pipeline with ID and POST"},
		{http.MethodGet, "/_ingest/pipeline/_simulate", "simulate pipeline without ID"},
		{http.MethodPost, "/_ingest/pipeline/_simulate", "simulate pipeline without ID and POST"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &http.Request{
				Method: tc.method,
				URL:    &url.URL{Path: tc.path},
			}

			conn, err := selector.SelectForRequest(connections, req)
			if err != nil {
				t.Errorf("Expected SelectForRequest to succeed for %s, got error: %v", tc.desc, err)
			}

			// Should select ingest node for ingest pipeline operations
			if conn != ingestConn {
				t.Errorf("Expected ingest pipeline operation to select ingest node, got %v", conn)
			}
		})
	}
}

func TestSmartSelectorNoMatchingNodes(t *testing.T) {
	selector := NewSmartSelector()

	// Create connections without required roles
	coordOnlyConn := &Connection{
		URL:   &url.URL{Host: "coord:9200"},
		Roles: newRoleSet([]string{}), // Coordinating-only node
	}
	connections := []*Connection{coordOnlyConn}

	// Test bulk operation with no ingest nodes
	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_bulk"},
	}

	conn, err := selector.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed with fallback, got error: %v", err)
	}

	// Should fall back to round-robin and select the available coordinating node
	if conn != coordOnlyConn {
		t.Errorf("Expected smart selector to fall back to available node")
	}
}

func TestSmartSelectorNoConnections(t *testing.T) {
	selector := NewSmartSelector()

	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_bulk"},
	}

	_, err := selector.SelectForRequest([]*Connection{}, req)
	if err == nil {
		t.Errorf("Expected SelectForRequest to fail with no connections")
	}

	if !errors.Is(err, ErrNoConnections) {
		t.Errorf("Expected ErrNoConnections, got: %v", err)
	}
}

func TestSmartSelectorUnmatchedRequest(t *testing.T) {
	selector := NewSmartSelector()

	// Create a connection
	conn := &Connection{
		URL:   &url.URL{Host: "node:9200"},
		Roles: newRoleSet([]string{RoleData}),
	}
	connections := []*Connection{conn}

	// Test a request that doesn't match any specific patterns
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/_cluster/health"},
	}

	selectedConn, err := selector.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed with fallback, got error: %v", err)
	}

	// Should fall back to round-robin and select the available connection
	if selectedConn != conn {
		t.Errorf("Expected smart selector to fall back to available connection")
	}
}

func TestNewSmartSelectorWithRoutes(t *testing.T) {
	customSelector := NewRoundRobinSelector()
	routes := []Route{
		{"GET /custom", customSelector},
	}

	selector := NewSmartSelectorWithRoutes(routes)

	if selector == nil {
		t.Errorf("Expected NewSmartSelectorWithRoutes to return non-nil selector")
	}

	// Should return a SelectorMux
	mux, ok := selector.(*SelectorMux)
	if !ok {
		t.Errorf("Expected NewSmartSelectorWithRoutes to return SelectorMux, got %T", selector)
	}

	// Test the custom route
	conn := &Connection{URL: &url.URL{Host: "node:9200"}}
	connections := []*Connection{conn}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/custom"},
	}

	selectedConn, err := mux.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed for custom route, got error: %v", err)
	}

	if selectedConn != conn {
		t.Errorf("Expected custom route to select connection")
	}
}
