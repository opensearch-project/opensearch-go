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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

func TestDiscoveryIntegration(t *testing.T) {
	// Use standardized URL construction and config
	u := getTestURL(t)

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
	assert.Equal(t, "data", RoleData)
	assert.Equal(t, "ingest", RoleIngest)
	assert.Equal(t, "master", RoleMaster)
	assert.Equal(t, "cluster_manager", RoleClusterManager)
	assert.Equal(t, "remote_cluster_client", RoleRemoteClusterClient)
	assert.Equal(t, "search", RoleSearch)
	assert.Equal(t, "warm", RoleWarm)
	assert.Equal(t, "ml", RoleML)
	assert.Equal(t, "coordinating_only", RoleCoordinatingOnly)
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
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestRoleSetHas verifies O(1) role lookups
func TestRoleSetHas(t *testing.T) {
	rs := newRoleSet([]string{RoleData, RoleClusterManager, RoleIngest})

	assert.True(t, rs.has(RoleData))
	assert.True(t, rs.has(RoleClusterManager))
	assert.True(t, rs.has(RoleIngest))
	assert.False(t, rs.has(RoleMaster))
	assert.False(t, rs.has(RoleSearch))
	assert.False(t, rs.has("nonexistent"))
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
			assert.Equal(t, tt.expectClusterManager, isClusterManagerEligible)
			assert.Equal(t, tt.expectData, rs.has(RoleData))
			assert.Equal(t, tt.expectIngest, rs.has(RoleIngest))
			assert.Equal(t, tt.expectSearch, rs.has(RoleSearch))
			assert.Equal(t, tt.expectWarm, rs.has(RoleWarm))
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
			assert.Equal(t, tt.shouldSkip, result)
		})
	}
}

// TestDiscoverNodesWithNewRoleValidation verifies the enhanced discovery behavior
func TestDiscoverNodesWithNewRoleValidation(t *testing.T) {
	tests := []struct {
		name            string
		nodes           map[string][]string // nodeName -> roles
		expectedNodes   []string            // nodes that should be included
		expectedSkipped []string            // nodes that should be skipped
	}{
		{
			"mixed node types with validation",
			map[string][]string{
				"cm-only":     {RoleClusterManager},           // should be skipped
				"master-only": {RoleMaster},                   // should be skipped
				"data-node":   {RoleData},                     // should be included
				"mixed-good":  {RoleClusterManager, RoleData}, // should be included
				"search-only": {RoleSearch},                   // should be included
			},
			[]string{"data-node", "mixed-good", "search-only"},
			[]string{"cm-only", "master-only"},
		},
		{
			"OpenSearch 3.X compliant setup",
			map[string][]string{
				"dedicated-cm": {RoleClusterManager},   // should be skipped
				"data-hot":     {RoleData, RoleIngest}, // should be included
				"data-warm":    {RoleWarm, RoleData},   // should be included
				"search-node":  {RoleSearch},           // should be included
				"coordinating": {RoleCoordinatingOnly}, // should be included
			},
			[]string{"data-hot", "data-warm", "search-node", "coordinating"},
			[]string{"dedicated-cm"},
		},
		{
			"cluster manager and remote cluster client filtering",
			map[string][]string{
				"cm-rcc":    {RoleClusterManager, RoleRemoteClusterClient}, // should be skipped
				"cm-data":   {RoleClusterManager, RoleData},                // should be included
				"rcc-only":  {RoleRemoteClusterClient},                     // should be included
				"data-node": {RoleData},                                    // should be included
			},
			[]string{"cm-data", "rcc-only", "data-node"},
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
			pool, ok := c.mu.connectionPool.(*statusConnectionPool)
			require.True(t, ok, "Expected statusConnectionPool")

			// Check that expected nodes are included
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.live {
				actualNodes[conn.Name] = true
			}

			assert.Len(t, actualNodes, len(tt.expectedNodes),
				"Expected %d nodes but got %d: %v", len(tt.expectedNodes), len(actualNodes), actualNodes)

			for _, expectedNode := range tt.expectedNodes {
				assert.True(t, actualNodes[expectedNode],
					"Expected node %q to be included but it wasn't", expectedNode)
			}

			for _, skippedNode := range tt.expectedSkipped {
				assert.False(t, actualNodes[skippedNode],
					"Expected node %q to be skipped but it was included", skippedNode)
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
		expectedIncluded                []string            // nodes that should be included
		expectedExcluded                []string            // nodes that should be excluded
	}{
		{
			name:                            "IncludeDedicatedClusterManagers enabled - includes all nodes",
			includeDedicatedClusterManagers: true,
			nodes: map[string][]string{
				"cm-only":   {RoleClusterManager},
				"data-node": {RoleData},
			},
			expectedIncluded: []string{"cm-only", "data-node"},
			expectedExcluded: []string{},
		},
		{
			name:                            "IncludeDedicatedClusterManagers disabled (default) - excludes dedicated CM nodes",
			includeDedicatedClusterManagers: false,
			nodes: map[string][]string{
				"cm-only":   {RoleClusterManager},
				"data-node": {RoleData},
				"dummy":     {RoleData}, // Add second node to avoid single connection pool
			},
			expectedIncluded: []string{"data-node", "dummy"},
			expectedExcluded: []string{"cm-only"},
		},
		{
			name:                            "Mixed roles with CM always included regardless of setting",
			includeDedicatedClusterManagers: false,
			nodes: map[string][]string{
				"cm-data": {RoleClusterManager, RoleData},
				"dummy":   {RoleData}, // Add second node to avoid single connection pool
			},
			expectedIncluded: []string{"cm-data", "dummy"},
			expectedExcluded: []string{},
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

			// Perform discovery
			err = c.DiscoverNodes(t.Context())
			require.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*statusConnectionPool)
			require.True(t, ok, "Expected statusConnectionPool")

			// Check included nodes
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.live {
				actualNodes[conn.Name] = true
			}

			for _, expectedNode := range tt.expectedIncluded {
				assert.True(t, actualNodes[expectedNode],
					"Expected node %q to be included but it wasn't", expectedNode)
			}

			for _, excludedNode := range tt.expectedExcluded {
				assert.False(t, actualNodes[excludedNode],
					"Expected node %q to be excluded but it was included", excludedNode)
			}

			// Verify total count
			expectedTotal := len(tt.expectedIncluded)
			assert.Len(t, actualNodes, expectedTotal,
				"Expected %d nodes but got %d", expectedTotal, len(actualNodes))
		})
	}
}

// TestRolePolicies tests the router+policy with various configurations
func TestRolePolicies(t *testing.T) {
	// Create test connections with different roles
	connections := []*Connection{
		{Name: "data-node", URL: &url.URL{Host: "data:9200"}, Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", URL: &url.URL{Host: "ingest:9200"}, Roles: newRoleSet([]string{RoleIngest})},
		{Name: "data-ingest-node", URL: &url.URL{Host: "data-ingest:9200"}, Roles: newRoleSet([]string{RoleData, RoleIngest})},
		{Name: "cluster-manager-node", URL: &url.URL{Host: "cm:9200"}, Roles: newRoleSet([]string{RoleClusterManager})},
		{Name: "warm-node", URL: &url.URL{Host: "warm:9200"}, Roles: newRoleSet([]string{RoleWarm})},
		{Name: "search-node", URL: &url.URL{Host: "search:9200"}, Roles: newRoleSet([]string{RoleSearch})},
		{Name: "coordinating-node", URL: &url.URL{Host: "coord:9200"}, Roles: newRoleSet([]string{})}, // No specific roles
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
		pool, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, pool)

		// Connections are initially dead, need to simulate health checks
		// Get a connection (zombie) and mark it as successful
		conn, err := pool.Next()
		require.NoError(t, err)
		// Should get either "ingest-node" or "data-ingest-node"
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)

		// Simulate successful health check to move connection to live pool
		statusPool := pool.(*statusConnectionPool)
		statusPool.OnSuccess(conn)

		// Now get connection from live pool
		liveConn, err := pool.Next()
		require.NoError(t, err)
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, liveConn.Name)

		// Test with data-only connections (no ingest nodes)
		dataOnlyConns := []*Connection{
			{Name: "data-node", URL: &url.URL{Host: "data:9200"}, Roles: newRoleSet([]string{RoleData})},                    // No ingest
			{Name: "cluster-manager-node", URL: &url.URL{Host: "cm:9200"}, Roles: newRoleSet([]string{RoleClusterManager})}, // No ingest
		}

		// Adding non-matching connections should not affect the policy
		// The role matching logic should filter them out entirely
		err = policy.DiscoveryUpdate(dataOnlyConns, nil, nil)
		require.NoError(t, err)

		// With proper role matching, non-ingest connections should not be added at all
		// So the policy should remain enabled with only the original ingest connections
		require.True(t, policy.IsEnabled()) // Should remain true (original ingest connections still there)

		// Get a fresh pool after the update
		pool2, err2 := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err2)
		require.NotNil(t, pool2) // Should not be nil

		// Should still get ingest connections, not the data-only ones
		finalConn, err := pool2.Next()
		require.NoError(t, err)
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, finalConn.Name)
	})
}
