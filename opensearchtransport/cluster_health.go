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
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// NodeStatsResponse represents the response from GET /_nodes/_local/stats/jvm,breaker,thread_pool.
// Only the "nodes" map is used; the top-level "_nodes" and "cluster_name" fields are ignored.
// All fields are present in OpenSearch 1.3.0+.
type NodeStatsResponse struct {
	Nodes map[string]NodeStats `json:"nodes"`
}

// NodeStats represents per-node statistics used for load shedding and
// congestion window updates.
type NodeStats struct {
	JVM         JVMStats                   `json:"jvm"`
	Breakers    map[string]BreakerStats    `json:"breakers"`
	ThreadPools map[string]ThreadPoolStats `json:"thread_pool,omitempty"`
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
// Cancelled by c.ctx.
func (c *Client) scheduleNodeStats() {
	go func() {
		interval := c.nodeStatsInterval
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.pollNodeStats()

				if c.nodeStatsIntervalAuto {
					newInterval := c.calculateNodeStatsInterval()
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
func (c *Client) calculateNodeStatsInterval() time.Duration {
	liveNodes := c.countReadyNodes()
	if liveNodes <= 0 {
		liveNodes = 1
	}

	c.mu.RLock()
	clientsPerServer := c.clientsPerServer
	healthCheckRate := c.healthCheckRate
	c.mu.RUnlock()

	intervalSec := float64(liveNodes) * clientsPerServer / healthCheckRate
	interval := min(
		max(
			time.Duration(intervalSec*float64(time.Second)), defaultNodeStatsIntervalMin), defaultNodeStatsIntervalMax)

	return interval
}

// pollNodeStats snapshots connections and evaluates overload for each.
// Ready connections are polled via /_nodes/_local/stats/jvm,breaker.
// Overload-demoted dead connections are re-evaluated to detect recovery.
func (c *Client) pollNodeStats() {
	c.mu.RLock()
	pool, ok := c.mu.connectionPool.(*multiServerPool)
	c.mu.RUnlock()

	if !ok || pool == nil {
		return
	}

	// Snapshot connections to evaluate.
	pool.RLock()
	snapshot := make([]*Connection, 0, len(pool.mu.ready)+len(pool.mu.dead))
	snapshot = append(snapshot, pool.mu.ready...)
	// Include overload-demoted dead connections so we can promote them if recovered
	for _, conn := range pool.mu.dead {
		if conn.loadConnState().lifecycle().has(lcOverloaded) {
			snapshot = append(snapshot, conn)
		}
	}
	pool.RUnlock()

	for _, conn := range snapshot {
		c.fetchAndEvaluateNodeStats(conn, pool)
	}
}

// fetchAndEvaluateNodeStats polls a single node's stats, updates per-pool
// congestion windows (AIMD), and evaluates node-level overload state.
//
// Two data sources are combined for the overload decision:
//  1. Node-level stats from GET /_nodes/_local/stats/jvm,breaker,thread_pool (fetched here)
//  2. Cluster health from conn.mu.clusterHealth (already populated by clusterHealthCheck)
//
// Thread pool stats drive per-pool AIMD congestion control via [updatePoolCongestion].
// This avoids redundant HTTP calls since the cluster health check already collects
// cluster health data during normal health check cycles.
func (c *Client) fetchAndEvaluateNodeStats(conn *Connection, pool *multiServerPool) {
	ctx, cancel := context.WithTimeout(c.ctx, c.healthCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_nodes/_local/stats/jvm,breaker,thread_pool", nil)
	if err != nil {
		return
	}

	c.setReqURL(conn.URL, req)
	c.setReqAuth(conn.URL, req)
	c.setReqUserAgent(req)

	if c.healthCheckRequestModifier != nil {
		c.healthCheckRequestModifier(req)
	}

	res, err := c.transport.RoundTrip(req)
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
			if debugLogger != nil {
				debugLogger.Logf("Stats poll failed for %q, clearing overloaded flag (resurrection scheduler will handle): %v\n", conn.URL, err)
			}
		}
		return
	}
	if res.Body != nil {
		defer res.Body.Close()
	}

	if res.StatusCode != http.StatusOK || res.Body == nil {
		return
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return
	}

	var stats NodeStatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		return
	}

	// The response contains a map with a single node entry (since we used _local)
	var nodeStats *NodeStats
	for _, ns := range stats.Nodes {
		nodeStats = &ns
		break
	}
	if nodeStats == nil {
		return
	}

	// Update per-pool congestion windows (AIMD) from thread pool stats.
	updatePoolCongestion(conn, nodeStats.ThreadPools)

	overloaded := c.evaluateOverload(conn, nodeStats)

	wasOverloaded := conn.loadConnState().lifecycle().has(lcOverloaded)

	switch {
	case overloaded && !wasOverloaded:
		pool.demoteOverloaded(conn)
	case !overloaded && wasOverloaded:
		pool.promoteFromOverloaded(conn)
	}
}

// evaluateOverload checks both node-level stats and cluster health against overload thresholds.
// Returns true if any overload condition is met.
//
// # Node-level checks (from /_nodes/_local/stats/jvm,breaker)
//
//   - JVM heap_used_percent >= overloadedHeapThreshold (default 85%)
//   - Any circuit breaker's estimated_size / limit_size >= overloadedBreakerRatio (default 0.90)
//   - Any circuit breaker's tripped count increased since the last poll (delta detection)
//
// # Cluster health checks (from conn.mu.clusterHealth, populated by clusterHealthCheck)
//
//   - Cluster status is "red" (data loss or primary shards unassigned)
//
// The cluster health data is a free signal -- it's already collected during periodic health
// checks via /_cluster/health?local=true without any additional HTTP calls.
//
// Updates conn.mu.lastBreakerTripped for delta detection on next poll.
func (c *Client) evaluateOverload(conn *Connection, stats *NodeStats) bool {
	overloaded := false

	// --- Cluster health checks (reuse data from clusterHealthCheck) ---

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	conn.mu.RUnlock()

	if health != nil && health.Status == "red" {
		if debugLogger != nil {
			debugLogger.Logf("Node %q overloaded: cluster status is red\n", conn.URL)
		}
		overloaded = true
	}

	// --- Node-level stats checks ---

	// JVM heap usage
	if stats.JVM.Mem.HeapUsedPercent >= c.overloadedHeapThreshold {
		if debugLogger != nil {
			debugLogger.Logf("Node %q overloaded: heap_used_percent=%d >= threshold=%d\n",
				conn.URL, stats.JVM.Mem.HeapUsedPercent, c.overloadedHeapThreshold)
		}
		overloaded = true
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
			if ratio >= c.overloadedBreakerRatio {
				if debugLogger != nil {
					debugLogger.Logf("Node %q overloaded: breaker %q size ratio=%.3f >= threshold=%.3f\n",
						conn.URL, name, ratio, c.overloadedBreakerRatio)
				}
				overloaded = true
			}
		}

		// Trip delta check (cumulative)
		prevTripped, existed := conn.mu.lastBreakerTripped[name]
		conn.mu.lastBreakerTripped[name] = breaker.Tripped

		if existed && breaker.Tripped > prevTripped {
			if debugLogger != nil {
				debugLogger.Logf("Node %q overloaded: breaker %q tripped %d times since last poll (prev=%d, now=%d)\n",
					conn.URL, name, breaker.Tripped-prevTripped, prevTripped, breaker.Tripped)
			}
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
// Cancelled by c.ctx.
func (c *Client) scheduleClusterHealthRefresh() {
	go func() {
		interval := c.calculateClusterHealthRefreshInterval()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.pollClusterHealth()

				// Recalculate interval after each poll (node count may have changed)
				newInterval := c.calculateClusterHealthRefreshInterval()
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
func (c *Client) calculateClusterHealthRefreshInterval() time.Duration {
	liveNodes := c.countReadyNodes()
	if liveNodes <= 0 {
		liveNodes = 1 // Prevent zero interval; will be short-circuited by single-node check in pollClusterHealth
	}

	c.mu.RLock()
	clientsPerServer := c.clientsPerServer
	healthCheckRate := c.healthCheckRate
	c.mu.RUnlock()

	intervalSec := float64(liveNodes) * clientsPerServer / healthCheckRate
	interval := min(
		// Clamp to [min, max]
		max(

			time.Duration(intervalSec*float64(time.Second)), defaultClusterHealthRefreshMin), defaultClusterHealthRefreshMax)

	return interval
}

// countReadyNodes returns the number of ready connections in the current pool.
func (c *Client) countReadyNodes() int {
	c.mu.RLock()
	pool := c.mu.connectionPool
	c.mu.RUnlock()

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
func (c *Client) pollClusterHealth() {
	c.mu.RLock()
	pool := c.mu.connectionPool
	c.mu.RUnlock()

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

	conns := c.snapshotClusterHealthConnections()
	if len(conns) == 0 {
		return
	}

	for _, conn := range conns {
		c.refreshClusterHealth(conn)
	}
}

// snapshotClusterHealthConnections returns ready connections that have HasClusterHealth()
// from the current connection pool.
func (c *Client) snapshotClusterHealthConnections() []*Connection {
	c.mu.RLock()
	pool := c.mu.connectionPool
	c.mu.RUnlock()

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
		if conn.loadClusterHealthState().HasClusterHealth() {
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
//   - 401/403: Permission revoked at runtime. Reset clusterHealthState to pending,
//     zero out clusterHealth. The connection will fall back to baseline GET / health checks.
//   - Transient errors (network, 5xx): Log and skip. The next poll cycle will retry.
// ---------------------------------------------------------------------------
// Cluster health types -- moved from connection.go for cohesion
// ---------------------------------------------------------------------------

// clusterHealthProbeState represents the capability state of a connection's cluster health check support.
// It is stored as an atomic.Int64 bitfield on Connection and loaded once per decision point
// to avoid redundant atomic loads within the same code path.
type clusterHealthProbeState int64

const (
	// clusterHealthProbed indicates the node has been probed for cluster health capability.
	// Bit 0 (value 1).
	clusterHealthProbed clusterHealthProbeState = 1 << iota

	// clusterHealthAvailable indicates the probe succeeded and /_cluster/health?local=true is usable.
	// Bit 1 (value 2). Only meaningful when clusterHealthProbed is also set.
	clusterHealthAvailable
)

// HasClusterHealth returns true if the connection has been probed and supports /_cluster/health?local=true.
func (e clusterHealthProbeState) HasClusterHealth() bool {
	return e&clusterHealthProbed != 0 && e&clusterHealthAvailable != 0
}

// Pending returns true if cluster health has never been probed on this connection.
func (e clusterHealthProbeState) Pending() bool {
	return e == 0
}

// Unavailable returns true if cluster health was probed and found unavailable.
// This occurs when the node returned 401 (authentication failure) or 403 (authorization failure,
// i.e., the authenticated user lacks the cluster:monitor/health privilege).
func (e clusterHealthProbeState) Unavailable() bool {
	return e&clusterHealthProbed != 0 && e&clusterHealthAvailable == 0
}

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
// GET / when it is not. See [Client.DefaultHealthCheck] for the capability detection lifecycle.
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
		Number string `json:"number"`
	} `json:"version"`
}

// loadClusterHealthState atomically loads the cluster health state bitfield once.
// Use the returned clusterHealthProbeState value for all subsequent checks within a single code path
// to avoid redundant atomic loads.
func (c *Connection) loadClusterHealthState() clusterHealthProbeState {
	return clusterHealthProbeState(c.clusterHealthState.Load())
}

// ClusterHealth returns the most recent cluster health snapshot for this connection, or nil
// if cluster health has not been probed or is unavailable.
func (c *Connection) ClusterHealth() *ClusterHealthLocal {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mu.clusterHealth
}

func (c *Client) refreshClusterHealth(conn *Connection) {
	applyModifier := c.healthCheckRequestModifier

	health, statusCode, err := c.fetchClusterHealth(c.ctx, conn.URL, applyModifier)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Logf("Cluster health refresh failed for %q: %v\n", conn.URL, err)
		}
		return
	}

	switch {
	case statusCode == http.StatusOK && health != nil:
		storeClusterHealth(conn, health)

	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		resetClusterHealth(conn)

		if debugLogger != nil {
			debugLogger.Logf("Cluster health refresh got %d for %q, resetting to pending\n",
				statusCode, conn.URL)
		}

	default:
		// Unexpected status code; skip update and retry on next poll cycle.
	}
}
