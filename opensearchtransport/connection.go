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
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultResurrectTimeoutInitial      = 60 * time.Second
	defaultResurrectTimeoutFactorCutoff = 5
	defaultMinimumResurrectTimeout      = 10 * time.Millisecond
	defaultJitterScale                  = 0.1
	defaultHealthCheckTimeout           = 5 * time.Second
)

// Errors
var (
	ErrNoConnections = errors.New("no connections available")
)

// Selector defines the interface for selecting connections from the pool.
type Selector interface {
	Select([]*Connection) (*Connection, error)
}

// RequestAwareSelector extends the basic Selector interface to support request-aware node selection.
// This allows selectors to make routing decisions based on the request being performed.
type RequestAwareSelector interface {
	Selector // Embed existing interface for backward compatibility
	SelectForRequest([]*Connection, Request) (*Connection, error)
}

// Request represents a request that can be used for routing decisions.
// This interface allows selectors to examine request properties without importing opensearchapi.
type Request interface {
	GetMethod() string
	GetPath() string
	GetHeaders() map[string]string
}

// NodeFilter defines a function type for filtering connections based on their properties.
type NodeFilter func(*Connection) bool

// ConnectionPool defines the interface for the connection pool.
type ConnectionPool interface {
	Next() (*Connection, error)  // Next returns the next available connection.
	OnSuccess(*Connection)       // OnSuccess reports that the connection was successful.
	OnFailure(*Connection) error // OnFailure reports that the connection failed.
	URLs() []*url.URL            // URLs returns the list of URLs of available connections.
}

// RequestAwareConnectionPool extends ConnectionPool to support request-aware connection selection.
type RequestAwareConnectionPool interface {
	ConnectionPool
	NextForRequest(Request) (*Connection, error) // NextForRequest returns connection optimized for the request.
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
	Roles      []string
	Attributes map[string]any

	failures atomic.Int64

	mu struct {
		sync.RWMutex
		isDead    bool
		deadSince time.Time
	}
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

	selector                     Selector
	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutFactorCutoff int
	minimumResurrectTimeout      time.Duration
	jitterScale                  float64

	// Health check function - returns HTTP response on success, error on failure
	healthCheck func(context.Context, *url.URL) (*http.Response, error)

	metrics *metrics
}

type roundRobinSelector struct {
	curr atomic.Int64 // Index of the current connection
}

// NewRoundRobinSelector creates a new round-robin connection selector.
func NewRoundRobinSelector() *roundRobinSelector {
	s := &roundRobinSelector{}
	s.curr.Store(-1)
	return s
}

// Compile-time checks to ensure interface compliance
var (
	_ ConnectionPool             = (*singleConnectionPool)(nil)
	_ RequestAwareConnectionPool = (*singleConnectionPool)(nil)

	_ ConnectionPool             = (*statusConnectionPool)(nil)
	_ RequestAwareConnectionPool = (*statusConnectionPool)(nil)
	_ rwLocker                   = (*statusConnectionPool)(nil)
)

// NewConnectionPool creates and returns a default connection pool.
func NewConnectionPool(conns []*Connection, selector Selector) ConnectionPool {
	if len(conns) == 1 {
		return &singleConnectionPool{connection: conns[0]}
	}

	if selector == nil {
		s := &roundRobinSelector{}
		s.curr.Store(-1)
		selector = s
	}

	pool := &statusConnectionPool{
		selector:                     selector,
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

// NextForRequest returns the connection from pool (request-aware version).
// For single connection pools, this behaves the same as Next().
func (cp *singleConnectionPool) NextForRequest(req Request) (*Connection, error) {
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
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Return next live connection
	if len(cp.mu.live) > 0 {
		return cp.selector.Select(cp.mu.live)
	} else if len(cp.mu.dead) > 0 {
		// No live connection is available, resurrect one of the dead ones.
		c := cp.mu.dead[len(cp.mu.dead)-1]
		cp.mu.dead = cp.mu.dead[:len(cp.mu.dead)-1]
		c.mu.Lock()
		defer c.mu.Unlock()
		cp.resurrectWithLock(c, false)
		return c, nil
	}

	return nil, errors.New("no connection available")
}

// NextForRequest returns a connection from pool optimized for the request, or an error.
func (cp *statusConnectionPool) NextForRequest(req Request) (*Connection, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Return next live connection using request-aware selection if available
	if len(cp.mu.live) > 0 {
		// Try request-aware selection first
		if ras, ok := cp.selector.(RequestAwareSelector); ok {
			return ras.SelectForRequest(cp.mu.live, req)
		}
		// Fall back to basic selection
		return cp.selector.Select(cp.mu.live)
	} else if len(cp.mu.dead) > 0 {
		// No live connection is available, resurrect one of the dead ones.
		c := cp.mu.dead[len(cp.mu.dead)-1]
		cp.mu.dead = cp.mu.dead[:len(cp.mu.dead)-1]
		c.mu.Lock()
		defer c.mu.Unlock()
		cp.resurrectWithLock(c, false)
		return c, nil
	}

	return nil, errors.New("no connection available")
}

// OnSuccess marks the connection as successful.
func (cp *statusConnectionPool) OnSuccess(c *Connection) {
	// Establish consistent lock ordering: Pool -> Connection
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Short-circuit for live connection
	if !c.mu.isDead {
		return
	}

	c.markAsHealthyWithLock()
	cp.resurrectWithLock(c, true)
}

// OnFailure marks the connection as failed.
func (cp *statusConnectionPool) OnFailure(c *Connection) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()

	if c.mu.isDead {
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

	cp.scheduleResurrect(c, deadSince)

	// Push item to dead list and sort slice by number of failures
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

	// Check if connection exists in the list, return error if not.
	index := -1

	for i, conn := range cp.mu.live {
		if conn == c {
			index = i
		}
	}

	if index < 0 {
		// Does this error even get raised? Under what conditions can the connection not be in the cp.mu.live list?
		// If the connection is marked dead the function already ended
		return errors.New("connection not in live list")
	}

	// Remove item; https://github.com/golang/go/wiki/SliceTricks
	copy(cp.mu.live[index:], cp.mu.live[index+1:])
	cp.mu.live = cp.mu.live[:len(cp.mu.live)-1]

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

// resurrect adds the connection to the list of available connections after health validation.
// When removeDead is true, it also removes it from the dead list.
//
// CALLER RESPONSIBILITIES:
//   - Caller should verify external connectivity/health before resurrection
//     (this method only updates internal bookkeeping, not connection health)
//   - Caller must handle any errors from subsequent connection attempts
func (cp *statusConnectionPool) resurrectWithLock(c *Connection, removeDead bool) {
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

	if removeDead {
		index := -1

		for i, conn := range cp.mu.dead {
			if conn == c {
				index = i
			}
		}

		if index >= 0 {
			// Remove item; https://github.com/golang/go/wiki/SliceTricks
			copy(cp.mu.dead[index:], cp.mu.dead[index+1:])
			cp.mu.dead = cp.mu.dead[:len(cp.mu.dead)-1]
		}
	}
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

		if !c.mu.isDead {
			if debugLogger != nil {
				debugLogger.Logf("Already resurrected %q\n", c.URL)
			}
			return
		}

		cp.resurrectWithLock(c, true)
	})
}

// Select returns the connection in a round-robin fashion.
func (s *roundRobinSelector) Select(conns []*Connection) (*Connection, error) {
	if len(conns) == 0 {
		return nil, errors.New("no connections available")
	}

	// Atomic increment with wrap-around
	next := s.curr.Add(1)
	index := int(next % int64(len(conns)))
	return conns[index], nil
}

// markAsDead marks the connection as dead.
func (c *Connection) markAsDead() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.markAsDeadWithLock()
}

// markAsDeadWithLock marks the connection as dead (caller must hold lock).
func (c *Connection) markAsDeadWithLock() {
	c.mu.isDead = true
	if c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Now().UTC()
	}
	c.failures.Add(1)
}

// markAsLiveWithLock marks the connection as alive (caller must hold lock).
func (c *Connection) markAsLiveWithLock() {
	c.mu.isDead = false
}

// markAsHealthyWithLock marks the connection as healthy (caller must hold lock).
func (c *Connection) markAsHealthyWithLock() {
	c.mu.isDead = false
	c.mu.deadSince = time.Time{}
	c.failures.Store(0)
}

// String returns a readable connection representation.
func (c *Connection) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return fmt.Sprintf("<%s> dead=%v failures=%d", c.URL, c.mu.isDead, c.failures.Load())
}

// Role-based node selector implementations

// RoleBasedSelector filters connections based on node roles and applies a fallback selector.
type RoleBasedSelector struct {
	requiredRoles []string // Nodes must have at least one of these roles
	excludedRoles []string // Nodes must not have any of these roles
	fallback      Selector // Fallback selector for load balancing among filtered nodes
	allowFallback bool     // If true, use any node if no role-matching nodes are available
}

// RoleBasedSelectorOption configures a role-based selector.
type RoleBasedSelectorOption func(*RoleBasedSelector)

// WithRequiredRoles specifies roles that nodes must have.
func WithRequiredRoles(roles ...string) RoleBasedSelectorOption {
	return func(s *RoleBasedSelector) {
		s.requiredRoles = append(s.requiredRoles, roles...)
	}
}

// WithExcludedRoles specifies roles that nodes must not have.
func WithExcludedRoles(roles ...string) RoleBasedSelectorOption {
	return func(s *RoleBasedSelector) {
		s.excludedRoles = append(s.excludedRoles, roles...)
	}
}

// WithStrictMode disables fallback when no matching nodes are found.
func WithStrictMode() RoleBasedSelectorOption {
	return func(s *RoleBasedSelector) {
		s.allowFallback = false
	}
}

// WithFallback sets the fallback selector used when no role-matching nodes are available.
func WithFallback(fallback Selector) RoleBasedSelectorOption {
	return func(s *RoleBasedSelector) {
		s.fallback = fallback
	}
}

// NewRoleBasedSelector creates a role-based connection selector with the specified options.
// If no fallback is provided via WithFallback, a round-robin selector is used by default.
func NewRoleBasedSelector(opts ...RoleBasedSelectorOption) *RoleBasedSelector {
	s := &RoleBasedSelector{
		allowFallback: true,                    // Default to allowing fallback
		fallback:      NewRoundRobinSelector(), // Default fallback
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Select filters connections based on role requirements and applies fallback selection.
func (s *RoleBasedSelector) Select(connections []*Connection) (*Connection, error) {
	if len(connections) == 0 {
		return nil, ErrNoConnections
	}

	// Filter connections based on role requirements
	filtered := s.filterByRoles(connections)

	// If we have role-matching nodes, use them
	if len(filtered) > 0 {
		return s.fallback.Select(filtered)
	}

	// If no role-matching nodes and fallback is allowed, use any available node
	if s.allowFallback {
		return s.fallback.Select(connections)
	}

	// Strict mode: no fallback allowed
	return nil, fmt.Errorf("no connections found matching required roles: %v (available connections: %d)",
		s.requiredRoles, len(connections))
}

// filterByRoles filters connections based on required and excluded roles.
func (s *RoleBasedSelector) filterByRoles(connections []*Connection) []*Connection {
	filtered := make([]*Connection, 0, len(connections))

	for _, conn := range connections {
		// Check if connection has at least one required role
		hasRequiredRole := len(s.requiredRoles) == 0 // If no required roles, all nodes qualify
		if !hasRequiredRole {
			for _, role := range conn.Roles {
				if slices.Contains(s.requiredRoles, role) {
					hasRequiredRole = true
					break
				}
			}
		}

		if !hasRequiredRole {
			continue
		}

		// Check if connection has any excluded roles
		hasExcludedRole := false
		for _, role := range conn.Roles {
			if slices.Contains(s.excludedRoles, role) {
				hasExcludedRole = true
				break
			}
		}

		if hasExcludedRole {
			continue
		}

		filtered = append(filtered, conn)
	}

	return filtered
}

// SmartSelector examines request properties to route to optimal nodes.
type SmartSelector struct {
	ingestSelector  Selector // Used for ingest operations
	searchSelector  Selector // Used for search operations
	defaultSelector Selector // Used for other operations
}

// NewSmartSelector creates a request-aware selector that routes based on operation type.
func NewSmartSelector(defaultFallback Selector) *SmartSelector {
	return &SmartSelector{
		ingestSelector: NewRoleBasedSelector(
			WithRequiredRoles(RoleIngest),
			WithFallback(defaultFallback),
		),
		searchSelector: NewRoleBasedSelector(
			WithRequiredRoles(RoleData),
			WithFallback(defaultFallback),
		),
		defaultSelector: defaultFallback,
	}
}

// Select implements the basic Selector interface using default routing.
func (s *SmartSelector) Select(connections []*Connection) (*Connection, error) {
	return s.defaultSelector.Select(connections)
}

// SelectForRequest implements RequestAwareSelector to route based on request properties.
func (s *SmartSelector) SelectForRequest(connections []*Connection, req Request) (*Connection, error) {
	if len(connections) == 0 {
		return nil, ErrNoConnections
	}

	// Route based on request path and method
	path := req.GetPath()
	method := req.GetMethod()

	// Ingest operations - prefer ingest-capable nodes
	if isIngestOperation(path, method) {
		return s.ingestSelector.Select(connections)
	}

	// Search operations - prefer data nodes
	if isSearchOperation(path, method) {
		return s.searchSelector.Select(connections)
	}

	// Default routing for other operations
	return s.defaultSelector.Select(connections)
}

// isIngestOperation determines if a request is an ingest operation.
func isIngestOperation(path, method string) bool {
	if method != "POST" && method != "PUT" {
		return false
	}

	// Ingest pipeline operations
	if strings.Contains(path, "/_ingest/") {
		return true
	}

	// Bulk operations (often involve ingest pipelines)
	if strings.HasSuffix(path, "/_bulk") {
		return true
	}

	// Document indexing with pipeline parameter would need header inspection
	// This is a basic implementation - can be enhanced based on requirements

	return false
}

// isSearchOperation determines if a request is a search operation.
func isSearchOperation(path, method string) bool {
	if method != "GET" && method != "POST" {
		return false
	}

	// Search operations
	if strings.HasSuffix(path, "/_search") || strings.HasPrefix(path, "/_search/") {
		return true
	}

	// Multi-search
	if strings.HasSuffix(path, "/_msearch") {
		return true
	}

	// Document retrieval
	if method == "GET" && !strings.HasPrefix(path, "/_") {
		return true
	}

	return false
}
