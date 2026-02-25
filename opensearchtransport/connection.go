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
	"fmt"
	"net/http"
	"net/url"
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
	// Large clusters cap at 30s to limit N*M polling amplification.
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
	URLString  string // Cached URL.String() -- set once at construction, never changes
	ID         string
	Name       string
	Roles      roleSet
	Attributes map[string]any
	// version stores the server version discovered during health check.
	// Atomic: written by performHealthCheck, read by newConnectionEvent.
	version atomic.Value // string

	// weight is the number of entries this connection occupies in the ready list
	// for weighted round-robin selection. Default 1. In heterogeneous clusters,
	// nodes with more cores get proportionally higher weights (GCD-normalized).
	// Atomic: set during discovery, read during selection and observer events.
	weight atomic.Int32

	// allocatedProcessors is the node's core count discovered from /_nodes/_local/http,os.
	// 0 means unknown (not yet discovered or unavailable).
	// Atomic: written by hardwareInfoHealthCheck, read by computeWeights/recalculateCapacityModel.
	allocatedProcessors atomic.Int32

	// rttRing tracks health check RTT measurements for this connection.
	// All slots initialize to rttBucketUnknown; the median naturally drops
	// to a measured tier once enough health checks have run. Used by
	// rendezvousTopK for RTT-aware slot selection.
	rttRing *rttRing

	// affinityCounter tracks estimated CPU time deposited on this node by
	// this client. Updated after each successful request with the estimated
	// server-side processing time normalized by the node's core count.
	// Includes coordinator overhead when the node proxies to shard nodes.
	// Uses time-weighted EWMA: decay is tied to wall clock, not request rate.
	affinityCounter timeWeightedCounter

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

// loadVersion atomically loads the server version string.
func (c *Connection) loadVersion() string {
	v, _ := c.version.Load().(string)
	return v
}

// storeVersion atomically stores the server version string.
func (c *Connection) storeVersion(v string) {
	c.version.Store(v)
}

// loadAllocatedProcessors atomically loads the node's core count.
func (c *Connection) loadAllocatedProcessors() int {
	return int(c.allocatedProcessors.Load())
}

// storeAllocatedProcessors atomically stores the node's core count.
func (c *Connection) storeAllocatedProcessors(v int) {
	c.allocatedProcessors.Store(int32(v)) //nolint:gosec // core counts always fit in int32
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

// RTTMedian returns the median health-check round-trip time for this connection.
// Returns -1 if no RTT data is available (the connection has not completed
// enough health checks for the ring buffer median to drop below the unknown
// sentinel).
func (c *Connection) RTTMedian() time.Duration {
	if c.rttRing == nil {
		return -1
	}
	bucket := c.rttRing.medianBucket()
	if bucket.IsUnknown() {
		return -1
	}
	return bucket.Micros().Duration()
}

// RTTBucket returns the raw median RTT bucket for this connection.
// Buckets use power-of-two quantization: bucket = floor(log2(microseconds)),
// clamped to a floor of 8 (256us). Returns -1 if no RTT data is available.
//
// This is the value used in affinity score calculations:
//
//	score = rttBucket * decayCounter * shardCostMultiplier
func (c *Connection) RTTBucket() int64 {
	if c.rttRing == nil {
		return -1
	}
	bucket := c.rttRing.medianBucket()
	if bucket.IsUnknown() {
		return -1
	}
	return bucket.Int64()
}

// AffinityLoad returns the current CPU-load accumulator value for this
// connection. The value reflects the exponentially decaying sum of estimated
// server-side processing time per processor. Higher values indicate the node
// is handling more work from this client.
//
// Note: the estimate includes time the node spends coordinating sub-requests
// to other nodes, not only local CPU time. This is directionally correct for
// load-shedding (a busy coordinator is still a busy node from this client's
// perspective) and most accurate when affinity routing is effective (requests
// hit shard-hosting nodes, minimizing coordinator overhead).
func (c *Connection) AffinityLoad() float64 {
	return c.affinityCounter.load()
}

// recordCPUTime estimates the server-side CPU time consumed by a completed
// request and adds it to the connection's affinity accumulator.
//
// The estimate is: (requestDuration - healthCheckRTT) / allocatedProcessors.
// healthCheckRTT approximates wire time (GET / is near-zero CPU), so the
// difference isolates on-CPU processing time. Dividing by processor count
// normalizes for node capacity.
//
// The cost is then divided by rttBucket to cancel the rttBucket multiplier
// in the scoring formula. At steady state with rate r and per-request
// server time s:
//
//	counter = r * s / (proc * bucket * lambda)
//	score   = bucket * counter * wp
//	        = r * s / (proc * lambda) * wp
//
// The bucket terms cancel, so setting score_i = score_j gives
// r_i * wp_i = r_j * wp_j, i.e., rate is proportional to 1/wp
// regardless of RTT tier. This replaces the previous maxBucket/thisBucket
// inflation which required tracking the fan-out's maximum bucket.
//
// When the node acts as a coordinator, the measured duration includes time
// spent waiting for sub-requests to other data nodes. The estimate therefore
// reflects total interaction cost with this node, not strictly local CPU.
// This is acceptable: a node doing more coordinating work is busier from
// this client's perspective, and affinity routing minimizes coordinator
// overhead by preferring shard-hosting nodes.
//
// All arithmetic is performed in integer nanoseconds via [durationNanos],
// then converted to microseconds at the boundary to keep the accumulator
// in the same order of magnitude as RTT buckets (256 us granules).
//
// The counter uses time-weighted EWMA ([timeWeightedCounter]): decay is
// tied to wall clock time, not request rate.
func (c *Connection) recordCPUTime(requestDuration time.Duration) {
	baseline := c.RTTMedian()
	if baseline <= 0 {
		return // No health-check baseline yet
	}
	serverTime := durationFromStd(requestDuration - baseline)
	if !serverTime.IsPositive() {
		return // Request was faster than baseline (cached or timing jitter)
	}
	processors := c.loadAllocatedProcessors()
	if processors <= 0 {
		processors = defaultServerCoreCount
	}
	// Integer division in nanoseconds, then convert to microseconds.
	// This avoids any float64 intermediate for the duration conversion.
	cpuNanos := durationNanos(int64(serverTime) / int64(processors))

	// Divide by rttBucket to cancel the bucket multiplier in the scoring
	// formula (score = rttBucket * counter * wp). This makes the equilibrium
	// distribution depend only on wp, not on RTT tier placement.
	cost := float64(cpuNanos.Micros())
	thisBucket := float64(c.rttRing.medianBucket())
	if thisBucket > 0 {
		cost /= thisBucket
	}

	c.affinityCounter.add(cost)
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
