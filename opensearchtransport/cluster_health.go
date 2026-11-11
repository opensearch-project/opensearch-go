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
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/mod/semver"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

const clusterStatusRed = "red"

const (
	// nodeStatsPath is the node stats poll's request path.
	nodeStatsPath = "/_nodes/_local/stats/jvm,breaker,thread_pool,adaptive_selection"

	// nodeStatsPathResourceUsage adds resource_usage_stats, which the server
	// rejects as an unrecognized metric before OpenSearch 2.12.0.
	nodeStatsPathResourceUsage = nodeStatsPath + ",resource_usage_stats"

	// resourceUsageStatsMinVersion is the first version that serves resource_usage_stats.
	resourceUsageStatsMinVersion = "v2.12.0"
)

// nodeStatsPathFor returns the node stats request path for a server version.
// An empty or non-semver version gets the path every version accepts.
func nodeStatsPathFor(version string) string {
	v := withV(version)
	if semver.IsValid(v) && semver.Compare(v, resourceUsageStatsMinVersion) >= 0 {
		return nodeStatsPathResourceUsage
	}
	return nodeStatsPath
}

// NodeStatsResponse represents the response from the node stats poll (GET /_nodes/_local/stats).
// Only the "nodes" map is used; the top-level "_nodes" and "cluster_name" fields are ignored.
// All fields are present in OpenSearch 1.3.0+ except [NodeStats.ResourceUsageStats].
type NodeStatsResponse struct {
	Nodes map[string]NodeStats `json:"nodes"`
}

// NodeStats represents per-node statistics used for load shedding and
// congestion window updates.
type NodeStats struct {
	JVM         JVMStats                   `json:"jvm"`
	Breakers    map[string]BreakerStats    `json:"breakers"`
	ThreadPools map[string]ThreadPoolStats `json:"thread_pool,omitempty"`
	// AdaptiveSelection is the polled node's view of each target node as a
	// coordinator, keyed by target node ID.
	AdaptiveSelection map[string]AdaptiveSelectionStats `json:"adaptive_selection,omitempty"`
	// ResourceUsageStats is keyed by node ID. Present on OpenSearch 2.12.0+.
	ResourceUsageStats map[string]ResourceUsageStats `json:"resource_usage_stats,omitempty"`
}

// ResourceUsageStats is one node's CPU and IO utilization. The server sends
// the percentages as JSON strings; they are parsed when evaluated.
type ResourceUsageStats struct {
	CPUUtilizationPercent string `json:"cpu_utilization_percent"`
	// IOUsageStats is present on Linux servers only.
	IOUsageStats *IOUsageStats `json:"io_usage_stats,omitempty"`
}

// IOUsageStats is a node's IO utilization.
type IOUsageStats struct {
	MaxIOUtilizationPercent string `json:"max_io_utilization_percent"`
}

// AdaptiveSelectionStats is one target's adaptive replica selection stats.
// The server's rank and human-readable time strings are ignored.
type AdaptiveSelectionStats struct {
	OutgoingSearches  int64 `json:"outgoing_searches"`
	AvgQueueSize      int64 `json:"avg_queue_size"`
	AvgServiceTimeNs  int64 `json:"avg_service_time_ns"`
	AvgResponseTimeNs int64 `json:"avg_response_time_ns"`
}

// JVMStats contains JVM-level statistics.
type JVMStats struct {
	Mem JVMMemStats `json:"mem"`
}

// JVMMemStats contains JVM memory statistics.
type JVMMemStats struct {
	HeapUsedPercent int `json:"heap_used_percent"`
}

// BreakerStats contains circuit breaker statistics for a single breaker.
// OpenSearch exposes breakers for: fielddata, request, in_flight_requests, accounting, parent.
type BreakerStats struct {
	LimitSizeInBytes     int64 `json:"limit_size_in_bytes"`
	EstimatedSizeInBytes int64 `json:"estimated_size_in_bytes"`
	Tripped              int64 `json:"tripped"`
}

// scheduleNodeStats starts a background ticker that periodically polls node stats
// for load shedding. When nodeStatsIntervalAuto is true, the interval is recalculated
// on each tick based on cluster size:
//
//	interval = clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 30s)
//
// Cancelled by tr.ctx.
func (tr *Transport) scheduleNodeStats() {
	go func() {
		interval := tr.nodeStatsInterval
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-tr.ctx.Done():
				return
			case <-ticker.C:
				tr.pollNodeStats()

				if tr.nodeStatsIntervalAuto {
					newInterval := tr.calculateNodeStatsInterval()
					if newInterval != interval {
						interval = newInterval
						ticker.Reset(interval)
					}
				}
			}
		}
	}()
}

// calculateNodeStatsInterval computes the polling interval based on the current
// cluster size and the configured health check rate.
//
//	interval = clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 30s)
func (tr *Transport) calculateNodeStatsInterval() time.Duration {
	liveNodes := tr.countReadyNodes()
	if liveNodes <= 0 {
		liveNodes = 1
	}

	tr.mu.RLock()
	clientsPerServer := tr.clientsPerServer
	healthCheckRate := tr.healthCheckRate
	tr.mu.RUnlock()

	intervalSec := float64(liveNodes) * clientsPerServer / healthCheckRate
	interval := min(
		max(
			time.Duration(intervalSec*float64(time.Second)), defaultNodeStatsIntervalMin), defaultNodeStatsIntervalMax)

	return interval
}

// pollNodeStats polls node stats for all connections in the connection pool,
// regardless of pool type. Updates per-pool AIMD congestion windows and
// evaluates node-level overload state.
//
// Connection pointers are shared across all policies (see guides/transport-routing.md
// "Shared Connections Across Policies"). Polling a connection updates the
// authoritative atomic state on the Connection itself, which is immediately
// visible to every policy.
func (tr *Transport) pollNodeStats() {
	tr.mu.RLock()
	cp := tr.mu.connectionPool
	tr.mu.RUnlock()

	switch pool := cp.(type) {
	case *multiServerPool:
		// Snapshot connections to evaluate.
		pool.RLock()
		snapshot := make([]*Connection, 0, len(pool.mu.ready)+len(pool.mu.dead))
		snapshot = append(snapshot, pool.mu.ready...)
		// Include overload-demoted dead connections so we can promote them if recovered.
		for _, conn := range pool.mu.dead {
			if conn.loadConnState().lifecycle().has(lcOverloaded) {
				snapshot = append(snapshot, conn)
			}
		}
		pool.RUnlock()

		samples := make([]nodeSearchSample, 0, len(snapshot))
		for _, conn := range snapshot {
			if sample, ok := tr.fetchAndEvaluateNodeStats(conn, pool); ok {
				samples = append(samples, sample)
			}
		}
		if len(samples) > 0 {
			tr.clusterSearch.update(samples)
		}

	case *singleServerPool:
		// Single-node pool: poll the connection for AIMD updates.
		// Overload demotion is not applicable (no pool to demote within).
		conn := pool.connection
		if conn != nil {
			if sample, ok := tr.fetchAndEvaluateNodeStats(conn, nil); ok {
				tr.clusterSearch.update([]nodeSearchSample{sample})
			}
		}
	}
}

// fetchAndEvaluateNodeStats polls a single node's stats, updates per-pool
// congestion windows (AIMD), and evaluates node-level overload state.
//
// Two data sources are combined for the overload decision:
//  1. Node-level stats from GET /_nodes/_local/stats (metric list from [nodeStatsPathFor]; fetched here)
//  2. Cluster health from conn.mu.clusterHealth (already populated by clusterHealthCheck)
//
// Thread pool stats drive per-pool AIMD congestion control via [updatePoolCongestion].
// This avoids redundant HTTP calls since the cluster health check already collects
// cluster health data during normal health check cycles.
//
// Returns the search thread pool sample for cluster-wide aggregation (see
// [clusterSearchAIMD]). ok is false when the poll fails or the node has no
// search pool data.
func (tr *Transport) fetchAndEvaluateNodeStats(conn *Connection, pool *multiServerPool) (nodeSearchSample, bool) {
	var sample nodeSearchSample

	ctx, cancel := tr.healthCheckContext(tr.ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeStatsPathFor(conn.loadVersion()), nil)
	if err != nil {
		return sample, false
	}

	if err = tr.prepareInternalRequest(conn.URL, req, tr.healthCheckRequestModifier); err != nil {
		Debug().Str("conn", conn.URLString).Err(err).Msg("Stats poll failed, cannot prepare request")
		return sample, false
	}

	res, err := tr.transport.RoundTrip(req)
	if err != nil {
		// Can't reach node -- if overload-demoted, clear overloaded flag so the normal
		// resurrection scheduler can take over (the node may actually be down, not just overloaded).
		if conn.loadConnState().lifecycle().has(lcOverloaded) {
			conn.mu.Lock()
			//nolint:errcheck // lock held; only errLifecycleNoop possible
			conn.casLifecycle(
				conn.loadConnState(), 0,
				lcDead|lcNeedsWarmup,
				lcReady|lcActive|lcStandby,
			)
			conn.mu.Unlock()
			Debug().
				Str("conn", conn.URLString).
				Err(err).
				Msg("Stats poll failed, clearing overloaded flag (resurrection scheduler will handle)")
		}
		return sample, false
	}
	if res.Body != nil {
		// Drain on close so http.Transport can reuse the connection. This is a
		// raw RoundTrip path via Stream; body is caller-owned, so closing a
		// partially-read body (e.g. on the non-200 early return below) would
		// otherwise defeat keep-alive. On the success path io.ReadAll has
		// already reached EOF, so the drain is a cheap no-op.
		defer func() {
			//nolint:errcheck // best-effort drain before close on a cleanup path
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}()
	}

	if res.StatusCode == http.StatusTooManyRequests {
		// The node is rejecting work itself, so an overload flag still holds.
		return sample, false
	}
	if res.StatusCode != http.StatusOK || res.Body == nil {
		clearUnverifiedOverload(conn, pool, "status "+strconv.Itoa(res.StatusCode))
		return sample, false
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		clearUnverifiedOverload(conn, pool, "body read failed")
		return sample, false
	}

	var stats NodeStatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		clearUnverifiedOverload(conn, pool, "body not parseable")
		return sample, false
	}

	// The response contains a map with a single node entry (since we used _local)
	// The map key is the node ID; conn.ID may be unset for seed connections.
	var nodeStats *NodeStats
	var nodeID string
	for id, ns := range stats.Nodes {
		nodeID, nodeStats = id, &ns
		break
	}
	if nodeStats == nil {
		clearUnverifiedOverload(conn, pool, "no node in response")
		return sample, false
	}

	recordAdaptiveSelection(conn, pool, nodeID, nodeStats.AdaptiveSelection)

	// Update per-pool congestion windows (AIMD) from thread pool stats.
	updatePoolCongestion(conn, nodeStats.ThreadPools)

	// Extract search pool stats for cluster-wide MCSR aggregation.
	var ok bool
	if searchTP, found := nodeStats.ThreadPools[poolSearch]; found {
		var searchMaxCwnd int32
		if searchPC := conn.pools.get(poolSearch); searchPC != nil {
			searchPC.mu.Lock()
			searchMaxCwnd = searchPC.mu.maxCwnd
			searchPC.mu.Unlock()
		}
		sample = nodeSearchSample{
			conn:    conn,
			stats:   searchTP,
			maxCwnd: searchMaxCwnd,
		}
		ok = true
	}

	overloaded := tr.evaluateOverload(conn, nodeStats, nodeID)

	// Overload demotion/promotion requires a multiServerPool (single-node pools
	// pass nil since there is nowhere to demote to).
	if pool == nil {
		return sample, ok
	}

	wasOverloaded := conn.loadConnState().lifecycle().has(lcOverloaded)

	switch {
	case overloaded && !wasOverloaded:
		pool.demoteOverloaded(conn)
	case !overloaded && wasOverloaded:
		pool.promoteFromOverloaded(conn)
	}

	return sample, ok
}

// clearUnverifiedOverload clears conn's overload flag when its node answered the
// stats poll but the answer cannot be evaluated. Only this poll clears the flag,
// so leaving it set would hold a node that keeps answering this way out of
// rotation for good. The node returns to standby rotation, where request
// failures and health checks judge it as usual. pool is nil for a single-node
// pool, which never demotes.
func clearUnverifiedOverload(conn *Connection, pool *multiServerPool, reason string) {
	if pool == nil || !conn.loadConnState().lifecycle().has(lcOverloaded) {
		return
	}
	Debug().Str("conn", conn.URLString).Str("reason", reason).Msg("Stats poll unusable, clearing overloaded flag")
	pool.promoteFromOverloaded(conn)
}

// percentAtLeast reports whether s parses as a number >= threshold.
// An empty or unparseable s is false.
func percentAtLeast(s string, threshold float64) bool {
	v, err := strconv.ParseFloat(s, 64)
	return err == nil && v >= threshold
}

// evaluateOverload checks both node-level stats and cluster health against overload thresholds.
// Returns true if any overload condition is met.
//
// # Node-level checks (jvm, breaker and resource_usage_stats sections of the node stats poll)
//
//   - JVM heap_used_percent >= overloadedHeapThreshold (default 85%)
//   - Any circuit breaker's estimated_size / limit_size >= overloadedBreakerRatio (default 0.90)
//   - Any circuit breaker's tripped count increased since the last poll (delta detection)
//   - The polled node's resource_usage_stats cpu_utilization_percent >= overloadedCPUPercent (default 95) (OpenSearch 2.12.0+)
//   - The polled node's resource_usage_stats max_io_utilization_percent >= overloadedIOPercent (default 95) (OpenSearch 2.12.0+, Linux)
//
// nodeID is the polled node's ID, the key of its own resource_usage_stats entry.
//
// # Cluster health checks (from conn.mu.clusterHealth, populated by clusterHealthCheck)
//
//   - Cluster status is "red" (data loss or primary shards unassigned)
//
// The cluster health data is a free signal -- it's already collected during periodic health
// checks via /_cluster/health?local=true without any additional HTTP calls.
//
// Updates conn.mu.lastBreakerTripped for delta detection on next poll.
func (tr *Transport) evaluateOverload(conn *Connection, stats *NodeStats, nodeID string) bool {
	overloaded := false

	// --- Cluster health checks (reuse data from clusterHealthCheck) ---

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	conn.mu.RUnlock()

	if health != nil && health.Status == clusterStatusRed {
		Debug().Str("conn", conn.URLString).Msg("Node overloaded: cluster status is red")
		overloaded = true
	}

	// --- Node-level stats checks ---

	// JVM heap usage
	if stats.JVM.Mem.HeapUsedPercent >= tr.overloadedHeapThreshold {
		Debug().
			Str("conn", conn.URLString).
			Int("heap_used_percent", stats.JVM.Mem.HeapUsedPercent).
			Int("threshold", tr.overloadedHeapThreshold).
			Msg("Node overloaded: heap over threshold")
		overloaded = true
	}

	// CPU and IO utilization of the polled node (OpenSearch 2.12.0+).
	// memory_utilization_percent is not checked: the server averages JVM heap
	// use for it, which the heap check above already covers.
	if rus, found := stats.ResourceUsageStats[nodeID]; found {
		if percentAtLeast(rus.CPUUtilizationPercent, float64(tr.overloadedCPUPercent)) {
			Debug().
				Str("conn", conn.URLString).
				Str("cpu_utilization_percent", rus.CPUUtilizationPercent).
				Int("threshold", tr.overloadedCPUPercent).
				Msg("Node overloaded: CPU over threshold")
			overloaded = true
		}
		if rus.IOUsageStats != nil && percentAtLeast(rus.IOUsageStats.MaxIOUtilizationPercent, float64(tr.overloadedIOPercent)) {
			Debug().
				Str("conn", conn.URLString).
				Str("max_io_utilization_percent", rus.IOUsageStats.MaxIOUtilizationPercent).
				Int("threshold", tr.overloadedIOPercent).
				Msg("Node overloaded: IO over threshold")
			overloaded = true
		}
	}

	// Circuit breakers
	conn.mu.Lock()
	if conn.mu.lastBreakerTripped == nil {
		conn.mu.lastBreakerTripped = make(map[string]int64, len(stats.Breakers))
	}

	for name, breaker := range stats.Breakers {
		// Size ratio check (instantaneous)
		if breaker.LimitSizeInBytes > 0 {
			ratio := float64(breaker.EstimatedSizeInBytes) / float64(breaker.LimitSizeInBytes)
			if ratio >= tr.overloadedBreakerRatio {
				Debug().
					Str("conn", conn.URLString).
					Str("breaker", name).
					Float64("ratio", ratio).
					Float64("threshold", tr.overloadedBreakerRatio).
					Msg("Node overloaded: breaker size over threshold")
				overloaded = true
			}
		}

		// Trip delta check (cumulative)
		prevTripped, existed := conn.mu.lastBreakerTripped[name]
		conn.mu.lastBreakerTripped[name] = breaker.Tripped

		if existed && breaker.Tripped > prevTripped {
			Debug().
				Str("conn", conn.URLString).
				Str("breaker", name).
				Int64("tripped_delta", breaker.Tripped-prevTripped).
				Int64("tripped_from", prevTripped).
				Int64("tripped_to", breaker.Tripped).
				Msg("Node overloaded: breaker tripped since last poll")
			overloaded = true
		}
	}
	conn.mu.Unlock()

	return overloaded
}

// scheduleClusterHealthRefresh starts a background goroutine that periodically refreshes
// /_cluster/health?local=true data on ready connections that support cluster health.
// The refresh interval is dynamically calculated using:
//
//	refreshInterval = clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 5min)
//
// Single-node clusters skip refresh entirely since health data cannot influence routing.
// Cancelled by tr.ctx.
func (tr *Transport) scheduleClusterHealthRefresh() {
	go func() {
		interval := tr.calculateClusterHealthRefreshInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-tr.ctx.Done():
				return
			case <-ticker.C:
				tr.pollClusterHealth()

				// Recalculate interval after each poll (node count may have changed)
				newInterval := tr.calculateClusterHealthRefreshInterval()
				if newInterval != interval {
					interval = newInterval
					ticker.Reset(interval)
				}
			}
		}
	}()
}

// calculateClusterHealthRefreshInterval computes the polling interval based on
// the current cluster size and the configured health check rate.
//
//	interval = clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 5min)
func (tr *Transport) calculateClusterHealthRefreshInterval() time.Duration {
	liveNodes := tr.countReadyNodes()
	if liveNodes <= 0 {
		liveNodes = 1 // Prevent zero interval; will be short-circuited by single-node check in pollClusterHealth
	}

	tr.mu.RLock()
	clientsPerServer := tr.clientsPerServer
	healthCheckRate := tr.healthCheckRate
	tr.mu.RUnlock()

	intervalSec := float64(liveNodes) * clientsPerServer / healthCheckRate
	interval := min(
		// Clamp to [min, max]
		max(

			time.Duration(intervalSec*float64(time.Second)), defaultClusterHealthRefreshMin), defaultClusterHealthRefreshMax)

	return interval
}

// countReadyNodes returns the number of ready connections in the current pool.
func (tr *Transport) countReadyNodes() int {
	tr.mu.RLock()
	pool := tr.mu.connectionPool
	tr.mu.RUnlock()

	switch p := pool.(type) {
	case *singleServerPool:
		return 1
	case *multiServerPool:
		p.mu.RLock()
		count := len(p.mu.ready)
		p.mu.RUnlock()
		return count
	default:
		return 0
	}
}

// pollClusterHealth refreshes /_cluster/health?local=true on all ready connections that
// have HasClusterHealth(). Skips single-node clusters and connections without cluster health.
func (tr *Transport) pollClusterHealth() {
	tr.mu.RLock()
	pool := tr.mu.connectionPool
	tr.mu.RUnlock()

	// Skip single-node clusters: no value in refreshing health when we cannot route away.
	switch p := pool.(type) {
	case *singleServerPool:
		return
	case *multiServerPool:
		p.mu.RLock()
		totalNodes := len(p.mu.ready) + len(p.mu.dead)
		p.mu.RUnlock()
		if totalNodes <= 1 {
			return
		}
	default:
		return
	}

	conns := tr.snapshotClusterHealthConnections()
	if len(conns) == 0 {
		return
	}

	for _, conn := range conns {
		tr.refreshClusterHealth(conn)
	}
}

// snapshotClusterHealthConnections returns ready connections that have HasClusterHealth()
// from the current connection pool.
func (tr *Transport) snapshotClusterHealthConnections() []*Connection {
	tr.mu.RLock()
	pool := tr.mu.connectionPool
	tr.mu.RUnlock()

	if pool == nil {
		return nil
	}

	p, ok := pool.(*multiServerPool)
	if !ok {
		return nil
	}

	p.mu.RLock()
	snapshot := make([]*Connection, 0, len(p.mu.ready))
	for _, conn := range p.mu.ready {
		if conn.hasClusterHealth() {
			snapshot = append(snapshot, conn)
		}
	}
	p.mu.RUnlock()

	return snapshot
}

// refreshClusterHealth performs a single /_cluster/health?local=true request against
// the given connection and updates conn.mu.clusterHealth.
//
// Error handling:
//   - 200: Parse response, store ClusterHealthLocal on connection, update timestamp.
//   - 401/403: Permission revoked at runtime. Reset cluster health lifecycle bits to pending,
//     zero out clusterHealth. The connection will fall back to baseline GET / health checks.
//   - Transient errors (network, 5xx): Log and skip. The next poll cycle will retry.

// ClusterHealthLocal represents the response from GET /_cluster/health?local=true.
// The local=true parameter causes the request to be served from the connected node's
// local cluster state cache rather than requiring a round-trip to the cluster-manager node,
// making it suitable for fast, lightweight health probes.
//
// This endpoint requires the cluster:monitor/health action privilege. If the OpenSearch
// Security plugin is installed, requests without valid credentials receive 401 Unauthorized,
// and authenticated users who lack the privilege receive 403 Forbidden. To grant the
// minimum required permission, create a role with:
//
//	health_check_role:
//	  cluster_permissions:
//	    - "cluster:monitor/health"
//
// The client automatically detects whether this permission is available and falls back to
// GET / when it is not. See [Transport.DefaultHealthCheck] for the capability detection lifecycle.
//
// All fields are present in OpenSearch 1.3.0+.
type ClusterHealthLocal struct {
	ClusterName                 string  `json:"cluster_name"`
	Status                      string  `json:"status"` // "green", "yellow", "red"
	TimedOut                    bool    `json:"timed_out"`
	NumberOfNodes               int     `json:"number_of_nodes"`
	NumberOfDataNodes           int     `json:"number_of_data_nodes"`
	ActivePrimaryShards         int     `json:"active_primary_shards"`
	ActiveShards                int     `json:"active_shards"`
	RelocatingShards            int     `json:"relocating_shards"`
	InitializingShards          int     `json:"initializing_shards"`
	UnassignedShards            int     `json:"unassigned_shards"`
	DelayedUnassignedShards     int     `json:"delayed_unassigned_shards"`
	NumberOfPendingTasks        int     `json:"number_of_pending_tasks"`
	NumberOfInFlightFetch       int     `json:"number_of_in_flight_fetch"`
	TaskMaxWaitingInQueueMillis int     `json:"task_max_waiting_in_queue_millis"`
	ActiveShardsPercentAsNumber float64 `json:"active_shards_percent_as_number"`
	// Added in OpenSearch 2.4.0
	DiscoveredClusterManager *bool `json:"discovered_cluster_manager,omitempty"`
}

// healthCheckInfo represents the minimal structure needed to extract version from health check response
type healthCheckInfo struct {
	Version struct {
		// Distribution is "opensearch" on OpenSearch nodes. A 1.x node running with
		// compatibility.override_main_response_version omits it and reports number 7.10.2.
		Distribution string `json:"distribution"`
		Number       string `json:"number"`
	} `json:"version"`
}

// ClusterHealth returns the most recent cluster health snapshot for this connection, or nil
// if cluster health has not been probed or is unavailable.
func (conn *Connection) ClusterHealth() *ClusterHealthLocal {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.mu.clusterHealth
}

func (tr *Transport) refreshClusterHealth(conn *Connection) {
	applyModifier := tr.healthCheckRequestModifier

	health, statusCode, err := tr.fetchClusterHealth(tr.ctx, conn.URL, applyModifier)
	if err != nil {
		Debug().Str("conn", conn.URLString).Err(err).Msg("Cluster health refresh failed")
		return
	}

	switch {
	case statusCode == http.StatusOK && health != nil:
		conn.mu.Lock()
		storeClusterHealth(conn, health)
		conn.setLifecycleBit(lcClusterHealthProbed | lcClusterHealthAvailable) //nolint:errcheck // noop is fine
		conn.mu.Unlock()

	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		resetClusterHealth(conn)

		Debug().
			Int("status_code", statusCode).
			Str("conn", conn.URLString).
			Msg("Cluster health refresh got unexpected status, resetting to pending")

	default:
		// Unexpected status code; skip update and retry on next poll cycle.
	}
}
