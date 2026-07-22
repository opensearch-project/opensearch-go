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
	"fmt"
	"math"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Compile-time interface compliance checks.
var (
	_ Router             = (*PolicyChain)(nil)
	_ Policy             = (*PolicyChain)(nil)
	_ policyConfigurable = (*PolicyChain)(nil)
	_ policyTyped        = (*PolicyChain)(nil)
	_ policyOverrider    = (*PolicyChain)(nil)
)

// Router defines the interface for request routing.
type Router interface {
	Route(ctx context.Context, req *http.Request) (NextHop, error)
	OnSuccess(*Connection)                                            // Report successful connection use
	OnFailure(*Connection) error                                      // Report failed connection use
	DiscoveryUpdate(added, removed, unchanged []*Connection) error    // Update router with discovered nodes
	CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error // Health check dead connections across all policies
	RotateStandby(ctx context.Context, count int) (int, error)        // Rotate standby connections across all policy pools
}

// PolicyChain is a composite policy that tries sub-policies in sequence
// until one matches. It implements both [Router] and [Policy].
type PolicyChain struct {
	policies    []Policy
	policyState atomic.Int32 // Bitfield: psEnabled|psDisabled|psEnvEnabled|psEnvDisabled
}

func (r *PolicyChain) policyTypeName() string      { return policyTypeNameChain }
func (r *PolicyChain) setEnvOverride(enabled bool) { psSetEnvOverride(&r.policyState, enabled) }

// NewRouter creates a router that tries policies in order.
func NewRouter(policies ...Policy) Router {
	return &PolicyChain{policies: policies}
}

// NewDefaultRoutes returns the default routing patterns for intelligent request routing.
// Creates optimized routing policies that leverage specialized node roles when available:
//   - Search operations: search nodes (3.0+) -> data nodes -> pass
//   - Warm operations: warm nodes (2.4+) -> data nodes -> pass
//   - Ingest operations: ingest nodes -> pass
//   - Data operations: data nodes -> pass (shard maintenance: refresh, flush, forcemerge, segments)
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

	// Cluster-manager routing: cluster-state operations are served by the
	// elected cluster manager; prefer cluster_manager nodes, fall back to data
	// nodes (which coordinate-forward to the manager).
	clusterMgrPolicy := mustRolePolicy(RoleClusterManager)
	clusterMgrDataFallbackPolicy := mustRolePolicy(RoleData)
	clusterMgrIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return clusterMgrPolicy.IsEnabled() },
		clusterMgrPolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return clusterMgrDataFallbackPolicy.IsEnabled() },
			clusterMgrDataFallbackPolicy,
			NewNullPolicy(),
		),
	)

	// Define routes for different OpenSearch operations
	// Using the exact same patterns as OpenSearch server
	return buildRoleRoutes(defaultRoleRoutes(ingestIfEnabled, searchIfEnabled, warmIfEnabled, dataIfEnabled, clusterMgrIfEnabled))
}

// OpenSearch server-side thread pool names. Used as the pool identifier in
// [wrapWithRouter] so that per-pool congestion windows track the correct
// server-side resource. Names match the output of GET /_cat/thread_pool.
//
// Authoritative source: ThreadPool.Names in the OpenSearch server:
// server/src/main/java/org/opensearch/threadpool/ThreadPool.java
const (
	poolWrite      = "write"
	poolSearch     = "search"
	poolGet        = "get"
	poolManagement = "management"
	poolRefresh    = "refresh"
	poolFlush      = "flush"
	poolForceMerge = "force_merge"
)

// roleRoutes groups per-role policy references used by [buildRoleRoutes].
// Each field maps a combination of node role and server-side thread pool
// to a policy. For scored routes, the policy wrapper carries the pool name
// and shard cost table; for non-scored routes, they are unused.
type roleRoutes struct {
	// Bulk/reindex -> ingest nodes, server "write" pool
	ingestWrite Policy
	// Ingest pipeline management -> ingest nodes, server "management" pool
	ingestMgmt Policy
	// Search, msearch, count, scroll, PIT, search template, msearch template,
	// search shards, validate, rank eval, delete/update_by_query -> search/data nodes, server "search" pool
	searchRead Policy
	// Get, mget, source, explain, termvectors, mtermvectors -> search/data nodes, server "get" pool
	getRead Policy
	// Single-doc writes (index, create, update, delete) -> data nodes, server "write" pool
	dataWrite Policy
	// Refresh -> data nodes, server "refresh" pool
	dataRefresh Policy
	// Flush, synced flush -> data nodes, server "flush" pool
	dataFlush Policy
	// Forcemerge -> data nodes, server "force_merge" pool
	dataForceMerge Policy
	// Segments, cache clear, recovery, shard stores, stats -> data nodes, server "management" pool
	dataMgmt Policy
	// Rethrottle -> data nodes, server "management" pool (reuses dataMgmt)
	// Field capabilities -> search/data nodes, server "management" pool
	searchMgmt Policy
	// Warm/tier operations -> warm/data nodes, server "management" pool
	warmMgmt Policy
	// Cluster-state reads (health, state, settings-get, index/mapping/alias/
	// template get) -> cluster_manager nodes, server "management" pool
	clusterMgrRead Policy
	// Cluster-state writes (settings-put, reroute, index create/delete, mapping/
	// alias/template put, snapshot, dangling, data-stream writes) ->
	// cluster_manager nodes, server "write" pool
	clusterMgrWrite Policy
}

// defaultRoleRoutes creates a roleRoutes where all routes for a given role
// use the same policy. This is the non-scoring configuration used by
// [NewDefaultRoutes] where pool-level differentiation isn't needed.
func defaultRoleRoutes(ingest, search, warm, data, clusterMgr Policy) roleRoutes {
	return roleRoutes{
		ingestWrite:     ingest,
		ingestMgmt:      ingest,
		searchRead:      search,
		getRead:         search,
		dataWrite:       data,
		dataRefresh:     data,
		dataFlush:       data,
		dataForceMerge:  data,
		dataMgmt:        data,
		searchMgmt:      search,
		warmMgmt:        warm,
		clusterMgrRead:  clusterMgr,
		clusterMgrWrite: clusterMgr,
	}
}

// buildRoleRoutes constructs the canonical route table mapping OpenSearch REST
// API patterns to role-based policies. Both [NewDefaultRoutes] and
// [newScoredRoutes] use this to avoid duplicating the pattern list.
//
// Pool names are NOT set on routes --they are a property of the policy.
// For scored routes, the [poolRouter] carries the pool name
// and sets it on [NextHop.PoolName] during Eval(). For non-scored routes,
// the pool name is empty (uses the default pool for congestion tracking).
func buildRoleRoutes(r roleRoutes) []Route {
	return []Route{
		// -- Cluster info -- any node, "management" pool
		// From RestMainAction.java
		NewRoute("GET /", r.searchMgmt).Op(OpClusterInfo).MustBuild(),
		NewRoute("HEAD /", r.searchMgmt).Op(OpClusterInfo).MustBuild(),

		// -- Bulk operations -- ingest nodes, "write" pool
		// From RestBulkAction.java
		NewRoute("POST /_bulk", r.ingestWrite).Op(OpBulk).MustBuild(),
		NewRoute("PUT /_bulk", r.ingestWrite).Op(OpBulk).MustBuild(),
		NewRoute("POST /{index}/_bulk", r.ingestWrite).Op(OpBulk).MustBuild(),
		NewRoute("PUT /{index}/_bulk", r.ingestWrite).Op(OpBulk).MustBuild(),

		// Streaming bulk operations (from RestBulkStreamingAction.java)
		// NOTE: Requires OpenSearch 3.0.0+; older versions will return HTTP 404
		NewRoute("POST /_bulk/stream", r.ingestWrite).Op(OpBulkStream).MustBuild(),
		NewRoute("PUT /_bulk/stream", r.ingestWrite).Op(OpBulkStream).MustBuild(),
		NewRoute("POST /{index}/_bulk/stream", r.ingestWrite).Op(OpBulkStream).MustBuild(),
		NewRoute("PUT /{index}/_bulk/stream", r.ingestWrite).Op(OpBulkStream).MustBuild(),

		// Reindex operations (from RestReindexAction.java, module: reindex)
		NewRoute("POST /_reindex", r.ingestWrite).Op(OpReindex).MustBuild(),

		// -- Ingest pipeline management -- ingest nodes, "management" pool
		NewRoute("PUT /_ingest/pipeline/{id}", r.ingestMgmt).Op(OpIngestCreate).MustBuild(),
		NewRoute("POST /_ingest/pipeline/{id}", r.ingestMgmt).Op(OpIngestCreate).MustBuild(),
		NewRoute("GET /_ingest/pipeline/{id}", r.ingestMgmt).Op(OpIngestGet).MustBuild(),
		NewRoute("DELETE /_ingest/pipeline/{id}", r.ingestMgmt).Op(OpIngestDelete).MustBuild(),
		NewRoute("GET /_ingest/pipeline/", r.ingestMgmt).Op(OpIngestGet).MustBuild(),
		NewRoute("GET /_ingest/pipeline", r.ingestMgmt).Op(OpIngestGet).MustBuild(),
		NewRoute("GET /_ingest/pipeline/{id}/_simulate", r.ingestMgmt).Op(OpIngestSimulate).MustBuild(),
		NewRoute("POST /_ingest/pipeline/{id}/_simulate", r.ingestMgmt).Op(OpIngestSimulate).MustBuild(),
		NewRoute("GET /_ingest/pipeline/_simulate", r.ingestMgmt).Op(OpIngestSimulate).MustBuild(),
		NewRoute("POST /_ingest/pipeline/_simulate", r.ingestMgmt).Op(OpIngestSimulate).MustBuild(),

		// -- Searchable snapshot operations -- warm nodes, "management" pool
		// From RestRepositoryMountAction.java and RestRepositoryUnmountAction.java (OpenSearch 2.4+)
		NewRoute("POST /_snapshot/{repository}/_mount", r.warmMgmt).Op(OpOther).MustBuild(),
		NewRoute("POST /_snapshot/{repository}/{snapshot}/_mount", r.warmMgmt).Op(OpOther).MustBuild(),
		NewRoute("DELETE /_snapshot/{repository}/{snapshot}/_mount/{index}", r.warmMgmt).Op(OpOther).MustBuild(),

		// -- Search operations -- search/data nodes, "search" pool
		// From RestSearchAction.java
		NewRoute("GET /_search", r.searchRead).Op(OpSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("POST /_search", r.searchRead).Op(OpSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("GET /{index}/_search", r.searchRead).Op(OpSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("POST /{index}/_search", r.searchRead).Op(OpSearch).InjectAdaptiveMCSR().MustBuild(),

		// Multi-search operations (from RestMultiSearchAction.java)
		NewRoute("GET /_msearch", r.searchRead).Op(OpMSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("POST /_msearch", r.searchRead).Op(OpMSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("GET /{index}/_msearch", r.searchRead).Op(OpMSearch).InjectAdaptiveMCSR().MustBuild(),
		NewRoute("POST /{index}/_msearch", r.searchRead).Op(OpMSearch).InjectAdaptiveMCSR().MustBuild(),

		// Count queries (from RestCountAction.java)
		NewRoute("GET /_count", r.searchRead).Op(OpCount).MustBuild(),
		NewRoute("POST /_count", r.searchRead).Op(OpCount).MustBuild(),
		NewRoute("GET /{index}/_count", r.searchRead).Op(OpCount).MustBuild(),
		NewRoute("POST /{index}/_count", r.searchRead).Op(OpCount).MustBuild(),

		// Query operations (from RestDeleteByQueryAction.java, RestUpdateByQueryAction.java)
		NewRoute("POST /{index}/_delete_by_query", r.searchRead).Op(OpDeleteByQuery).MustBuild(),
		NewRoute("POST /{index}/_update_by_query", r.searchRead).Op(OpUpdateByQuery).MustBuild(),

		// Explain queries (from RestExplainAction.java)
		NewRoute("GET /{index}/_explain/{id}", r.getRead).Op(OpExplain).MustBuild(),
		NewRoute("POST /{index}/_explain/{id}", r.getRead).Op(OpExplain).MustBuild(),

		// Document retrieval operations (from RestGetAction.java)
		NewRoute("GET /{index}/_doc/{id}", r.getRead).Op(OpDocGet).MustBuild(),
		NewRoute("HEAD /{index}/_doc/{id}", r.getRead).Op(OpDocExists).MustBuild(),

		// -- Single-document write operations -- data nodes, "write" pool
		// From RestIndexAction.java
		NewRoute("PUT /{index}/_doc/{id}", r.dataWrite).Op(OpDocIndex).MustBuild(),
		NewRoute("POST /{index}/_doc/{id}", r.dataWrite).Op(OpDocIndex).MustBuild(),
		NewRoute("POST /{index}/_doc", r.dataWrite).Op(OpDocIndex).MustBuild(),

		// Single-document create operations (from RestIndexAction.java)
		NewRoute("PUT /{index}/_create/{id}", r.dataWrite).Op(OpDocCreate).MustBuild(),
		NewRoute("POST /{index}/_create/{id}", r.dataWrite).Op(OpDocCreate).MustBuild(),

		// Single-document update operations (from RestUpdateAction.java)
		NewRoute("POST /{index}/_update/{id}", r.dataWrite).Op(OpDocUpdate).MustBuild(),

		// Single-document delete operations (from RestDeleteAction.java)
		NewRoute("DELETE /{index}/_doc/{id}", r.dataWrite).Op(OpDocDelete).MustBuild(),

		// -- Get / source / multi-get / term vectors -- search/data nodes, "get" pool
		// From RestGetSourceAction.java
		NewRoute("GET /{index}/_source/{id}", r.getRead).Op(OpDocSourceGet).MustBuild(),
		NewRoute("HEAD /{index}/_source/{id}", r.getRead).Op(OpDocSourceExist).MustBuild(),

		// Multi-get operations (from RestMultiGetAction.java)
		NewRoute("GET /_mget", r.getRead).Op(OpMGet).MustBuild(),
		NewRoute("POST /_mget", r.getRead).Op(OpMGet).MustBuild(),
		NewRoute("GET /{index}/_mget", r.getRead).Op(OpMGet).MustBuild(),
		NewRoute("POST /{index}/_mget", r.getRead).Op(OpMGet).MustBuild(),

		// Term vectors operations (from RestTermVectorsAction.java)
		NewRoute("GET /{index}/_termvectors", r.getRead).Op(OpTermVectors).MustBuild(),
		NewRoute("POST /{index}/_termvectors", r.getRead).Op(OpTermVectors).MustBuild(),
		NewRoute("GET /{index}/_termvectors/{id}", r.getRead).Op(OpTermVectors).MustBuild(),
		NewRoute("POST /{index}/_termvectors/{id}", r.getRead).Op(OpTermVectors).MustBuild(),

		// Multi-term vectors operations (from RestMultiTermVectorsAction.java)
		NewRoute("GET /_mtermvectors", r.getRead).Op(OpMTermVectors).MustBuild(),
		NewRoute("POST /_mtermvectors", r.getRead).Op(OpMTermVectors).MustBuild(),
		NewRoute("GET /{index}/_mtermvectors", r.getRead).Op(OpMTermVectors).MustBuild(),
		NewRoute("POST /{index}/_mtermvectors", r.getRead).Op(OpMTermVectors).MustBuild(),

		// -- Search template / msearch template -- search/data nodes, "search" pool
		// From RestSearchTemplateAction.java
		NewRoute("GET /{index}/_search/template", r.searchRead).Op(OpSearchTemplate).MustBuild(),
		NewRoute("POST /{index}/_search/template", r.searchRead).Op(OpSearchTemplate).MustBuild(),
		NewRoute("GET /_search/template", r.searchRead).Op(OpSearchTemplate).MustBuild(),
		NewRoute("POST /_search/template", r.searchRead).Op(OpSearchTemplate).MustBuild(),

		// Multi-search template operations (from RestMultiSearchTemplateAction.java, module: lang-mustache)
		NewRoute("GET /_msearch/template", r.searchRead).Op(OpMSearchTmpl).MustBuild(),
		NewRoute("POST /_msearch/template", r.searchRead).Op(OpMSearchTmpl).MustBuild(),
		NewRoute("GET /{index}/_msearch/template", r.searchRead).Op(OpMSearchTmpl).MustBuild(),
		NewRoute("POST /{index}/_msearch/template", r.searchRead).Op(OpMSearchTmpl).MustBuild(),

		// -- Search shards -- search/data nodes, "search" pool
		// From RestClusterSearchShardsAction.java
		NewRoute("GET /_search_shards", r.searchRead).Op(OpSearchShards).MustBuild(),
		NewRoute("POST /_search_shards", r.searchRead).Op(OpSearchShards).MustBuild(),
		NewRoute("GET /{index}/_search_shards", r.searchRead).Op(OpSearchShards).MustBuild(),
		NewRoute("POST /{index}/_search_shards", r.searchRead).Op(OpSearchShards).MustBuild(),

		// -- Scroll operations -- search/data nodes, "search" pool
		// From RestSearchScrollAction.java, RestClearScrollAction.java
		NewRoute("GET /_search/scroll", r.searchRead).Op(OpScrollGet).MustBuild(),
		NewRoute("POST /_search/scroll", r.searchRead).Op(OpScrollGet).MustBuild(),
		NewRoute("GET /_search/scroll/{scroll_id}", r.searchRead).Op(OpScrollGet).MustBuild(),
		NewRoute("POST /_search/scroll/{scroll_id}", r.searchRead).Op(OpScrollGet).MustBuild(),
		NewRoute("DELETE /_search/scroll", r.searchRead).Op(OpScrollDelete).MustBuild(),
		NewRoute("DELETE /_search/scroll/{scroll_id}", r.searchRead).Op(OpScrollDelete).MustBuild(),

		// -- Point-in-time operations -- search/data nodes, "search" pool (OpenSearch 2.0+)
		// From RestCreatePitAction.java, RestDeletePitAction.java, RestGetAllPitsAction.java
		NewRoute("POST /{index}/_search/point_in_time", r.searchRead).Op(OpPITCreate).MustBuild(),
		NewRoute("DELETE /_search/point_in_time", r.searchRead).Op(OpPITDelete).MustBuild(),
		NewRoute("DELETE /_search/point_in_time/_all", r.searchRead).Op(OpPITDelete).MustBuild(),
		NewRoute("GET /_search/point_in_time/_all", r.searchRead).Op(OpPITList).MustBuild(),

		// -- Field capabilities -- search/data nodes, "management" pool
		// From RestFieldCapabilitiesAction.java
		NewRoute("GET /_field_caps", r.searchMgmt).Op(OpFieldCaps).MustBuild(),
		NewRoute("POST /_field_caps", r.searchMgmt).Op(OpFieldCaps).MustBuild(),
		NewRoute("GET /{index}/_field_caps", r.searchMgmt).Op(OpFieldCaps).MustBuild(),
		NewRoute("POST /{index}/_field_caps", r.searchMgmt).Op(OpFieldCaps).MustBuild(),

		// -- Validate query -- search/data nodes, "search" pool
		// From RestValidateQueryAction.java
		NewRoute("GET /_validate/query", r.searchRead).Op(OpValidate).MustBuild(),
		NewRoute("POST /_validate/query", r.searchRead).Op(OpValidate).MustBuild(),
		NewRoute("GET /{index}/_validate/query", r.searchRead).Op(OpValidate).MustBuild(),
		NewRoute("POST /{index}/_validate/query", r.searchRead).Op(OpValidate).MustBuild(),

		// -- Rank evaluation -- search/data nodes, "search" pool
		// From RestRankEvalAction.java, module: rank-eval
		NewRoute("GET /_rank_eval", r.searchRead).Op(OpRankEval).MustBuild(),
		NewRoute("POST /_rank_eval", r.searchRead).Op(OpRankEval).MustBuild(),
		NewRoute("GET /{index}/_rank_eval", r.searchRead).Op(OpRankEval).MustBuild(),
		NewRoute("POST /{index}/_rank_eval", r.searchRead).Op(OpRankEval).MustBuild(),

		// -- Data tier operations -- warm/data nodes, "management" pool
		NewRoute("POST /{index}/_settings", r.warmMgmt).Op(OpOther).MustBuild(),
		NewRoute("PUT /{index}/_settings", r.warmMgmt).Op(OpOther).MustBuild(),

		// -- Refresh -- data nodes, "refresh" pool
		// From RestRefreshAction.java
		NewRoute("GET /_refresh", r.dataRefresh).Op(OpRefresh).MustBuild(),
		NewRoute("POST /_refresh", r.dataRefresh).Op(OpRefresh).MustBuild(),
		NewRoute("GET /{index}/_refresh", r.dataRefresh).Op(OpRefresh).MustBuild(),
		NewRoute("POST /{index}/_refresh", r.dataRefresh).Op(OpRefresh).MustBuild(),

		// -- Flush -- data nodes, "flush" pool
		// From RestFlushAction.java
		NewRoute("GET /_flush", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("POST /_flush", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("GET /{index}/_flush", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("POST /{index}/_flush", r.dataFlush).Op(OpFlush).MustBuild(),

		// From RestSyncedFlushAction.java (deprecated in OpenSearch 2.0+, returns deprecation warning)
		NewRoute("GET /_flush/synced", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("POST /_flush/synced", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("GET /{index}/_flush/synced", r.dataFlush).Op(OpFlush).MustBuild(),
		NewRoute("POST /{index}/_flush/synced", r.dataFlush).Op(OpFlush).MustBuild(),

		// -- Force merge -- data nodes, "force_merge" pool
		// From RestForceMergeAction.java
		NewRoute("POST /_forcemerge", r.dataForceMerge).Op(OpForceMerge).MustBuild(),
		NewRoute("POST /{index}/_forcemerge", r.dataForceMerge).Op(OpForceMerge).MustBuild(),

		// -- Data management -- data nodes, "management" pool
		// From RestIndicesSegmentsAction.java
		NewRoute("GET /_segments", r.dataMgmt).Op(OpSegments).MustBuild(),
		NewRoute("GET /{index}/_segments", r.dataMgmt).Op(OpSegments).MustBuild(),

		// From RestClearIndicesCacheAction.java
		NewRoute("POST /_cache/clear", r.dataMgmt).Op(OpCacheClear).MustBuild(),
		NewRoute("POST /{index}/_cache/clear", r.dataMgmt).Op(OpCacheClear).MustBuild(),

		// From RestRecoveryAction.java
		NewRoute("GET /{index}/_recovery", r.dataMgmt).Op(OpRecovery).MustBuild(),

		// From RestIndicesShardStoresAction.java
		NewRoute("GET /{index}/_shard_stores", r.dataMgmt).Op(OpShardStores).MustBuild(),

		// From RestIndicesStatsAction.java
		NewRoute("GET /_stats", r.dataMgmt).Op(OpStats).MustBuild(),
		NewRoute("GET /_stats/{metric}", r.dataMgmt).Op(OpStats).MustBuild(),
		NewRoute("GET /{index}/_stats", r.dataMgmt).Op(OpStats).MustBuild(),
		NewRoute("GET /{index}/_stats/{metric}", r.dataMgmt).Op(OpStats).MustBuild(),

		// -- Rethrottle -- data nodes, "management" pool
		// From RestRethrottleAction.java, module: reindex
		NewRoute("POST /_reindex/{taskId}/_rethrottle", r.dataMgmt).Op(OpReindexRethrottle).MustBuild(),
		NewRoute("POST /_update_by_query/{taskId}/_rethrottle", r.dataMgmt).Op(OpUBQRethrottle).MustBuild(),
		NewRoute("POST /_delete_by_query/{taskId}/_rethrottle", r.dataMgmt).Op(OpDBQRethrottle).MustBuild(),

		// -- Cluster state reads -- cluster_manager nodes, "management" pool
		NewRoute("GET /_cluster/health", r.clusterMgrRead).Op(OpClusterHealth).MustBuild(),
		NewRoute("GET /_cluster/health/{index}", r.clusterMgrRead).Op(OpClusterHealth).MustBuild(),
		NewRoute("GET /_cluster/stats", r.clusterMgrRead).Op(OpClusterStats).MustBuild(),
		NewRoute("GET /_cluster/stats/nodes/{nodeId}", r.clusterMgrRead).Op(OpClusterStats).MustBuild(),
		NewRoute("GET /_cluster/state", r.clusterMgrRead).Op(OpClusterState).MustBuild(),
		NewRoute("GET /_cluster/state/{metric}", r.clusterMgrRead).Op(OpClusterState).MustBuild(),
		NewRoute("GET /_cluster/state/{metric}/{index}", r.clusterMgrRead).Op(OpClusterState).MustBuild(),
		NewRoute("GET /_cluster/settings", r.clusterMgrRead).Op(OpClusterSettingsGet).MustBuild(),
		NewRoute("GET /_cluster/pending_tasks", r.clusterMgrRead).Op(OpClusterPendingTasks).MustBuild(),
		NewRoute("GET /_cluster/allocation/explain", r.clusterMgrRead).Op(OpClusterAllocExplain).MustBuild(),
		NewRoute("POST /_cluster/allocation/explain", r.clusterMgrRead).Op(OpClusterAllocExplain).MustBuild(),
		NewRoute("GET /_remote/info", r.clusterMgrRead).Op(OpClusterRemoteInfo).MustBuild(),

		// -- Cluster state writes -- cluster_manager nodes, "write" pool
		NewRoute("PUT /_cluster/settings", r.clusterMgrWrite).Op(OpClusterSettingsPut).MustBuild(),
		NewRoute("POST /_cluster/reroute", r.clusterMgrWrite).Op(OpClusterReroute).MustBuild(),
		NewRoute("POST /_cluster/voting_config_exclusions", r.clusterMgrWrite).Op(OpClusterVotingConfigEx).MustBuild(),
		NewRoute("DELETE /_cluster/voting_config_exclusions", r.clusterMgrWrite).Op(OpClusterVotingConfigEx).MustBuild(),

		// -- Cat APIs -- reads; cluster-state-derived on management pool, node-local on searchMgmt
		NewRoute("GET /_cat/indices", r.searchMgmt).Op(OpCatIndices).MustBuild(),
		NewRoute("GET /_cat/indices/{index}", r.searchMgmt).Op(OpCatIndices).MustBuild(),
		NewRoute("GET /_cat/nodes", r.searchMgmt).Op(OpCatNodes).MustBuild(),
		NewRoute("GET /_cat/shards", r.searchMgmt).Op(OpCatShards).MustBuild(),
		NewRoute("GET /_cat/shards/{index}", r.searchMgmt).Op(OpCatShards).MustBuild(),
		NewRoute("GET /_cat/health", r.searchMgmt).Op(OpCatHealth).MustBuild(),
		NewRoute("GET /_cat/allocation", r.searchMgmt).Op(OpCatAllocation).MustBuild(),
		NewRoute("GET /_cat/allocation/{nodeId}", r.searchMgmt).Op(OpCatAllocation).MustBuild(),
		NewRoute("GET /_cat/count", r.searchMgmt).Op(OpCatCount).MustBuild(),
		NewRoute("GET /_cat/count/{index}", r.searchMgmt).Op(OpCatCount).MustBuild(),
		NewRoute("GET /_cat/fielddata", r.searchMgmt).Op(OpCatFielddata).MustBuild(),
		NewRoute("GET /_cat/fielddata/{fields}", r.searchMgmt).Op(OpCatFielddata).MustBuild(),
		NewRoute("GET /_cat/master", r.searchMgmt).Op(OpCatMaster).MustBuild(),
		NewRoute("GET /_cat/cluster_manager", r.searchMgmt).Op(OpCatClusterMgr).MustBuild(),
		NewRoute("GET /_cat/nodeattrs", r.searchMgmt).Op(OpCatNodeAttrs).MustBuild(),
		NewRoute("GET /_cat/pending_tasks", r.searchMgmt).Op(OpCatPendingTask).MustBuild(),
		NewRoute("GET /_cat/plugins", r.searchMgmt).Op(OpCatPlugins).MustBuild(),
		NewRoute("GET /_cat/recovery", r.dataMgmt).Op(OpCatRecovery).MustBuild(),
		NewRoute("GET /_cat/recovery/{index}", r.dataMgmt).Op(OpCatRecovery).MustBuild(),
		NewRoute("GET /_cat/repositories", r.searchMgmt).Op(OpCatRepos).MustBuild(),
		NewRoute("GET /_cat/segments", r.dataMgmt).Op(OpCatSegments).MustBuild(),
		NewRoute("GET /_cat/segments/{index}", r.dataMgmt).Op(OpCatSegments).MustBuild(),
		NewRoute("GET /_cat/snapshots/{repository}", r.searchMgmt).Op(OpCatSnapshots).MustBuild(),
		NewRoute("GET /_cat/tasks", r.searchMgmt).Op(OpCatTasks).MustBuild(),
		NewRoute("GET /_cat/templates", r.searchMgmt).Op(OpCatTemplates).MustBuild(),
		NewRoute("GET /_cat/templates/{name}", r.searchMgmt).Op(OpCatTemplates).MustBuild(),
		NewRoute("GET /_cat/thread_pool", r.searchMgmt).Op(OpCatThreadPool).MustBuild(),
		NewRoute("GET /_cat/thread_pool/{thread_pools}", r.searchMgmt).Op(OpCatThreadPool).MustBuild(),
		NewRoute("GET /_cat/aliases", r.searchMgmt).Op(OpCatAliases).MustBuild(),
		NewRoute("GET /_cat/aliases/{name}", r.searchMgmt).Op(OpCatAliases).MustBuild(),

		// -- Nodes APIs -- node-local reads, "management" pool (any node)
		NewRoute("GET /_nodes", r.searchMgmt).Op(OpNodesInfo).MustBuild(),
		NewRoute("GET /_nodes/{nodeId}", r.searchMgmt).Op(OpNodesInfo).MustBuild(),
		NewRoute("GET /_nodes/stats", r.searchMgmt).Op(OpNodesStats).MustBuild(),
		NewRoute("GET /_nodes/{nodeId}/stats", r.searchMgmt).Op(OpNodesStats).MustBuild(),
		NewRoute("GET /_nodes/stats/{metric}", r.searchMgmt).Op(OpNodesStats).MustBuild(),
		NewRoute("GET /_nodes/usage", r.searchMgmt).Op(OpNodesUsage).MustBuild(),
		NewRoute("GET /_nodes/{nodeId}/usage", r.searchMgmt).Op(OpNodesUsage).MustBuild(),
		NewRoute("GET /_nodes/hot_threads", r.searchMgmt).Op(OpNodesHotThreads).MustBuild(),
		NewRoute("GET /_nodes/{nodeId}/hot_threads", r.searchMgmt).Op(OpNodesHotThreads).MustBuild(),
		NewRoute("POST /_nodes/reload_secure_settings", r.dataMgmt).Op(OpNodesReloadSecurity).MustBuild(),
		NewRoute("POST /_nodes/{nodeId}/reload_secure_settings", r.dataMgmt).Op(OpNodesReloadSecurity).MustBuild(),

		// -- Tasks APIs -- node-local, "management" pool
		NewRoute("GET /_tasks", r.searchMgmt).Op(OpTasksList).MustBuild(),
		NewRoute("GET /_tasks/{taskId}", r.searchMgmt).Op(OpTasksGet).MustBuild(),
		NewRoute("POST /_tasks/_cancel", r.dataMgmt).Op(OpTasksCancel).MustBuild(),
		NewRoute("POST /_tasks/{taskId}/_cancel", r.dataMgmt).Op(OpTasksCancel).MustBuild(),

		// -- Snapshot / repository -- cluster_manager operations
		NewRoute("GET /_snapshot", r.clusterMgrRead).Op(OpSnapshotRepoGet).MustBuild(),
		NewRoute("GET /_snapshot/{repository}", r.clusterMgrRead).Op(OpSnapshotRepoGet).MustBuild(),
		NewRoute("PUT /_snapshot/{repository}", r.clusterMgrWrite).Op(OpSnapshotRepoCreate).MustBuild(),
		NewRoute("POST /_snapshot/{repository}", r.clusterMgrWrite).Op(OpSnapshotRepoCreate).MustBuild(),
		NewRoute("DELETE /_snapshot/{repository}", r.clusterMgrWrite).Op(OpSnapshotRepoDelete).MustBuild(),
		NewRoute("POST /_snapshot/{repository}/_verify", r.clusterMgrWrite).Op(OpSnapshotRepoVerify).MustBuild(),
		NewRoute("POST /_snapshot/{repository}/_cleanup", r.clusterMgrWrite).Op(OpSnapshotRepoClean).MustBuild(),
		NewRoute("GET /_snapshot/_status", r.clusterMgrRead).Op(OpSnapshotStatus).MustBuild(),
		NewRoute("GET /_snapshot/{repository}/_status", r.clusterMgrRead).Op(OpSnapshotStatus).MustBuild(),
		NewRoute("GET /_snapshot/{repository}/{snapshot}", r.clusterMgrRead).Op(OpSnapshotGet).MustBuild(),
		NewRoute("PUT /_snapshot/{repository}/{snapshot}", r.clusterMgrWrite).Op(OpSnapshotCreate).MustBuild(),
		NewRoute("POST /_snapshot/{repository}/{snapshot}", r.clusterMgrWrite).Op(OpSnapshotCreate).MustBuild(),
		NewRoute("DELETE /_snapshot/{repository}/{snapshot}", r.clusterMgrWrite).Op(OpSnapshotDelete).MustBuild(),
		NewRoute("GET /_snapshot/{repository}/{snapshot}/_status", r.clusterMgrRead).Op(OpSnapshotStatus).MustBuild(),
		NewRoute("POST /_snapshot/{repository}/{snapshot}/_restore", r.clusterMgrWrite).Op(OpSnapshotRestore).MustBuild(),
		NewRoute("PUT /_snapshot/{repository}/{snapshot}/_clone/{target_snapshot}", r.clusterMgrWrite).Op(OpSnapshotClone).MustBuild(),

		// -- Stored scripts -- cluster_manager operations (stored in cluster state)
		NewRoute("GET /_scripts/{id}", r.clusterMgrRead).Op(OpScriptGet).MustBuild(),
		NewRoute("PUT /_scripts/{id}", r.clusterMgrWrite).Op(OpScriptPut).MustBuild(),
		NewRoute("PUT /_scripts/{id}/{context}", r.clusterMgrWrite).Op(OpScriptPut).MustBuild(),
		NewRoute("POST /_scripts/{id}", r.clusterMgrWrite).Op(OpScriptPut).MustBuild(),
		NewRoute("POST /_scripts/{id}/{context}", r.clusterMgrWrite).Op(OpScriptPut).MustBuild(),
		NewRoute("DELETE /_scripts/{id}", r.clusterMgrWrite).Op(OpScriptDelete).MustBuild(),
		NewRoute("GET /_script_context", r.searchMgmt).Op(OpScriptContext).MustBuild(),
		NewRoute("GET /_script_language", r.searchMgmt).Op(OpScriptLanguage).MustBuild(),
		NewRoute("GET /_scripts/painless/_execute", r.searchMgmt).Op(OpScriptPainlessExec).MustBuild(),
		NewRoute("POST /_scripts/painless/_execute", r.searchMgmt).Op(OpScriptPainlessExec).MustBuild(),

		// -- Dangling indices -- cluster_manager operations
		NewRoute("GET /_dangling", r.clusterMgrRead).Op(OpDanglingGet).MustBuild(),
		NewRoute("POST /_dangling/{index_uuid}", r.clusterMgrWrite).Op(OpDanglingImport).MustBuild(),
		NewRoute("DELETE /_dangling/{index_uuid}", r.clusterMgrWrite).Op(OpDanglingDelete).MustBuild(),

		// -- Data streams -- reads are index-metadata; writes mutate cluster state
		NewRoute("GET /_data_stream", r.clusterMgrRead).Op(OpDataStreamGet).MustBuild(),
		NewRoute("GET /_data_stream/{name}", r.clusterMgrRead).Op(OpDataStreamGet).MustBuild(),
		NewRoute("PUT /_data_stream/{name}", r.clusterMgrWrite).Op(OpDataStreamCreate).MustBuild(),
		NewRoute("DELETE /_data_stream/{name}", r.clusterMgrWrite).Op(OpDataStreamDelete).MustBuild(),
		NewRoute("GET /_data_stream/{name}/_stats", r.dataMgmt).Op(OpDataStreamStats).MustBuild(),

		// -- Index management -- reads are index-metadata; writes mutate cluster state
		NewRoute("GET /_resolve/index/{name}", r.clusterMgrRead).Op(OpIndexResolve).MustBuild(),
		NewRoute("PUT /{index}/_mapping", r.clusterMgrWrite).Op(OpMappingPut).MustBuild(),
		NewRoute("POST /{index}/_mapping", r.clusterMgrWrite).Op(OpMappingPut).MustBuild(),
		NewRoute("PUT /_mapping", r.clusterMgrWrite).Op(OpMappingPut).MustBuild(),
		NewRoute("GET /{index}/_mapping", r.clusterMgrRead).Op(OpMappingGet).MustBuild(),
		NewRoute("GET /_mapping", r.clusterMgrRead).Op(OpMappingGet).MustBuild(),
		NewRoute("GET /{index}/_mapping/field/{fields}", r.clusterMgrRead).Op(OpMappingGet).MustBuild(),
		NewRoute("GET /_mapping/field/{fields}", r.clusterMgrRead).Op(OpMappingGet).MustBuild(),
		NewRoute("GET /{index}/_alias", r.clusterMgrRead).Op(OpAliasGet).MustBuild(),
		NewRoute("GET /{index}/_alias/{name}", r.clusterMgrRead).Op(OpAliasGet).MustBuild(),
		NewRoute("GET /_alias", r.clusterMgrRead).Op(OpAliasGet).MustBuild(),
		NewRoute("GET /_alias/{name}", r.clusterMgrRead).Op(OpAliasGet).MustBuild(),
		NewRoute("GET /_aliases", r.clusterMgrRead).Op(OpAliasGet).MustBuild(),
		NewRoute("PUT /{index}/_alias/{name}", r.clusterMgrWrite).Op(OpAliasPut).MustBuild(),
		NewRoute("POST /{index}/_alias/{name}", r.clusterMgrWrite).Op(OpAliasPut).MustBuild(),
		NewRoute("DELETE /{index}/_alias/{name}", r.clusterMgrWrite).Op(OpAliasDelete).MustBuild(),
		NewRoute("POST /_aliases", r.clusterMgrWrite).Op(OpAliasPut).MustBuild(),
		NewRoute("GET /{index}/_analyze", r.dataMgmt).Op(OpIndexAnalyze).MustBuild(),
		NewRoute("POST /{index}/_analyze", r.dataMgmt).Op(OpIndexAnalyze).MustBuild(),
		NewRoute("GET /_analyze", r.dataMgmt).Op(OpIndexAnalyze).MustBuild(),
		NewRoute("POST /_analyze", r.dataMgmt).Op(OpIndexAnalyze).MustBuild(),
		NewRoute("POST /{index}/_open", r.clusterMgrWrite).Op(OpIndexOpen).MustBuild(),
		NewRoute("POST /{index}/_close", r.clusterMgrWrite).Op(OpIndexClose).MustBuild(),
		NewRoute("POST /{index}/_clone/{target}", r.clusterMgrWrite).Op(OpIndexClone).MustBuild(),
		NewRoute("PUT /{index}/_clone/{target}", r.clusterMgrWrite).Op(OpIndexClone).MustBuild(),
		NewRoute("POST /{index}/_shrink/{target}", r.clusterMgrWrite).Op(OpIndexShrink).MustBuild(),
		NewRoute("PUT /{index}/_shrink/{target}", r.clusterMgrWrite).Op(OpIndexShrink).MustBuild(),
		NewRoute("POST /{index}/_split/{target}", r.clusterMgrWrite).Op(OpIndexSplit).MustBuild(),
		NewRoute("PUT /{index}/_split/{target}", r.clusterMgrWrite).Op(OpIndexSplit).MustBuild(),
		NewRoute("POST /{index}/_rollover", r.clusterMgrWrite).Op(OpIndexRollover).MustBuild(),
		NewRoute("POST /{index}/_rollover/{target}", r.clusterMgrWrite).Op(OpIndexRollover).MustBuild(),
		NewRoute("PUT /{index}/_block/{block}", r.clusterMgrWrite).Op(OpIndexBlock).MustBuild(),

		// -- Index create/get/delete (bare {index}) -- keep AFTER more-specific
		// /{index}/... routes; the trie prefers exact segments over wildcards, so
		// these only match a single-segment path.
		NewRoute("GET /{index}", r.clusterMgrRead).Op(OpIndexGet).MustBuild(),
		NewRoute("HEAD /{index}", r.clusterMgrRead).Op(OpIndexExists).MustBuild(),
		NewRoute("PUT /{index}", r.clusterMgrWrite).Op(OpIndexCreate).MustBuild(),
		NewRoute("DELETE /{index}", r.clusterMgrWrite).Op(OpIndexDelete).MustBuild(),

		// -- Index templates (composable) -- cluster_manager operations
		NewRoute("GET /_index_template", r.clusterMgrRead).Op(OpIndexTemplateGet).MustBuild(),
		NewRoute("GET /_index_template/{name}", r.clusterMgrRead).Op(OpIndexTemplateGet).MustBuild(),
		NewRoute("PUT /_index_template/{name}", r.clusterMgrWrite).Op(OpIndexTemplateCreate).MustBuild(),
		NewRoute("POST /_index_template/{name}", r.clusterMgrWrite).Op(OpIndexTemplateCreate).MustBuild(),
		NewRoute("DELETE /_index_template/{name}", r.clusterMgrWrite).Op(OpIndexTemplateDelete).MustBuild(),
		NewRoute("HEAD /_index_template/{name}", r.clusterMgrRead).Op(OpIndexTemplateExists).MustBuild(),
		NewRoute("POST /_index_template/_simulate", r.clusterMgrRead).Op(OpIndexTemplateSimulate).MustBuild(),
		NewRoute("POST /_index_template/_simulate/{name}", r.clusterMgrRead).Op(OpIndexTemplateSimulate).MustBuild(),
		NewRoute("POST /_index_template/_simulate_index/{name}", r.clusterMgrRead).Op(OpIndexTemplateSimulateIndex).MustBuild(),

		// -- Component templates -- cluster_manager operations
		NewRoute("GET /_component_template", r.clusterMgrRead).Op(OpComponentTemplateGet).MustBuild(),
		NewRoute("GET /_component_template/{name}", r.clusterMgrRead).Op(OpComponentTemplateGet).MustBuild(),
		NewRoute("PUT /_component_template/{name}", r.clusterMgrWrite).Op(OpComponentTemplateCreate).MustBuild(),
		NewRoute("POST /_component_template/{name}", r.clusterMgrWrite).Op(OpComponentTemplateCreate).MustBuild(),
		NewRoute("DELETE /_component_template/{name}", r.clusterMgrWrite).Op(OpComponentTemplateDelete).MustBuild(),
		NewRoute("HEAD /_component_template/{name}", r.clusterMgrRead).Op(OpComponentTemplateExists).MustBuild(),

		// -- Legacy index templates -- cluster_manager operations
		NewRoute("GET /_template", r.clusterMgrRead).Op(OpLegacyTemplateGet).MustBuild(),
		NewRoute("GET /_template/{name}", r.clusterMgrRead).Op(OpLegacyTemplateGet).MustBuild(),
		NewRoute("PUT /_template/{name}", r.clusterMgrWrite).Op(OpLegacyTemplateCreate).MustBuild(),
		NewRoute("POST /_template/{name}", r.clusterMgrWrite).Op(OpLegacyTemplateCreate).MustBuild(),
		NewRoute("DELETE /_template/{name}", r.clusterMgrWrite).Op(OpLegacyTemplateDelete).MustBuild(),
		NewRoute("HEAD /_template/{name}", r.clusterMgrRead).Op(OpLegacyTemplateExists).MustBuild(),
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
// For connection-scoring routing that adds per-index node consistency and
// RTT-based scoring, use [NewDefaultPolicy] instead.
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
// For role-based routing, use [NewMuxRoutePolicy]. For connection-scoring routing,
// use [NewDefaultPolicy].
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
// See [NewMuxRoutePolicy] for the full set of routing rules.
//
// For connection-scoring routing that adds per-index consistency and
// RTT-based scoring, use [NewDefaultRouter] instead.
func NewMuxRouter() Router {
	return NewRouter(NewMuxRoutePolicy())
}

// NewRoundRobinRouter creates a router with simple coordinating node preference.
//
//   - If coordinating-only nodes are available, routes all requests to them
//   - Otherwise, falls back to round-robin across all available nodes
//
// For role-based routing, use [NewMuxRouter]. For the recommended
// production router with connection scoring, use [NewDefaultRouter].
func NewRoundRobinRouter() Router {
	return NewRouter(NewRoundRobinDefaultPolicy())
}

// RouterOption configures a connection-scoring router created by [NewDefaultRouter].
type RouterOption func(*routerConfig)

type routerConfig struct {
	minFanOut       int
	maxFanOut       int
	overrides       map[string]int
	idleEvictionTTL time.Duration
	decay           float64
	fanOutPerReq    float64

	// Shard cost override spec. Parsed at NewDefaultPolicy time.
	// See [envShardCost] for format documentation.
	shardCostSpec string

	// Feature configuration from environment variables.
	// Applied after programmatic options; env overrides options.
	routingFeatures   routingFeatures
	discoveryFeatures discoveryFeatures

	// Adaptive max_concurrent_shard_requests configuration.
	// Set via WithAdaptiveConcurrencyLimits or OPENSEARCH_GO_SHARD_REQUESTS.
	adaptiveConcurrency adaptiveConcurrencyConfig

	// errs collects configuration errors from RouterOption functions.
	// Logged at router creation time via debugLogger.
	errs []error
}

func defaultRouterConfig() routerConfig {
	return routerConfig{
		minFanOut:       defaultMinFanOut,
		maxFanOut:       defaultMaxFanOut,
		idleEvictionTTL: defaultIdleEvictionTTL,
		decay:           defaultDecayFactor,
		fanOutPerReq:    defaultFanOutPerRequest,
	}
}

// WithMinFanOut sets the minimum number of nodes in an index slot.
// Default: 1.
func WithMinFanOut(n int) RouterOption {
	return func(c *routerConfig) { c.minFanOut = n }
}

// WithMaxFanOut sets the maximum number of nodes in an index slot.
// 0 uses the default (32). Default: 32.
func WithMaxFanOut(n int) RouterOption {
	return func(c *routerConfig) { c.maxFanOut = n }
}

// WithIndexFanOut sets per-index fan-out overrides. Overrides take
// precedence over dynamic fan-out calculation.
func WithIndexFanOut(m map[string]int) RouterOption {
	return func(c *routerConfig) { c.overrides = m }
}

// WithIdleEvictionTTL sets how long idle index slots persist before
// being evicted from the cache. Default: 90 minutes.
func WithIdleEvictionTTL(d time.Duration) RouterOption {
	return func(c *routerConfig) { c.idleEvictionTTL = d }
}

// WithDecayFactor sets the exponential decay factor for request counters.
// Must be between 0 and 1 exclusive (NaN and Inf are rejected). Default: 0.999.
//
// Out-of-range values are recorded in [routerConfig.errs] and the field
// retains its compile-time default; the operator sees the error logged at
// router build time rather than silently getting the default.
func WithDecayFactor(d float64) RouterOption {
	return func(c *routerConfig) {
		if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || d >= 1 {
			c.errs = append(c.errs, fmt.Errorf(
				"WithDecayFactor(%v): must satisfy 0 < d < 1", d))
			return
		}
		c.decay = d
	}
}

// WithFanOutPerRequest sets the decay-counter-to-fan-out divisor.
// When the decay counter reaches this threshold, fan-out grows by 1.
// Must be strictly positive (NaN and Inf are rejected). Default: 500.
//
// Out-of-range values are recorded in [routerConfig.errs] and the field
// retains its compile-time default; the operator sees the error logged at
// router build time rather than silently getting the default.
func WithFanOutPerRequest(f float64) RouterOption {
	return func(c *routerConfig) {
		if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			c.errs = append(c.errs, fmt.Errorf(
				"WithFanOutPerRequest(%v): must be > 0 and finite", f))
			return
		}
		c.fanOutPerReq = f
	}
}

// WithShardExactRouting enables or disables murmur3 shard-exact routing.
// When disabled, calcSingleKeyCost and calcMultiKeyCost return nil and
// shard-exact routing is bypassed. Default: true (enabled).
//
// Can also be controlled via OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact.
// The environment variable takes precedence over this option.
func WithShardExactRouting(enabled bool) RouterOption {
	return func(c *routerConfig) {
		if enabled {
			c.routingFeatures &^= routingSkipShardExact
		} else {
			c.routingFeatures |= routingSkipShardExact
		}
	}
}

// WithAdaptiveConcurrency enables or disables adaptive
// max_concurrent_shard_requests injection on search requests.
// When enabled, the transport derives the shard fan-out limit from the
// selected connection's search pool congestion window. Default: true.
//
// Can also be controlled via OPENSEARCH_GO_SHARD_REQUESTS=false.
// The environment variable takes precedence over this option.
func WithAdaptiveConcurrency(enabled bool) RouterOption {
	return func(c *routerConfig) {
		if enabled {
			c.routingFeatures &^= routingSkipAdaptiveConcurrency
		} else {
			c.routingFeatures |= routingSkipAdaptiveConcurrency
		}
	}
}

// WithAdaptiveConcurrencyLimits sets the min and max for adaptive
// max_concurrent_shard_requests. Zero values retain the defaults
// (minVal=5, maxVal=256). Negative values for both min and max disable
// adaptive concurrency entirely -- equivalent to
// WithAdaptiveConcurrency(false).
//
// If min > max after applying both values, a configuration error is
// recorded and logged at router creation time. The invalid bounds are
// preserved as-is (no swap) so the caller observes the bug.
//
// The max can be set above the compile-time default of 256 when the
// cluster has large search thread pools -- e.g.,
// WithAdaptiveConcurrencyLimits(10, 512).
//
// Can also be controlled via OPENSEARCH_GO_SHARD_REQUESTS=10:512.
// The environment variable takes precedence over this option.
func WithAdaptiveConcurrencyLimits(minVal, maxVal int) RouterOption {
	return func(c *routerConfig) {
		if minVal < 0 && maxVal < 0 {
			c.routingFeatures |= routingSkipAdaptiveConcurrency
			return
		}
		c.routingFeatures &^= routingSkipAdaptiveConcurrency
		if minVal > 0 {
			c.adaptiveConcurrency.minVal = minVal
		}
		if maxVal > 0 {
			c.adaptiveConcurrency.maxVal = maxVal
		}
		if c.adaptiveConcurrency.minVal > 0 && c.adaptiveConcurrency.maxVal > 0 &&
			c.adaptiveConcurrency.minVal > c.adaptiveConcurrency.maxVal {
			c.errs = append(c.errs, fmt.Errorf(
				"WithAdaptiveConcurrencyLimits(%d, %d): min exceeds max",
				minVal, maxVal))
		}
	}
}

// WithShardCosts overrides shard cost multipliers used for connection scoring.
// The spec string uses the same format as the [OPENSEARCH_GO_SHARD_COST]
// environment variable:
//
//   - Bare numeric (e.g., "1.0"): sets r:base (primary read cost at idle)
//     to the given value. Other parameters keep their defaults.
//   - Key=value (e.g., "r:base=0.9,r:amplify=2.5,r:exponent=1.5"):
//     dynamic read curve keys are prefixed with "r:". Static keys
//     (unknown, relocating, initializing, replica, write_primary,
//     write_replica) override shard state costs in the lookup tables.
//   - Any static value ≤ 0 is replaced by the compile-time default.
//
// The environment variable takes precedence over this option.
func WithShardCosts(spec string) RouterOption {
	return func(c *routerConfig) { c.shardCostSpec = spec }
}

// buildStandaloneRouterConfig applies a slice of [RouterOption]s to a fresh
// [routerConfig], applies environment variable overrides, and resolves the
// shard cost configuration. Shared by [NewDefaultPolicy], [NewIndexRouter],
// and [NewDocRouter] so all three honor the same option/env precedence.
func buildStandaloneRouterConfig(opts []RouterOption) (routerConfig, *shardCostConfig, error) {
	cfg := defaultRouterConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	// Apply environment variable overrides after programmatic options.
	// Env vars take precedence: a user can override a programmatic
	// WithShardExactRouting(true) with OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact.
	if val, ok := os.LookupEnv(envRoutingConfig); ok && val != "" {
		cfg.routingFeatures = parseRoutingConfig(val)
	}
	if val, ok := os.LookupEnv(envDiscoveryConfig); ok && val != "" {
		cfg.discoveryFeatures = parseDiscoveryConfig(val)
	}
	if val, ok := os.LookupEnv(envShardRequests); ok && val != "" {
		cfg.adaptiveConcurrency, cfg.routingFeatures = parseShardRequests(val, cfg.routingFeatures)
		// Validate after env override (env may have set min > max).
		if cfg.adaptiveConcurrency.minVal > 0 && cfg.adaptiveConcurrency.maxVal > 0 &&
			cfg.adaptiveConcurrency.minVal > cfg.adaptiveConcurrency.maxVal {
			cfg.errs = append(cfg.errs, fmt.Errorf(
				"%s=%q: min (%d) exceeds max (%d)",
				envShardRequests, val, cfg.adaptiveConcurrency.minVal, cfg.adaptiveConcurrency.maxVal))
		}
	}

	// Log configuration errors. These are caller bugs that should be fixed.
	if dl := loadDebugLogger(); dl != nil {
		for _, err := range cfg.errs {
			dl.Logf("routerConfig error: %v\n", err)
		}
	}

	// Resolve shard cost tables and scoring function.
	// Priority: env var > WithShardCosts() RouterOption > compile-time defaults.
	shardCostSpec := cfg.shardCostSpec
	if envVal, ok := os.LookupEnv(envShardCost); ok && envVal != "" {
		shardCostSpec = envVal
	}

	costCfg, costErr := parseShardCostConfig(shardCostSpec)
	if costErr != nil {
		return cfg, nil, fmt.Errorf("parsing %q: %w", shardCostSpec, costErr)
	}

	return cfg, costCfg, nil
}

// indexSlotCacheConfigFromRouter projects a parsed [routerConfig] into the
// matching [indexSlotCacheConfig] used by [newIndexSlotCache].
func indexSlotCacheConfigFromRouter(cfg routerConfig) indexSlotCacheConfig {
	return indexSlotCacheConfig{
		minFanOut:           cfg.minFanOut,
		maxFanOut:           cfg.maxFanOut,
		overrides:           cfg.overrides,
		idleEvictionTTL:     cfg.idleEvictionTTL,
		decayFactor:         cfg.decay,
		fanOutPerReq:        cfg.fanOutPerReq,
		features:            cfg.routingFeatures,
		adaptiveConcurrency: cfg.adaptiveConcurrency,
	}
}

// NewDefaultPolicy creates a request-aware policy with connection-scoring routing.
// This is the recommended policy for production clusters. It extends
// role-based mux routing with per-index connection scoring:
//
//  1. If coordinating-only nodes exist, route all traffic to them with
//     RTT + congestion scoring (coordinating nodes don't host shards, so
//     all receive costUnknown and selection is purely by latency/cwnd)
//  2. MuxPolicy with router-wrapped role routes: within each role pool
//     (data, search, ingest, warm), rendezvous hashing and RTT-based scoring
//     select the best connection for the target index
//  3. RoundRobinPolicy fallback for unmatched requests
//
// See [guides/connection_scoring.md] for the full algorithm description.
func NewDefaultPolicy(opts ...RouterOption) (Policy, error) {
	cfg, costCfg, err := buildStandaloneRouterConfig(opts)
	if err != nil {
		return nil, err
	}

	cacheCfg := indexSlotCacheConfigFromRouter(cfg)
	cache := newIndexSlotCache(cacheCfg)

	coordinatingPolicy := mustRolePolicy(RoleCoordinatingOnly)

	// Wrap coordinating-only nodes with RTT + congestion scoring.
	// Coordinating nodes don't host shards, so all receive costUnknown
	// and selection is purely by latency and congestion window.
	coordinatingScored := wrapWithRouter(coordinatingPolicy, cache, &costCfg.reads, "", nil)

	// The mux policy delegates each matched request to a router-wrapped
	// role policy. Within data/search/ingest/warm nodes, the router wrapper
	// applies RTT-based scoring and rendezvous hashing to select the
	// best connection for the target index, achieving cache locality and AZ-aware
	// load distribution within each role pool.
	muxPolicy := NewMuxPolicy(newScoredRoutes(cache, costCfg))

	roundRobinPolicy := NewRoundRobinPolicy()

	return NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
		coordinatingScored,
		NewPolicy(
			muxPolicy,
			roundRobinPolicy,
		),
	), nil
}

// NewDefaultRouter creates a router with connection-scoring request routing.
// This is the recommended router for production clusters. It combines
// role-based routing with per-index connection scoring, RTT-based AZ preference,
// and self-stabilizing load distribution.
//
// See [NewDefaultPolicy] for the full routing chain and [guides/connection_scoring.md]
// for the algorithm description.
func NewDefaultRouter(opts ...RouterOption) (Router, error) {
	p, err := NewDefaultPolicy(opts...)
	if err != nil {
		return nil, err
	}
	return NewRouter(p), nil
}

// newScoredRoutes mirrors [NewDefaultRoutes] but wraps each role-based
// sub-policy with a [poolRouter]. Each wrapper carries the
// server-side thread pool name, shard cost table, and optional scoring
// function, so the pool name flows through [NextHop.PoolName] without
// redundancy on the route.
func newScoredRoutes(cache *indexSlotCache, costCfg *shardCostConfig) []Route {
	wrap := func(p Policy, costs *shardCostMultiplier, pool string, scoreFunc connScoreFunc) Policy {
		return wrapWithRouter(p, cache, costs, pool, scoreFunc)
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

	// Cluster-manager routing for cluster-state operations, with data fallback.
	clusterMgrPolicy := mustRolePolicy(RoleClusterManager)
	clusterMgrDataFallbackPolicy := mustRolePolicy(RoleData)
	clusterMgrIfEnabled := NewIfEnabledPolicy(
		func(ctx context.Context, req *http.Request) bool { return clusterMgrPolicy.IsEnabled() },
		clusterMgrPolicy,
		NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return clusterMgrDataFallbackPolicy.IsEnabled() },
			clusterMgrDataFallbackPolicy,
			NewNullPolicy(),
		),
	)

	// Create per-(role, shardCost, poolName) wrappers. The pool name is
	// baked into the wrapper at construction time and flows through
	// NextHop.PoolName --no redundant .Pool() calls on routes needed.
	return buildRoleRoutes(roleRoutes{
		ingestWrite:     wrap(ingestIfEnabled, &costCfg.writes, poolWrite, nil),
		ingestMgmt:      wrap(ingestIfEnabled, &costCfg.reads, poolManagement, nil),
		searchRead:      wrap(searchIfEnabled, &costCfg.reads, poolSearch, costCfg.scoreFunc),
		getRead:         wrap(searchIfEnabled, &costCfg.reads, poolGet, costCfg.scoreFunc),
		dataWrite:       wrap(dataIfEnabled, &costCfg.writes, poolWrite, nil),
		dataRefresh:     wrap(dataIfEnabled, &costCfg.writes, poolRefresh, nil),
		dataFlush:       wrap(dataIfEnabled, &costCfg.writes, poolFlush, nil),
		dataForceMerge:  wrap(dataIfEnabled, &costCfg.writes, poolForceMerge, nil),
		dataMgmt:        wrap(dataIfEnabled, &costCfg.reads, poolManagement, nil),
		searchMgmt:      wrap(searchIfEnabled, &costCfg.reads, poolManagement, nil),
		warmMgmt:        wrap(warmIfEnabled, &costCfg.reads, poolManagement, nil),
		clusterMgrRead:  wrap(clusterMgrIfEnabled, &costCfg.reads, poolManagement, nil),
		clusterMgrWrite: wrap(clusterMgrIfEnabled, &costCfg.writes, poolWrite, nil),
	})
}
