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

type nodeInfo struct {
	ID         string         `json:"id"`   // Available since OpenSearch 1.0.0
	Name       string         `json:"name"` // Available since OpenSearch 1.0.0
	url        *url.URL       // Client-side field, not from server
	Roles      []string       `json:"roles"`      // Available since OpenSearch 1.0.0
	Attributes map[string]any `json:"attributes"` // Available since OpenSearch 1.0.0
	HTTP       nodeInfoHTTP   `json:"http"`       // Available since OpenSearch 1.0.0

	// Internal fields (not part of JSON)
	roleSet roleSet
}

// DiscoverNodes reloads the client connections by fetching information from the cluster.
func (c *Client) DiscoverNodes(ctx context.Context) error {
	// Prevent concurrent discovery operations
	c.mu.Lock()
	if c.mu.discoveryInProgress {
		c.mu.Unlock()
		if debugLogger != nil {
			debugLogger.Logf("Discovery already in progress, skipping\n")
		}
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
	return c.nodeDiscovery(ctx, discovered)
}

// nodeDiscoveryAsyncStart handles discovery with asynchronous connection startup - prioritizes fast startup.
func (c *Client) nodeDiscoveryAsyncStart(ctx context.Context, discovered []nodeInfo) error {
	// Async start - assume all connections are live for fast startup
	liveConnections := make([]*Connection, 0, len(discovered))

	for _, node := range discovered {
		conn := c.createConnection(node)
		liveConnections = append(liveConnections, conn)

		// Async health check - will be handled by normal pool mechanics
		go func(conn *Connection) {
			c.healthCheckWithRetries(ctx, conn, c.discoveryHealthCheckRetries)
		}(conn)
	}

	return c.updateConnectionPool(liveConnections, []*Connection{})
}

// nodeDiscovery handles discovery for running clusters - waits for health checks.
func (c *Client) nodeDiscovery(ctx context.Context, discovered []nodeInfo) error {
	// Running cluster - health check before categorizing
	var wg sync.WaitGroup

	// Pre-allocate based on total discovered nodes
	liveConnections := make([]*Connection, 0, len(discovered))
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
				liveConnections = append(liveConnections, conn)
			} else {
				deadConnections = append(deadConnections, conn)
			}
			discoMu.Unlock()
		}(node)
	}

	wg.Wait()
	return c.updateConnectionPool(liveConnections, deadConnections)
}

// createConnection creates a Connection from nodeInfo with proper role processing.
func (c *Client) createConnection(node nodeInfo) *Connection {
	// Build role set for efficient O(1) lookups
	node.roleSet = newRoleSet(node.Roles)

	return &Connection{
		URL:        node.url,
		ID:         node.ID,
		Name:       node.Name,
		Roles:      node.roleSet,
		Attributes: node.Attributes,
	}
}

// updateConnectionPool atomically updates the connection pool with new connection information
// and notifies the router of cluster topology changes.
func (c *Client) updateConnectionPool(liveConnections, deadConnections []*Connection) error {
	totalNodes := len(liveConnections) + len(deadConnections)
	allConnections := make([]*Connection, 0, totalNodes)
	allConnections = append(allConnections, liveConnections...)
	allConnections = append(allConnections, deadConnections...)

	// Get current connections for diff calculation (minimal lock time)
	c.mu.RLock()
	currentPool := c.mu.connectionPool
	c.mu.RUnlock()

	var currentConnections []*Connection
	if currentPool != nil {
		// Convert URLs back to connections for diff calculation
		currentURLs := currentPool.URLs()
		currentConnectionMap := make(map[string]*Connection)
		for _, conn := range allConnections {
			currentConnectionMap[conn.URL.String()] = conn
		}

		for _, url := range currentURLs {
			if conn, exists := currentConnectionMap[url.String()]; exists {
				currentConnections = append(currentConnections, conn)
			}
		}
	}

	// Calculate diff outside the lock
	currentURLs := make(map[string]struct{})
	for _, conn := range currentConnections {
		currentURLs[conn.URL.String()] = struct{}{}
	}

	newURLs := make(map[string]*Connection)
	for _, conn := range allConnections {
		newURLs[conn.URL.String()] = conn
	}

	var added []*Connection
	var removed []*Connection
	var unchanged []*Connection

	// Find added connections
	for url, conn := range newURLs {
		if _, exists := currentURLs[url]; !exists {
			added = append(added, conn)
		} else {
			unchanged = append(unchanged, conn)
		}
	}

	// Find removed connections
	for _, conn := range currentConnections {
		if _, exists := newURLs[conn.URL.String()]; !exists {
			removed = append(removed, conn)
		}
	}

	// Prepare new connection pool outside the lock, then atomically swap
	c.mu.Lock()
	defer c.mu.Unlock()

	var newConnectionPool ConnectionPool
	if totalNodes == 1 {
		newConnectionPool = c.createOrUpdateSingleNodePool(liveConnections, deadConnections)
	} else {
		newConnectionPool = c.createOrUpdateMultiNodePool(liveConnections, deadConnections)
	}

	// Perform swap of connection pools
	c.mu.connectionPool = newConnectionPool
	router := c.router // Capture router reference while holding lock

	if router != nil {
		if err := router.DiscoveryUpdate(added, removed, unchanged); err != nil {
			if debugLogger != nil {
				debugLogger.Logf("Router DiscoveryUpdate error: %s\n", err)
			}
			// Continue - don't fail discovery due to router errors
		}
	}

	return nil
}

// createOrUpdateSingleNodePool handles single-node connection pool creation/updates.
// Caller must hold c.mu.Lock().
func (c *Client) createOrUpdateSingleNodePool(liveConnections, deadConnections []*Connection) ConnectionPool {
	// Single node - check if we need to demote from statusConnectionPool
	if _, isStatusPool := c.mu.connectionPool.(*statusConnectionPool); isStatusPool {
		// Demote from multi-node to single-node pool
		return c.demoteConnectionPoolWithLock()
	}

	// Create or update single connection pool
	var connection *Connection
	if len(liveConnections) == 1 {
		connection = liveConnections[0]
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

// createOrUpdateMultiNodePool handles multi-node connection pool creation/updates.
// Caller must hold c.mu.Lock().
func (c *Client) createOrUpdateMultiNodePool(liveConnections, deadConnections []*Connection) ConnectionPool {
	// Multi-node - check if we need to promote from singleConnectionPool
	if _, isSinglePool := c.mu.connectionPool.(*singleConnectionPool); isSinglePool {
		// Promote from single-node to multi-node pool
		return c.promoteConnectionPoolWithLock(liveConnections, deadConnections)
	}

	// Update existing statusConnectionPool or create new one
	// Apply client-level filtering for dedicated cluster managers
	flatLiveConns := make([]*Connection, 0, len(liveConnections))
	flatDeadConns := make([]*Connection, 0, len(deadConnections))

	for _, conn := range liveConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			if debugLogger != nil {
				debugLogger.Logf("Excluding dedicated cluster manager %q from connection pool\n", conn.Name)
			}
			continue
		}
		flatLiveConns = append(flatLiveConns, conn)
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
	resurrectTimeoutFactorCutoff := c.resurrectTimeoutFactorCutoff
	var metrics *metrics

	if existingPool, ok := c.mu.connectionPool.(*statusConnectionPool); ok {
		// Preserve settings from existing pool (should match client settings)
		resurrectTimeoutInitial = existingPool.resurrectTimeoutInitial
		resurrectTimeoutFactorCutoff = existingPool.resurrectTimeoutFactorCutoff
		metrics = existingPool.metrics
	}

	flatPool := &statusConnectionPool{
		resurrectTimeoutInitial:      resurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
		metrics:                      metrics,
	}
	flatPool.mu.live = flatLiveConns
	flatPool.mu.dead = flatDeadConns
	return flatPool
}

func (c *Client) getNodesInfo(ctx context.Context) ([]nodeInfo, error) {
	scheme := c.urls[0].Scheme

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_nodes/http", nil)
	if err != nil {
		return nil, err
	}

	conn, err := getConnectionFromPool(c, req)
	// TODO: If no connection is returned, fallback to original URLs
	if err != nil {
		return nil, err
	}

	c.setReqURL(conn.URL, req)
	c.setReqAuth(conn.URL, req)
	c.setReqUserAgent(req)

	res, err := c.transport.RoundTrip(req)
	if err != nil {
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
