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

//go:build integration

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

func TestDiscoveryIntegration(t *testing.T) {
	// Verify cluster is reachable with the configured scheme
	testutil.WaitForCluster(t)

	// OpenSearch < 2.2.0 with the security plugin has a non-thread-safe User
	// serialization race (java.io.OptionalDataException) during inter-node
	// transport. Fixed in 2.2.0 by opensearch-project/security#1970.
	if testutil.IsSecure(t) {
		testutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	// Use standardized URL construction and config
	u := testutil.GetTestURL(t)

	t.Run("DiscoverNodes with health validation", func(t *testing.T) {
		cfg := getTestConfig(t, []*url.URL{u})
		client, err := New(cfg)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Discovery should work with health validation
		err = client.DiscoverNodes(t.Context())
		if err != nil {
			t.Errorf("DiscoverNodes() failed: %v", err)
		}

		// Should have at least one connection after discovery
		urls := client.URLs()
		if len(urls) == 0 {
			t.Error("Expected at least one URL after discovery")
		}

		t.Logf("Discovered %d nodes", len(urls))
	})

	t.Run("Role based nodes discovery with health validation", func(t *testing.T) {
		cfg := getTestConfig(t, []*url.URL{u})
		client, err := New(cfg)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Test discovery with role filtering
		err = client.DiscoverNodes(t.Context())
		if err != nil {
			t.Errorf("DiscoverNodes() failed: %v", err)
		}

		// Get the actual discovered connections for role testing
		urls := client.URLs()
		t.Logf("Role-based discovery found %d nodes", len(urls))

		// In a real cluster, we should have at least one data/coordinator node
		// (cluster_manager-only nodes are filtered out)
		if len(urls) == 0 {
			t.Error("Expected at least one non-cluster_manager-only node")
		}
	})
}

// TestRoleConstants verifies that role constants match expected values
func TestRoleConstants(t *testing.T) {
	require.Equal(t, "data", RoleData)
	require.Equal(t, "ingest", RoleIngest)
	require.Equal(t, "master", RoleMaster)
	require.Equal(t, "cluster_manager", RoleClusterManager)
	require.Equal(t, "remote_cluster_client", RoleRemoteClusterClient)
	require.Equal(t, "search", RoleSearch)
	require.Equal(t, "warm", RoleWarm)
	require.Equal(t, "ml", RoleML)
	require.Equal(t, "coordinating_only", RoleCoordinatingOnly)
}

// TestNewRoleSet verifies efficient role set creation
func TestNewRoleSet(t *testing.T) {
	tests := []struct {
		name  string
		roles []string
		want  roleSet
	}{
		{
			"empty roles",
			[]string{},
			roleSet{},
		},
		{
			"single role",
			[]string{RoleData},
			roleSet{RoleData: {}},
		},
		{
			"multiple roles",
			[]string{RoleData, RoleIngest, RoleClusterManager},
			roleSet{
				RoleData:           {},
				RoleIngest:         {},
				RoleClusterManager: {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newRoleSet(tt.roles)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestRoleSetHas verifies O(1) role lookups
func TestRoleSetHas(t *testing.T) {
	rs := newRoleSet([]string{RoleData, RoleClusterManager, RoleIngest})

	require.True(t, rs.has(RoleData))
	require.True(t, rs.has(RoleClusterManager))
	require.True(t, rs.has(RoleIngest))
	require.False(t, rs.has(RoleMaster))
	require.False(t, rs.has(RoleSearch))
	require.False(t, rs.has("nonexistent"))
}

// TestRoleCheckFunctions verifies role-specific check functions
func TestRoleCheckFunctions(t *testing.T) {
	tests := []struct {
		name                 string
		roles                []string
		expectClusterManager bool
		expectData           bool
		expectIngest         bool
		expectSearch         bool
		expectWarm           bool
	}{
		{
			"cluster manager eligible with cluster_manager role",
			[]string{RoleClusterManager},
			true, false, false, false, false,
		},
		{
			"cluster manager eligible with deprecated master role",
			[]string{RoleMaster},
			true, false, false, false, false,
		},
		{
			"data node",
			[]string{RoleData},
			false, true, false, false, false,
		},
		{
			"ingest node",
			[]string{RoleIngest},
			false, false, true, false, false,
		},
		{
			"search node",
			[]string{RoleSearch},
			false, false, false, true, false,
		},
		{
			"warm node",
			[]string{RoleWarm},
			false, false, false, false, true,
		},
		{
			"mixed roles",
			[]string{RoleData, RoleIngest, RoleClusterManager},
			true, true, true, false, false,
		},
		{
			"warm and data roles",
			[]string{RoleWarm, RoleData},
			false, true, false, false, true,
		},
		{
			"no roles",
			[]string{},
			false, false, false, false, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := newRoleSet(tt.roles)

			// Check cluster manager eligibility
			isClusterManagerEligible := rs.has(RoleMaster) || rs.has(RoleClusterManager)
			require.Equal(t, tt.expectClusterManager, isClusterManagerEligible)
			require.Equal(t, tt.expectData, rs.has(RoleData))
			require.Equal(t, tt.expectIngest, rs.has(RoleIngest))
			require.Equal(t, tt.expectSearch, rs.has(RoleSearch))
			require.Equal(t, tt.expectWarm, rs.has(RoleWarm))
		})
	}
}

// TestShouldSkipDedicatedClusterManagers verifies upstream-compatible node selection
func TestShouldSkipDedicatedClusterManagers(t *testing.T) {
	tests := []struct {
		name       string
		roles      []string
		shouldSkip bool
	}{
		{
			"cluster_manager only - should skip",
			[]string{RoleClusterManager},
			true,
		},
		{
			"master only - should skip (deprecated)",
			[]string{RoleMaster},
			true,
		},
		{
			"cluster_manager with data - should not skip",
			[]string{RoleClusterManager, RoleData},
			false,
		},
		{
			"cluster_manager with ingest - should not skip",
			[]string{RoleClusterManager, RoleIngest},
			false,
		},
		{
			"cluster_manager with warm - should not skip (OpenSearch 3.0 searchable snapshots)",
			[]string{RoleClusterManager, RoleWarm},
			false,
		},
		{
			"cluster_manager with data and ingest - should not skip",
			[]string{RoleClusterManager, RoleData, RoleIngest},
			false,
		},
		{
			"data only - should not skip",
			[]string{RoleData},
			false,
		},
		{
			"ingest only - should not skip",
			[]string{RoleIngest},
			false,
		},
		{
			"search only - should not skip",
			[]string{RoleSearch},
			false,
		},
		{
			"warm only - should not skip",
			[]string{RoleWarm},
			false,
		},
		{
			"warm and data - should not skip",
			[]string{RoleWarm, RoleData},
			false,
		},
		{
			"ml only - should not skip",
			[]string{RoleML},
			false,
		},
		{
			"cluster_manager with ml - should not skip",
			[]string{RoleClusterManager, RoleML},
			false,
		},
		{
			"master with remote_cluster_client - should skip",
			[]string{RoleMaster, RoleRemoteClusterClient},
			true,
		},
		{
			"cluster_manager with remote_cluster_client - should skip",
			[]string{RoleClusterManager, RoleRemoteClusterClient},
			true,
		},
		{
			"no roles - should not skip",
			[]string{},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := newRoleSet(tt.roles)
			result := rs.isDedicatedClusterManager()
			require.Equal(t, tt.shouldSkip, result)
		})
	}
}

// TestDiscoverNodesWithNewRoleValidation verifies the enhanced discovery behavior
func TestDiscoverNodesWithNewRoleValidation(t *testing.T) {
	tests := []struct {
		name  string
		nodes map[string][]string // nodeName -> roles
		// expectedInInventory lists nodes that must appear in the allConns pool,
		// which holds every discovered node regardless of role.
		expectedInInventory []string
		// expectedNotRoutable lists dedicated cluster managers that must not be
		// selected for request routing.
		expectedNotRoutable []string
	}{
		{
			"mixed node types with validation",
			map[string][]string{
				"cm-only":     {RoleClusterManager},           // dedicated CM: not routable
				"master-only": {RoleMaster},                   // dedicated CM: not routable
				"data-node":   {RoleData},                     // routable
				"mixed-good":  {RoleClusterManager, RoleData}, // routable
				"search-only": {RoleSearch},                   // routable
			},
			[]string{"cm-only", "master-only", "data-node", "mixed-good", "search-only"},
			[]string{"cm-only", "master-only"},
		},
		{
			"OpenSearch 3.X compliant setup",
			map[string][]string{
				"dedicated-cm": {RoleClusterManager},   // dedicated CM: not routable
				"data-hot":     {RoleData, RoleIngest}, // routable
				"data-warm":    {RoleWarm, RoleData},   // routable
				"search-node":  {RoleSearch},           // routable
				"coordinating": {RoleCoordinatingOnly}, // routable
			},
			[]string{"dedicated-cm", "data-hot", "data-warm", "search-node", "coordinating"},
			[]string{"dedicated-cm"},
		},
		{
			"cluster manager and remote cluster client filtering",
			map[string][]string{
				"cm-rcc":    {RoleClusterManager, RoleRemoteClusterClient}, // dedicated CM: not routable
				"cm-data":   {RoleClusterManager, RoleData},                // routable
				"rcc-only":  {RoleRemoteClusterClient},                     // routable
				"data-node": {RoleData},                                    // routable
			},
			[]string{"cm-rcc", "cm-data", "rcc-only", "data-node"},
			[]string{"cm-rcc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock transport with standard handlers for discovery testing
			transport := mockhttp.NewTransportFromRoutes(t, mockhttp.GetDefaultHandlersWithNodes(t, tt.nodes))

			u := mockhttp.GetOpenSearchURL(t)
			c, err := New(Config{
				URLs:      []*url.URL{u},
				Transport: transport,
			})
			require.NoError(t, err)

			// Perform discovery
			err = c.DiscoverNodes(t.Context())
			require.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*multiServerPool)
			require.Truef(t, ok, "Expected multiServerPool but got %T with URLs: %v",
				c.mu.connectionPool, c.mu.connectionPool.URLs())

			// allConns holds every discovered node regardless of role (in either the
			// ready or dead list, since newly discovered nodes start dead pending
			// health checks). Dedicated cluster managers stay in the inventory so
			// discovery can reuse and evict them symmetrically; they are excluded at
			// selection time instead.
			pool.mu.RLock()
			inventory := make(map[string]struct{}, len(pool.mu.ready)+len(pool.mu.dead))
			for _, conn := range pool.mu.ready {
				inventory[conn.Name] = struct{}{}
			}
			for _, conn := range pool.mu.dead {
				inventory[conn.Name] = struct{}{}
			}
			pool.mu.RUnlock()

			require.Len(t, inventory, len(tt.expectedInInventory),
				"Expected %d nodes in inventory but got %d: %v",
				len(tt.expectedInInventory), len(inventory), inventory)

			for _, expectedNode := range tt.expectedInInventory {
				_, ok := inventory[expectedNode]
				require.True(t, ok,
					"Expected node %q in the connection inventory but it wasn't", expectedNode)
			}

			// Dedicated cluster managers stay in the inventory but must not be handed
			// out for routing. With no router, routing uses the inventory pool's
			// Next(), which skips them.
			for _, dcm := range tt.expectedNotRoutable {
				for i := 0; i < len(tt.nodes)*4; i++ {
					conn, nextErr := pool.Next()
					if nextErr != nil {
						break
					}
					require.NotEqual(t, dcm, conn.Name,
						"Dedicated cluster manager %q must not be selected for routing", dcm)
				}
			}
		})
	}
}

// TestIncludeDedicatedClusterManagersConfiguration verifies the configurable behavior
func TestIncludeDedicatedClusterManagersConfiguration(t *testing.T) {
	tests := []struct {
		name                            string
		includeDedicatedClusterManagers bool
		nodes                           map[string][]string // nodeName -> roles
		// expectedInInventory lists nodes that must appear in the allConns pool,
		// which holds every discovered node regardless of role.
		expectedInInventory []string
		// expectedNotRoutable lists dedicated cluster managers that must not be
		// selected for request routing.
		expectedNotRoutable []string
	}{
		{
			name:                            "IncludeDedicatedClusterManagers enabled - all nodes routable",
			includeDedicatedClusterManagers: true,
			nodes: map[string][]string{
				"cm-only":   {RoleClusterManager},
				"data-node": {RoleData},
			},
			expectedInInventory: []string{"cm-only", "data-node"},
			expectedNotRoutable: []string{},
		},
		{
			name:                            "IncludeDedicatedClusterManagers disabled (default) - dedicated CM in inventory but not routable",
			includeDedicatedClusterManagers: false,
			nodes: map[string][]string{
				"cm-only":   {RoleClusterManager},
				"data-node": {RoleData},
				"dummy":     {RoleData}, // Add second node to avoid single connection pool
			},
			expectedInInventory: []string{"cm-only", "data-node", "dummy"},
			expectedNotRoutable: []string{"cm-only"},
		},
		{
			name:                            "Mixed roles with CM always routable regardless of setting",
			includeDedicatedClusterManagers: false,
			nodes: map[string][]string{
				"cm-data": {RoleClusterManager, RoleData},
				"dummy":   {RoleData}, // Add second node to avoid single connection pool
			},
			expectedInInventory: []string{"cm-data", "dummy"},
			expectedNotRoutable: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock transport with standard handlers for discovery testing
			transport := mockhttp.NewTransportFromRoutes(t, mockhttp.GetDefaultHandlersWithNodes(t, tt.nodes))

			u := mockhttp.GetOpenSearchURL(t)
			c, err := New(Config{
				URLs:                            []*url.URL{u},
				Transport:                       transport,
				IncludeDedicatedClusterManagers: tt.includeDedicatedClusterManagers,
			})
			require.NoError(t, err)

			// Run discovery repeatedly. The inventory must converge to exactly one
			// connection per node and stay there: an unbounded pool that re-created
			// connections each cycle (the dedicated-cluster-manager leak) would grow
			// with every iteration. Asserting the exact length on every cycle is the
			// regression guard.
			const cycles = 5
			var pool *multiServerPool
			for cycle := 1; cycle <= cycles; cycle++ {
				require.NoErrorf(t, c.DiscoverNodes(t.Context()), "discovery cycle %d", cycle)

				var ok bool
				pool, ok = c.mu.connectionPool.(*multiServerPool)
				require.Truef(t, ok, "Expected multiServerPool but got %T with URLs: %v",
					c.mu.connectionPool, c.mu.connectionPool.URLs())

				pool.mu.RLock()
				readyLen := len(pool.mu.ready)
				deadLen := len(pool.mu.dead)
				membersLen := len(pool.mu.members)
				inventory := make(map[string]struct{}, readyLen+deadLen)
				for _, conn := range pool.mu.ready {
					inventory[conn.Name] = struct{}{}
				}
				for _, conn := range pool.mu.dead {
					inventory[conn.Name] = struct{}{}
				}
				pool.mu.RUnlock()

				require.Equalf(t, len(tt.nodes), readyLen+deadLen,
					"cycle %d: inventory connection count (ready=%d dead=%d)", cycle, readyLen, deadLen)
				require.Equalf(t, readyLen+deadLen, membersLen,
					"cycle %d: members map must match ready+dead", cycle)
				require.Lenf(t, inventory, len(tt.nodes),
					"cycle %d: one connection per node (no duplicates)", cycle)
				for _, expectedNode := range tt.expectedInInventory {
					_, present := inventory[expectedNode]
					require.Truef(t, present,
						"cycle %d: expected node %q in the connection inventory", cycle, expectedNode)
				}
			}

			// Dedicated cluster managers stay in the inventory but must not be
			// handed out for routing. With no router, routing uses the inventory
			// pool's Next(), which skips them.
			for _, dcm := range tt.expectedNotRoutable {
				for i := 0; i < len(tt.nodes)*4; i++ {
					conn, nextErr := pool.Next()
					if nextErr != nil {
						break
					}
					require.NotEqual(t, dcm, conn.Name,
						"Dedicated cluster manager %q must not be selected for routing", dcm)
				}
			}
		})
	}
}

// TestRolePolicies tests the router+policy with various configurations
func TestRolePolicies(t *testing.T) {
	// Create test connections with different roles. Mark them viable: they
	// stand in for nodes already proven reachable, so availableForRouting
	// (which gates on lcViable for non-seed connections) keeps them eligible.
	connections := []*Connection{
		{Name: "data-node", URL: &url.URL{Host: "data:9200"}, Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", URL: &url.URL{Host: "ingest:9200"}, Roles: newRoleSet([]string{RoleIngest})},
		{Name: "data-ingest-node", URL: &url.URL{Host: "data-ingest:9200"}, Roles: newRoleSet([]string{RoleData, RoleIngest})},
		{Name: "cluster-manager-node", URL: &url.URL{Host: "cm:9200"}, Roles: newRoleSet([]string{RoleClusterManager})},
		{Name: "warm-node", URL: &url.URL{Host: "warm:9200"}, Roles: newRoleSet([]string{RoleWarm})},
		{Name: "search-node", URL: &url.URL{Host: "search:9200"}, Roles: newRoleSet([]string{RoleSearch})},
		{Name: "coordinating-node", URL: &url.URL{Host: "coord:9200"}, Roles: newRoleSet([]string{})}, // No specific roles
	}
	for _, c := range connections {
		c.setLifecycleBit(lcViable) //nolint:errcheck // fresh conn, no concurrent access
	}

	t.Run("IngestPolicy", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleIngest)
		require.NoError(t, err)

		// Configure pool factories for the policy (needed for tests that create policies directly)
		err = configureTestPolicySettings(t, policy)
		require.NoError(t, err)

		// Update with connections
		err = policy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)

		// Should be enabled (has ingest nodes)
		require.True(t, policy.IsEnabled())

		// Should prefer ingest nodes
		hop, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, hop.Conn)

		// Should get either "ingest-node" or "data-ingest-node"
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, hop.Conn.Name)

		// Simulate successful health check to move connection to ready pool.
		// Access the underlying pool directly since Eval now returns NextHop.
		rolePolicy := policy.(*RolePolicy)
		rolePolicy.pool.OnSuccess(hop.Conn)

		// Now get connection from ready pool
		hop2, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, hop2.Conn)
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, hop2.Conn.Name)

		// Test with data-only connections (no ingest nodes)
		dataOnlyConns := []*Connection{
			{Name: "data-node", URL: &url.URL{Host: "data:9200"}, Roles: newRoleSet([]string{RoleData})},                    // No ingest
			{Name: "cluster-manager-node", URL: &url.URL{Host: "cm:9200"}, Roles: newRoleSet([]string{RoleClusterManager})}, // No ingest
		}
		for _, c := range dataOnlyConns {
			c.setLifecycleBit(lcViable) //nolint:errcheck // fresh conn, no concurrent access
		}

		// Adding non-matching connections should not affect the policy
		// The role matching logic should filter them out entirely
		err = policy.DiscoveryUpdate(dataOnlyConns, nil, nil)
		require.NoError(t, err)

		// With proper role matching, non-ingest connections should not be added at all
		// So the policy should remain enabled with only the original ingest connections
		require.True(t, policy.IsEnabled()) // Should remain true (original ingest connections still there)

		// Get a fresh hop after the update
		hop3, err3 := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err3)
		require.NotNil(t, hop3.Conn) // Should not be nil

		// Should still get ingest connections, not the data-only ones
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, hop3.Conn.Name)
	})
}
