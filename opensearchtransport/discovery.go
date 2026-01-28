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

	// Check if it has any "work" roles that make it non-dedicated
	workRoles := []string{
		RoleData,   // stores and retrieves data
		RoleIngest, // processes incoming data
		RoleWarm,   // handles warm/cold data storage
		RoleSearch, // dedicated search processing
		RoleML,     // machine learning tasks
	}

	return !slices.ContainsFunc(workRoles, rs.has)
}

// Discoverable defines the interface for transports supporting node discovery.
type Discoverable interface {
	DiscoverNodes() error
}

// nodeInfo represents the information about node in a cluster.
type nodeInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	URL        *url.URL `json:"url"`
	Roles      []string `json:"roles"`
	roleSet    roleSet
	Attributes map[string]any `json:"attributes"`
	HTTP       struct {
		PublishAddress string `json:"publish_address"`
	} `json:"http"`
}

// DiscoverNodes reloads the client connections by fetching information from the cluster.
func (c *Client) DiscoverNodes() error {
	conns := make([]*Connection, 0)

	nodes, err := c.getNodesInfo()
	if err != nil {
		if debugLogger != nil {
			debugLogger.Logf("Error getting nodes info: %s\n", err)
		}

		return fmt.Errorf("discovery: get nodes: %w", err)
	}

	for _, node := range nodes {
		// Build role set for efficient O(1) lookups
		node.roleSet = newRoleSet(node.Roles)

		// Skip this node if the user wants to exclude cluster managers (default) and this node is a dedicated cluster master.
		shouldSkip := !c.includeDedicatedClusterManagers && node.roleSet.isDedicatedClusterManager()

		if debugLogger != nil {
			var skip string
			if shouldSkip {
				skip = "; [SKIP: dedicated cluster manager]"
			}

			debugLogger.Logf("Discovered node %q; %s; roles=%v%s\n", node.Name, node.URL, node.Roles, skip)
		}

		// Skip dedicated cluster managers (matching upstream Java client behavior)
		if shouldSkip {
			continue
		}

		conns = append(conns, &Connection{
			URL:        node.URL,
			ID:         node.ID,
			Name:       node.Name,
			Roles:      node.Roles,
			Attributes: node.Attributes,
		})
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if lockable, ok := c.mu.pool.(sync.Locker); ok {
		lockable.Lock()
		defer lockable.Unlock()
	}

	if c.poolFunc != nil {
		c.mu.pool = c.poolFunc(conns, c.selector)
	} else {
		// TODO: Replace only live connections, leave dead scheduled for resurrect?
		c.mu.pool = NewConnectionPool(conns, c.selector)
	}

	return nil
}

func (c *Client) getNodesInfo() ([]nodeInfo, error) {
	scheme := c.urls[0].Scheme

	req, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, "/_nodes/http", nil)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	conn, err := c.mu.pool.Next()
	c.mu.Unlock()
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
		node.URL = u
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
	go c.DiscoverNodes()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mu.discoverNodesTimer != nil {
		c.mu.discoverNodesTimer.Stop()
	}

	c.mu.discoverNodesTimer = time.AfterFunc(c.discoverNodesInterval, func() {
		c.scheduleDiscoverNodes()
	})
}
