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

//go:build !integration

package opensearchtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// TestDiscovery tests the node discovery functionality
func TestDiscovery(t *testing.T) {
	// Create default ServeMux for most tests
	defaultMux := http.NewServeMux()
	defaultMux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
		// Serve the default nodes info JSON
		f, err := os.Open("testdata/nodes.info.json")
		if err != nil {
			http.Error(w, fmt.Sprintf("Fixture error: %s", err), http.StatusInternalServerError)
			return
		}
		io.Copy(w, f)
	})
	defaultMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Health check endpoint - return a simple 200 OK
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Create TLS-specific ServeMux that returns nodes on TLS ports
	tlsMux := http.NewServeMux()
	tlsMux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
		// Custom nodes info for TLS test with TLS ports
		const (
			clusterName = "opensearch"
			node1ID     = "8g1UNpQNS06tlH1DUMBNhg"
			node2ID     = "8YR2EBk_QvWI4guQK292RA"
			node1Name   = "es1"
			node2Name   = "es2"
			node1Addr   = "127.0.0.1:20001"
			node2Addr   = "localhost:20002"
		)

		// Build response using structs and marshal to JSON
		nodeRoles := []string{"ingest", "cluster_manager", "data"}

		// Define the _nodes stats structure
		// NOTE: This parallels opensearchapi structs but cannot import them due to circular dependencies:
		// opensearchapi -> opensearch-go/v5 -> opensearchtransport
		type nodesStats struct {
			Total      int `json:"total"`
			Successful int `json:"successful"`
			Failed     int `json:"failed"`
		}

		tlsNodesResp := struct {
			NodesStats  nodesStats          `json:"_nodes"`
			ClusterName string              `json:"cluster_name"`
			Nodes       map[string]nodeInfo `json:"nodes"`
		}{
			NodesStats: nodesStats{
				Total:      2,
				Successful: 2,
				Failed:     0,
			},
			ClusterName: clusterName,
			Nodes: map[string]nodeInfo{
				node1ID: {
					Name:  node1Name,
					Roles: nodeRoles,
					HTTP: nodeInfoHTTP{
						PublishAddress: node1Addr,
					},
				},
				node2ID: {
					Name:  node2Name,
					Roles: nodeRoles,
					HTTP: nodeInfoHTTP{
						PublishAddress: node2Addr,
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tlsNodesResp)
	})
	tlsMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Health check endpoint
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{Addr: "127.0.0.1:10001", Handler: defaultMux, ReadTimeout: 1 * time.Second}
	srv2 := &http.Server{Addr: "localhost:10002", Handler: defaultMux, ReadTimeout: 1 * time.Second}
	// TLS servers on different ports to avoid conflict, using TLS-specific handler
	srvTLS1 := &http.Server{Addr: "127.0.0.1:20001", Handler: tlsMux, ReadTimeout: 1 * time.Second}
	srvTLS2 := &http.Server{Addr: "localhost:20002", Handler: tlsMux, ReadTimeout: 1 * time.Second}

	// Create listeners first to ensure ports are bound before tests run
	ln1, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("Failed to create listener for srv: %s", err)
	}
	ln2, err := net.Listen("tcp", srv2.Addr)
	if err != nil {
		t.Fatalf("Failed to create listener for srv2: %s", err)
	}
	lnTLS1, err := net.Listen("tcp", srvTLS1.Addr)
	if err != nil {
		t.Fatalf("Failed to create listener for srvTLS1: %s", err)
	}
	lnTLS2, err := net.Listen("tcp", srvTLS2.Addr)
	if err != nil {
		t.Fatalf("Failed to create listener for srvTLS2: %s", err)
	}

	go func() {
		if err := srv.Serve(ln1); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start server: %s", err)
			return
		}
	}()
	go func() {
		if err := srv2.Serve(ln2); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start server2: %s", err)
			return
		}
	}()
	go func() {
		if err := srvTLS1.ServeTLS(lnTLS1, "testdata/cert.pem", "testdata/key.pem"); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start TLS server1: %s", err)
			return
		}
	}()
	go func() {
		if err := srvTLS2.ServeTLS(lnTLS2, "testdata/cert.pem", "testdata/key.pem"); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start TLS server2: %s", err)
			return
		}
	}()
	defer func() { srv.Close() }()
	defer func() { srv2.Close() }()
	defer func() { srvTLS1.Close() }()
	defer func() { srvTLS2.Close() }()

	t.Run("getNodesInfo()", func(t *testing.T) {
		u, _ := url.Parse("http://" + srv.Addr)
		tp, _ := New(Config{URLs: []*url.URL{u}})

		nodes, err := tp.getNodesInfo(t.Context())
		if err != nil {
			t.Fatalf("ERROR: %s", err)
		}

		if len(nodes) != 4 {
			t.Errorf("Unexpected number of nodes, want=4, got=%d", len(nodes))
		}

		for _, node := range nodes {
			switch node.Name {
			case "es1":
				if node.url.String() != "http://127.0.0.1:10001" {
					t.Errorf("Unexpected URL: %q", node.url.String())
				}
			case "es2":
				if node.url.String() != "http://localhost:10002" {
					t.Errorf("Unexpected URL: %q", node.url.String())
				}
			case "es3":
				if node.url.String() != "http://127.0.0.1:10003" {
					t.Errorf("Unexpected URL: %q", node.url.String())
				}
			case "es4":
				if node.url.String() != "http://[fc99:3528::a04:812c]:10004" {
					t.Errorf("Unexpected URL: %q", node.url.String())
				}
			}
		}
	})

	t.Run("getNodesInfo() empty Body", func(t *testing.T) {
		newRoundTripper := func() http.RoundTripper {
			return mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{Header: http.Header{}}, nil
			})
		}

		u, _ := url.Parse("http://localhost:8080")
		tp, err := New(Config{URLs: []*url.URL{u}, Transport: newRoundTripper()})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected empty body")
	})

	t.Run("DiscoverNodes()", func(t *testing.T) {
		u, _ := url.Parse("http://" + srv.Addr)
		tp, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		err = tp.DiscoverNodes(t.Context())
		require.NoError(t, err, "Discovery should succeed")

		pool, ok := tp.mu.connectionPool.(*multiServerPool)
		require.True(t, ok, "Expected multiServerPool after discovery")

		// The inventory holds every discovered node regardless of role: es1 and
		// es2 (data+ingest+cluster_manager) plus the dedicated cluster managers
		// es3 and es4 (cluster_manager only).
		totalConnections := len(pool.mu.ready) + len(pool.mu.dead)
		require.Equal(t, 4, totalConnections, "Should have all discovered nodes in the inventory")

		// The exact split between ready/dead depends on health checks,
		// but we should have the right nodes
		foundNodes := make([]string, 0, len(pool.mu.ready)+len(pool.mu.dead))
		for _, conn := range pool.mu.ready {
			foundNodes = append(foundNodes, conn.Name)
		}
		for _, conn := range pool.mu.dead {
			foundNodes = append(foundNodes, conn.Name)
		}

		require.Contains(t, foundNodes, "es1", "Should include es1")
		require.Contains(t, foundNodes, "es2", "Should include es2")
		require.Contains(t, foundNodes, "es3", "Inventory should include es3 (cluster_manager only)")
		require.Contains(t, foundNodes, "es4", "Inventory should include es4 (cluster_manager only)")

		// The dedicated cluster managers es3 and es4 stay in the inventory but
		// must never be handed out for request routing.
		for i := 0; i < len(foundNodes)*4; i++ {
			conn, err := pool.Next()
			if err != nil {
				break
			}
			require.NotEqual(t, "es3", conn.Name, "Dedicated cluster manager es3 must not be selected for routing")
			require.NotEqual(t, "es4", conn.Name, "Dedicated cluster manager es4 must not be selected for routing")
		}
	})

	t.Run("DiscoverNodes() with SSL and authorization", func(t *testing.T) {
		u, _ := url.Parse("https://" + srvTLS1.Addr)
		tp, _ := New(Config{
			URLs:               []*url.URL{u},
			Username:           "foo",
			Password:           "bar",
			HealthCheck:        NoOpHealthCheck, // Disable health checks for test resurrection simulation
			InsecureSkipVerify: true,
		})

		err := tp.DiscoverNodes(t.Context())
		require.NoError(t, err, "DiscoverNodes should succeed with TLS")

		pool, ok := tp.mu.connectionPool.(*multiServerPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=multiServerPool, got=%T", tp.mu.connectionPool)
		}

		// Discovered nodes are in cold-start mode, need to force them alive for testing
		// Use resurrectWithLock to directly move connections from dead to ready pool.
		// Must hold pool lock to avoid racing with scheduleResurrect goroutines.
		pool.mu.Lock()
		deadConnections := make([]*Connection, len(pool.mu.dead))
		copy(deadConnections, pool.mu.dead)

		for _, conn := range deadConnections {
			conn.mu.Lock()
			pool.resurrectWithLock(conn)
			conn.mu.Unlock()
		}

		// Check pool state after resurrection (still under pool lock)
		if len(pool.mu.ready) != 2 {
			t.Errorf("Unexpected number of ready nodes after health simulation, want=2, got=%d", len(pool.mu.ready))
		}

		// Verify the discovered connections have correct HTTPS URLs
		expectedURLs := map[string]bool{
			"https://127.0.0.1:20001": false,
			"https://localhost:20002": false,
		}

		for _, conn := range pool.mu.ready {
			if expected, exists := expectedURLs[conn.URL.String()]; exists {
				if !expected {
					expectedURLs[conn.URL.String()] = true
				}
			} else {
				t.Errorf("Unexpected connection URL: %q (name=%q)", conn.URL.String(), conn.Name)
			}
		}
		pool.mu.Unlock()
	})

	t.Run("Role based nodes discovery", func(t *testing.T) {
		// NOTE: Transport tests cannot import opensearchapi due to circular dependencies:
		// opensearchapi -> opensearch-go/v5 -> opensearchtransport
		// Therefore, we create minimal test response structures that match the API format
		// needed by the discovery logic. These are NOT duplicates - they are test utilities
		// that simulate the actual API responses without importing the real API structs.

		type testNode struct {
			URL   string
			Roles []string
		}

		type testFields struct {
			Nodes map[string]testNode
		}
		type testWants struct {
			wantErr    bool
			wantsNConn int
			// wantsNotRoutable lists dedicated cluster managers that live in the
			// inventory but must never be handed out by the routing pool's Next().
			wantsNotRoutable []string
		}
		tests := []struct {
			name string
			args testFields
			want testWants
		}{
			{
				"Default roles should allow every node to be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"cluster_manager",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"cluster_manager",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es3": {
							URL: "http://es3:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"cluster_manager",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
					},
				},
				testWants{
					wantErr:    false,
					wantsNConn: 3,
				},
			},
			{
				"Cluster manager only node should not be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"cluster_manager",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"cluster_manager",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es3": {
							URL: "http://es3:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"cluster_manager",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
					},
				},

				testWants{
					wantErr:          false,
					wantsNConn:       3,
					wantsNotRoutable: []string{"es1"},
				},
			},
			{
				"Cluster manager and data only nodes should be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"data",
								"cluster_manager",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"cluster_manager",
							},
						},
					},
				},

				testWants{
					wantErr:    false,
					wantsNConn: 2,
				},
			},
			{
				"Default roles should allow every node to be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"master",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"master",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es3": {
							URL: "http://es3:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"master",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
					},
				},
				testWants{
					wantErr:    false,
					wantsNConn: 3,
				},
			},
			{
				"Master only node should not be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"master",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"master",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
						"es3": {
							URL: "http://es3:9200",
							Roles: []string{
								"data",
								"data_cold",
								"data_content",
								"data_frozen",
								"data_hot",
								"data_warm",
								"ingest",
								"master",
								"ml",
								"remote_cluster_client",
								"transform",
							},
						},
					},
				},

				testWants{
					wantErr:          false,
					wantsNConn:       3,
					wantsNotRoutable: []string{"es1"},
				},
			},
			{
				"Master and data only nodes should be selected",
				testFields{
					Nodes: map[string]testNode{
						"es1": {
							URL: "http://es1:9200",
							Roles: []string{
								"data",
								"master",
							},
						},
						"es2": {
							URL: "http://es2:9200",
							Roles: []string{
								"data",
								"master",
							},
						},
					},
				},

				testWants{
					wantErr:    false,
					wantsNConn: 2,
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Create a custom ServeMux for this test's node configuration
				testMux := http.NewServeMux()

				// Start a test server first so we have the address
				testServer := &http.Server{
					Addr:              "127.0.0.1:0",
					Handler:           testMux,
					ReadHeaderTimeout: 5 * time.Second,
				}
				listener, err := net.Listen("tcp", testServer.Addr)
				require.NoError(t, err)
				testServer.Addr = listener.Addr().String()

				// Add health check handler (catch-all for /{$} and /)
				testMux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
					healthResp := map[string]any{
						"name":         "test-node",
						"cluster_name": "test-cluster",
						"version": map[string]any{
							"number": "2.0.0",
						},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(healthResp)
				})

				// Catch-all 404 handler
				testMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					// For unknown paths, return 404
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{"error": "Not Found"})
				})

				// Add nodes info handler with this test's data
				testMux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
					// Create a simple response structure compatible with the discovery parsing
					response := map[string]any{
						"_nodes": map[string]any{
							"total":      len(tt.args.Nodes),
							"successful": len(tt.args.Nodes),
							"failed":     0,
						},
						"cluster_name": "test-cluster",
						"nodes":        make(map[string]any),
					}

					nodes := response["nodes"].(map[string]any)
					port := 9200
					for name, node := range tt.args.Nodes {
						// Assign unique ports to each node so discovery treats them as separate
						nodes[name] = map[string]any{
							"name":  name,
							"host":  "127.0.0.1",
							"ip":    "127.0.0.1",
							"roles": node.Roles,
							"http": map[string]any{
								"publish_address": net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
							},
						}
						port++
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(response)
				})

				go func() {
					testServer.Serve(listener)
				}()
				defer func() { testServer.Close() }()

				// Use the test server for discovery.
				// Use a custom Transport that rewrites all requests to the test server's
				// actual address so that health checks and subsequent discovery calls
				// on discovered nodes (which have fake publish_addresses) still reach
				// the test server.
				urls := []*url.URL{{Scheme: "http", Host: testServer.Addr}}
				redirectTransport := http.DefaultTransport.(*http.Transport).Clone()
				redirectTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					// Always connect to the test server regardless of the target address
					return (&net.Dialer{}).DialContext(ctx, network, testServer.Addr)
				}
				c, _ := New(Config{URLs: urls, Transport: redirectTransport})

				err = c.DiscoverNodes(t.Context())
				require.NoError(t, err, "DiscoverNodes should succeed")

				pool, ok := c.mu.connectionPool.(*multiServerPool)
				if !ok {
					t.Fatalf("Unexpected pool, want=multiServerPool, got=%T", c.mu.connectionPool)
				}

				// New connections from first discovery go to the dead list (pool
				// resurrection handles health-checking). Verify total connections
				// across both ready and dead lists.
				allConns := make([]*Connection, 0, len(pool.mu.ready)+len(pool.mu.dead))
				allConns = append(allConns, pool.mu.ready...)
				allConns = append(allConns, pool.mu.dead...)
				if len(allConns) != tt.want.wantsNConn {
					t.Errorf("Unexpected number of nodes, want=%d, got=%d (ready=%d, dead=%d)",
						tt.want.wantsNConn, len(allConns), len(pool.mu.ready), len(pool.mu.dead))
				}

				for _, conn := range allConns {
					expectedRoles := make([]string, len(tt.args.Nodes[conn.ID].Roles))
					copy(expectedRoles, tt.args.Nodes[conn.ID].Roles)
					slices.Sort(expectedRoles)

					actualRoles := conn.Roles.toSlice()

					if !reflect.DeepEqual(expectedRoles, actualRoles) {
						t.Errorf("Unexpected roles for node %q, want=%q, got=%q", conn.Name, expectedRoles, actualRoles)
					}
				}

				// Dedicated cluster managers stay in the inventory but must never
				// be handed out for request routing.
				for _, dcm := range tt.want.wantsNotRoutable {
					for i := 0; i < len(allConns)*4; i++ {
						conn, err := pool.Next()
						if err != nil {
							break
						}
						require.NotEqual(t, dcm, conn.Name,
							"Dedicated cluster manager %q must not be selected for routing", dcm)
					}
				}

				if err := c.DiscoverNodes(t.Context()); (err != nil) != tt.want.wantErr {
					t.Errorf("DiscoverNodes() error = %v, wantErr %v", err, tt.want.wantErr)
				}
			})
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
		// expectedInInventory lists nodes that must appear in the allConns pool.
		// The inventory holds every discovered node regardless of role.
		expectedInInventory []string
		// expectedNotRoutable lists dedicated cluster managers that stay in the
		// inventory but must never be handed out for request routing.
		expectedNotRoutable []string
	}{
		{
			"mixed node types with validation",
			map[string][]string{
				"cm-only":     {RoleClusterManager},           // dedicated cluster manager
				"master-only": {RoleMaster},                   // dedicated cluster manager
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
				"dedicated-cm": {RoleClusterManager},   // dedicated cluster manager
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
				"cm-rcc":    {RoleClusterManager, RoleRemoteClusterClient}, // dedicated cluster manager
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
			// Create a ServeMux with mock handlers
			mux := http.NewServeMux()

			// Health check endpoint - exact root path match
			mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
				healthResp := map[string]any{
					"name":         "test-node",
					"cluster_name": "test-cluster",
					"version": map[string]any{
						"number": "2.0.0",
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(healthResp)
			})

			// Nodes info endpoint
			mux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{
					"_nodes": map[string]any{
						"total":      len(tt.nodes),
						"successful": len(tt.nodes),
						"failed":     0,
					},
					"cluster_name": "test-cluster",
					"nodes":        make(map[string]any),
				}

				nodes := response["nodes"].(map[string]any)
				port := 9200
				for name, roles := range tt.nodes {
					nodes[name] = map[string]any{
						"name":  name,
						"host":  "127.0.0.1",
						"ip":    "127.0.0.1",
						"roles": roles,
						"http": map[string]any{
							"publish_address": net.JoinHostPort("localhost", strconv.Itoa(port)),
						},
					}
					port++
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			})

			// Catch-all 404 handler
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "Not Found"})
			})

			// Create mock transport that uses the ServeMux
			newRoundTripper := func() http.RoundTripper {
				return mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					// Use httptest.NewRecorder to capture the ServeMux response
					recorder := httptest.NewRecorder()
					mux.ServeHTTP(recorder, req)

					// Convert the recorded response to http.Response
					resp := recorder.Result()
					return resp, nil
				},
				)
			}

			u, _ := url.Parse("http://localhost:9200")
			c, err := New(Config{
				URLs:      []*url.URL{u},
				Transport: newRoundTripper(),
			})
			require.NoError(t, err)

			// Perform discovery
			err = c.DiscoverNodes(t.Context())
			require.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*multiServerPool)
			require.True(t, ok, "Expected multiServerPool")

			// The allConns inventory holds every discovered node regardless of
			// role so discovery can reuse and evict connections.
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.ready {
				actualNodes[conn.Name] = true
			}
			for _, conn := range pool.mu.dead {
				actualNodes[conn.Name] = true
			}

			require.Len(t, actualNodes, len(tt.expectedInInventory),
				"Expected %d nodes but got %d: %v", len(tt.expectedInInventory), len(actualNodes), actualNodes)

			for _, expectedNode := range tt.expectedInInventory {
				require.True(t, actualNodes[expectedNode],
					"Expected node %q in the connection inventory but it wasn't", expectedNode)
			}

			// Dedicated cluster managers stay in the inventory but must never be
			// handed out for request routing.
			for _, dcm := range tt.expectedNotRoutable {
				for i := 0; i < len(actualNodes)*4; i++ {
					conn, err := pool.Next()
					if err != nil {
						break
					}
					require.NotEqual(t, dcm, conn.Name,
						"Dedicated cluster manager %q must not be selected for routing", dcm)
				}
			}
		})
	}
}

// TestDedicatedClusterManagersExcludedFromRouting verifies that dedicated
// cluster managers are held in the connection inventory but never routed to.
func TestDedicatedClusterManagersExcludedFromRouting(t *testing.T) {
	tests := []struct {
		name  string
		nodes map[string][]string // nodeName -> roles
		// expectedInInventory lists nodes that must appear in the allConns pool.
		// The inventory holds every discovered node regardless of role so that
		// discovery can reuse and evict connections; dedicated cluster managers
		// are kept here and excluded from routing separately.
		expectedInInventory []string
		// expectedNotRoutable lists dedicated cluster managers that must be kept
		// out of the round-robin routing pool.
		expectedNotRoutable []string
	}{
		{
			name: "dedicated cluster manager in inventory but not routable",
			nodes: map[string][]string{
				"cm-only":   {RoleClusterManager},
				"data-node": {RoleData},
				"dummy":     {RoleData}, // Add second node to avoid single connection pool
			},
			expectedInInventory: []string{"cm-only", "data-node", "dummy"},
			expectedNotRoutable: []string{"cm-only"},
		},
		{
			name: "mixed cluster_manager and data role is routable",
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
			// Create a shared handler for all mock node servers.
			testMux := http.NewServeMux()

			// Create one listener per node so each has a unique publish_address.
			// nodeAddrs maps node name -> "host:port" of its listener.
			nodeAddrs := make(map[string]string, len(tt.nodes))
			var servers []*http.Server

			for name := range tt.nodes {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)

				srv := &http.Server{
					Handler:           testMux,
					ReadHeaderTimeout: 5 * time.Second,
				}
				servers = append(servers, srv)
				nodeAddrs[name] = ln.Addr().String()

				go func() { srv.Serve(ln) }()
			}
			t.Cleanup(func() {
				for _, srv := range servers {
					srv.Close()
				}
			})

			// Use the first node's address as the seed URL for discovery.
			var seedAddr string
			for _, addr := range nodeAddrs {
				seedAddr = addr
				break
			}

			// Health check endpoint (catch-all for /{$} and /)
			testMux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
				healthResp := map[string]any{
					"name":         "test-node",
					"cluster_name": "test-cluster",
					"version": map[string]any{
						"number": "2.0.0",
					},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(healthResp)
			})

			// Nodes info endpoint
			testMux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{
					"_nodes": map[string]any{
						"total":      len(tt.nodes),
						"successful": len(tt.nodes),
						"failed":     0,
					},
					"cluster_name": "test-cluster",
					"nodes":        make(map[string]any),
				}

				nodes := response["nodes"].(map[string]any)
				for name, roles := range tt.nodes {
					nodes[name] = map[string]any{
						"name":  name,
						"host":  "127.0.0.1",
						"ip":    "127.0.0.1",
						"roles": roles,
						"http": map[string]any{
							"publish_address": nodeAddrs[name],
						},
					}
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			})

			// Catch-all 404 handler
			testMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "Not Found"})
			})

			// Use the seed address for discovery
			urls := []*url.URL{{Scheme: "http", Host: seedAddr}}
			c, err := New(Config{
				URLs: urls,
			})
			require.NoError(t, err)

			pool, ok := c.mu.connectionPool.(*multiServerPool)
			require.False(t, ok, "expected a single-server seed pool before discovery, got %T", c.mu.connectionPool)

			// Run discovery repeatedly. The inventory must converge to exactly one
			// connection per node and stay there: an unbounded pool that re-created
			// connections each cycle (the dedicated-cluster-manager leak) would grow
			// with every iteration. Asserting the exact length on every cycle is the
			// regression guard.
			const cycles = 5
			for cycle := 1; cycle <= cycles; cycle++ {
				require.NoError(t, c.DiscoverNodes(t.Context()), "discovery cycle %d", cycle)

				pool, ok = c.mu.connectionPool.(*multiServerPool)
				require.True(t, ok, "Expected multiServerPool after discovery")

				pool.mu.RLock()
				readyLen := len(pool.mu.ready)
				deadLen := len(pool.mu.dead)
				membersLen := len(pool.mu.members)
				inventory := make(map[string]bool, readyLen+deadLen)
				for _, conn := range pool.mu.ready {
					inventory[conn.Name] = true
				}
				for _, conn := range pool.mu.dead {
					inventory[conn.Name] = true
				}
				pool.mu.RUnlock()

				// The inventory holds exactly one connection per discovered node,
				// regardless of role, and never grows across cycles.
				require.Equalf(t, len(tt.nodes), readyLen+deadLen,
					"cycle %d: inventory connection count (ready=%d dead=%d)", cycle, readyLen, deadLen)
				require.Equalf(t, readyLen+deadLen, membersLen,
					"cycle %d: members map must match ready+dead", cycle)
				require.Lenf(t, inventory, len(tt.nodes),
					"cycle %d: one connection per node (no duplicates)", cycle)
				for _, expectedNode := range tt.expectedInInventory {
					require.Truef(t, inventory[expectedNode],
						"cycle %d: expected node %q in the connection inventory", cycle, expectedNode)
				}
			}

			// Dedicated cluster managers stay in the inventory but must not be
			// handed out for request routing. With no router configured, routing
			// uses the inventory pool's Next(), which skips them.
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

// TestGenericRoleBasedSelector tests the new generic role-based selector
func TestGenericRoleBasedSelector(t *testing.T) {
	connections := []*Connection{
		{Name: "data-node", Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", Roles: newRoleSet([]string{RoleIngest})},
		{Name: "data-ingest-node", Roles: newRoleSet([]string{RoleData, RoleIngest})},
		{Name: "cluster-manager-node", Roles: newRoleSet([]string{RoleClusterManager})},
		{Name: "warm-node", Roles: newRoleSet([]string{RoleWarm})},
		{Name: "coordinating-node", Roles: newRoleSet([]string{})}, // No specific roles
	}
	// Model verified, reachable discovered nodes: latch lcViable so they count
	// as availableForRouting.
	for _, c := range connections {
		c.setLifecycleBit(lcViable)
	}

	t.Run("ChainPolicy with multiple role requirements (OR logic)", func(t *testing.T) {
		// Create individual policies for each role combination
		dataIngestPolicy, err := NewRolePolicy(RoleData, RoleIngest)
		require.NoError(t, err)
		dataSearchPolicy, err := NewRolePolicy(RoleData, RoleSearch)
		require.NoError(t, err)

		// Create a policy that tries (data && ingest) OR (data && search)
		policy := NewPolicy(dataIngestPolicy, dataSearchPolicy)

		// Configure pool factories for the policy (needed for tests that create policies directly)
		err = configureTestPolicySettings(t, policy)
		require.NoError(t, err)

		// Update policy with connections
		err = policy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)

		// Policy should be enabled (has nodes with data+ingest)
		require.True(t, policy.IsEnabled())

		// Get connection - should match data-ingest-node first
		hop, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, hop.Conn)

		// Should match only data-ingest-node (has both data and ingest roles)
		require.Equal(t, "data-ingest-node", hop.Conn.Name)
	})

	t.Run("RolePolicy with cluster manager exclusion", func(t *testing.T) {
		// Create connections for testing
		connections := []*Connection{
			{Name: "data-node", URL: &url.URL{Host: "data-node:9200"}, Roles: newRoleSet([]string{RoleData})},
			{Name: "cluster-manager-node", URL: &url.URL{Host: "cm-node:9200"}, Roles: newRoleSet([]string{RoleClusterManager})},
		}
		// Model verified, reachable discovered nodes: latch lcViable so they
		// count as availableForRouting.
		for _, c := range connections {
			c.setLifecycleBit(lcViable)
		}

		// Create a RolePolicy for data nodes (excludes cluster managers)
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		// Configure pool factories for the policy (needed for tests that create policies directly)
		err = configureTestPolicySettings(t, policy)
		require.NoError(t, err)

		// Update policy with connections
		err = policy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)

		// Policy should be enabled (has data nodes)
		require.True(t, policy.IsEnabled())

		// Get connection and verify it only contains data node
		hop, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, hop.Conn)

		// The result should contain only the data node, not the cluster manager
		require.Equal(t, "data-node", hop.Conn.Name)
	})

	t.Run("RolePolicy for warm nodes", func(t *testing.T) {
		// Create connections for testing (no warm nodes)
		connections := []*Connection{
			{Name: "data-node", URL: &url.URL{Host: "data-node:9200"}, Roles: newRoleSet([]string{RoleData})},
			{Name: "ingest-node", URL: &url.URL{Host: "ingest-node:9200"}, Roles: newRoleSet([]string{RoleIngest})},
		}

		// Create a RolePolicy for warm nodes
		policy, err := NewRolePolicy(RoleWarm)
		require.NoError(t, err)

		// Configure pool factories for the policy (needed for tests that create policies directly)
		err = configureTestPolicySettings(t, policy)
		require.NoError(t, err)

		// Update policy with connections
		err = policy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)

		// Policy should NOT be enabled (no warm nodes)
		require.False(t, policy.IsEnabled())

		// Eval should return nil conn (no matching connections)
		hop, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.Nil(t, hop.Conn)
	})

	t.Run("Options pattern flexibility", func(t *testing.T) {
		// Test that options pattern allows flexible configuration
		ingestPolicy, err := NewRolePolicy(RoleIngest)
		require.NoError(t, err)

		warmPolicy, err := NewRolePolicy(RoleWarm) // Try warm nodes (which don't exist)
		require.NoError(t, err)

		// Configure pool factories for the policies (needed for tests that create policies directly)
		err = configureTestPolicySettings(t, ingestPolicy)
		require.NoError(t, err)
		err = configureTestPolicySettings(t, warmPolicy)
		require.NoError(t, err)

		// Update policies with connections
		err = ingestPolicy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)
		err = warmPolicy.DiscoveryUpdate(connections, nil, nil)
		require.NoError(t, err)

		// Test ingest policy
		require.True(t, ingestPolicy.IsEnabled())
		hop1, err1 := ingestPolicy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err1)
		require.NotNil(t, hop1.Conn)

		// Should return ingest-capable nodes
		require.Contains(t, []string{"ingest-node", "data-ingest-node"}, hop1.Conn.Name)

		// Test warm policy
		require.True(t, warmPolicy.IsEnabled())
		hop2, err2 := warmPolicy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err2)
		require.NotNil(t, hop2.Conn)

		require.Equal(t, "warm-node", hop2.Conn.Name)
	})
}

// TestRolePolicies_Unit tests the role-based policies with various
// configurations using in-memory connections (built under !integration).
func TestRolePolicies_Unit(t *testing.T) {
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
	// Model verified, reachable discovered nodes: set lcViable so they count
	// as availableForRouting.
	for _, c := range connections {
		c.setLifecycleBit(lcViable)
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

func TestGCD(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{8, 16, 8},
		{16, 8, 8},
		{8, 24, 8},
		{24, 32, 8},
		{32, 40, 8},
		{7, 13, 1},
		{12, 18, 6},
		{100, 100, 100},
		{1, 1, 1},
	}
	for _, tt := range tests {
		got := gcd(tt.a, tt.b)
		require.Equal(t, tt.want, got, "gcd(%d, %d)", tt.a, tt.b)
	}
}

func TestComputeWeights(t *testing.T) {
	makeConn := func(cores int) *Connection {
		c := &Connection{}
		c.storeAllocatedProcessors(cores)
		c.weight.Store(1)
		return c
	}

	t.Run("homogeneous cluster", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(8), makeConn(8)}
		computeWeights(conns)
		for _, c := range conns {
			require.Equal(t, int32(1), c.weight.Load())
		}
	})

	t.Run("two sizes", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16)}
		computeWeights(conns)
		require.Equal(t, int32(1), conns[0].weight.Load()) // 8/8
		require.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
	})

	t.Run("three sizes", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16), makeConn(24)}
		computeWeights(conns)
		require.Equal(t, int32(1), conns[0].weight.Load())
		require.Equal(t, int32(2), conns[1].weight.Load())
		require.Equal(t, int32(3), conns[2].weight.Load())
	})

	t.Run("non-power-of-2 mixed", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16), makeConn(32), makeConn(40)}
		computeWeights(conns)
		require.Equal(t, int32(1), conns[0].weight.Load()) // 8/8
		require.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
		require.Equal(t, int32(4), conns[2].weight.Load()) // 32/8
		require.Equal(t, int32(5), conns[3].weight.Load()) // 40/8
	})

	t.Run("larger non-power-of-2", func(t *testing.T) {
		conns := []*Connection{makeConn(24), makeConn(32), makeConn(40)}
		computeWeights(conns)
		require.Equal(t, int32(3), conns[0].weight.Load()) // 24/8
		require.Equal(t, int32(4), conns[1].weight.Load()) // 32/8
		require.Equal(t, int32(5), conns[2].weight.Load()) // 40/8
	})

	t.Run("unknown cores get weight 1", func(t *testing.T) {
		conns := []*Connection{makeConn(0), makeConn(16), makeConn(8)}
		computeWeights(conns)
		require.Equal(t, int32(1), conns[0].weight.Load()) // unknown -> 1
		require.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
		require.Equal(t, int32(1), conns[2].weight.Load()) // 8/8
	})

	t.Run("all unknown leaves weights unchanged", func(t *testing.T) {
		conns := []*Connection{makeConn(0), makeConn(0)}
		conns[0].weight.Store(3) // pre-set
		conns[1].weight.Store(5)
		computeWeights(conns)
		require.Equal(t, int32(3), conns[0].weight.Load()) // unchanged
		require.Equal(t, int32(5), conns[1].weight.Load()) // unchanged
	})

	t.Run("empty slice is no-op", func(t *testing.T) {
		computeWeights(nil)
		computeWeights([]*Connection{})
	})
}

func TestCreateOrUpdateSingleNodePool(t *testing.T) {
	t.Run("single ready conn creates singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		conn.setLifecycleBit(lcActive)

		client := &Transport{}
		// Start with an existing singleServerPool (what you'd have in practice)
		client.mu.connectionPool = &singleServerPool{}

		client.mu.Lock()
		pool := client.createOrUpdateSingleNodePool([]*Connection{conn}, nil)
		client.mu.Unlock()

		sp, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool")
		require.Equal(t, conn, sp.connection)
	})

	t.Run("single dead conn creates singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		conn.setLifecycleBit(lcDead)

		client := &Transport{}
		client.mu.connectionPool = &singleServerPool{}

		client.mu.Lock()
		pool := client.createOrUpdateSingleNodePool(nil, []*Connection{conn})
		client.mu.Unlock()

		sp, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool")
		require.Equal(t, conn, sp.connection)
	})

	t.Run("demote from multiServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		conn.setLifecycleBit(lcActive)

		msp := &multiServerPool{}
		msp.mu.ready = []*Connection{conn}
		msp.mu.activeCount = 1
		msp.mu.dead = []*Connection{}

		client := &Transport{}
		client.mu.connectionPool = msp

		client.mu.Lock()
		pool := client.createOrUpdateSingleNodePool([]*Connection{conn}, nil)
		client.mu.Unlock()

		sp, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool after demotion")
		require.Equal(t, conn, sp.connection)
	})

	t.Run("preserves metrics from existing singleServerPool", func(t *testing.T) {
		oldConn := &Connection{URL: &url.URL{Scheme: "http", Host: "old:9200"}}
		existingMetrics := &metrics{}
		existingPool := &singleServerPool{connection: oldConn, metrics: existingMetrics}

		newConn := &Connection{URL: &url.URL{Scheme: "http", Host: "new:9200"}}
		newConn.setLifecycleBit(lcActive)

		client := &Transport{}
		client.mu.connectionPool = existingPool

		client.mu.Lock()
		pool := client.createOrUpdateSingleNodePool([]*Connection{newConn}, nil)
		client.mu.Unlock()

		sp, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool")
		require.Equal(t, newConn, sp.connection)
		require.Equal(t, existingMetrics, sp.metrics, "metrics should be preserved")
	})
}

func TestFindConnectionByURL(t *testing.T) {
	t.Run("finds in singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		pool := &singleServerPool{connection: conn}
		client := &Transport{}

		found := client.findConnectionByURL(pool, "http://node1:9200")
		require.Equal(t, conn, found)
	})

	t.Run("returns nil when not in singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		pool := &singleServerPool{connection: conn}
		client := &Transport{}

		found := client.findConnectionByURL(pool, "http://other:9200")
		require.Nil(t, found)
	})

	t.Run("finds in multiServerPool ready list", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{conn}
		pool.mu.dead = []*Connection{}
		client := &Transport{}

		found := client.findConnectionByURL(pool, "http://node1:9200")
		require.Equal(t, conn, found)
	})

	t.Run("finds in multiServerPool dead list", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "dead:9200"}}
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{conn}
		client := &Transport{}

		found := client.findConnectionByURL(pool, "http://dead:9200")
		require.Equal(t, conn, found)
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{}
		client := &Transport{}

		found := client.findConnectionByURL(pool, "http://missing:9200")
		require.Nil(t, found)
	})
}

func TestRecalculateCapacityModel(t *testing.T) {
	makeConnWithCores := func(host string, cores int) *Connection {
		c := &Connection{URL: &url.URL{Host: host}}
		c.storeAllocatedProcessors(cores)
		return c
	}

	t.Run("updates fields from minimum core count", func(t *testing.T) {
		conns := []*Connection{
			makeConnWithCores("a:9200", 8),
			makeConnWithCores("b:9200", 16),
			makeConnWithCores("c:9200", 4),
		}

		client := &Transport{
			serverMaxNewConnsPerSec: 0,
			clientsPerServer:        0,
			healthCheckRate:         0,
		}

		client.recalculateCapacityModel(conns)

		// Min cores = 4
		require.InDelta(t, float64(4)*serverMaxNewConnsPerSecMultiplier, client.serverMaxNewConnsPerSec, 1e-9)
		require.InDelta(t, float64(4), client.clientsPerServer, 1e-9)
		require.InDelta(t, float64(4)*healthCheckRateMultiplier, client.healthCheckRate, 1e-9)
	})

	t.Run("no-op when no cores known", func(t *testing.T) {
		conns := []*Connection{
			makeConnWithCores("a:9200", 0),
		}

		client := &Transport{
			serverMaxNewConnsPerSec: 99.0,
			clientsPerServer:        99.0,
			healthCheckRate:         99.0,
		}

		client.recalculateCapacityModel(conns)

		// Should remain unchanged
		require.InDelta(t, 99.0, client.serverMaxNewConnsPerSec, 1e-9)
		require.InDelta(t, 99.0, client.clientsPerServer, 1e-9)
		require.InDelta(t, 99.0, client.healthCheckRate, 1e-9)
	})
}

func TestUpdateConnectionPool(t *testing.T) {
	makeConn := func(host string) *Connection {
		c := &Connection{
			URL:   &url.URL{Scheme: "http", Host: host},
			Roles: newRoleSet([]string{"data"}),
		}
		c.weight.Store(1)
		return c
	}

	newDiscoveryClient := func() *Transport {
		ctx, cancel := context.WithCancel(context.Background())
		client := &Transport{
			ctx:        ctx,
			cancelFunc: cancel,
			urls:       []*url.URL{{Scheme: "http", Host: "seed:9200"}},
			// Provide a transport that returns errors so background health checks
			// (from scheduleResurrect goroutines) don't panic on nil transport.
			transport: http.DefaultTransport.(*http.Transport).Clone(),
		}
		return client
	}

	t.Run("cold start creates single-node pool", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		conn := makeConn("node1:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn}, nil)
		require.NoError(t, err)

		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		_, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool for 1 node")
	})

	t.Run("cold start creates multi-node pool", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		conn1 := makeConn("node1:9200")
		conn2 := makeConn("node2:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn1, conn2}, nil)
		require.NoError(t, err)

		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		_, ok := pool.(*multiServerPool)
		require.True(t, ok, "expected multiServerPool for 2 nodes")
	})

	t.Run("observer notified of added/removed/unchanged", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		client.observer.Store(&iface)

		// Initial pool with 2 nodes
		conn1 := makeConn("node1:9200")
		conn2 := makeConn("node2:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn1, conn2}, nil)
		require.NoError(t, err)

		// Second discovery: node2 gone, node3 added, node1 unchanged (same pointer)
		conn3 := makeConn("node3:9200")
		err = client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn1, conn3}, nil)
		require.NoError(t, err)

		require.Positive(t, obs.count("discovery_add"))
		require.Positive(t, obs.count("discovery_remove"))
		require.Positive(t, obs.count("discovery_unchanged"))
	})

	t.Run("stale dead state resurrected", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		// Create initial pool with a connection
		conn := makeConn("node1:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn}, nil)
		require.NoError(t, err)

		// Mark the connection as dead in the past
		conn.mu.Lock()
		conn.storeDeadSince(time.Now().Add(-5 * time.Second))
		conn.mu.Unlock()
		conn.failures.Store(3)

		// Re-discover with the SAME pointer (simulating nodeDiscovery reuse) and
		// healthCheckedAt AFTER the deadSince -> stale dead state should be cleared
		healthCheckedAt := time.Now()
		err = client.updateConnectionPool(t.Context(), healthCheckedAt, []*Connection{conn}, nil)
		require.NoError(t, err)

		// The old connection's dead state should have been cleared (resurrected)
		conn.mu.RLock()
		deadSince := conn.loadDeadSince()
		conn.mu.RUnlock()
		require.True(t, deadSince.IsZero(), "stale dead state should be cleared")
		require.Equal(t, int64(0), conn.failures.Load(), "failures should be reset")
	})

	t.Run("concurrent dead state preserved", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		// Create initial pool
		conn := makeConn("node1:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn}, nil)
		require.NoError(t, err)

		// Health check at t=0
		healthCheckedAt := time.Now()

		// Mark dead AFTER the health check time
		conn.mu.Lock()
		conn.storeDeadSince(healthCheckedAt.Add(1 * time.Second))
		conn.mu.Unlock()

		// Re-discover with SAME pointer: dead state is newer than healthCheckedAt -> should stay dead
		err = client.updateConnectionPool(t.Context(), healthCheckedAt, []*Connection{conn}, nil)
		require.NoError(t, err)

		conn.mu.RLock()
		deadSince := conn.loadDeadSince()
		conn.mu.RUnlock()
		require.False(t, deadSince.IsZero(), "concurrent dead state should be preserved")
	})

	t.Run("role change treated as remove+add", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		client.observer.Store(&iface)

		conn1 := makeConn("node1:9200")
		conn2 := makeConn("node2:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{conn1, conn2}, nil)
		require.NoError(t, err)

		// Reset observer counts from initial discovery
		obs.mu.Lock()
		obs.events = make(map[string][]ConnectionEvent)
		obs.mu.Unlock()

		// Re-discover with node1 having different roles
		newConn1 := makeConn("node1:9200")
		newConn1.Roles = newRoleSet([]string{"data", "ingest"}) // Changed roles
		newConn2 := makeConn("node2:9200")
		err = client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{newConn1, newConn2}, nil)
		require.NoError(t, err)

		// Role change for node1 should generate remove + add
		addEvents := obs.get("discovery_add")
		removeEvents := obs.get("discovery_remove")

		addURLs := make([]string, len(addEvents))
		for i, e := range addEvents {
			addURLs[i] = e.URL
		}
		removeURLs := make([]string, len(removeEvents))
		for i, e := range removeEvents {
			removeURLs[i] = e.URL
		}
		require.Contains(t, addURLs, "http://node1:9200")
		require.Contains(t, removeURLs, "http://node1:9200")
	})

	t.Run("dead connections placed in dead list", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		ready := makeConn("node1:9200")
		dead := makeConn("node2:9200")
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{ready}, []*Connection{dead})
		require.NoError(t, err)

		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		mp, ok := pool.(*multiServerPool)
		require.True(t, ok)
		require.Len(t, mp.mu.dead, 1)
		require.Equal(t, "node2:9200", mp.mu.dead[0].URL.Host)
	})

	t.Run("dead connections have deadSince and lcUnknown invariants", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		ready := makeConn("node1:9200")
		dead := makeConn("node2:9200")

		// Verify deadSince is initially zero
		dead.mu.RLock()
		require.True(t, dead.loadDeadSince().IsZero(), "deadSince should be zero before pool placement")
		dead.mu.RUnlock()

		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{ready}, []*Connection{dead})
		require.NoError(t, err)

		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		mp, ok := pool.(*multiServerPool)
		require.True(t, ok)
		require.Len(t, mp.mu.dead, 1)

		deadConn := mp.mu.dead[0]

		// Verify deadSince was set by appendToDeadWithLock
		deadConn.mu.RLock()
		require.False(t, deadConn.loadDeadSince().IsZero(), "deadSince must be set for dead-list connections")
		deadConn.mu.RUnlock()

		// Verify lcUnknown is set
		lc := deadConn.loadConnState().lifecycle()
		require.True(t, lc.has(lcUnknown), "dead-list connections must have lcUnknown set, got %s", lc)
	})

	t.Run("dead connections from discovery get resurrection scheduled", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		ready := makeConn("node1:9200")
		dead := makeConn("node2:9200")
		dead.setLifecycleBit(lcDead | lcNeedsWarmup | lcNeedsHardware)

		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{ready}, []*Connection{dead})
		require.NoError(t, err)

		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		mp, ok := pool.(*multiServerPool)
		require.True(t, ok)
		require.Len(t, mp.mu.dead, 1)

		deadConn := mp.mu.dead[0]

		// Verify health checking was scheduled (lcHealthChecking bit set by scheduleResurrect)
		require.Eventually(t, func() bool {
			return deadConn.loadConnState().lifecycle().has(lcHealthChecking)
		}, 2*time.Second, 10*time.Millisecond, "dead connection should have lcHealthChecking set after scheduleResurrect")
	})

	t.Run("nodeDiscovery reuses existing connections", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		// Set up initial pool with one connection
		existing := makeConn("node1:9200")
		existing.ID = "node-id-1"
		existing.Name = "node1"
		existing.setLifecycleBit(lcActive)
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{existing}, nil)
		require.NoError(t, err)

		// Simulate discovery returning the same node
		discovered := []nodeInfo{
			{
				ID:    "node-id-1",
				Name:  "node1",
				Roles: []string{"data"},
				url:   existing.URL,
			},
		}

		err = client.nodeDiscovery(t.Context(), discovered)
		require.NoError(t, err)

		// Verify the same Connection pointer was reused
		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		switch p := pool.(type) {
		case *singleServerPool:
			require.Same(t, existing, p.connection, "should reuse existing Connection pointer")
		case *multiServerPool:
			require.Contains(t, p.mu.ready, existing, "should reuse existing Connection pointer in ready list")
		default:
			t.Fatalf("unexpected pool type: %T", pool)
		}
	})

	t.Run("nodeDiscovery creates new connection when ID changes", func(t *testing.T) {
		client := newDiscoveryClient()
		defer client.cancelFunc()

		// Set up initial pool with one connection
		existing := makeConn("node1:9200")
		existing.ID = "old-id"
		existing.Name = "node1"
		existing.setLifecycleBit(lcActive)
		err := client.updateConnectionPool(t.Context(), time.Time{}, []*Connection{existing}, nil)
		require.NoError(t, err)

		// Simulate discovery returning same URL but different ID (node replaced)
		discovered := []nodeInfo{
			{
				ID:    "new-id",
				Name:  "node1",
				Roles: []string{"data"},
				url:   existing.URL,
			},
		}

		err = client.nodeDiscovery(t.Context(), discovered)
		require.NoError(t, err)

		// Verify a new connection was created (old one removed, new one added to dead)
		client.mu.RLock()
		pool := client.mu.connectionPool
		client.mu.RUnlock()

		switch p := pool.(type) {
		case *singleServerPool:
			require.NotSame(t, existing, p.connection, "should create new Connection when ID changes")
			require.Equal(t, "new-id", p.connection.ID)
		case *multiServerPool:
			allConns := make([]*Connection, 0, len(p.mu.ready)+len(p.mu.dead))
			allConns = append(allConns, p.mu.ready...)
			allConns = append(allConns, p.mu.dead...)
			require.Len(t, allConns, 1)
			require.Equal(t, "new-id", allConns[0].ID)
			require.NotSame(t, existing, allConns[0], "should create new Connection when ID changes")
		default:
			t.Fatalf("unexpected pool type: %T", pool)
		}
	})
}

func TestNodesMeta_formatFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		failures []json.RawMessage
		want     string
	}{
		{
			name:     "nil failures",
			failures: nil,
			want:     "[]",
		},
		{
			name:     "empty failures",
			failures: []json.RawMessage{},
			want:     "[]",
		},
		{
			name:     "single failure",
			failures: []json.RawMessage{json.RawMessage(`{"node_id":"n1","reason":"timeout"}`)},
			want:     `[{"node_id":"n1","reason":"timeout"}]`,
		},
		{
			name: "multiple failures",
			failures: []json.RawMessage{
				json.RawMessage(`{"node_id":"n1","reason":"timeout"}`),
				json.RawMessage(`{"node_id":"n2","reason":"OptionalDataException"}`),
			},
			want: `[{"node_id":"n1","reason":"timeout"},{"node_id":"n2","reason":"OptionalDataException"}]`,
		},
		{
			name:     "malformed raw JSON triggers marshal error",
			failures: []json.RawMessage{json.RawMessage("\xff")},
			want: "[<1 failures, marshal error: json: error calling MarshalJSON " +
				"for type json.RawMessage: invalid character 'ÿ' looking for beginning of value>]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &_NodesMeta{Failures: tt.failures}
			got := m.formatFailures()
			require.Equal(t, tt.want, got)
		})
	}
}

// TestGetNodesInfoNodesMeta tests the _nodes metadata guard in getNodesInfo.
func TestGetNodesInfoNodesMeta(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		enableDebug     bool
		wantErrIs       error
		wantErrContains string
		wantNodes       int
	}{
		{
			name: "all nodes failed",
			body: `{
				"_nodes": {"total": 3, "successful": 0, "failed": 3, "failures": [{"node_id": "n1", "reason": "timeout"}]},
				"cluster_name": "test",
				"nodes": {}
			}`,
			enableDebug:     true,
			wantErrIs:       errDiscoveryEmpty,
			wantErrContains: `failures=[{"node_id":"n1","reason":"timeout"}]`,
		},
		{
			name: "no _nodes metadata and empty nodes",
			body: `{
				"cluster_name": "test",
				"nodes": {}
			}`,
			enableDebug: true,
			wantErrIs:   errDiscoveryEmpty,
		},
		{
			name: "partial failure with successful nodes",
			body: `{
				"_nodes": {"total": 3, "successful": 2, "failed": 1, "failures": [{"node_id": "n3", "reason": "timeout"}]},
				"cluster_name": "test",
				"nodes": {
					"n1": {"name": "node1", "host": "127.0.0.1", "ip": "127.0.0.1", "roles": ["data"], "http": {"publish_address": "127.0.0.1:9200"}},
					"n2": {"name": "node2", "host": "127.0.0.1", "ip": "127.0.0.1", "roles": ["data"], "http": {"publish_address": "127.0.0.1:9201"}}
				}
			}`,
			enableDebug: true,
			wantNodes:   2,
		},
		{
			name: "malformed _nodes metadata",
			body: `{
				"_nodes": "not-an-object",
				"cluster_name": "test",
				"nodes": {}
			}`,
			wantErrContains: "parse _nodes metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
				}, nil
			})

			u, _ := url.Parse("http://localhost:9200")
			tp, err := New(Config{
				URLs:              []*url.URL{u},
				Transport:         rt,
				EnableDebugLogger: tt.enableDebug,
			})
			require.NoError(t, err)

			nodes, err := tp.getNodesInfo(t.Context())

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
				require.Nil(t, nodes)
			}
			if tt.wantErrContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrContains)
				require.Nil(t, nodes)
			}
			if tt.wantErrIs == nil && tt.wantErrContains == "" {
				require.NoError(t, err)
				require.Len(t, nodes, tt.wantNodes)
			}
		})
	}
}

// gatedNodesHandler returns a /_nodes/http handler that signals entered when
// the request arrives, then blocks until gate is closed before responding with
// a single data node. The handler also selects on t.Context().Done() so that
// test cleanup can unblock it.
func gatedNodesHandler(t *testing.T, entered chan<- struct{}, gate <-chan struct{}) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-t.Context().Done():
			http.Error(w, "test context cancelled", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"_nodes":{"total":1,"successful":1,"failed":0},
			"cluster_name":"test",
			"nodes":{
				"n1":{
					"name":"n1",
					"roles":["data","ingest"],
					"http":{"publish_address":"127.0.0.1:9200"}
				}
			}
		}`)
	}
}

// gatedErrorNodesHandler returns a /_nodes/http handler that signals entered
// when the request arrives, then blocks until gate is closed before responding
// with an HTTP 503 to trigger a discovery error. The handler also selects on
// t.Context().Done() so that test cleanup can unblock it.
func gatedErrorNodesHandler(t *testing.T, entered chan<- struct{}, gate <-chan struct{}) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-t.Context().Done():
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}
}

// newGatedDiscoverClient creates a transport Client wired to the given
// handler routes, with discoverMu.cond properly initialized.
func newGatedDiscoverClient(t *testing.T, routes mockhttp.HandlerMap) *Transport {
	t.Helper()
	transport := mockhttp.NewTransportFromRoutes(t, routes)
	u, _ := url.Parse("http://127.0.0.1:9200")
	tp, err := New(Config{URLs: []*url.URL{u}, Transport: transport})
	require.NoError(t, err)
	tp.discoverMu.cond = sync.NewCond(&tp.discoverMu)
	return tp
}

func TestDiscoverNodesBlocking(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})

	routes := mockhttp.GetDefaultHandlers(t)
	routes["/_nodes/http"] = gatedNodesHandler(t, entered, gate)
	tp := newGatedDiscoverClient(t, routes)

	// Goroutine A: start discovery (blocks in handler on gate).
	var wg sync.WaitGroup
	wg.Go(func() {
		tp.DiscoverNodes(t.Context())
	})

	// Wait for handler to be entered -- discovery is now in-flight.
	<-entered

	// Goroutine B: should block in DiscoverNodes until A finishes.
	bDone := make(chan error, 1)
	go func() {
		bDone <- tp.DiscoverNodes(t.Context())
	}()

	// B should not have returned yet (gate still closed, A still blocked).
	select {
	case <-bDone:
		t.Fatal("goroutine B returned before discovery finished")
	default:
	}

	// Release A -- A finishes, B wakes up.
	close(gate)
	wg.Wait()

	err := <-bDone
	require.NoError(t, err, "goroutine B should succeed after waiting")
}

func TestDiscoverNodesBlockingPropagatesError(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})

	routes := mockhttp.GetDefaultHandlers(t)
	routes["/_nodes/http"] = gatedErrorNodesHandler(t, entered, gate)
	tp := newGatedDiscoverClient(t, routes)

	// Goroutine A: start discovery that will fail.
	aDone := make(chan error, 1)
	go func() {
		aDone <- tp.DiscoverNodes(t.Context())
	}()

	<-entered // A is in handler, discovery in-flight.

	// Goroutine B: waits for A, receives the same error via lastErr.
	bDone := make(chan error, 1)
	go func() {
		bDone <- tp.DiscoverNodes(t.Context())
	}()

	close(gate)

	errA := <-aDone
	errB := <-bDone

	require.Error(t, errA, "runner should report discovery error")
	require.Error(t, errB, "waiter should receive the same error")
	require.Equal(t, errA, errB)
}

func TestDiscoverNodesBlockingContextCancel(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	defer close(gate) // prevent goroutine leak

	routes := mockhttp.GetDefaultHandlers(t)
	routes["/_nodes/http"] = gatedNodesHandler(t, entered, gate)
	tp := newGatedDiscoverClient(t, routes)

	// Goroutine A: start slow discovery.
	go func() {
		tp.DiscoverNodes(t.Context())
	}()

	<-entered // A is in handler, discovery in-flight.

	// B: call DiscoverNodes with an already-cancelled context.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := tp.DiscoverNodes(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTryDiscoverNodesNonBlocking(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	defer close(gate) // prevent goroutine leak

	routes := mockhttp.GetDefaultHandlers(t)
	routes["/_nodes/http"] = gatedNodesHandler(t, entered, gate)
	tp := newGatedDiscoverClient(t, routes)

	// Start discovery in a goroutine.
	go func() {
		tp.DiscoverNodes(t.Context())
	}()

	<-entered // discovery is in-flight.

	// tryDiscoverNodes should return nil immediately without blocking.
	err := tp.tryDiscoverNodes(t.Context())
	require.NoError(t, err)
}

func TestDiscoverNodesSequential(t *testing.T) {
	routes := mockhttp.GetDefaultHandlersWithNodes(t, map[string][]string{
		"node1": {"data", "ingest"},
	})
	tp := newGatedDiscoverClient(t, routes)

	// First call succeeds.
	err := tp.DiscoverNodes(t.Context())
	require.NoError(t, err)

	// Second call also succeeds (starts a fresh discovery).
	err = tp.DiscoverNodes(t.Context())
	require.NoError(t, err)
}

// TestDiscoverNodesWaiterDoesNotInheritRunnerCancellation guards the F6
// regression: a goroutine that blocks waiting on an in-progress discovery
// must not inherit the runner's context.Canceled when its own context is
// healthy. The runner's cancellation is stored in lastErr; before the fix the
// waiter returned it verbatim, which looked like the waiter's own
// cancellation and could suppress a legitimate retry. The waiter now gets the
// errDiscoveryInterrupted sentinel instead.
func TestDiscoverNodesWaiterDoesNotInheritRunnerCancellation(t *testing.T) {
	c := &Transport{}
	c.discoverMu.cond = sync.NewCond(&c.discoverMu)

	// Simulate an in-progress discovery whose runner was cancelled.
	c.discoverMu.Lock()
	c.discoverMu.inProgress = true
	c.discoverMu.lastErr = context.Canceled
	c.discoverMu.Unlock()

	done := make(chan error, 1)
	go func() {
		// A healthy context: the waiter's own context is NOT cancelled.
		done <- c.DiscoverNodes(context.Background())
	}()

	// Let the waiter observe inProgress=true and park in cond.Wait().
	time.Sleep(100 * time.Millisecond)

	// Complete the "discovery", leaving the runner's cancellation in lastErr.
	c.discoverMu.Lock()
	c.discoverMu.inProgress = false
	c.discoverMu.Unlock()

	// Wake the waiter (retry to cover a late park) and assert it reports the
	// sentinel rather than the runner's context.Canceled.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(2 * time.Second)
	for {
		c.discoverMu.Lock()
		c.discoverMu.cond.Broadcast()
		c.discoverMu.Unlock()
		select {
		case err := <-done:
			require.ErrorIs(t, err, errDiscoveryInterrupted)
			require.NotErrorIs(t, err, context.Canceled)
			return
		case <-ticker.C:
		case <-timeout:
			t.Fatal("waiter did not return")
		}
	}
}

// TestDiscoverNodesEarlyCtxCancellation covers the early-exit branches in
// DiscoverNodes and tryDiscoverNodes: if ctx is already cancelled, both must
// return ctx.Err() before touching discoverMu or starting any I/O.
func TestDiscoverNodesEarlyCtxCancellation(t *testing.T) {
	routes := mockhttp.GetDefaultHandlersWithNodes(t, map[string][]string{
		"node1": {"data", "ingest"},
	})
	tp := newGatedDiscoverClient(t, routes)

	cases := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "DiscoverNodes returns ctx.Err()",
			call: tp.DiscoverNodes,
		},
		{
			name: "tryDiscoverNodes returns ctx.Err()",
			call: tp.tryDiscoverNodes,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := c.call(ctx)
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

// TestTryDiscoverNodesStartsDiscovery covers the path where tryDiscoverNodes
// finds no discovery in progress and runs one itself via doDiscoverNodes.
// TestTryDiscoverNodesNonBlocking only exercises the in-progress branch.
func TestTryDiscoverNodesStartsDiscovery(t *testing.T) {
	routes := mockhttp.GetDefaultHandlersWithNodes(t, map[string][]string{
		"node1": {"data", "ingest"},
	})
	tp := newGatedDiscoverClient(t, routes)

	require.False(t, tp.discoverMu.inProgress, "precondition: no discovery in flight")
	err := tp.tryDiscoverNodes(t.Context())
	require.NoError(t, err)
	require.False(t, tp.discoverMu.inProgress, "postcondition: discovery completed")
}

// TestDiscoverNodesWaiterReceivesNonCtxRunnerError covers the branch where the
// waiter's own context is healthy and the runner failed with an error that is
// neither context.Canceled nor context.DeadlineExceeded -- the waiter should
// receive the runner's error verbatim, not the errDiscoveryInterrupted sentinel.
func TestDiscoverNodesWaiterReceivesNonCtxRunnerError(t *testing.T) {
	c := &Transport{}
	c.discoverMu.cond = sync.NewCond(&c.discoverMu)

	runnerErr := fmt.Errorf("simulated discovery failure")

	c.discoverMu.Lock()
	c.discoverMu.inProgress = true
	c.discoverMu.lastErr = runnerErr
	c.discoverMu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- c.DiscoverNodes(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)

	c.discoverMu.Lock()
	c.discoverMu.inProgress = false
	c.discoverMu.Unlock()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(2 * time.Second)
	for {
		c.discoverMu.Lock()
		c.discoverMu.cond.Broadcast()
		c.discoverMu.Unlock()
		select {
		case err := <-done:
			require.ErrorIs(t, err, runnerErr)
			require.NotErrorIs(t, err, errDiscoveryInterrupted)
			return
		case <-ticker.C:
		case <-timeout:
			t.Fatal("waiter did not return")
		}
	}
}

// TestDiscoverNodesWaiterCtxCancelDuringWait covers the in-loop ctx.Err()
// branch in DiscoverNodes: a waiter parked in cond.Wait() whose own context
// is cancelled mid-wait must be woken by the AfterFunc broadcast and return
// ctx.Err() rather than waiting on the runner to finish.
func TestDiscoverNodesWaiterCtxCancelDuringWait(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	defer close(gate)

	routes := mockhttp.GetDefaultHandlers(t)
	routes["/_nodes/http"] = gatedNodesHandler(t, entered, gate)
	tp := newGatedDiscoverClient(t, routes)

	go func() {
		tp.DiscoverNodes(t.Context())
	}()
	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- tp.DiscoverNodes(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not return after its own ctx was cancelled")
	}
}
