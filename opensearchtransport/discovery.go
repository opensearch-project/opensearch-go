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
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Node role constants to match upstream OpenSearch server definitions
const (
	// RoleData nodes store and retrieve data, perform indexing, searching, and
	// aggregating operations on local shards. Available since OpenSearch 1.0.
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleData = "data"

	// RoleIngest nodes pre-process data before storing via ingest pipelines.
	// Available since OpenSearch 1.0.
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleIngest = "ingest"

	// RoleClusterManager nodes manage overall cluster operations, cluster state,
	// index creation/deletion, node health checks, and shard allocation.
	// Available since OpenSearch 1.0.
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleClusterManager = "cluster_manager"

	// RoleRemoteClusterClient nodes can act as cross-cluster clients and connect
	// to remote clusters. Available since OpenSearch 1.0 (based on Elasticsearch 7.8.0).
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleRemoteClusterClient = "remote_cluster_client"

	// RoleSearch nodes are dedicated to hosting search replica shards, allowing
	// separation of search workloads from indexing workloads. Added in OpenSearch 3.0.0-beta1.
	//
	// IMPORTANT: This role cannot be combined with any other node role. This restriction
	// has been enforced since OpenSearch 3.0.0-beta1.
	//
	// For searchable snapshots, use RoleWarm instead (recommended in OpenSearch 3.0+).
	// See: https://docs.opensearch.org/latest/tuning-your-cluster/separate-index-and-search-workloads/
	RoleSearch = "search"

	// RoleWarm nodes provide access to warm indices and searchable snapshots.
	// Added in OpenSearch 2.4. In OpenSearch 3.0+, warm role replaces search role
	// for searchable snapshot functionality.
	// See: https://docs.opensearch.org/latest/tuning-your-cluster/index/
	RoleWarm = "warm"

	// RoleML nodes are dedicated to running machine learning tasks and models.
	// This is a dynamic role added by the ML Commons plugin, not a built-in server role.
	// Available when ML Commons plugin is installed (typically OpenSearch 1.3+).
	// See: https://docs.opensearch.org/latest/ml-commons-plugin/cluster-settings/
	RoleML = "ml"

	// RoleCoordinatingOnly represents nodes with no explicit roles (node.roles: []).
	// These nodes delegate client requests to shards on data nodes and aggregate results.
	// This is not a built-in role but a derived state when no roles are specified.
	// Available since OpenSearch 1.0 as a configuration pattern.
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleCoordinatingOnly = "coordinating_only"

	// RoleMaster is Deprecated: Use RoleClusterManager instead for inclusive language.
	// Both roles are functionally identical but master role is deprecated.
	// See: https://docs.opensearch.org/latest/install-and-configure/configuring-opensearch/configuration-system/
	RoleMaster = "master"
)

// roleSet represents a set of node roles for efficient O(1) role lookups.
type roleSet map[string]struct{}

// workRoles defines node roles that perform actual work (data processing, storage, or search).
// Used to distinguish dedicated cluster managers from nodes that handle client requests.
// This matches the Java client's NodeSelector.SKIP_DEDICATED_CLUSTER_MASTERS logic.
//
//nolint:gochecknoglobals // This global constant defines the standard work roles
var workRoles = []string{
	RoleData,   // stores and retrieves data
	RoleIngest, // processes incoming data
	RoleWarm,   // handles warm/cold data storage
	RoleSearch, // dedicated search processing
	RoleML,     // machine learning tasks
}

// newRoleSet creates a roleSet from a slice of role names.
func newRoleSet(roles []string) roleSet {
	rs := make(roleSet, len(roles))
	for _, role := range roles {
		rs[role] = struct{}{}
		if role == RoleMaster {
			// Alias deprecated "master" role to "cluster_manager" for internal checks,
			// so we only need to perform a single check for "cluster_manager" elsewhere in the library.
			rs[RoleClusterManager] = struct{}{}
		}
	}
	return rs
}

// has checks if the roleSet contains a specific role using O(1) map lookup.
func (rs roleSet) has(roleName string) bool {
	_, exists := rs[roleName]
	return exists
}

// toSlice converts the roleSet back to a []string slice for compatibility.
// The roles are sorted alphabetically for consistent ordering.
// IMPORTANT: compareConnectionRoles() depends on this returning a sorted slice.
func (rs roleSet) toSlice() []string {
	if len(rs) == 0 {
		return nil
	}

	roles := make([]string, 0, len(rs))
	for role := range rs {
		// Skip the internal cluster_manager alias added for deprecated master role
		if role == RoleClusterManager {
			// Check if this was added as an alias for deprecated master role
			if _, hasMaster := rs[RoleMaster]; hasMaster {
				continue // Skip the alias, keep only the original master role
			}
		}
		roles = append(roles, role)
	}

	// Sort roles alphabetically for consistent ordering
	slices.Sort(roles)
	return roles
}

// isDedicatedClusterManager implements the logic from upstream Java client
// NodeSelector.SKIP_DEDICATED_CLUSTER_MASTERS to determine if a node should be skipped.
// It returns true for nodes that are cluster-manager eligible but have no "work" roles
// (i.e., roles that actually process/store data or handle requests).
// This matches OpenSearch server's SniffConnectionStrategy.DEFAULT_NODE_PREDICATE behavior.
func (rs roleSet) isDedicatedClusterManager() bool {
	// Must be cluster manager eligible first
	if !rs.has(RoleClusterManager) {
		return false
	}

	return !slices.ContainsFunc(workRoles, rs.has)
}

// Discoverable defines the interface for transports supporting node discovery.
type Discoverable interface {
	DiscoverNodes(ctx context.Context) error
}

// nodeInfo represents the information about node in a cluster.
// nodeInfoHTTP represents the HTTP configuration from node info
type nodeInfoHTTP struct {
	PublishAddress string `json:"publish_address"` // Available since OpenSearch 1.0.0
}

// nodeInfoOS represents the OS configuration from node info.
type nodeInfoOS struct {
	AllocatedProcessors *int `json:"allocated_processors"` // Available since OpenSearch 1.0.0
}

type nodeInfo struct {
	ID         string         `json:"id"`   // Available since OpenSearch 1.0.0
	Name       string         `json:"name"` // Available since OpenSearch 1.0.0
	url        *url.URL       // Client-side field, not from server
	Roles      []string       `json:"roles"`      // Available since OpenSearch 1.0.0
	Attributes map[string]any `json:"attributes"` // Available since OpenSearch 1.0.0
	HTTP       nodeInfoHTTP   `json:"http"`       // Available since OpenSearch 1.0.0
	OS         nodeInfoOS     `json:"os"`         // Available since OpenSearch 1.0.0

	// Internal fields (not part of JSON)
	roleSet roleSet
}

// DiscoverNodes reloads the client connections by fetching information from the cluster.
func (c *Client) DiscoverNodes(ctx context.Context) error {
	// Bail out early if the context is already cancelled (e.g. client shutting down).
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Prevent concurrent discovery operations
	c.mu.Lock()
	if c.mu.discoveryInProgress {
		c.mu.Unlock()
		return nil
	}
	c.mu.discoveryInProgress = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.mu.discoveryInProgress = false
		c.mu.Unlock()
	}()

	discovered, err := c.getNodesInfo(ctx)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Logf("Error getting nodes info: %s\n", err)
		}
		return fmt.Errorf("discovery: get nodes: %w", err)
	}

	c.mu.RLock()
	connPool := c.mu.connectionPool
	c.mu.RUnlock()
	isColdStart := connPool == nil

	if isColdStart {
		return c.nodeDiscoveryAsyncStart(ctx, discovered)
	}

	if err := c.nodeDiscovery(ctx, discovered); err != nil {
		return err
	}

	// Rotate standby connections after discovery completes.
	// This piggybacks on the discovery interval rather than using a separate timer.
	// Each rotation health-checks one standby and, if healthy, swaps it with a random active.
	if c.activeListCap > 0 && c.standbyRotationInterval >= 0 {
		c.mu.RLock()
		pool, ok := c.mu.connectionPool.(*statusConnectionPool)
		c.mu.RUnlock()

		if ok && pool != nil {
			pool.rotateStandby(ctx, c.standbyRotationCount)
		}
	}

	return nil
}

// nodeDiscoveryAsyncStart handles discovery with asynchronous connection startup - prioritizes fast startup.
func (c *Client) nodeDiscoveryAsyncStart(ctx context.Context, discovered []nodeInfo) error {
	// Async start - assume all connections are ready for fast startup
	readyConnections := make([]*Connection, 0, len(discovered))

	for _, node := range discovered {
		conn := c.createConnection(node)
		readyConnections = append(readyConnections, conn)

		// Async health check - will be handled by normal pool mechanics
		go func(conn *Connection) {
			c.healthCheckWithRetries(ctx, conn, c.discoveryHealthCheckRetries)
		}(conn)
	}

	// Cold start -- no existing connections to compare timestamps against.
	return c.updateConnectionPool(time.Time{}, readyConnections, []*Connection{})
}

// nodeDiscovery handles discovery for running clusters - waits for health checks.
func (c *Client) nodeDiscovery(ctx context.Context, discovered []nodeInfo) error {
	// Capture the time before health checks begin. Any connection whose deadSince
	// predates this timestamp has stale dead state that the health check supersedes.
	healthCheckedAt := time.Now()

	// Running cluster - health check before categorizing
	var wg sync.WaitGroup

	// Pre-allocate based on total discovered nodes
	readyConnections := make([]*Connection, 0, len(discovered))
	deadConnections := make([]*Connection, 0, len(discovered))
	discoMu := sync.Mutex{}

	for _, node := range discovered {
		wg.Add(1)
		go func(node nodeInfo) {
			defer wg.Done()

			conn := c.createConnection(node)
			healthy := c.healthCheckWithRetries(ctx, conn, c.discoveryHealthCheckRetries)

			discoMu.Lock()
			if healthy {
				readyConnections = append(readyConnections, conn)
			} else {
				deadConnections = append(deadConnections, conn)
			}
			discoMu.Unlock()
		}(node)
	}

	wg.Wait()

	return c.updateConnectionPool(healthCheckedAt, readyConnections, deadConnections)
}

// createConnection creates a Connection from nodeInfo with proper role processing.
// New connections start in lcDead state so that the flat pool's partition logic
// can distinguish them from reused connections (which retain their policy-pool
// lifecycle -- lcActive, lcStandby, etc.). The caller is responsible for
// transitioning new connections to the appropriate lifecycle after health checking.
func (c *Client) createConnection(node nodeInfo) *Connection {
	// Build role set for efficient O(1) lookups
	node.roleSet = newRoleSet(node.Roles)

	initialState := lcDead | lcNeedsWarmup | lcNeedsHardware

	conn := &Connection{
		URL:        node.url,
		ID:         node.ID,
		Name:       node.Name,
		Roles:      node.roleSet,
		Attributes: node.Attributes,
		weight:     1,
	}

	// Store allocated_processors if provided by the /_nodes/http,os response.
	if node.OS.AllocatedProcessors != nil {
		conn.allocatedProcessors = *node.OS.AllocatedProcessors
		initialState &^= lcNeedsHardware // hardware info obtained
	}

	conn.state.Store(int64(newConnState(initialState)))
	return conn
}

// updateConnectionPool atomically updates the connection pool with new connection information
// and notifies the router of cluster topology changes.
//
// healthCheckedAt is the timestamp before health checks began. When reusing an existing
// Connection object whose deadSince predates healthCheckedAt, the more recent health check
// result wins and the connection is resurrected. If deadSince is newer (set concurrently
// during the health check window), the dead state is preserved. Zero means no timestamp
// comparison (cold start -- no existing connections to compare against).
func (c *Client) updateConnectionPool(healthCheckedAt time.Time, readyConnections, deadConnections []*Connection) error {
	totalNodes := len(readyConnections) + len(deadConnections)
	allConnections := make([]*Connection, 0, totalNodes)
	allConnections = append(allConnections, readyConnections...)
	allConnections = append(allConnections, deadConnections...)

	// Get current connections with their role information for diff calculation
	c.mu.RLock()
	currentPool := c.mu.connectionPool
	c.mu.RUnlock()

	var currentConnectionsByURL map[string]*Connection

	if currentPool != nil {
		currentURLs := currentPool.URLs()

		// Pre-size map based on current pool size
		currentConnectionsByURL = make(map[string]*Connection, len(currentURLs))

		// Get actual connections from the pool to preserve old role information
		// We need the old connections to detect role changes
		for _, urlPtr := range currentURLs {
			url := urlPtr.String()
			// Try to find this URL in the current pool
			if conn := c.findConnectionByURL(currentPool, url); conn != nil {
				currentConnectionsByURL[url] = conn
			}
		}
	} else {
		currentConnectionsByURL = make(map[string]*Connection)
	}

	// Build map of new connections by URL
	newConnectionsByURL := make(map[string]*Connection, totalNodes)
	for _, conn := range allConnections {
		newConnectionsByURL[conn.URL.String()] = conn
	}

	// Calculate diffs: added, removed, unchanged
	// Note: We reuse existing Connection objects when roles haven't changed to avoid policy churn
	added := make([]*Connection, 0, len(newConnectionsByURL))
	removed := make([]*Connection, 0, len(currentConnectionsByURL))
	unchanged := make([]*Connection, 0, len(currentConnectionsByURL))

	// Build final connection list (mix of reused old and new objects)
	finalConnectionsByURL := make(map[string]*Connection, len(newConnectionsByURL))

	// Find added connections and connections with role changes
	for url, newConn := range newConnectionsByURL {
		oldConn, existed := currentConnectionsByURL[url]
		if !existed {
			// Brand new connection
			added = append(added, newConn)
			finalConnectionsByURL[url] = newConn
			continue
		}

		if oldConn == newConn {
			// Same object - truly unchanged (shouldn't happen with current code but handle it)
			unchanged = append(unchanged, newConn)
			finalConnectionsByURL[url] = newConn
			continue
		}

		// Different Connection objects for same URL - check if roles changed
		if compareConnectionRoles(oldConn, newConn) != 0 {
			// Roles changed - treat as remove+add so policies can re-evaluate
			removed = append(removed, oldConn)
			added = append(added, newConn)
			finalConnectionsByURL[url] = newConn
		} else {
			// Same URL, same roles - reuse old object to avoid policy churn
			unchanged = append(unchanged, oldConn)
			finalConnectionsByURL[url] = oldConn
		}
	}

	// Find removed connections (existed before but not in new discovery)
	for url, oldConn := range currentConnectionsByURL {
		if _, exists := newConnectionsByURL[url]; !exists {
			removed = append(removed, oldConn)
			if debugLogger != nil {
				debugLogger.Logf("Discovery: Connection %q removed from cluster (roles: %v)\n", url, oldConn.Roles.toSlice())
			}
		}
	}

	// Build final ready/dead lists from the connection objects we've chosen to use
	// (mix of reused old objects and new objects).
	//
	// When we reuse an old Connection object that has deadSince set, we compare it
	// against healthCheckedAt to determine which information is newer:
	//   - deadSince < healthCheckedAt -> dead state is stale, health check is newer -> resurrect
	//   - deadSince >= healthCheckedAt -> dead state set concurrently/after health check -> keep dead
	// When healthCheckedAt is zero (cold start), there are no old connections to reuse,
	// so the comparison is moot.
	readyURLs := make(map[string]struct{}, len(readyConnections))
	for _, conn := range readyConnections {
		readyURLs[conn.URL.String()] = struct{}{}
	}

	finalReady := make([]*Connection, 0, len(finalConnectionsByURL))
	finalDead := make([]*Connection, 0, len(finalConnectionsByURL))

	for url, conn := range finalConnectionsByURL {
		if _, isReady := readyURLs[url]; isReady {
			conn.mu.Lock()
			deadSince := conn.mu.deadSince
			stale := !deadSince.IsZero() && !healthCheckedAt.IsZero() && deadSince.Before(healthCheckedAt)
			switch {
			case stale:
				// Dead state predates the health check -- resurrect.
				conn.mu.deadSince = time.Time{}
				conn.mu.Unlock()
				conn.failures.Store(0)
			case !deadSince.IsZero():
				// Dead state is concurrent or newer -- keep dead.
				conn.mu.Unlock()
				finalDead = append(finalDead, conn)
				continue
			default:
				conn.mu.Unlock()
			}
			finalReady = append(finalReady, conn)
		} else {
			finalDead = append(finalDead, conn)
		}
	}

	// Compute GCD-normalized weights from allocatedProcessors before connections
	// are added to pools. This must happen before the pool swap so that the
	// duplicate-pointer insertion (appendToReadyActiveWithLock) uses the correct
	// weight for each connection.
	allFinal := make([]*Connection, 0, len(finalReady)+len(finalDead))
	allFinal = append(allFinal, finalReady...)
	allFinal = append(allFinal, finalDead...)
	computeWeights(allFinal)

	// Dynamically recalculate capacity model from discovered hardware.
	// Use the minimum allocatedProcessors across all nodes with known values --
	// the smallest node is the bottleneck for per-server rate limits.
	c.recalculateCapacityModel(allFinal)

	// Atomically swap the connection pool under c.mu, then release the lock
	// before notifying the router and observers. This avoids holding c.mu (W)
	// while router policies acquire pool-level locks (the lock ordering that
	// caused the deadlock: c.mu(W) -> pool.mu(W) vs pool.mu(R) in the request path).
	c.mu.Lock()

	totalFinalNodes := len(finalReady) + len(finalDead)
	var newConnectionPool ConnectionPool
	if totalFinalNodes == 1 {
		newConnectionPool = c.createOrUpdateSingleNodePool(finalReady, finalDead)
	} else {
		newConnectionPool = c.createOrUpdateMultiNodePoolWithLock(finalReady, finalDead)
	}

	// Perform swap of connection pools
	c.mu.connectionPool = newConnectionPool

	// Set up health check function and observer for pools that support it
	if pool, ok := c.mu.connectionPool.(*statusConnectionPool); ok {
		pool.healthCheck = c.DefaultHealthCheck
		if obs := c.observer.Load(); obs != nil {
			pool.observer.Store(obs)
		}
	}

	c.mu.Unlock()

	// Notify router outside c.mu -- router.DiscoveryUpdate may acquire pool-level
	// locks via RolePolicy.discoveryUpdateAdd/enforceActiveCapWithLock. Holding c.mu
	// here would create the lock inversion: c.mu(W) -> pool.mu(W).
	// c.router is immutable after construction, so no lock is needed to read it.
	if c.router != nil {
		// Pass calculated diffs to router so policies don't have to recalculate
		if err := c.router.DiscoveryUpdate(added, removed, unchanged); err != nil {
			// Continue - don't fail discovery due to router errors
			_ = err
		}
	}

	// Notify observer of discovery changes (observer is atomic, safe without c.mu)
	if obs := observerFromAtomic(&c.observer); obs != nil {
		readyCount := len(finalReady)
		deadCount := len(finalDead)
		for _, conn := range added {
			obs.OnDiscoveryAdd(newConnectionEvent("flat", conn, readyCount, deadCount))
		}
		for _, conn := range removed {
			obs.OnDiscoveryRemove(newConnectionEvent("flat", conn, readyCount, deadCount))
		}
		for _, conn := range unchanged {
			obs.OnDiscoveryUnchanged(newConnectionEvent("flat", conn, readyCount, deadCount))
		}
	}

	return nil
}

// compareConnectionRoles compares two connections' roles.
// Returns 0 if roles are identical, non-zero if different.
// Assumes toSlice() returns sorted slices (required for consistent comparison).
func compareConnectionRoles(a, b *Connection) int {
	// First compare URLs - if different, return non-zero
	if cmp := strings.Compare(a.URL.String(), b.URL.String()); cmp != 0 {
		return cmp
	}

	// URLs match, now compare roles
	// toSlice() returns sorted slices, so we can compare directly
	aRoles := a.Roles.toSlice()
	bRoles := b.Roles.toSlice()

	// Compare sorted role slices
	// Returns 0 if identical, non-zero if different
	return slices.Compare(aRoles, bRoles)
}

// gcd returns the greatest common divisor of two positive integers using the
// Euclidean algorithm. Both arguments must be positive.
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// computeWeights sets each connection's weight based on its allocatedProcessors
// value, normalized by the GCD of all known core counts. Connections with unknown
// core counts (allocatedProcessors == 0) get weight 1.
//
// Examples:
//
//	Cores [8, 16]          -> GCD=8  -> weights [1, 2]
//	Cores [8, 16, 24]      -> GCD=8  -> weights [1, 2, 3]
//	Cores [8, 16, 32, 40]  -> GCD=8  -> weights [1, 2, 4, 5]
//	Cores [24, 32, 40]     -> GCD=8  -> weights [3, 4, 5]
//	Cores [8, 8, 8]        -> GCD=8  -> weights [1, 1, 1]  (homogeneous)
//
// If no connections have known core counts, all weights remain at their current
// value (typically 1 from initialization).
func computeWeights(conns []*Connection) {
	// Collect all known core counts.
	d := 0
	for _, c := range conns {
		if c.allocatedProcessors > 0 {
			if d == 0 {
				d = c.allocatedProcessors
			} else {
				d = gcd(d, c.allocatedProcessors)
			}
		}
	}

	if d == 0 {
		// No connections have known core counts -- leave weights unchanged.
		return
	}

	for _, c := range conns {
		if c.allocatedProcessors > 0 {
			c.weight = c.allocatedProcessors / d
		} else {
			c.weight = 1
		}
	}
}

// recalculateCapacityModel updates the client's internal capacity model fields
// based on discovered hardware info. Uses the minimum allocatedProcessors across
// all nodes (the bottleneck) to derive serverMaxNewConnsPerSec, clientsPerServer,
// and healthCheckRate. Skips recalculation if no nodes have known core counts.
func (c *Client) recalculateCapacityModel(conns []*Connection) {
	minCores := 0
	for _, conn := range conns {
		if conn.allocatedProcessors > 0 {
			if minCores == 0 || conn.allocatedProcessors < minCores {
				minCores = conn.allocatedProcessors
			}
		}
	}

	if minCores == 0 {
		// No nodes have known core counts -- keep current defaults.
		return
	}

	c.serverMaxNewConnsPerSec = float64(minCores) * serverMaxNewConnsPerSecMultiplier
	c.clientsPerServer = float64(minCores)
	c.healthCheckRate = float64(minCores) * healthCheckRateMultiplier
}

// findConnectionByURL attempts to find a connection in the pool by URL.
// This helper extracts connections from different pool types to get their role information.
func (c *Client) findConnectionByURL(pool ConnectionPool, url string) *Connection {
	switch p := pool.(type) {
	case *singleConnectionPool:
		if p.connection != nil && p.connection.URL.String() == url {
			return p.connection
		}
	case *statusConnectionPool:
		// Check both ready and dead lists
		p.RLock()
		defer p.RUnlock()

		for _, conn := range p.mu.ready {
			if conn.URL.String() == url {
				return conn
			}
		}
		for _, conn := range p.mu.dead {
			if conn.URL.String() == url {
				return conn
			}
		}
	}

	return nil
}

// createOrUpdateSingleNodePool handles single-node connection pool creation/updates.
// Caller must hold c.mu.Lock().
func (c *Client) createOrUpdateSingleNodePool(readyConnections, deadConnections []*Connection) ConnectionPool {
	// Single node - check if we need to demote from statusConnectionPool
	if _, isStatusPool := c.mu.connectionPool.(*statusConnectionPool); isStatusPool {
		// Demote from multi-node to single-node pool
		return c.demoteConnectionPoolWithLock()
	}

	// Create or update single connection pool
	var connection *Connection
	if len(readyConnections) == 1 {
		connection = readyConnections[0]
	} else if len(deadConnections) == 1 {
		connection = deadConnections[0]
	}

	// Preserve metrics from existing single connection pool
	var metrics *metrics
	if existingPool, ok := c.mu.connectionPool.(*singleConnectionPool); ok {
		metrics = existingPool.metrics
	}

	return &singleConnectionPool{
		connection: connection,
		metrics:    metrics,
	}
}

// createOrUpdateMultiNodePoolWithLock handles multi-node connection pool creation/updates.
// Caller must hold c.mu.Lock().
func (c *Client) createOrUpdateMultiNodePoolWithLock(readyConnections, deadConnections []*Connection) ConnectionPool {
	// Multi-node - check if we need to promote from singleConnectionPool
	if _, isSinglePool := c.mu.connectionPool.(*singleConnectionPool); isSinglePool {
		// Promote from single-node to multi-node pool
		return c.promoteConnectionPoolWithLock(readyConnections, deadConnections)
	}

	// Update existing statusConnectionPool or create new one
	// Apply client-level filtering for dedicated cluster managers
	flatReadyConns := make([]*Connection, 0, len(readyConnections))
	flatDeadConns := make([]*Connection, 0, len(deadConnections))

	for _, conn := range readyConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			if debugLogger != nil {
				debugLogger.Logf("Excluding dedicated cluster manager %q from connection pool\n", conn.Name)
			}
			continue
		}
		flatReadyConns = append(flatReadyConns, conn)
	}

	for _, conn := range deadConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			continue
		}
		flatDeadConns = append(flatDeadConns, conn)
	}

	// Preserve settings and metrics from existing statusConnectionPool,
	// or use client-configured settings for new pools
	resurrectTimeoutInitial := c.resurrectTimeoutInitial
	resurrectTimeoutMax := c.resurrectTimeoutMax
	resurrectTimeoutFactorCutoff := c.resurrectTimeoutFactorCutoff
	minimumResurrectTimeout := c.minimumResurrectTimeout
	jitterScale := c.jitterScale
	serverMaxNewConnsPerSec := c.serverMaxNewConnsPerSec
	clientsPerServer := c.clientsPerServer
	healthCheck := c.healthCheck
	activeListCap := c.activeListCap
	activeListCapConfig := c.activeListCapConfig
	standbyPromotionChecks := c.standbyPromotionChecks
	var metrics *metrics

	if existingPool, ok := c.mu.connectionPool.(*statusConnectionPool); ok {
		// Preserve settings from existing pool (should match client settings)
		resurrectTimeoutInitial = existingPool.resurrectTimeoutInitial
		resurrectTimeoutMax = existingPool.resurrectTimeoutMax
		resurrectTimeoutFactorCutoff = existingPool.resurrectTimeoutFactorCutoff
		minimumResurrectTimeout = existingPool.minimumResurrectTimeout
		jitterScale = existingPool.jitterScale
		serverMaxNewConnsPerSec = existingPool.serverMaxNewConnsPerSec
		clientsPerServer = existingPool.clientsPerServer
		healthCheck = existingPool.healthCheck
		activeListCap = existingPool.activeListCap
		activeListCapConfig = existingPool.activeListCapConfig
		standbyPromotionChecks = existingPool.standbyPromotionChecks
		metrics = existingPool.metrics
	}

	// Shuffle connections for load distribution unless disabled
	if !c.skipConnectionShuffle && len(flatReadyConns) > 1 {
		rand.Shuffle(len(flatReadyConns), func(i, j int) {
			flatReadyConns[i], flatReadyConns[j] = flatReadyConns[j], flatReadyConns[i]
		})
	}

	flatPool := &statusConnectionPool{
		name:                         "flat",
		resurrectTimeoutInitial:      resurrectTimeoutInitial,
		resurrectTimeoutMax:          resurrectTimeoutMax,
		resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      minimumResurrectTimeout,
		jitterScale:                  jitterScale,
		serverMaxNewConnsPerSec:      serverMaxNewConnsPerSec,
		clientsPerServer:             clientsPerServer,
		healthCheck:                  healthCheck,
		metrics:                      metrics,
		activeListCap:                activeListCap,
		activeListCapConfig:          activeListCapConfig,
		standbyPromotionChecks:       standbyPromotionChecks,
	}
	flatPool.mu.ready = flatReadyConns
	flatPool.mu.dead = flatDeadConns

	// Recalculate activeListCap and warmup parameters for the flat pool before
	// partitioning so startWarmup calls use the correctly-scaled values.
	flatPool.recalculateWarmupParams(len(flatReadyConns) + len(flatDeadConns))

	// Partition ready connections by their current lifecycle state.
	// Reused connections (unchanged in discovery) may already be in standby
	// in policy pools via the shared Connection.state atomic. Forcing them all
	// to lcActive would clobber the state that policy pools depend on, causing
	// Next() to evict "externally-demoted" connections from the wrong partition.
	//
	// Instead, read each connection's current state and partition accordingly:
	//   - lcActive (or warming) -> active partition (no state mutation)
	//   - lcStandby (with or without lcNeedsWarmup) -> standby partition (no state mutation)
	//   - lcDead -> newly discovered connection (createConnection initializes to
	//     lcDead). Transition to lcActive with warmup so the connection ramps up
	//     traffic gradually.
	activeCount := 0
	for i, conn := range flatReadyConns {
		lc := conn.loadConnState().lifecycle()
		switch {
		case lc.has(lcActive):
			// Already active (possibly warming) -- keep in active partition.
			if i != activeCount {
				flatReadyConns[i], flatReadyConns[activeCount] = flatReadyConns[activeCount], flatReadyConns[i]
			}
			activeCount++
		case lc.has(lcStandby):
			// Standby in a policy pool -- leave past activeCount boundary.
		default:
			// New connection (lcDead from createConnection) or unexpected state.
			// Transition to active with warmup for gradual traffic ramp-up.
			conn.mu.Lock()
			conn.casLifecycle(conn.loadConnState(), 0, lcActive, lcUnknown|lcStandby)
			conn.mu.Unlock()
			rounds, skip := flatPool.getWarmupParams()
			conn.startWarmup(rounds, skip)
			if i != activeCount {
				flatReadyConns[i], flatReadyConns[activeCount] = flatReadyConns[activeCount], flatReadyConns[i]
			}
			activeCount++
		}
	}
	flatPool.mu.activeCount = activeCount

	// NOTE: We intentionally do NOT call enforceActiveCapWithLock() here.
	// The flat pool is a transport-level container for discovery bookkeeping.
	// Cap enforcement is owned by the policy pools (RoundRobinPolicy, RolePolicy),
	// which manage their own active/standby partitions via DiscoveryUpdate.
	// Calling enforceActiveCapWithLock on the flat pool would mutate the shared
	// Connection.state atomics (setting lcStandby + clearing warmup), clobbering
	// the warmup progress that policy pools are tracking. This caused connections
	// to never finish warmup when the discovery interval was shorter than the
	// warmup duration.

	return flatPool
}

func (c *Client) getNodesInfo(ctx context.Context) ([]nodeInfo, error) {
	scheme := c.urls[0].Scheme

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_nodes/http,os", nil)
	if err != nil {
		return nil, err
	}

	conn, err := getConnectionFromPool(c, req)
	// TODO: If no connection is returned, fallback to original URLs
	if err != nil {
		if len(c.urls) > 0 {
			// Create temporary connections from startup URLs and use round-robin selection
			startupConns := make([]*Connection, len(c.urls))
			for i, u := range c.urls {
				startupConns[i] = &Connection{URL: u, weight: 1}
			}

			// Use round-robin selector to pick a startup URL
			selector := &roundRobinSelector{}
			selector.curr.Store(-1)
			conn, err = selector.Select(startupConns)
			if err != nil {
				return nil, fmt.Errorf("failed to select startup URL: %w", err)
			}
		} else {
			return nil, err
		}
	}

	c.setReqURL(conn.URL, req)
	c.setReqAuth(conn.URL, req)
	c.setReqUserAgent(req)

	res, err := c.transport.RoundTrip(req)
	if err != nil {
		// Report connection failure to the pool if we got the connection from the pool
		// Note: getConnectionFromPool always uses pool, so we always report
		c.mu.RLock()
		pool := c.mu.connectionPool
		c.mu.RUnlock()
		if pool != nil {
			if poolErr := pool.OnFailure(conn); poolErr != nil && debugLogger != nil {
				debugLogger.Logf("Failed to mark connection as failed: %v\n", poolErr)
			}
		}
		return nil, err
	}

	if res.Body == nil {
		return nil, fmt.Errorf("unexpected empty body")
	}
	defer res.Body.Close()

	if res.StatusCode > http.StatusOK {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("server error: %s: %w", res.Status, err)
		}
		return nil, fmt.Errorf("server error: %s: %s", res.Status, body)
	}

	var env map[string]json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return nil, err
	}

	var nodes map[string]nodeInfo
	if err := json.Unmarshal(env["nodes"], &nodes); err != nil {
		return nil, err
	}

	out := make([]nodeInfo, len(nodes))
	idx := 0

	for id, node := range nodes {
		node.ID = id
		u := c.getNodeURL(node, scheme)
		node.url = u
		out[idx] = node
		idx++
	}

	// Report connection success to the pool if we got the connection from the pool
	// Note: getConnectionFromPool always uses pool, so we always report
	c.mu.RLock()
	pool := c.mu.connectionPool
	c.mu.RUnlock()
	if pool != nil {
		pool.OnSuccess(conn)
	}

	return out, nil
}

func (c *Client) getNodeURL(node nodeInfo, scheme string) *url.URL {
	var (
		host string
		port string
		err  error

		addrs = strings.Split(node.HTTP.PublishAddress, "/")
		ports = strings.Split(node.HTTP.PublishAddress, ":")
	)

	if len(addrs) > 1 {
		host = addrs[0]
	} else {
		host, _, err = net.SplitHostPort(addrs[0])
		if err != nil {
			host = strings.Split(addrs[0], ":")[0]
		}
	}
	port = ports[len(ports)-1]
	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
	}

	return u
}

func (c *Client) scheduleDiscoverNodes() {
	// Don't schedule or run discovery if the client is shutting down.
	if c.ctx.Err() != nil {
		return
	}

	//nolint:errcheck // errors are logged inside the function
	go c.DiscoverNodes(c.ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mu.discoverNodesTimer != nil {
		c.mu.discoverNodesTimer.Stop()
	}

	c.mu.discoverNodesTimer = time.AfterFunc(c.discoverNodesInterval, func() {
		c.scheduleDiscoverNodes()
	})
}
