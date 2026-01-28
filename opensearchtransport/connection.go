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
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultResurrectTimeoutInitial      = 60 * time.Second
	defaultResurrectTimeoutFactorCutoff = 5
	defaultMinimumResurrectTimeout      = 10 * time.Millisecond
	defaultJitterScale                  = 0.1
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

	failures atomic.Int64

	mu struct {
		sync.RWMutex
		deadSince      time.Time
		checkStartedAt time.Time
	}
}

// checkHealth performs a health check on this connection with concurrency protection.
// Updates isDead and checkStartedAt state based on health check results.
// Returns error if health check fails or if already checking.
func (c *Connection) checkHealth(ctx context.Context, healthCheck func(context.Context, *url.URL) (*http.Response, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Skip if already checking to prevent concurrent health checks
	if !c.mu.checkStartedAt.IsZero() {
		duration := time.Since(c.mu.checkStartedAt)
		return fmt.Errorf("health check already in progress for %v", duration)
	}

	// Store original deadSince to detect race conditions
	originalDeadSince := c.mu.deadSince

	// Set checking timestamp
	c.mu.checkStartedAt = time.Now()
	defer func() {
		c.mu.checkStartedAt = time.Time{}
	}()

	// Perform actual health check
	c.mu.Unlock() // Release lock during network call
	resp, err := healthCheck(ctx, c.URL)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	c.mu.Lock() // Reacquire for state update

	// Check if connection was marked dead more recently than when we started
	if c.mu.deadSince.After(originalDeadSince) {
		// Connection was marked dead while we were checking, discard result
		return nil
	}

	// Update connection state based on health check result
	if err != nil {
		// Health check failed
		if c.mu.deadSince.IsZero() {
			c.mu.deadSince = time.Now()
		}
		return err
	}

	// Health check passed
	if !c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Time{} // Reset deadSince
	}

	return nil
}

// checkDead syncs dead/live lists based on Connection.mu.isDead state and performs health checks.
func (cp *statusConnectionPool) checkDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if healthCheck == nil {
		return errors.New("healthCheck function cannot be nil")
	}

	// Get snapshot of dead connections without holding lock during health checks
	cp.mu.RLock()
	deadConns := make([]*Connection, len(cp.mu.dead))
	copy(deadConns, cp.mu.dead)
	cp.mu.RUnlock()

	// Perform health checks without holding the pool lock
	for _, conn := range deadConns {
		err := conn.checkHealth(ctx, healthCheck)
		if err != nil {
			// Health check failed or already in progress, skip
			continue
		}

		// Check if connection is now alive and resurrect if needed
		conn.mu.RLock()
		isDead := !conn.mu.deadSince.IsZero()
		conn.mu.RUnlock()

		if !isDead {
			// Connection is alive, resurrect it
			cp.mu.Lock()
			conn.mu.Lock()
			// Double-check state after acquiring locks to avoid race
			if conn.mu.deadSince.IsZero() {
				cp.resurrectWithLock(conn)
			}
			conn.mu.Unlock()
			cp.mu.Unlock()
		}
	}

	return nil
}

type singleConnectionPool struct {
	connection *Connection

	metrics *metrics
}

type statusConnectionPool struct {
	mu struct {
		sync.RWMutex
		live []*Connection // List of live connections
		dead []*Connection // List of dead connections
	}

	nextLive atomic.Int64 // Round-robin counter for live connections

	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutFactorCutoff int
	minimumResurrectTimeout      time.Duration
	jitterScale                  float64

	// Health check function - returns HTTP response on success, error on failure
	healthCheck func(context.Context, *url.URL) (*http.Response, error)

	metrics *metrics
}

// Compile-time checks to ensure interface compliance
var (
	_ ConnectionPool = (*singleConnectionPool)(nil)

	_ ConnectionPool = (*statusConnectionPool)(nil)
	_ rwLocker       = (*statusConnectionPool)(nil)
)

// NewConnectionPool creates and returns a default connection pool.
func NewConnectionPool(conns []*Connection, selector Selector) ConnectionPool {
	if len(conns) == 1 {
		return &singleConnectionPool{connection: conns[0]}
	}

	if selector == nil {
		selector = &roundRobinSelector{}
		selector.(*roundRobinSelector).curr.Store(-1)
	}

	pool := &statusConnectionPool{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
		jitterScale:                  defaultJitterScale,
	}
	pool.mu.live = conns
	pool.mu.dead = []*Connection{}

	return pool
}

// Next returns the connection from pool.
func (cp *singleConnectionPool) Next() (*Connection, error) {
	return cp.connection, nil
}

// OnSuccess is a no-op for single connection pool.
func (cp *singleConnectionPool) OnSuccess(*Connection) {}

// OnFailure is a no-op for single connection pool.
func (cp *singleConnectionPool) OnFailure(*Connection) error { return nil }

// URLs returns the list of URLs of available connections.
func (cp *singleConnectionPool) URLs() []*url.URL { return []*url.URL{cp.connection.URL} }

func (cp *singleConnectionPool) connections() []*Connection { return []*Connection{cp.connection} }

// Next returns a connection from pool, or an error.
func (cp *statusConnectionPool) Next() (*Connection, error) {
	cp.mu.RLock()

	// Return next live connection using round-robin
	switch {
	case len(cp.mu.live) > 0:
		conn := cp.getNextLiveConnWithLock()
		cp.mu.RUnlock()
		return conn, nil
	case len(cp.mu.dead) == 0:
		cp.mu.RUnlock()
		return nil, ErrNoConnections
	}

	// No live connections are available, try using a dead connection.
	cp.mu.RUnlock() // Release read lock
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Double-check after acquiring write lock
	switch {
	case len(cp.mu.live) > 0:
		return cp.getNextLiveConnWithLock(), nil
	case len(cp.mu.dead) == 0:
		return nil, ErrNoConnections
	default:
		// We can now assume: cp.mu.dead > 0
		c := cp.tryZombieWithLock()
		return c, nil
	}
}

// OnSuccess marks the connection as successful.
func (cp *statusConnectionPool) OnSuccess(c *Connection) {
	// Establish consistent lock ordering: Pool -> Connection
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Short-circuit for live connection
	if c.mu.deadSince.IsZero() {
		return
	}

	c.markAsHealthyWithLock()
	cp.resurrectWithLock(c)
}

// OnFailure marks the connection as failed.
func (cp *statusConnectionPool) OnFailure(c *Connection) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()

	if !c.mu.deadSince.IsZero() {
		if debugLogger != nil {
			debugLogger.Logf("Already removed %s\n", c.URL)
		}
		c.mu.Unlock()

		return nil
	}

	if debugLogger != nil {
		debugLogger.Logf("Removing %s...\n", c.URL)
	}

	c.markAsDeadWithLock()
	deadSince := c.mu.deadSince
	c.mu.Unlock()

	// Find connection in live list
	idx := -1
	for i, conn := range cp.mu.live {
		if conn == c {
			idx = i
			break
		}
	}

	if idx < 0 {
		// Invariant violation: connection marked dead but not in live list.
		// This indicates a bug in connection lifecycle management.
		if debugLogger != nil {
			debugLogger.Logf("BUG: Connection %s marked dead but not in live list\n", c.URL)
		}
		return errors.New("connection not in live list")
	}

	// Remove from live list and add to dead list
	cp.mu.live = append(cp.mu.live[:idx], cp.mu.live[idx+1:]...)
	cp.mu.dead = append(cp.mu.dead, c)

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

	// Schedule resurrection after connection has been moved to dead list
	cp.scheduleResurrect(c, deadSince)

	return nil
}

// URLs returns the list of URLs of available connections.
func (cp *statusConnectionPool) URLs() []*url.URL {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	urls := make([]*url.URL, len(cp.mu.live))
	for idx, c := range cp.mu.live {
		urls[idx] = c.URL
	}

	return urls
}

func (cp *statusConnectionPool) connections() []*Connection {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	conns := make([]*Connection, 0, len(cp.mu.live)+len(cp.mu.dead))
	conns = append(conns, cp.mu.live...)
	conns = append(conns, cp.mu.dead...)

	return conns
}

// RLock acquires a read lock on the connection pool.
// Implements rwLocker interface for efficient concurrent read access.
func (cp *statusConnectionPool) RLock() {
	cp.mu.RLock()
}

// RUnlock releases the read lock on the connection pool.
// Implements rwLocker interface for efficient concurrent read access.
func (cp *statusConnectionPool) RUnlock() {
	cp.mu.RUnlock()
}

// Lock acquires a write lock on the connection pool.
// Implements rwLocker interface (via embedded sync.Locker).
func (cp *statusConnectionPool) Lock() {
	cp.mu.Lock()
}

// Unlock releases the write lock on the connection pool.
// Implements rwLocker interface (via embedded sync.Locker).
func (cp *statusConnectionPool) Unlock() {
	cp.mu.Unlock()
}

// performHealthCheck executes the health check for a connection.
// Returns true if health check passes, false if it fails (and schedules retry).
func (cp *statusConnectionPool) performHealthCheck(c *Connection) bool {
	// Use background context for health check operations
	// Health checks are independent operations during resurrection
	ctx := context.Background()

	resp, err := cp.healthCheck(ctx, c.URL)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Logf("Health check failed for %q: %s; will retry later\n", c.URL, err)
		}
		// Schedule retry on health check failure
		cp.scheduleResurrect(c, c.mu.deadSince)
		return false
	}

	if debugLogger != nil {
		// Clean up response body if present
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		debugLogger.Logf("Health check passed for %q\n", c.URL)
	}
	return true
}

// getNextLiveConnWithLock returns the next live connection using round-robin selection.
// This provides fair distribution of requests across all live connections.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool read or write lock
//   - Caller must ensure len(cp.mu.live) > 0 before calling
func (cp *statusConnectionPool) getNextLiveConnWithLock() *Connection {
	next := cp.nextLive.Add(1)
	idx := int(next-1) % len(cp.mu.live)
	return cp.mu.live[idx]
}

// resurrectWithLock unconditionally moves a connection from dead to live list.
// This should only be called after a successful health check or when the connection
// has been verified to be healthy. Used by OnSuccess() and checkDead() to promote
// connections that have proven to be working.
//
// CALLER RESPONSIBILITIES:
//   - Caller must verify connection health before calling this method
//   - Caller must hold both pool lock and connection lock
//   - Connection should exist in the dead list
func (cp *statusConnectionPool) resurrectWithLock(c *Connection) {
	if debugLogger != nil {
		debugLogger.Logf("Attempting to resurrect %q\n", c.URL)
	}

	// Execute health check if configured
	if cp.healthCheck != nil {
		if !cp.performHealthCheck(c) {
			return // Health check failed, resurrection scheduled
		}
	}

	if debugLogger != nil {
		debugLogger.Logf("Resurrecting %q\n", c.URL)
	}

	c.markAsLiveWithLock()
	cp.mu.live = append(cp.mu.live, c)

	// Always remove from dead list to avoid duplicates
	idx := -1
	for i, conn := range cp.mu.dead {
		if conn == c {
			idx = i
			break
		}
	}

	if idx >= 0 {
		cp.mu.dead = append(cp.mu.dead[:idx], cp.mu.dead[idx+1:]...)
	}
}

// tryZombieWithLock returns a dead connection for temporary use without moving it to the live list.
// This allows attempting requests on potentially dead connections when no live connections are available.
// The connection remains on the dead list and will continue to be subject to periodic health checks.
// Used by Next() when no live connections are available, providing a way to short-circuit the periodic
// heartbeat timer by attempting requests on dead connections immediately.
//
// The function rotates through dead connections by popping from the front and pushing to the back,
// ensuring fair distribution of retry attempts across all dead connections.
//
// CONCURRENCY NOTE: This function races with OnFailure() over dead list ordering. OnFailure()
// sorts dead connections by failure count while this function rotates the list for fair distribution.
// The design assumes that during failure scenarios, we iterate through the entire dead list faster
// than new connections fail and trigger list resorting in OnFailure(). This ensures fair rotation
// is maintained most of the time, with occasional resorting to prioritize connections with fewer failures.
//
// CALLER RESPONSIBILITIES:
//   - Caller must hold pool write lock
//   - Caller should call OnSuccess() if the connection proves to work (which will resurrect it)
//   - Caller should call OnFailure() if the connection fails (which is a no-op since it's already dead)
func (cp *statusConnectionPool) tryZombieWithLock() *Connection {
	if len(cp.mu.dead) == 0 {
		return nil
	}

	// Pop from front, push to back (rotate the queue) in one operation
	var c *Connection
	c, cp.mu.dead = cp.mu.dead[0], append(cp.mu.dead[1:], cp.mu.dead[0])

	if debugLogger != nil {
		debugLogger.Logf("Trying zombie connection %s\n", c.URL)
	}

	return c
}

// scheduleResurrect schedules the connection to be resurrected using cluster-aware timing.
// Formula: ((1 - ((total - live) / total)) * total) * jitterScale
// - All dead: immediate resurrection
// - Healthy clusters: longer waits with more jitter
// - Incident scenarios: faster recovery
func (cp *statusConnectionPool) scheduleResurrect(c *Connection, deadSince time.Time) {
	// Calculate basic exponential backoff factor
	failures := c.failures.Load()
	factor := math.Min(float64(failures-1), float64(cp.resurrectTimeoutFactorCutoff))
	baseTimeout := time.Duration(cp.resurrectTimeoutInitial.Seconds() * math.Exp2(factor) * float64(time.Second))

	// Get cluster health metrics
	totalNodes := len(cp.mu.live) + len(cp.mu.dead)
	liveNodes := len(cp.mu.live)

	var finalTimeout time.Duration

	if totalNodes == 0 || liveNodes == 0 {
		// All dead or no nodes: immediate resurrection
		finalTimeout = cp.minimumResurrectTimeout
	} else {
		// Cluster-aware formula: ((1 - ((total - live) / total)) * total) * jitterScale
		deadNodes := totalNodes - liveNodes
		healthRatio := 1.0 - (float64(deadNodes) / float64(totalNodes))
		clusterFactor := healthRatio * float64(totalNodes) * cp.jitterScale

		// Apply base timeout and cluster factor
		clusterTimeout := time.Duration(float64(baseTimeout) * clusterFactor)

		// Add random jitter (0 to clusterTimeout range)
		// #nosec G404 - Non-cryptographic randomness is acceptable for connection timing jitter
		jitter := time.Duration(rand.Float64() * float64(clusterTimeout))
		finalTimeout = jitter

		// Ensure minimum timeout
		if finalTimeout < cp.minimumResurrectTimeout {
			finalTimeout = cp.minimumResurrectTimeout
		}
	}

	if debugLogger != nil {
		debugLogger.Logf(
			"Resurrect %q (failures=%d, factor=%1.1f, live=%d, dead=%d, total=%d, base=%s, final=%s) in %s\n",
			c.URL,
			failures,
			factor,
			liveNodes,
			len(cp.mu.dead),
			totalNodes,
			baseTimeout,
			finalTimeout,
			deadSince.Add(finalTimeout).Sub(time.Now().UTC()).Truncate(time.Millisecond),
		)
	}

	time.AfterFunc(finalTimeout, func() {
		cp.mu.Lock()
		defer cp.mu.Unlock()

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.mu.deadSince.IsZero() {
			if debugLogger != nil {
				debugLogger.Logf("Already resurrected %q\n", c.URL)
			}
			return
		}

		cp.resurrectWithLock(c)
	})
}

// markAsDeadWithLock marks the connection as dead (caller must hold lock).
func (c *Connection) markAsDeadWithLock() {
	if c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Now().UTC()
	}
	c.failures.Add(1)
}

// markAsLiveWithLock marks the connection as alive (caller must hold lock).
func (c *Connection) markAsLiveWithLock() {
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
