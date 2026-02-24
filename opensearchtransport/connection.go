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
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultResurrectTimeoutInitial      = 5 * time.Second
	defaultResurrectTimeoutMax          = 30 * time.Second
	defaultResurrectTimeoutFactorCutoff = 5
	defaultMinimumResurrectTimeout      = 500 * time.Millisecond
	defaultJitterScale                  = 0.5

	// defaultDrainingQuiescingChecks is the number of consecutive successful health checks
	// required before a draining connection can be resurrected. This gives the server time
	// to fully quiesce after a stream reset. Each health check is spaced by the normal
	// resurrection timeout (no additional backoff), so the total quiesce window is
	// approximately defaultDrainingQuiescingChecks * resurrectTimeoutInitial.
	defaultDrainingQuiescingChecks int64 = 3

	// Health check rate limiting defaults.
	//
	// The per-client health check rate is derived from the server core count:
	//   serverMaxNewConnsPerSec = serverCoreCount * serverMaxNewConnsPerSecMultiplier
	//   clientsPerServer        = serverCoreCount
	//   healthCheckRate         = serverCoreCount * healthCheckRateMultiplier
	//   perClientRate           = serverMaxNewConnsPerSec / clientsPerServer
	//                           = serverMaxNewConnsPerSecMultiplier (constant at 4.0)
	//
	// With defaults (serverCoreCount=8): serverMaxNewConnsPerSec=32, clientsPerServer=8,
	// perClientRate=4.0 health checks/sec.
	defaultServerCoreCount            = 8    // default assumed core count per server
	serverMaxNewConnsPerSecMultiplier = 4.0  // serverMaxNewConnsPerSec = cores * this
	healthCheckRateMultiplier         = 0.10 // unified health check rate = cores * this

	// Node stats / load shedding defaults.
	//
	// A node is marked overloaded (and demoted from the ready list) when any of these
	// thresholds are breached. The stats poller promotes the node back when all
	// conditions clear.
	//
	// The polling interval is auto-derived from the cluster size when NodeStatsInterval == 0:
	//   nodeStatsInterval = clamp(liveNodes * clientsPerServer / healthCheckRate, min, max)
	//
	// With defaults (serverCoreCount=8): clientsPerServer=8, healthCheckRate=0.8:
	//   nodeStatsInterval = clamp(liveNodes * 10, 5s, 30s)
	//
	// Small clusters (1-3 nodes) get aggressive 5s polling for fast overload detection.
	// Large clusters cap at 30s to limit N×M polling amplification.
	defaultNodeStatsIntervalMin    = 5 * time.Second  // minimum poll interval (small clusters)
	defaultNodeStatsIntervalMax    = 30 * time.Second // maximum poll interval (large clusters)
	defaultOverloadedHeapThreshold = 85               // JVM heap_used_percent (0-100)
	defaultOverloadedBreakerRatio  = 0.90             // circuit breaker estimated_size / limit_size (0.0-1.0)

	// Cluster health refresh defaults for ready connections.
	//
	// Ready connections with cluster health info are periodically refreshed to keep
	// conn.mu.clusterHealth data current for load-shedding decisions. The refresh
	// interval is calculated per the formula:
	//   refreshInterval = clamp(liveNodes * clientsPerServer / healthCheckRate, min, max)
	//
	// With defaults (serverCoreCount=8): healthCheckRate=0.8, clientsPerServer=8:
	//   refreshInterval = clamp(liveNodes * 10, min, max)
	//
	// Single-node clusters skip refresh entirely (no value since we cannot route away).
	defaultClusterHealthRefreshMin = 5 * time.Second // minimum refresh interval (small clusters)
	defaultClusterHealthRefreshMax = 5 * time.Minute // maximum refresh interval (large clusters)

	// Standby pool defaults.
	//
	// When activeListCap > 0, the pool maintains a standby list of idle connections
	// that are rotated into the ready list periodically. This caps the number of
	// active connections per client, preventing fan-out overload in large clusters.
	defaultStandbyPromotionChecks int64 = 3 // consecutive health checks before standby->ready
	defaultStandbyRotationCount   int   = 1 // standby rotations per discovery cycle
)

// Errors
var (
	ErrNoConnections = errors.New("no connections available")
)

// NodeFilter defines a function type for filtering connections based on their properties.
type NodeFilter func(*Connection) bool

// ConnectionPool defines the interface for the connection pool.
type ConnectionPool interface {
	Next() (*Connection, error)  // Next returns the next available connection.
	OnSuccess(*Connection)       // OnSuccess reports that the connection was successful.
	OnFailure(*Connection) error // OnFailure reports that the connection failed.
	URLs() []*url.URL            // URLs returns the list of URLs of available connections.
}

// RequestRoutingConnectionPool extends ConnectionPool to support request-based connection routing.
type RequestRoutingConnectionPool interface {
	ConnectionPool
	NextForRequest(*http.Request) (*Connection, error) // NextForRequest returns connection optimized for the request.
}

// rwLocker defines the interface for connection pools that support read-write locking.
// This allows for more efficient concurrent access when only read operations are needed.
type rwLocker interface {
	sync.Locker // Embeds Lock() and Unlock() methods
	RLock()
	RUnlock()
}

// Connection represents a connection to a node.
type Connection struct {
	URL        *url.URL
	ID         string
	Name       string
	Roles      roleSet
	Attributes map[string]any
	Version    string // Server version discovered during health check

	// weight is the number of entries this connection occupies in the ready list
	// for weighted round-robin selection. Default 1. In heterogeneous clusters,
	// nodes with more cores get proportionally higher weights (GCD-normalized).
	// Atomic: set during discovery, read during selection and observer events.
	weight atomic.Int32

	// allocatedProcessors is the node's core count discovered from /_nodes/http,os.
	// 0 means unknown (not yet discovered or unavailable).
	allocatedProcessors int

	failures           atomic.Int64
	clusterHealthState atomic.Int64 // Bitfield: clusterHealthProbed | clusterHealthAvailable
	state              atomic.Int64 // Packed connState: connLifecycle (12b) + 2*warmupManager (26b each)

	// drainingQuiescingRemaining counts the number of successful health checks remaining
	// before this connection can be resurrected. Set to defaultDrainingQuiescingChecks when
	// an HTTP/2 stream reset is observed (RST_STREAM, e.g., REFUSED_STREAM). Each successful
	// health check decrements by 1 via decrementDrainingQuiescing(). While > 0, OnSuccess
	// will not resurrect the connection -- this gives the server time to fully quiesce rather
	// than allowing a single lucky health check to bring the connection back prematurely.
	// The resurrection loop continues at its normal interval (with jitter to avoid thundering
	// herd across clients), so the total quiesce window is approximately
	// defaultDrainingQuiescingChecks * resurrectTimeout.
	drainingQuiescingRemaining atomic.Int64

	mu struct {
		sync.RWMutex
		deadSince              time.Time
		checkStartedAt         time.Time
		clusterHealth          *ClusterHealthLocal // Populated when clusterHealthAvailable is set
		clusterHealthCheckedAt time.Time           // When cluster health was last probed (for retry timing)
		overloadedAt           time.Time           // When overloaded state was last set (lcOverloaded metadata bit)
		lastBreakerTripped     map[string]int64    // Previous tripped counts for delta detection
	}

	// proactiveCheck guards proactive health checks triggered by server-initiated connection
	// closure (Response.Close, set when the server sends Connection: close or for HTTP/1.0
	// without keep-alive). Uses a double-check RWMutex pattern to rate-limit checks for
	// servers that routinely close connections (e.g., behind a connection-closing load balancer
	// or configured with a per-connection request limit).
	//
	// Fast path (common case): TryRLock reads lastAt -- if recent, bail immediately.
	// If TryRLock fails, a writer is active (health check being scheduled), so bail.
	// Slow path: TryLock for write, recheck lastAt, update timestamp, fire health check.
	// If TryLock fails, another goroutine won the race, so bail.
	proactiveCheck struct {
		mu struct {
			sync.RWMutex
			lastAt time.Time
		}
	}
}

// effectiveWeight returns the connection's weight for round-robin selection.
// Returns 1 if weight is zero (default for connections created without explicit weight).
func (c *Connection) effectiveWeight() int {
	w := int(c.weight.Load())
	if w <= 0 {
		return 1
	}
	return w
}

// decrementDrainingQuiescing atomically decrements the quiescing counter by 1 (if positive).
// Returns the remaining count after decrement (0 means quiescing is complete).
// Uses CompareAndSwap to avoid going negative under concurrent decrements.
func (c *Connection) decrementDrainingQuiescing() int64 {
	for {
		current := c.drainingQuiescingRemaining.Load()
		if current <= 0 {
			return 0
		}
		if c.drainingQuiescingRemaining.CompareAndSwap(current, current-1) {
			return current - 1
		}
	}
}

type singleServerPool struct {
	connection *Connection

	metrics *metrics
}

type multiServerPool struct {
	name string // Pool identity for metrics/debug (e.g. "roundrobin", "role:data")

	mu struct {
		sync.RWMutex
		ready       []*Connection // Partitioned: ready[:activeCount] are active (round-robin), ready[activeCount:] are standby
		dead        []*Connection // List of dead connections
		activeCount int           // Number of active connections; elements past this index are standby
	}

	nextReady atomic.Int64 // Round-robin counter for ready connections

	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutMax          time.Duration
	resurrectTimeoutFactorCutoff int
	minimumResurrectTimeout      time.Duration
	jitterScale                  float64
	serverMaxNewConnsPerSec      float64 // target max new health check conns a server accepts/sec from all clients
	clientsPerServer             float64 // estimated client instances per server

	// Standby pool configuration.
	// When activeListCap > 0, discovery overflow and resurrected connections go to standby
	// instead of standby when the ready list's active partition is at capacity.
	activeListCap          int   // 0 = disabled (all connections go to active)
	standbyPromotionChecks int64 // consecutive health checks before standby->ready

	// Dynamic warmup parameters, scaled by recalculateWarmupParams().
	// Small pools get lighter warmup (fewer rounds, fewer skips) so connections
	// ramp up quickly. Large pools get heavier warmup to avoid traffic spikes.
	warmupRounds    int // 0 = use defaultWarmupRounds
	warmupSkipCount int // 0 = use defaultWarmupSkipCount

	// activeListCapConfig preserves the user's original intent:
	//   nil = auto-scale activeListCap with cluster size during discovery
	//   non-nil = user-specified value (activeListCap is fixed)
	activeListCapConfig *int

	// Health check function - returns HTTP response on success, error on failure
	healthCheck HealthCheckFunc

	// Per-pool request counters (atomic, lock-free)
	poolRequests      atomic.Int64 // Connections returned by Next()
	poolSuccesses     atomic.Int64 // Resurrections via OnSuccess()
	poolFailures      atomic.Int64 // Demotions via OnFailure()
	poolWarmupSkips   atomic.Int64 // Requests skipped during warmup
	poolWarmupAccepts atomic.Int64 // Requests accepted during warmup

	metrics  *metrics
	observer atomic.Pointer[ConnectionObserver]
}

// Compile-time checks to ensure interface compliance
var (
	_ ConnectionPool = (*singleServerPool)(nil)

	_ ConnectionPool = (*multiServerPool)(nil)
	_ rwLocker       = (*multiServerPool)(nil)
)

// snapshot returns a point-in-time PoolSnapshot of this pool's partitions and counters.
func (cp *multiServerPool) snapshot() PoolSnapshot {
	cp.mu.RLock()
	activeCount := cp.mu.activeCount
	standbyCount := len(cp.mu.ready) - cp.mu.activeCount
	deadCount := len(cp.mu.dead)

	// Count warming connections in the active partition
	warmingCount := 0
	for i := range activeCount {
		if cp.mu.ready[i].loadConnState().isWarmingUp() {
			warmingCount++
		}
	}

	// Count health-checking connections across ready and dead lists
	healthCheckingCount := 0
	for _, c := range cp.mu.ready {
		if c.loadConnState().lifecycle().has(lcHealthChecking) {
			healthCheckingCount++
		}
	}
	for _, c := range cp.mu.dead {
		if c.loadConnState().lifecycle().has(lcHealthChecking) {
			healthCheckingCount++
		}
	}
	cp.mu.RUnlock()

	return PoolSnapshot{
		Name:                cp.name,
		ActiveCount:         activeCount,
		StandbyCount:        standbyCount,
		DeadCount:           deadCount,
		ActiveListCap:       cp.activeListCap,
		WarmingCount:        warmingCount,
		HealthCheckingCount: healthCheckingCount,
		Requests:            cp.poolRequests.Load(),
		Successes:           cp.poolSuccesses.Load(),
		Failures:            cp.poolFailures.Load(),
		WarmupSkips:         cp.poolWarmupSkips.Load(),
		WarmupAccepts:       cp.poolWarmupAccepts.Load(),
	}
}

// recalculateWarmupParams recalculates activeListCap (when auto-scaling) and sets
// warmupRounds/warmupSkipCount based on effective pool size.
//
// poolSize is the projected total number of connections in the pool (ready + dead)
// after the current DiscoveryUpdate completes. This is passed by the caller so
// that startWarmup calls during the add phase already use the correctly-scaled
// parameters.
//
// When activeListCapConfig is nil (auto-scale), activeListCap is updated to poolSize
// so the active/standby partition adapts to cluster resizes.
//
// The effective active partition size is min(activeListCap, poolSize) when the cap
// is set, or poolSize when cap is disabled. This adapts to cluster resizes:
// a cap of 10 on a 3-node cluster yields n=3, not n=10.
//
// Warmup formula:
//
//	n = min(activeListCap, poolSize) when activeListCap > 0
//	n = poolSize                   when activeListCap <= 0
//	rounds = clamp(n, minWarmupRounds, maxWarmupRounds)
//	skipCount = rounds * warmupSkipMultiple
func (cp *multiServerPool) recalculateWarmupParams(poolSize int) {
	// Auto-scale activeListCap when the user didn't specify an explicit value.
	if cp.activeListCapConfig == nil && poolSize > 0 {
		cp.activeListCap = poolSize
	}

	n := poolSize
	if cp.activeListCap > 0 && cp.activeListCap < n {
		n = cp.activeListCap
	}
	if n <= 0 {
		n = minWarmupRounds
	}

	rounds := max(min(n, maxWarmupRounds), minWarmupRounds)
	cp.warmupRounds = rounds
	cp.warmupSkipCount = rounds * warmupSkipMultiple
}

// getWarmupParams returns the effective warmup parameters for this pool.
// Returns pool-specific values if set, otherwise falls back to defaults.
func (cp *multiServerPool) getWarmupParams() (int, int) {
	rounds := cp.warmupRounds
	if rounds <= 0 {
		rounds = defaultWarmupRounds
	}
	skipCount := cp.warmupSkipCount
	if skipCount <= 0 {
		skipCount = defaultWarmupSkipCount
	}
	return rounds, skipCount
}

// NewConnectionPool creates and returns a default connection pool.
func NewConnectionPool(conns []*Connection, selector Selector) ConnectionPool {
	if len(conns) == 1 {
		return &singleServerPool{connection: conns[0]}
	}

	if selector == nil {
		selector = &roundRobinSelector{}
		selector.(*roundRobinSelector).curr.Store(-1)
	}

	pool := &multiServerPool{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutMax:          defaultResurrectTimeoutMax,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
		jitterScale:                  defaultJitterScale,
		serverMaxNewConnsPerSec:      float64(defaultServerCoreCount) * serverMaxNewConnsPerSecMultiplier,
		clientsPerServer:             float64(defaultServerCoreCount),
	}
	pool.mu.ready = conns
	pool.mu.activeCount = len(conns)
	pool.mu.dead = []*Connection{}

	return pool
}

// Next returns the connection from pool.
func (cp *singleServerPool) Next() (*Connection, error) {
	return cp.connection, nil
}

// OnSuccess is a no-op for single connection pool.
func (cp *singleServerPool) OnSuccess(*Connection) {}

// OnFailure is a no-op for single connection pool.
func (cp *singleServerPool) OnFailure(*Connection) error { return nil }

// URLs returns the list of URLs of available connections.
func (cp *singleServerPool) URLs() []*url.URL { return []*url.URL{cp.connection.URL} }

func (cp *singleServerPool) connections() []*Connection { return []*Connection{cp.connection} }

// OnSuccess marks the connection as successful.
func (cp *multiServerPool) OnSuccess(c *Connection) {
	// Establish consistent lock ordering: Pool -> Connection
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Only resurrect if the connection is currently dead
	if c.mu.deadSince.IsZero() {
		return
	}

	// Check if connection is draining (e.g., HTTP/2 GOAWAY received)
	if cp.shouldSkipDraining(c) {
		return
	}

	// Check if connection is overload-demoted
	if cp.shouldSkipOverloaded(c) {
		return
	}

	if debugLogger != nil {
		debugLogger.Logf("[%s] OnSuccess: %s transitioning from dead to ready\n", cp.name, c.URL)
	}
	c.markAsHealthyWithLock()
	cp.resurrectWithLock(c)
	cp.poolSuccesses.Add(1)

	if obs := observerFromAtomic(&cp.observer); obs != nil {
		obs.OnPromote(newConnectionEvent(cp.name, c, cp.mu.activeCount, len(cp.mu.dead)))
	}
}

// shouldSkipDraining returns true if connection is draining and should not be resurrected.
func (cp *multiServerPool) shouldSkipDraining(c *Connection) bool {
	if c.drainingQuiescingRemaining.Load() > 0 {
		if debugLogger != nil {
			debugLogger.Logf("[%s] OnSuccess: %s is draining (quiescing remaining=%d), skipping resurrection\n",
				cp.name, c.URL, c.drainingQuiescingRemaining.Load())
		}
		return true
	}
	return false
}

// shouldSkipOverloaded returns true if connection is overload-demoted and should not be resurrected.
func (cp *multiServerPool) shouldSkipOverloaded(c *Connection) bool {
	if c.loadConnState().lifecycle().has(lcOverloaded) {
		if debugLogger != nil {
			debugLogger.Logf("[%s] OnSuccess: %s is overload-demoted, skipping resurrection (stats poller manages lifecycle)\n", cp.name, c.URL)
		}
		return true
	}
	return false
}

// OnFailure marks the connection as failed.
func (cp *multiServerPool) OnFailure(c *Connection) error {
	cp.mu.Lock()
	holdingCPLock := true
	defer func() {
		if holdingCPLock {
			cp.mu.Unlock()
		}
	}()

	// Transition to dead -- handles Active, Standby, or Overloaded states.
	// A real failure supersedes any previous state.
	c.mu.Lock()
	if c.loadConnState().lifecycle().has(lcUnknown) {
		c.mu.Unlock()
		return nil
	}

	if !c.casLifecycle(c.loadConnState(), 0, lcDead|lcNeedsWarmup|lcNeedsHardware, lcReady|lcActive|lcStandby|lcOverloaded) {
		c.mu.Unlock()
		return nil
	}
	c.mu.overloadedAt = time.Time{}
	c.markAsDeadWithLock()
	c.mu.Unlock()

	cp.removeFromReadyWithLock(c)
	cp.appendToDeadWithLock(c)
	cp.poolFailures.Add(1)

	if cp.metrics != nil {
		cp.metrics.connectionsDemoted.Add(1)
	}

	// Sort by failure count for resurrection prioritization.
	// CONCURRENCY TRADEOFF: Atomic loads are used without additional locking during sort,
	// allowing failure counts to change mid-sort and resulting in slightly inconsistent
	// ordering. This design prioritizes common-case latency over absolute correctness
	// during failure scenarios. While failure counts could be snapshotted before sorting,
	// the list ordering is not guaranteed to remain perfectly sorted by failure count
	// between operations, making "mostly correct" sorting with atomics acceptable.
	// Any temporary misordering self-corrects on subsequent failure events.
	sort.Slice(cp.mu.dead, func(i, j int) bool {
		c1 := cp.mu.dead[i]
		c2 := cp.mu.dead[j]

		failures1 := c1.failures.Load()
		failures2 := c2.failures.Load()

		return failures1 > failures2
	})

	// Snapshot standby availability while we hold the lock -- used after unlock
	// to decide whether to spawn async standby promotion (1:1 failure replacement).
	hasStandby := len(cp.mu.ready) > cp.mu.activeCount

	// Build observer event while pool counts are still valid under lock
	var demoteEvent ConnectionEvent
	obs := observerFromAtomic(&cp.observer)
	if obs != nil {
		demoteEvent = newConnectionEvent(cp.name, c, cp.mu.activeCount, len(cp.mu.dead))
	}

	// MUST release lock before scheduleResurrect to avoid deadlock:
	// scheduleResurrect needs cp.mu.RLock(), which blocks if we hold cp.mu.Lock()
	holdingCPLock = false
	cp.mu.Unlock()

	if obs != nil {
		obs.OnDemote(demoteEvent)
	}

	// Schedule resurrection after connection has been moved to dead list
	// Context is not passed as scheduleResurrect uses time.AfterFunc which cannot be cancelled
	cp.scheduleResurrect(context.TODO(), c)

	// If standby connections are available, asynchronously promote one to fill the gap
	// left by the failed connection (1:1 replacement).
	if hasStandby {
		go cp.asyncPromoteStandby(context.TODO())
	}

	return nil
}

// URLs returns the list of URLs of available connections.
func (cp *multiServerPool) URLs() []*url.URL {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	urls := make([]*url.URL, 0, len(cp.mu.ready)+len(cp.mu.dead))
	for _, c := range cp.mu.ready {
		urls = append(urls, c.URL)
	}
	for _, c := range cp.mu.dead {
		urls = append(urls, c.URL)
	}

	return urls
}

func (cp *multiServerPool) connections() []*Connection {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	conns := make([]*Connection, 0, len(cp.mu.ready)+len(cp.mu.dead))
	conns = append(conns, cp.mu.ready...) // includes both active and standby partitions
	conns = append(conns, cp.mu.dead...)

	return conns
}

// connectionsByState returns snapshots of the ready and dead lists.
// The ready list includes both active (ready[:activeCount]) and standby (ready[activeCount:])
// connections. Callers can use Connection.loadConnState() to distinguish them.
func (cp *multiServerPool) connectionsByState() ([]*Connection, []*Connection) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	ready := make([]*Connection, len(cp.mu.ready))
	copy(ready, cp.mu.ready)

	dead := make([]*Connection, len(cp.mu.dead))
	copy(dead, cp.mu.dead)

	return ready, dead
}

// RLock acquires a read lock on the connection pool.
// Implements rwLocker interface for efficient concurrent read access.
func (cp *multiServerPool) RLock() {
	cp.mu.RLock()
}

// RUnlock releases the read lock on the connection pool.
// Implements rwLocker interface for efficient concurrent read access.
func (cp *multiServerPool) RUnlock() {
	cp.mu.RUnlock()
}

// Lock acquires a write lock on the connection pool.
// Implements rwLocker interface (via embedded sync.Locker).
func (cp *multiServerPool) Lock() {
	cp.mu.Lock()
}

// Unlock releases the write lock on the connection pool.
// Implements rwLocker interface (via embedded sync.Locker).
func (cp *multiServerPool) Unlock() {
	cp.mu.Unlock()
}

// resurrectWithLock unconditionally moves a connection from dead to the ready list.
// When the active partition is at capacity, the connection lands in the standby
// portion (past activeCount) automatically -- no separate list needed.
//
// CALLER RESPONSIBILITIES:
//   - Caller must verify connection health before calling this method
//   - Caller must hold both pool lock and connection lock
//   - Connection should exist in the dead list
func (cp *multiServerPool) resurrectWithLock(c *Connection) {
	if debugLogger != nil {
		debugLogger.Logf("[%s] Resurrecting %q\n", cp.name, c.URL)
	}

	// Clear overloaded state -- node just came back from dead, stats poller will re-evaluate.
	c.mu.overloadedAt = time.Time{}

	c.markAsReadyWithLock()

	// Remove from dead list
	cp.removeFromDeadWithLock(c)
	if cp.metrics != nil {
		cp.metrics.connectionsPromoted.Add(1)
	}

	// Add to ready list. If below cap (or cap disabled), promote to active with warmup.
	// Otherwise the connection lands in the standby portion (warmup deferred to promotion).
	if cp.activeListCap <= 0 || cp.mu.activeCount < cp.activeListCap {
		// Transition state: dead -> active with warmup (lcNeedsWarmup preserved if set)
		c.casLifecycle(c.loadConnState(), 0, lcActive, lcUnknown|lcStandby)
		rounds, skip := cp.getWarmupParams()
		c.startWarmup(rounds, skip)
		cp.appendToReadyActiveWithLock(c)
		cp.shuffleActiveWithLock()
	} else {
		// Transition state: dead -> standby (warmup deferred to promotion, lcNeedsWarmup preserved)
		c.casLifecycle(c.loadConnState(), 0, lcStandby, lcUnknown|lcActive)
		cp.appendToReadyStandbyWithLock(c)
		if debugLogger != nil {
			debugLogger.Logf("[%s] Resurrected %q to standby (active at cap=%d, standby=%d)\n",
				cp.name, c.URL, cp.activeListCap, len(cp.mu.ready)-cp.mu.activeCount)
		}
	}
}

// removeFromReadyWithLock removes ALL entries of a connection from the ready slice.
// A connection may have multiple entries due to weighted round-robin (c.weight > 1).
// Handles the active/standby partition correctly:
//   - Active entries (idx < activeCount): decrement activeCount per removal
//   - Standby entries (idx >= activeCount): removed from standby partition
//
// If the connection is not in the ready list at all, returns without modification.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) removeFromReadyWithLock(c *Connection) {
	// Remove all occurrences by filtering in-place.
	// Track how many active entries were removed to adjust activeCount.
	activeRemoved := 0
	writeIdx := 0
	for readIdx := 0; readIdx < len(cp.mu.ready); readIdx++ {
		if cp.mu.ready[readIdx] == c {
			if readIdx < cp.mu.activeCount {
				activeRemoved++
			}
			continue // skip this entry (remove it)
		}
		cp.mu.ready[writeIdx] = cp.mu.ready[readIdx]
		writeIdx++
	}
	if writeIdx == len(cp.mu.ready) {
		return // nothing was removed
	}
	// Clear trailing slots for GC
	for i := writeIdx; i < len(cp.mu.ready); i++ {
		cp.mu.ready[i] = nil
	}
	cp.mu.ready = cp.mu.ready[:writeIdx]
	cp.mu.activeCount -= activeRemoved
}

// removeFromDeadWithLock removes a connection from the dead slice.
// Swap-with-last for O(1) removal once found.
//
// If the connection is not in the dead list at all, returns without modification.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) removeFromDeadWithLock(c *Connection) {
	idx := -1
	// Search backward -- recently appended items are at the tail
	for i := len(cp.mu.dead) - 1; i >= 0; i-- {
		if cp.mu.dead[i] == c {
			idx = i
			break
		}
	}
	if idx < 0 {
		return // not in this dead list
	}

	last := len(cp.mu.dead) - 1

	if idx != last {
		cp.mu.dead[idx] = cp.mu.dead[last]
	}

	cp.mu.dead[last] = nil
	cp.mu.dead = cp.mu.dead[:last]
}

// appendToReadyActiveWithLock appends a connection to the ready slice in the active partition.
// Inserts c.effectiveWeight() copies for weighted round-robin selection.
// Each copy is placed at the activeCount boundary and activeCount is incremented.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) appendToReadyActiveWithLock(c *Connection) {
	w := c.effectiveWeight()
	for range w {
		tailIdx := len(cp.mu.ready)
		cp.mu.ready = append(cp.mu.ready, c)
		// Swap into active partition at the boundary
		cp.mu.ready[tailIdx], cp.mu.ready[cp.mu.activeCount] = cp.mu.ready[cp.mu.activeCount], cp.mu.ready[tailIdx]
		cp.mu.activeCount++
	}
}

// appendToReadyStandbyWithLock appends a connection to the standby portion of the ready slice.
// Inserts c.effectiveWeight() copies for weighted round-robin selection.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) appendToReadyStandbyWithLock(c *Connection) {
	w := c.effectiveWeight()
	for range w {
		cp.mu.ready = append(cp.mu.ready, c)
	}
}

// appendToDeadWithLock appends a connection to the dead slice.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) appendToDeadWithLock(c *Connection) {
	cp.mu.dead = append(cp.mu.dead, c)
}

// shuffleActiveWithLock randomizes the order of active connections to prevent hot-spotting.
// Only shuffles ready[:activeCount] to preserve the active/standby partition.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
func (cp *multiServerPool) shuffleActiveWithLock() {
	if cp.mu.activeCount <= 1 {
		return
	}
	rand.Shuffle(cp.mu.activeCount, func(i, j int) {
		cp.mu.ready[i], cp.mu.ready[j] = cp.mu.ready[j], cp.mu.ready[i]
	})
}

// markAsDeadWithLock marks the connection as dead (caller must hold lock).
func (c *Connection) markAsDeadWithLock() {
	if c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Now().UTC()
	}
	c.failures.Add(1)
}

// markAsReadyWithLock marks the connection as alive (caller must hold lock).
func (c *Connection) markAsReadyWithLock() {
	c.mu.deadSince = time.Time{}
}

// markAsHealthyWithLock marks the connection as healthy (caller must hold lock).
func (c *Connection) markAsHealthyWithLock() {
	c.mu.deadSince = time.Time{}
	c.failures.Store(0)
}

// String returns a readable connection representation.
func (c *Connection) String() string {
	c.mu.RLock()
	deadAt := c.mu.deadSince
	c.mu.RUnlock()

	if deadAt.IsZero() {
		return fmt.Sprintf("<%s> dead=false failures=%d", c.URL, c.failures.Load())
	}

	return fmt.Sprintf("<%s> dead=true age=%s failures=%d", c.URL, time.Since(deadAt), c.failures.Load())
}
