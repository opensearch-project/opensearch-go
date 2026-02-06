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
	"context"
	"net/http"
)

// NewDefaultRoutes returns the default routing patterns for intelligent request routing.
// Creates optimized routing policies that leverage specialized node roles when available:
//   - Search operations: search nodes (3.0+) -> data nodes -> null
//   - Warm operations: warm nodes (2.4+) -> data nodes -> null
//   - Ingest operations: ingest nodes -> null
//
// This is broken out as a helper function for composability and testing.
func NewDefaultRoutes() []Route {
	// Create role-based policies with IfEnabled wrappers
	ingestPolicy := mustRolePolicy(RoleIngest)
	ingestIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return ingestPolicy.IsEnabled() },
		ingestPolicy,
		NewNullPolicy(),
	)

	// Enhanced search routing: prefer dedicated search nodes (OpenSearch 3.0+), fallback to data nodes
	searchRolePolicy := mustRolePolicy(RoleSearch)
	dataPolicy := mustRolePolicy(RoleData)
	searchIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return searchRolePolicy.IsEnabled() },
		searchRolePolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return dataPolicy.IsEnabled() },
			dataPolicy,
			NewNullPolicy(),
		),
	)

	// Warm data routing: prefer warm nodes (OpenSearch 2.4+), fallback to data nodes
	warmPolicy := mustRolePolicy(RoleWarm)
	warmIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return warmPolicy.IsEnabled() },
		warmPolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return dataPolicy.IsEnabled() },
			dataPolicy,
			NewNullPolicy(),
		),
	)

	// Define routes for different OpenSearch operations
	// Using the exact same patterns as OpenSearch server
	return []Route{
		// Bulk operations - route to ingest nodes (exact patterns from RestBulkAction.java)
		mustNewRouteMux("POST /_bulk", ingestIfEnabled),
		mustNewRouteMux("PUT /_bulk", ingestIfEnabled),
		mustNewRouteMux("POST /{index}/_bulk", ingestIfEnabled),
		mustNewRouteMux("PUT /{index}/_bulk", ingestIfEnabled),

		// Streaming bulk operations - route to ingest nodes (from RestBulkStreamingAction.java)
		// NOTE: Requires OpenSearch 3.0.0+; older versions will return HTTP 404
		mustNewRouteMux("POST /_bulk/stream", ingestIfEnabled),
		mustNewRouteMux("PUT /_bulk/stream", ingestIfEnabled),
		mustNewRouteMux("POST /{index}/_bulk/stream", ingestIfEnabled),
		mustNewRouteMux("PUT /{index}/_bulk/stream", ingestIfEnabled),

		// Ingest pipeline operations - route to ingest nodes
		mustNewRouteMux("PUT /_ingest/pipeline/{id}", ingestIfEnabled),
		mustNewRouteMux("POST /_ingest/pipeline/{id}", ingestIfEnabled),
		mustNewRouteMux("GET /_ingest/pipeline/{id}", ingestIfEnabled),
		mustNewRouteMux("DELETE /_ingest/pipeline/{id}", ingestIfEnabled),
		mustNewRouteMux("GET /_ingest/pipeline/", ingestIfEnabled),
		mustNewRouteMux("GET /_ingest/pipeline", ingestIfEnabled),
		mustNewRouteMux("GET /_ingest/pipeline/{id}/_simulate", ingestIfEnabled),
		mustNewRouteMux("POST /_ingest/pipeline/{id}/_simulate", ingestIfEnabled),
		mustNewRouteMux("GET /_ingest/pipeline/_simulate", ingestIfEnabled),
		mustNewRouteMux("POST /_ingest/pipeline/_simulate", ingestIfEnabled),

		// Searchable snapshot operations - route to warm nodes (OpenSearch 2.4+)
		// From RestRepositoryMountAction.java and RestRepositoryUnmountAction.java
		// Now handled cleanly by dual-ServeMux routing (system vs index paths)
		mustNewRouteMux("POST /_snapshot/{repository}/_mount", warmIfEnabled),
		mustNewRouteMux("POST /_snapshot/{repository}/{snapshot}/_mount", warmIfEnabled),
		mustNewRouteMux("DELETE /_snapshot/{repository}/{snapshot}/_mount/{index}", warmIfEnabled),

		// Search operations - route to data nodes
		mustNewRouteMux("GET /_search", searchIfEnabled),
		mustNewRouteMux("POST /_search", searchIfEnabled),
		mustNewRouteMux("GET /{index}/_search", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_search", searchIfEnabled),

		// Multi-search operations - route to data nodes
		mustNewRouteMux("GET /_msearch", searchIfEnabled),
		mustNewRouteMux("POST /_msearch", searchIfEnabled),
		mustNewRouteMux("GET /{index}/_msearch", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_msearch", searchIfEnabled),

		// Count queries - route to data nodes (from RestCountAction.java)
		mustNewRouteMux("GET /_count", searchIfEnabled),
		mustNewRouteMux("POST /_count", searchIfEnabled),
		mustNewRouteMux("GET /{index}/_count", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_count", searchIfEnabled),

		// Query operations - route to data nodes
		mustNewRouteMux("POST /{index}/_delete_by_query", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_update_by_query", searchIfEnabled),

		// Explain queries - route to data nodes for query analysis
		mustNewRouteMux("GET /{index}/_explain/{id}", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_explain/{id}", searchIfEnabled),

		// Document retrieval operations - route to data nodes
		// Get document operations (from RestGetAction.java)
		mustNewRouteMux("GET /{index}/_doc/{id}", searchIfEnabled),
		mustNewRouteMux("HEAD /{index}/_doc/{id}", searchIfEnabled),

		// Get source operations
		mustNewRouteMux("GET /{index}/_source/{id}", searchIfEnabled),
		mustNewRouteMux("HEAD /{index}/_source/{id}", searchIfEnabled),

		// Multi-get operations - route to data nodes for bulk retrieval
		mustNewRouteMux("GET /_mget", searchIfEnabled),
		mustNewRouteMux("POST /_mget", searchIfEnabled),
		mustNewRouteMux("GET /{index}/_mget", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_mget", searchIfEnabled),

		// Term vectors operations - route to data nodes for analysis tasks
		mustNewRouteMux("GET /{index}/_termvectors", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_termvectors", searchIfEnabled),
		mustNewRouteMux("GET /{index}/_termvectors/{id}", searchIfEnabled),
		mustNewRouteMux("POST /{index}/_termvectors/{id}", searchIfEnabled),

		// Data tier operations - route to warm nodes for warm/cold management
		// These operations are typically used for index lifecycle management
		mustNewRouteMux("POST /{index}/_settings", warmIfEnabled), // Index settings changes often involve tier moves
		mustNewRouteMux("PUT /{index}/_settings", warmIfEnabled),

		// Snapshot-backed search operations - route to warm nodes
		// These handle searches against searchable snapshots
		mustNewRouteMux("GET /{index}/_search/template", searchIfEnabled), // Template searches on warm indices
		mustNewRouteMux("POST /{index}/_search/template", searchIfEnabled),
		mustNewRouteMux("GET /_search/template", searchIfEnabled),
		mustNewRouteMux("POST /_search/template", searchIfEnabled),
	}
}

// NewSmartPolicy creates a request-aware policy that routes based on operation type.
// This provides intelligent routing for OpenSearch operations based on server-side patterns:
//   - If coordinating-only nodes are available, uses them exclusively (no fallback)
//   - Otherwise falls back to role-specific routing with optimal fallback chains:
//   - Bulk operations (including streaming bulk) -> ingest nodes
//   - Ingest pipeline management -> ingest nodes
//   - Search operations -> search nodes (OpenSearch 3.0+) -> data nodes
//   - Document retrieval (get, mget, source, termvectors) -> search nodes -> data nodes
//   - Searchable snapshot operations -> warm nodes (OpenSearch 2.4+) -> data nodes
//   - Index settings/tier management -> warm nodes -> data nodes
//   - Other operations -> round-robin fallback
//
// Role Prioritization:
//   - Search operations prefer dedicated search nodes when available (OpenSearch 3.0+)
//   - Warm operations prefer warm nodes for searchable snapshots (OpenSearch 2.4+)
//   - All role-specific policies fallback to data nodes, then round-robin
//
// Routes are based on exact patterns from OpenSearch server REST actions to ensure
// compatibility and optimal node utilization across all supported operations.
//
// Version Compatibility Notes:
//   - Streaming bulk endpoints (/_bulk/stream) require OpenSearch 3.0.0+
//   - Search role routing requires OpenSearch 3.0.0+ (falls back to data nodes)
//   - Warm role routing requires OpenSearch 2.4.0+ (falls back to data nodes)
//   - Searchable snapshot operations require OpenSearch 2.4.0+
//   - Older versions will return HTTP 404 for unsupported endpoints
//   - All other routes are compatible with OpenSearch 1.0.0+
func NewSmartPolicy() Policy {
	coordinatingPolicy := mustRolePolicy(RoleCoordinatingOnly)
	muxPolicy := NewMuxPolicy(NewDefaultRoutes())
	roundRobinPolicy := NewRoundRobinPolicy()

	// Return the first applicable policy directly
	// Note: This will be called by NewSmartRouter() which handles the policy chaining
	return NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
		coordinatingPolicy,
		NewPolicy(
			muxPolicy,
			roundRobinPolicy,
		),
	)
}

// NewDefaultPolicy creates a default policy that prioritizes coordinating-only nodes
// if available, otherwise falls back to round-robin selection across all available nodes.
func NewDefaultPolicy() Policy {
	coordinatingPolicy := mustRolePolicy(RoleCoordinatingOnly)
	roundRobinPolicy := NewRoundRobinPolicy()

	return NewPolicy(
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
			coordinatingPolicy,
			NewNullPolicy(),
		),
		roundRobinPolicy,
	)
}

// NewSmartRouter creates a router with intelligent request routing.
// This router provides optimal performance by routing requests to appropriate node types:
//
//   - If coordinating-only nodes are available, routes all requests to them (no fallback)
//   - Otherwise, uses HTTP pattern matching:
//   - Bulk operations (/_bulk, /_bulk/stream) -> ingest nodes
//   - Search operations (/_search, /_count, /_explain) -> data nodes
//   - Document operations (/_doc, /_mget, /_source) -> data nodes
//   - Ingest pipeline operations (/_ingest) -> ingest nodes
//   - Falls back to round-robin if specialized nodes unavailable
//
// This is the recommended router for production clusters with dedicated node roles.
// Compatible with all OpenSearch versions (streaming bulk requires 3.0.0+).
func NewSmartRouter() Router {
	return NewRouter(NewSmartPolicy())
}

// NewDefaultRouter creates a router with simple coordinating node preference.
// This router provides a balance between performance and simplicity:
//
//   - If coordinating-only nodes are available, routes all requests to them (no fallback)
//   - Otherwise, falls back to round-robin across all available nodes
//
// This is ideal for:
//   - Simple production setups without dedicated node roles
//   - Clusters where you want coordinating node preference without operation-specific routing
//
// For clusters with dedicated ingest/data nodes, consider NewSmartRouter() instead.
func NewDefaultRouter() Router {
	return NewRouter(NewDefaultPolicy())
}
