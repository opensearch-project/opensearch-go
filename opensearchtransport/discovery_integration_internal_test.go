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

//go:build integration && (core || opensearchtransport)

package opensearchtransport

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

func TestDiscoveryIntegration(t *testing.T) {
	// Use standardized URL construction
	u := mockhttp.GetOpenSearchURL(t)

	t.Run("DiscoverNodes with health validation", func(t *testing.T) {
		client, err := New(Config{
			URLs: []*url.URL{u},
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Discovery should work with health validation
		err = client.DiscoverNodes()
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
		client, err := New(Config{
			URLs: []*url.URL{u},
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Test discovery with role filtering
		err = client.DiscoverNodes()
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
			err = c.DiscoverNodes()
			require.NoError(t, err)

			// Verify results
			pool, ok := c.mu.pool.(*statusConnectionPool)
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
			err = c.DiscoverNodes()
			require.NoError(t, err)

			// Verify results
			pool, ok := c.mu.pool.(*statusConnectionPool)
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

// TestGenericRoleBasedSelector tests the new generic role-based selector
func TestGenericRoleBasedSelector(t *testing.T) {
	connections := []*Connection{
		{Name: "data-node", Roles: []string{RoleData}},
		{Name: "ingest-node", Roles: []string{RoleIngest}},
		{Name: "data-ingest-node", Roles: []string{RoleData, RoleIngest}},
		{Name: "cluster-manager-node", Roles: []string{RoleClusterManager}},
		{Name: "coordinating-node", Roles: []string{}}, // No specific roles
	}

	fallback := &mockSelector{}

	t.Run("Generic selector with required roles", func(t *testing.T) {
		// Create a selector that requires either data or ingest roles
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleData, RoleIngest),
		)

		conn, err := selector.Select(connections)
		require.NoError(t, err)
		// Should match one of: data-node, ingest-node, or data-ingest-node
		assert.Contains(t, []string{"data-node", "ingest-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("Generic selector with excluded roles", func(t *testing.T) {
		// Create a selector that excludes cluster manager roles
		selector := NewRoleBasedSelector(
			WithExcludedRoles(RoleClusterManager),
		)

		conn, err := selector.Select(connections)
		require.NoError(t, err)
		// Should NOT be the cluster-manager-node
		assert.NotEqual(t, "cluster-manager-node", conn.Name)
	})

	t.Run("Generic selector strict mode", func(t *testing.T) {
		// Create a strict selector that only allows warm nodes
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleWarm),
			WithStrictMode(),
		)

		conn, err := selector.Select(connections)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "no connections found matching required roles")
	})

	t.Run("Options pattern flexibility", func(t *testing.T) {
		// Test that options pattern allows flexible configuration
		ingestSelector := NewRoleBasedSelector(
			WithRequiredRoles(RoleIngest),
			WithFallback(fallback),
		)

		strictIngestSelector := NewRoleBasedSelector(
			WithRequiredRoles(RoleWarm), // Try warm nodes (which don't exist)
			WithStrictMode(),
		)

		conn1, err1 := ingestSelector.Select(connections)
		conn2, err2 := strictIngestSelector.Select(connections)

		require.NoError(t, err1)
		require.Error(t, err2) // Strict mode should fail with no warm nodes
		assert.Nil(t, conn2)
		// Fallback selector should return ingest-capable nodes
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn1.Name)
	})
}

// TestRoleBasedSelectors tests the role-based selector with various configurations
func TestRoleBasedSelectors(t *testing.T) {
	// Create test connections with different roles
	connections := []*Connection{
		{Name: "data-node", Roles: []string{RoleData}},
		{Name: "ingest-node", Roles: []string{RoleIngest}},
		{Name: "data-ingest-node", Roles: []string{RoleData, RoleIngest}},
		{Name: "cluster-manager-node", Roles: []string{RoleClusterManager}},
		{Name: "warm-node", Roles: []string{RoleWarm}},
		{Name: "search-node", Roles: []string{RoleSearch}},
		{Name: "coordinating-node", Roles: []string{}}, // No specific roles
	}

	// Mock fallback selector that just returns the first connection
	fallback := &mockSelector{}

	t.Run("IngestPreferred", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleIngest),
			WithFallback(fallback),
		)

		// Should prefer ingest nodes
		conn, err := selector.Select(connections)
		require.NoError(t, err)
		// Should get either "ingest-node" or "data-ingest-node"
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)

		// Should fall back when no ingest nodes available
		dataOnlyConns := []*Connection{connections[0], connections[3]} // data and cluster-manager
		conn, err = selector.Select(dataOnlyConns)
		require.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Fallback should work
	})

	t.Run("DataPreferred", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleData),
			WithFallback(fallback),
		)

		conn, err := selector.Select(connections)
		require.NoError(t, err)
		// Should get a data-capable node
		assert.Contains(t, []string{"data-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("WarmPreferred", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleWarm),
			WithFallback(fallback),
		)

		conn, err := selector.Select(connections)
		require.NoError(t, err)
		assert.Equal(t, "warm-node", conn.Name)

		// Should fall back when no warm nodes available
		noWarmConns := []*Connection{connections[0], connections[1]} // data and ingest
		conn, err = selector.Select(noWarmConns)
		require.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Fallback should work
	})

	t.Run("IngestOnly", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleIngest),
			WithStrictMode(),
		)

		// Should work when ingest nodes are available
		conn, err := selector.Select(connections)
		require.NoError(t, err)
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)

		// Should fail when no ingest nodes available (strict mode)
		dataOnlyConns := []*Connection{connections[0], connections[3]} // data and cluster-manager
		conn, err = selector.Select(dataOnlyConns)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "no connections found matching required roles")
	})
}

// TestSmartSelector tests the request-aware smart selector
func TestSmartSelector(t *testing.T) {
	// Create test connections
	connections := []*Connection{
		{Name: "data-node", Roles: []string{RoleData}},
		{Name: "ingest-node", Roles: []string{RoleIngest}},
		{Name: "data-ingest-node", Roles: []string{RoleData, RoleIngest}},
	}

	fallback := &mockSelector{}
	selector := NewSmartSelector(fallback)

	t.Run("IngestOperationRouting", func(t *testing.T) {
		req := &mockRequest{
			method: http.MethodPost,
			path:   "/my-index/_bulk",
		}

		conn, err := selector.SelectForRequest(connections, req)
		require.NoError(t, err)
		// Should route to ingest-capable node for bulk operations
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("SearchOperationRouting", func(t *testing.T) {
		req := &mockRequest{
			method: http.MethodPost,
			path:   "/my-index/_search",
		}

		conn, err := selector.SelectForRequest(connections, req)
		require.NoError(t, err)
		// Should route to data-capable node for search operations
		assert.Contains(t, []string{"data-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("DefaultOperationRouting", func(t *testing.T) {
		req := &mockRequest{
			method: http.MethodGet,
			path:   "/_cluster/health",
		}

		conn, err := selector.SelectForRequest(connections, req)
		require.NoError(t, err)
		// Should use default routing
		assert.Equal(t, "data-node", conn.Name) // Mock selector returns first connection
	})

	t.Run("BasicSelectorInterface", func(t *testing.T) {
		// Test that SmartSelector implements basic Selector interface
		conn, err := selector.Select(connections)
		require.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Should use default selector
	})
}

// TestRequestAwareConnectionPool tests the enhanced connection pool
func TestRequestAwareConnectionPool(t *testing.T) {
	connections := []*Connection{
		{Name: "data-node", Roles: []string{RoleData}},
		{Name: "ingest-node", Roles: []string{RoleIngest}},
	}

	smartSelector := NewSmartSelector(&mockSelector{})
	pool := NewConnectionPool(connections, smartSelector)

	racp, ok := pool.(RequestAwareConnectionPool)
	assert.True(t, ok, "Should implement RequestAwareConnectionPool")

	t.Run("NextForRequest", func(t *testing.T) {
		req := &mockRequest{
			method: http.MethodPost,
			path:   "/my-index/_bulk",
		}

		conn, err := racp.NextForRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "ingest-node", conn.Name) // Should route to ingest node for bulk
	})

	t.Run("BackwardCompatibilityNext", func(t *testing.T) {
		conn, err := pool.Next()
		require.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Default behavior
	})
}

// TestOperationDetection tests the operation detection logic
func TestOperationDetection(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		isIngest bool
		isSearch bool
	}{
		// Ingest operations
		{"POST", "/my-index/_bulk", true, false},
		{"PUT", "/_ingest/pipeline/my-pipeline", true, false},
		{"POST", "/_ingest/pipeline/_simulate", true, false},

		// Search operations
		{"GET", "/my-index/_search", false, true},
		{"POST", "/my-index/_search", false, true},
		{"GET", "/_search", false, true},
		{"POST", "/_msearch", false, true},
		{"GET", "/my-index/_doc/1", false, true},

		// Other operations
		{"GET", "/_cluster/health", false, false},
		{"PUT", "/my-index", false, false},
		{"DELETE", "/my-index", false, false},
		{"GET", "/_cat/indices", false, false},
	}

	for _, test := range tests {
		t.Run(test.method+"_"+test.path, func(t *testing.T) {
			assert.Equal(t, test.isIngest, isIngestOperation(test.path, test.method))
			assert.Equal(t, test.isSearch, isSearchOperation(test.path, test.method))
		})
	}
}

// Mock implementations for testing

type mockSelector struct{}

func (s *mockSelector) Select(connections []*Connection) (*Connection, error) {
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil // Always return first connection
}

type mockRequest struct {
	method  string
	path    string
	headers http.Header
}

func (r *mockRequest) GetMethod() string { return r.method }
func (r *mockRequest) GetPath() string   { return r.path }
func (r *mockRequest) GetHeaders() http.Header {
	if r.headers == nil {
		return make(http.Header)
	}
	return r.headers
}
