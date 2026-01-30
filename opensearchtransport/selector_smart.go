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

// NewSmartSelector creates a request-aware selector that routes based on operation type.
// This provides intelligent routing for OpenSearch operations based on server-side patterns:
//   - Bulk operations (including streaming bulk) -> ingest nodes
//   - Ingest pipeline management -> ingest nodes
//   - Search operations (search, count, explain, by-query) -> data nodes
//   - Document retrieval (get, mget, source, termvectors) -> data nodes
//   - Other operations -> default round-robin fallback
//
// Routes are based on exact patterns from OpenSearch server REST actions to ensure
// compatibility and optimal node utilization across all supported operations.
//
// Version Compatibility Notes:
//   - Streaming bulk endpoints (/_bulk/stream) require OpenSearch 3.0.0+
//   - Older versions will return HTTP 404 for unsupported endpoints
//   - All other routes are compatible with OpenSearch 1.0.0+
//
// Returns a ChainSelector that tries specific routing first, then falls back to round-robin.
func NewSmartSelector() RequestAwareSelector {
	// Create role-based selectors for different operation types
	ingestSelector := NewRoleBasedSelector(
		WithRequiredRoles(RoleIngest),
	)

	searchSelector := NewRoleBasedSelector(
		WithRequiredRoles(RoleData),
	)

	// Define routes for different OpenSearch operations
	// Using the exact same patterns as OpenSearch server
	routes := []Route{
		// Bulk operations - route to ingest nodes (exact patterns from RestBulkAction.java)
		{"POST /_bulk", ingestSelector},
		{"PUT /_bulk", ingestSelector},
		{"POST /{index}/_bulk", ingestSelector},
		{"PUT /{index}/_bulk", ingestSelector},

		// Streaming bulk operations - route to ingest nodes (from RestBulkStreamingAction.java)
		// NOTE: Requires OpenSearch 3.0.0+; older versions will return HTTP 404
		{"POST /_bulk/stream", ingestSelector},
		{"PUT /_bulk/stream", ingestSelector},
		{"POST /{index}/_bulk/stream", ingestSelector},
		{"PUT /{index}/_bulk/stream", ingestSelector},

		// Ingest pipeline operations - route to ingest nodes
		{"PUT /_ingest/pipeline/{id}", ingestSelector},
		{"POST /_ingest/pipeline/{id}", ingestSelector},
		{"GET /_ingest/pipeline/{id}", ingestSelector},
		{"DELETE /_ingest/pipeline/{id}", ingestSelector},
		{"GET /_ingest/pipeline/", ingestSelector},
		{"GET /_ingest/pipeline", ingestSelector},
		{"GET /_ingest/pipeline/{id}/_simulate", ingestSelector},
		{"POST /_ingest/pipeline/{id}/_simulate", ingestSelector},
		{"GET /_ingest/pipeline/_simulate", ingestSelector},
		{"POST /_ingest/pipeline/_simulate", ingestSelector},

		// Search operations - route to data nodes
		{"GET /_search", searchSelector},
		{"POST /_search", searchSelector},
		{"GET /{index}/_search", searchSelector},
		{"POST /{index}/_search", searchSelector},

		// Multi-search operations - route to data nodes
		{"GET /_msearch", searchSelector},
		{"POST /_msearch", searchSelector},
		{"GET /{index}/_msearch", searchSelector},
		{"POST /{index}/_msearch", searchSelector},

		// Count queries - route to data nodes (from RestCountAction.java)
		{"GET /_count", searchSelector},
		{"POST /_count", searchSelector},
		{"GET /{index}/_count", searchSelector},
		{"POST /{index}/_count", searchSelector},

		// Query-based operations - route to data nodes (use search coordination)
		{"POST /{index}/_delete_by_query", searchSelector}, // from RestDeleteByQueryAction.java
		{"POST /{index}/_update_by_query", searchSelector}, // from RestUpdateByQueryAction.java

		// Explain queries - route to data nodes (from RestExplainAction.java)
		{"GET /{index}/_explain/{id}", searchSelector},
		{"POST /{index}/_explain/{id}", searchSelector},

		// Document retrieval operations - route to data nodes for read locality
		{"GET /{index}/_doc/{id}", searchSelector},
		{"HEAD /{index}/_doc/{id}", searchSelector},
		{"GET /{index}/_source/{id}", searchSelector},
		{"HEAD /{index}/_source/{id}", searchSelector},

		// Multi-get operations - route to data nodes for batch read optimization
		{"GET /_mget", searchSelector},
		{"POST /_mget", searchSelector},
		{"GET /{index}/_mget", searchSelector},
		{"POST /{index}/_mget", searchSelector},

		// Term vectors operations - route to data nodes for analysis tasks
		{"GET /{index}/_termvectors", searchSelector},
		{"POST /{index}/_termvectors", searchSelector},
		{"GET /{index}/_termvectors/{id}", searchSelector},
		{"POST /{index}/_termvectors/{id}", searchSelector},
	}

	// Create a chain selector with specific routing first, then fallback
	return NewChainSelector(
		WithSelector(NewSelectorMux(routes)),
		WithSelector(NewRoundRobinSelector()),
	)
}

// NewSmartSelectorWithRoutes creates a smart selector with custom routes.
// This allows users to define their own routing patterns while still leveraging
// the http.ServeMux-based pattern matching.
func NewSmartSelectorWithRoutes(routes []Route) RequestAwareSelector {
	return NewSelectorMux(routes)
}
