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
	"time"
)

// NewDefaultRoutes returns the default routing patterns for intelligent request routing.
// Creates optimized routing policies that leverage specialized node roles when available:
//   - Search operations: search nodes (3.0+) -> data nodes -> null
//   - Warm operations: warm nodes (2.4+) -> data nodes -> null
//   - Ingest operations: ingest nodes -> null
//   - Data operations: data nodes -> null (shard maintenance: refresh, flush, forcemerge, segments)
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
	// Create separate data policy instance to avoid duplicate DiscoveryUpdate calls
	warmPolicy := mustRolePolicy(RoleWarm)
	warmDataFallbackPolicy := mustRolePolicy(RoleData)
	warmIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return warmPolicy.IsEnabled() },
		warmPolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return warmDataFallbackPolicy.IsEnabled() },
			warmDataFallbackPolicy,
			NewNullPolicy(),
		),
	)

	// Direct data node routing: for shard-maintenance operations (refresh, flush, forcemerge)
	// that must target actual data shards, not search-only nodes.
	dataDirectPolicy := mustRolePolicy(RoleData)
	dataIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return dataDirectPolicy.IsEnabled() },
		dataDirectPolicy,
		NewNullPolicy(),
	)

	// Define routes for different OpenSearch operations
	// Using the exact same patterns as OpenSearch server
	return buildRoleRoutes(ingestIfEnabled, searchIfEnabled, warmIfEnabled, dataIfEnabled)
}

// buildRoleRoutes constructs the canonical route table mapping OpenSearch REST
// API patterns to role-based policies. Both [NewDefaultRoutes] and
// [newAffinityRoutes] use this to avoid duplicating the pattern list.
//
// Parameters:
//   - ingest: policy for bulk indexing and ingest pipeline operations
//   - search: policy for search, retrieval, and query operations (search → data fallback)
//   - warm: policy for searchable snapshot and data tier operations (warm → data fallback)
//   - data: policy for shard-maintenance operations that must target data nodes directly
func buildRoleRoutes(ingest, search, warm, data Policy) []Route {
	// Shorthand for routes that benefit from ?preference=_local.
	// Read operations that accept the preference parameter use this
	// so the server prefers shard copies local to the receiving node.
	pl := routeAttrPreferLocal

	return []Route{
		// Bulk operations - ingest nodes (exact patterns from RestBulkAction.java)
		mustNewRouteMux("POST /_bulk", ingest),
		mustNewRouteMux("PUT /_bulk", ingest),
		mustNewRouteMux("POST /{index}/_bulk", ingest),
		mustNewRouteMux("PUT /{index}/_bulk", ingest),

		// Streaming bulk operations (from RestBulkStreamingAction.java)
		// NOTE: Requires OpenSearch 3.0.0+; older versions will return HTTP 404
		mustNewRouteMux("POST /_bulk/stream", ingest),
		mustNewRouteMux("PUT /_bulk/stream", ingest),
		mustNewRouteMux("POST /{index}/_bulk/stream", ingest),
		mustNewRouteMux("PUT /{index}/_bulk/stream", ingest),

		// Reindex operations (from RestReindexAction.java, module: reindex)
		mustNewRouteMux("POST /_reindex", ingest),

		// Ingest pipeline operations
		mustNewRouteMux("PUT /_ingest/pipeline/{id}", ingest),
		mustNewRouteMux("POST /_ingest/pipeline/{id}", ingest),
		mustNewRouteMux("GET /_ingest/pipeline/{id}", ingest),
		mustNewRouteMux("DELETE /_ingest/pipeline/{id}", ingest),
		mustNewRouteMux("GET /_ingest/pipeline/", ingest),
		mustNewRouteMux("GET /_ingest/pipeline", ingest),
		mustNewRouteMux("GET /_ingest/pipeline/{id}/_simulate", ingest),
		mustNewRouteMux("POST /_ingest/pipeline/{id}/_simulate", ingest),
		mustNewRouteMux("GET /_ingest/pipeline/_simulate", ingest),
		mustNewRouteMux("POST /_ingest/pipeline/_simulate", ingest),

		// Searchable snapshot operations (OpenSearch 2.4+)
		// From RestRepositoryMountAction.java and RestRepositoryUnmountAction.java
		mustNewRouteMux("POST /_snapshot/{repository}/_mount", warm),
		mustNewRouteMux("POST /_snapshot/{repository}/{snapshot}/_mount", warm),
		mustNewRouteMux("DELETE /_snapshot/{repository}/{snapshot}/_mount/{index}", warm),

		// Search operations (from RestSearchAction.java)
		mustNewRouteMuxAttrs("GET /_search", search, pl),
		mustNewRouteMuxAttrs("POST /_search", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_search", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_search", search, pl),

		// Multi-search operations (from RestMultiSearchAction.java)
		mustNewRouteMuxAttrs("GET /_msearch", search, pl),
		mustNewRouteMuxAttrs("POST /_msearch", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_msearch", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_msearch", search, pl),

		// Count queries (from RestCountAction.java)
		mustNewRouteMuxAttrs("GET /_count", search, pl),
		mustNewRouteMuxAttrs("POST /_count", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_count", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_count", search, pl),

		// Query operations
		mustNewRouteMux("POST /{index}/_delete_by_query", search),
		mustNewRouteMux("POST /{index}/_update_by_query", search),

		// Explain queries (from RestExplainAction.java)
		mustNewRouteMuxAttrs("GET /{index}/_explain/{id}", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_explain/{id}", search, pl),

		// Document retrieval operations (from RestGetAction.java)
		mustNewRouteMuxAttrs("GET /{index}/_doc/{id}", search, pl),
		mustNewRouteMuxAttrs("HEAD /{index}/_doc/{id}", search, pl),

		// Single-document write operations (from RestIndexAction.java)
		mustNewRouteMux("PUT /{index}/_doc/{id}", data),
		mustNewRouteMux("POST /{index}/_doc/{id}", data),
		mustNewRouteMux("POST /{index}/_doc", data),

		// Single-document create operations (from RestIndexAction.java)
		mustNewRouteMux("PUT /{index}/_create/{id}", data),
		mustNewRouteMux("POST /{index}/_create/{id}", data),

		// Single-document update operations (from RestUpdateAction.java)
		mustNewRouteMux("POST /{index}/_update/{id}", data),

		// Single-document delete operations (from RestDeleteAction.java)
		mustNewRouteMux("DELETE /{index}/_doc/{id}", data),

		// Get source operations (from RestGetSourceAction.java)
		mustNewRouteMuxAttrs("GET /{index}/_source/{id}", search, pl),
		mustNewRouteMuxAttrs("HEAD /{index}/_source/{id}", search, pl),

		// Multi-get operations (from RestMultiGetAction.java)
		mustNewRouteMuxAttrs("GET /_mget", search, pl),
		mustNewRouteMuxAttrs("POST /_mget", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_mget", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_mget", search, pl),

		// Term vectors operations (from RestTermVectorsAction.java)
		mustNewRouteMuxAttrs("GET /{index}/_termvectors", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_termvectors", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_termvectors/{id}", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_termvectors/{id}", search, pl),

		// Multi-term vectors operations (from RestMultiTermVectorsAction.java)
		mustNewRouteMuxAttrs("GET /_mtermvectors", search, pl),
		mustNewRouteMuxAttrs("POST /_mtermvectors", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_mtermvectors", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_mtermvectors", search, pl),

		// Template search operations (from RestSearchTemplateAction.java)
		mustNewRouteMuxAttrs("GET /{index}/_search/template", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_search/template", search, pl),
		mustNewRouteMuxAttrs("GET /_search/template", search, pl),
		mustNewRouteMuxAttrs("POST /_search/template", search, pl),

		// Multi-search template operations (from RestMultiSearchTemplateAction.java, module: lang-mustache)
		mustNewRouteMuxAttrs("GET /_msearch/template", search, pl),
		mustNewRouteMuxAttrs("POST /_msearch/template", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_msearch/template", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_msearch/template", search, pl),

		// Search shards (from RestClusterSearchShardsAction.java)
		// Used for cross-cluster search coordination; server internally
		// routes to remote_cluster_client nodes when needed.
		mustNewRouteMuxAttrs("GET /_search_shards", search, pl),
		mustNewRouteMuxAttrs("POST /_search_shards", search, pl),
		mustNewRouteMuxAttrs("GET /{index}/_search_shards", search, pl),
		mustNewRouteMuxAttrs("POST /{index}/_search_shards", search, pl),

		// Scroll operations (from RestSearchScrollAction.java, RestClearScrollAction.java)
		mustNewRouteMux("GET /_search/scroll", search),
		mustNewRouteMux("POST /_search/scroll", search),
		mustNewRouteMux("GET /_search/scroll/{scroll_id}", search),
		mustNewRouteMux("POST /_search/scroll/{scroll_id}", search),
		mustNewRouteMux("DELETE /_search/scroll", search),
		mustNewRouteMux("DELETE /_search/scroll/{scroll_id}", search),

		// Point-in-time operations (OpenSearch 2.0+)
		// From RestCreatePitAction.java, RestDeletePitAction.java, RestGetAllPitsAction.java
		mustNewRouteMux("POST /{index}/_search/point_in_time", search),
		mustNewRouteMux("DELETE /_search/point_in_time", search),
		mustNewRouteMux("DELETE /_search/point_in_time/_all", search),
		mustNewRouteMux("GET /_search/point_in_time/_all", search),

		// Field capabilities (from RestFieldCapabilitiesAction.java)
		mustNewRouteMux("GET /_field_caps", search),
		mustNewRouteMux("POST /_field_caps", search),
		mustNewRouteMux("GET /{index}/_field_caps", search),
		mustNewRouteMux("POST /{index}/_field_caps", search),

		// Validate query (from RestValidateQueryAction.java)
		mustNewRouteMux("GET /_validate/query", search),
		mustNewRouteMux("POST /_validate/query", search),
		mustNewRouteMux("GET /{index}/_validate/query", search),
		mustNewRouteMux("POST /{index}/_validate/query", search),

		// Rank evaluation (from RestRankEvalAction.java, module: rank-eval)
		mustNewRouteMux("GET /_rank_eval", search),
		mustNewRouteMux("POST /_rank_eval", search),
		mustNewRouteMux("GET /{index}/_rank_eval", search),
		mustNewRouteMux("POST /{index}/_rank_eval", search),

		// Data tier operations - index settings changes often involve tier moves
		mustNewRouteMux("POST /{index}/_settings", warm),
		mustNewRouteMux("PUT /{index}/_settings", warm),

		// Shard maintenance operations - must target data nodes directly since
		// search-only nodes (OpenSearch 3.0+) don't hold primary/replica data shards.
		// From RestRefreshAction.java
		mustNewRouteMux("GET /_refresh", data),
		mustNewRouteMux("POST /_refresh", data),
		mustNewRouteMux("GET /{index}/_refresh", data),
		mustNewRouteMux("POST /{index}/_refresh", data),

		// From RestFlushAction.java
		mustNewRouteMux("GET /_flush", data),
		mustNewRouteMux("POST /_flush", data),
		mustNewRouteMux("GET /{index}/_flush", data),
		mustNewRouteMux("POST /{index}/_flush", data),

		// From RestSyncedFlushAction.java (deprecated in OpenSearch 2.0+, returns deprecation warning)
		mustNewRouteMux("GET /_flush/synced", data),
		mustNewRouteMux("POST /_flush/synced", data),
		mustNewRouteMux("GET /{index}/_flush/synced", data),
		mustNewRouteMux("POST /{index}/_flush/synced", data),

		// From RestForceMergeAction.java
		mustNewRouteMux("POST /_forcemerge", data),
		mustNewRouteMux("POST /{index}/_forcemerge", data),

		// From RestIndicesSegmentsAction.java
		mustNewRouteMux("GET /_segments", data),
		mustNewRouteMux("GET /{index}/_segments", data),

		// From RestClearIndicesCacheAction.java
		mustNewRouteMux("POST /_cache/clear", data),
		mustNewRouteMux("POST /{index}/_cache/clear", data),

		// From RestRecoveryAction.java
		mustNewRouteMux("GET /{index}/_recovery", data),

		// From RestIndicesShardStoresAction.java
		mustNewRouteMux("GET /{index}/_shard_stores", data),

		// From RestIndicesStatsAction.java
		mustNewRouteMux("GET /_stats", data),
		mustNewRouteMux("GET /_stats/{metric}", data),
		mustNewRouteMux("GET /{index}/_stats", data),
		mustNewRouteMux("GET /{index}/_stats/{metric}", data),

		// Rethrottle operations (from RestRethrottleAction.java, module: reindex)
		mustNewRouteMux("POST /_reindex/{taskId}/_rethrottle", data),
		mustNewRouteMux("POST /_update_by_query/{taskId}/_rethrottle", data),
		mustNewRouteMux("POST /_delete_by_query/{taskId}/_rethrottle", data),
	}
}

// NewMuxRoutePolicy creates a request-aware policy that routes based on operation type.
// This provides role-based routing for OpenSearch operations based on server-side patterns:
//   - If coordinating-only nodes are available, uses them exclusively (no fallback)
//   - Otherwise falls back to role-specific routing with optimal fallback chains:
//   - Bulk operations (including streaming bulk), reindex -> ingest nodes
//   - Ingest pipeline management -> ingest nodes
//   - Search operations (search, msearch, count, scroll, PIT, field_caps, validate, rank_eval) -> search nodes (3.0+) -> data nodes
//   - Document retrieval (get, mget, source, termvectors, mtermvectors) -> search nodes -> data nodes
//   - Template operations (search template, msearch template) -> search nodes -> data nodes
//   - Searchable snapshot operations -> warm nodes (OpenSearch 2.4+) -> data nodes
//   - Index settings/tier management -> warm nodes -> data nodes
//   - Shard maintenance (refresh, flush, synced flush, forcemerge, segments, cache clear) -> data nodes
//   - Shard diagnostics (recovery, shard_stores, stats) -> data nodes
//   - Rethrottle operations (reindex, update_by_query, delete_by_query) -> data nodes
//   - Other operations -> round-robin fallback
//
// For affinity-aware routing that adds per-index node consistency and
// RTT-based scoring, use [NewSmartPolicy] instead.
func NewMuxRoutePolicy() Policy {
	coordinatingPolicy := mustRolePolicy(RoleCoordinatingOnly)
	muxPolicy := NewMuxPolicy(NewDefaultRoutes())
	roundRobinPolicy := NewRoundRobinPolicy()

	return NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
		coordinatingPolicy,
		NewPolicy(
			muxPolicy,
			roundRobinPolicy,
		),
	)
}

// NewRoundRobinDefaultPolicy creates a policy that prioritizes coordinating-only nodes
// if available, otherwise falls back to round-robin selection across all available nodes.
//
// For role-based routing, use [NewMuxRoutePolicy]. For affinity-aware routing,
// use [NewSmartPolicy].
func NewRoundRobinDefaultPolicy() Policy {
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

// NewMuxRouter creates a router with role-based request routing.
// Routes operations to appropriate node types based on HTTP method and path:
//
//   - If coordinating-only nodes are available, routes all requests to them
//   - Bulk operations (/_bulk, /_bulk/stream), reindex -> ingest nodes
//   - Search operations (/_search, /_msearch, /_count, scroll, PIT, field_caps, validate, rank_eval) -> search nodes (3.0+) -> data nodes
//   - Document operations (/_doc, /_mget, /_source, /_termvectors, /_mtermvectors) -> search nodes (3.0+) -> data nodes
//   - Template operations (/_search/template, /_msearch/template) -> search nodes (3.0+) -> data nodes
//   - Searchable snapshots (/_snapshot/*/_mount) -> warm nodes (2.4+) -> data nodes
//   - Index settings (/{index}/_settings) -> warm nodes (2.4+) -> data nodes
//   - Shard maintenance (/_refresh, /_flush, /_flush/synced, /_forcemerge, /_segments, /_cache/clear) -> data nodes
//   - Shard diagnostics (/_recovery, /_shard_stores, /_stats) -> data nodes
//   - Falls back to round-robin if specialized nodes unavailable
//
// For affinity-aware routing that adds per-index node consistency and
// RTT-based scoring on top of role routing, use [NewSmartRouter] instead.
func NewMuxRouter() Router {
	return NewRouter(NewMuxRoutePolicy())
}

// NewRoundRobinRouter creates a router with simple coordinating node preference.
//
//   - If coordinating-only nodes are available, routes all requests to them
//   - Otherwise, falls back to round-robin across all available nodes
//
// For role-based routing, use [NewMuxRouter]. For the recommended
// production router with affinity, use [NewSmartRouter].
func NewRoundRobinRouter() Router {
	return NewRouter(NewRoundRobinDefaultPolicy())
}

// AffinityOption configures the affinity routing behavior.
type AffinityOption func(*affinityConfig)

type affinityConfig struct {
	minFanOut       int
	maxFanOut       int
	overrides       map[string]int
	idleEvictionTTL time.Duration
	decay           float64
	fanOutPerReq    float64
}

func defaultAffinityConfig() affinityConfig {
	return affinityConfig{
		minFanOut:       defaultMinFanOut,
		maxFanOut:       defaultMaxFanOut,
		idleEvictionTTL: defaultIdleEvictionTTL,
		decay:           defaultDecayFactor,
		fanOutPerReq:    defaultFanOutPerRequest,
	}
}

// WithMinFanOut sets the minimum number of nodes in an index slot.
// Default: 1.
func WithMinFanOut(n int) AffinityOption {
	return func(c *affinityConfig) { c.minFanOut = n }
}

// WithMaxFanOut sets the maximum number of nodes in an index slot.
// 0 uses the default (32). Default: 32.
func WithMaxFanOut(n int) AffinityOption {
	return func(c *affinityConfig) { c.maxFanOut = n }
}

// WithIndexFanOut sets per-index fan-out overrides. Overrides take
// precedence over dynamic fan-out calculation.
func WithIndexFanOut(m map[string]int) AffinityOption {
	return func(c *affinityConfig) { c.overrides = m }
}

// WithIdleEvictionTTL sets how long idle index slots persist before
// being evicted from the cache. Default: 90 minutes.
func WithIdleEvictionTTL(d time.Duration) AffinityOption {
	return func(c *affinityConfig) { c.idleEvictionTTL = d }
}

// WithDecayFactor sets the exponential decay factor for request counters.
// Must be between 0 and 1 exclusive. Default: 0.999.
func WithDecayFactor(d float64) AffinityOption {
	return func(c *affinityConfig) { c.decay = d }
}

// WithFanOutPerRequest sets the decay-counter-to-fan-out divisor.
// When the decay counter reaches this threshold, fan-out grows by 1.
// Default: 500.
func WithFanOutPerRequest(f float64) AffinityOption {
	return func(c *affinityConfig) { c.fanOutPerReq = f }
}

// NewSmartPolicy creates a request-aware policy with affinity routing.
// This is the recommended policy for production clusters. It extends
// role-based mux routing with per-index node affinity:
//
//  1. If coordinating-only nodes exist, route all traffic to them
//  2. MuxPolicy with affinity-wrapped role routes: within each role pool
//     (data, search, ingest, warm), rendezvous hashing and RTT-based scoring
//     select the best node for the target index
//  3. RoundRobinPolicy fallback for unmatched requests
//
// See [guides/affinity_routing.md] for the full algorithm description.
func NewSmartPolicy(opts ...AffinityOption) Policy {
	cfg := defaultAffinityConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	cacheCfg := indexSlotCacheConfig{
		minFanOut:       cfg.minFanOut,
		maxFanOut:       cfg.maxFanOut,
		overrides:       cfg.overrides,
		idleEvictionTTL: cfg.idleEvictionTTL,
		decayFactor:     cfg.decay,
		fanOutPerReq:    cfg.fanOutPerReq,
	}

	cache := newIndexSlotCache(cacheCfg)

	coordinatingPolicy := mustRolePolicy(RoleCoordinatingOnly)

	// The mux policy delegates each matched request to an affinity-wrapped
	// role policy. Within data/search/ingest/warm nodes, the affinity wrapper
	// applies this Client's RTT worldview and rendezvous hashing to select the
	// best node for the target index, giving us cache locality and AZ-aware
	// load distribution within each role pool.
	muxPolicy := NewMuxPolicy(newAffinityRoutes(cache, cfg.decay))

	roundRobinPolicy := NewRoundRobinPolicy()

	return NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
		coordinatingPolicy,
		NewPolicy(
			muxPolicy,
			roundRobinPolicy,
		),
	)
}

// NewSmartRouter creates a router with affinity-aware request routing.
// This is the recommended router for production clusters. It combines
// role-based routing with per-index node affinity, RTT-based AZ preference,
// and self-stabilizing load distribution.
//
// See [NewSmartPolicy] for the full routing chain and [guides/affinity_routing.md]
// for the algorithm description.
func NewSmartRouter(opts ...AffinityOption) Router {
	return NewRouter(NewSmartPolicy(opts...))
}

// NewDefaultPolicy creates the recommended default policy for production use.
// Equivalent to [NewSmartPolicy] with default options.
func NewDefaultPolicy() Policy {
	return NewSmartPolicy()
}

// NewDefaultRouter creates the recommended default router for production use.
// Equivalent to [NewSmartRouter] with default options.
func NewDefaultRouter() Router {
	return NewSmartRouter()
}

// newAffinityRoutes mirrors [NewDefaultRoutes] but wraps each role-based
// sub-policy with an [affinityPolicyWrapper]. When the mux matches a request
// to a role pool (e.g., data nodes for a search), the affinity wrapper applies
// rendezvous hashing and RTT-based scoring within that pool so the same index
// consistently routes to the same subset of role-appropriate nodes.
func newAffinityRoutes(cache *indexSlotCache, decay float64) []Route {
	wrapRead := func(p Policy) Policy {
		return wrapWithAffinity(p, cache, decay, &shardCostForReads)
	}
	wrapWrite := func(p Policy) Policy {
		return wrapWithAffinity(p, cache, decay, &shardCostForWrites)
	}

	// Create role-based policies (same as NewDefaultRoutes)
	ingestPolicy := mustRolePolicy(RoleIngest)
	ingestIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return ingestPolicy.IsEnabled() },
		ingestPolicy,
		NewNullPolicy(),
	)

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

	warmPolicy := mustRolePolicy(RoleWarm)
	warmDataFallbackPolicy := mustRolePolicy(RoleData)
	warmIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return warmPolicy.IsEnabled() },
		warmPolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return warmDataFallbackPolicy.IsEnabled() },
			warmDataFallbackPolicy,
			NewNullPolicy(),
		),
	)

	// Direct data node routing for shard-maintenance operations
	dataDirectPolicy := mustRolePolicy(RoleData)
	dataIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return dataDirectPolicy.IsEnabled() },
		dataDirectPolicy,
		NewNullPolicy(),
	)

	// Wrap each IfEnabled chain with affinity so that within each role pool,
	// affinity selection picks the best node for the target index.
	// Ingest routes handle bulk writes -> prefer primary-hosting nodes.
	// Search, warm, and data routes handle reads/maintenance -> prefer replica-hosting nodes.
	affinityIngest := wrapWrite(ingestIfEnabled)
	affinitySearch := wrapRead(searchIfEnabled)
	affinityWarm := wrapRead(warmIfEnabled)
	affinityData := wrapWrite(dataIfEnabled)

	return buildRoleRoutes(affinityIngest, affinitySearch, affinityWarm, affinityData)
}
