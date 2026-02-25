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
	"crypto/tls"
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
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
		// opensearchapi -> opensearch-go/v4 -> opensearchtransport
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
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected empty body")
	})

	t.Run("DiscoverNodes()", func(t *testing.T) {
		u, _ := url.Parse("http://" + srv.Addr)
		tp, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		err = tp.DiscoverNodes(t.Context())
		require.NoError(t, err, "Discovery should succeed")

		pool, ok := tp.mu.connectionPool.(*multiServerPool)
		require.True(t, ok, "Expected multiServerPool after discovery")

		// The discovery should include es1 and es2 (data+ingest+cluster_manager)
		// but exclude es3 and es4 (cluster_manager only)
		totalConnections := len(pool.mu.ready) + len(pool.mu.dead)
		require.Equal(t, 2, totalConnections, "Should have 2 total connections after policy filtering")

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
		require.NotContains(t, foundNodes, "es3", "Should not include es3 (cluster_manager only)")
		require.NotContains(t, foundNodes, "es4", "Should not include es4 (cluster_manager only)")
	})

	t.Run("DiscoverNodes() with SSL and authorization", func(t *testing.T) {
		u, _ := url.Parse("https://" + srvTLS1.Addr)
		tp, _ := New(Config{
			URLs:        []*url.URL{u},
			Username:    "foo",
			Password:    "bar",
			HealthCheck: NoOpHealthCheck, // Disable health checks for test resurrection simulation
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
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

	t.Run("scheduleDiscoverNodes()", func(t *testing.T) {
		t.Skip("Skip") // TODO(karmi): Investigate the intermittent failures of this test

		var numURLs int
		u, _ := url.Parse("http://" + srv.Addr)

		tp, _ := New(Config{URLs: []*url.URL{u}, DiscoverNodesInterval: 10 * time.Millisecond})

		tp.mu.Lock()
		numURLs = len(tp.mu.connectionPool.URLs())
		tp.mu.Unlock()
		if numURLs != 1 {
			t.Errorf("Unexpected number of nodes, want=1, got=%d", numURLs)
		}

		time.Sleep(18 * time.Millisecond) // Wait until (*Client).scheduleDiscoverNodes()
		tp.mu.Lock()
		numURLs = len(tp.mu.connectionPool.URLs())
		tp.mu.Unlock()
		if numURLs != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", numURLs)
		}
	})

	t.Run("Role based nodes discovery", func(t *testing.T) {
		// NOTE: Transport tests cannot import opensearchapi due to circular dependencies:
		// opensearchapi -> opensearch-go/v4 -> opensearchtransport
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
					false, 3,
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
					false, 2,
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
					false, 2,
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
					false, 3,
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
					false, 2,
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
					false, 2,
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
				redirectTransport := &http.Transport{
					DialContext: (&net.Dialer{}).DialContext,
				}
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
				allConns := append(pool.mu.ready, pool.mu.dead...)
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

				if err := c.DiscoverNodes(t.Context()); (err != nil) != tt.want.wantErr {
					t.Errorf("DiscoverNodes() error = %v, wantErr %v", err, tt.want.wantErr)
				}
			})
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
				nodes := make(map[string]map[string]nodeInfo)
				nodes["nodes"] = make(map[string]nodeInfo)

				port := 9200
				for name, roles := range tt.nodes {
					nodes["nodes"][name] = nodeInfo{
						ID:    name + "-id",
						Name:  name,
						Roles: roles,
						HTTP: nodeInfoHTTP{
							PublishAddress: net.JoinHostPort("localhost", strconv.Itoa(port)),
						},
					}
					port++
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(nodes)
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
			assert.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*multiServerPool)
			require.True(t, ok, "Expected multiServerPool")

			// Check that expected nodes are included (ready or dead list)
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.ready {
				actualNodes[conn.Name] = true
			}
			for _, conn := range pool.mu.dead {
				actualNodes[conn.Name] = true
			}

			assert.Equal(t, len(tt.expectedNodes), len(actualNodes),
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
			// Create a shared handler for all mock node servers.
			testMux := http.NewServeMux()

			// Create one listener per node so each has a unique publish_address.
			// nodeAddrs maps node name -> "host:port" of its listener.
			nodeAddrs := make(map[string]string, len(tt.nodes))
			var servers []*http.Server
			var listeners []net.Listener

			for name := range tt.nodes {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				listeners = append(listeners, ln)

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
				URLs:                            urls,
				IncludeDedicatedClusterManagers: tt.includeDedicatedClusterManagers,
			})
			require.NoError(t, err)

			// Perform discovery
			err = c.DiscoverNodes(t.Context())
			assert.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*multiServerPool)
			require.True(t, ok, "Expected multiServerPool")

			// Check included nodes (ready or dead list)
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.ready {
				actualNodes[conn.Name] = true
			}
			for _, conn := range pool.mu.dead {
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
			assert.Equal(t, expectedTotal, len(actualNodes),
				"Expected %d nodes but got %d", expectedTotal, len(actualNodes))
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
		assert.True(t, policy.IsEnabled())

		// Get connection pool - should match data-ingest-node first
		pool, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, pool)

		// Should match only data-ingest-node (has both data and ingest roles)
		conn, err := pool.Next()
		require.NoError(t, err)
		require.Equal(t, "data-ingest-node", conn.Name)
	})

	t.Run("RolePolicy with cluster manager exclusion", func(t *testing.T) {
		// Create connections for testing
		connections := []*Connection{
			{Name: "data-node", URL: &url.URL{Host: "data-node:9200"}, Roles: newRoleSet([]string{RoleData})},
			{Name: "cluster-manager-node", URL: &url.URL{Host: "cm-node:9200"}, Roles: newRoleSet([]string{RoleClusterManager})},
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
		assert.True(t, policy.IsEnabled())

		// Get connection pool and verify it only contains data node
		pool, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		require.NotNil(t, pool)

		// The pool should contain only the data node, not the cluster manager
		conn, err := pool.Next()
		require.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name)
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
		assert.False(t, policy.IsEnabled())

		// Eval should return nil (no matching connections)
		pool, err := policy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err)
		assert.Nil(t, pool)
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
		assert.True(t, ingestPolicy.IsEnabled())
		pool1, err1 := ingestPolicy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err1)
		require.NotNil(t, pool1)

		conn1, err := pool1.Next()
		require.NoError(t, err)
		// Should return ingest-capable nodes
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn1.Name)

		// Test warm policy
		assert.True(t, warmPolicy.IsEnabled())
		pool2, err2 := warmPolicy.Eval(t.Context(), &http.Request{})
		require.NoError(t, err2)
		require.NotNil(t, pool2)

		conn2, err := pool2.Next()
		require.NoError(t, err)
		assert.Equal(t, "warm-node", conn2.Name)
	})
}

// TestRolePolicies tests the role-based policies with various configurations
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

		// Simulate successful health check to move connection to ready pool
		statusPool := pool.(*multiServerPool)
		statusPool.OnSuccess(conn)

		// Now get connection from ready pool
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
		assert.Equal(t, tt.want, got, "gcd(%d, %d)", tt.a, tt.b)
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
			assert.Equal(t, int32(1), c.weight.Load())
		}
	})

	t.Run("two sizes", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16)}
		computeWeights(conns)
		assert.Equal(t, int32(1), conns[0].weight.Load()) // 8/8
		assert.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
	})

	t.Run("three sizes", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16), makeConn(24)}
		computeWeights(conns)
		assert.Equal(t, int32(1), conns[0].weight.Load())
		assert.Equal(t, int32(2), conns[1].weight.Load())
		assert.Equal(t, int32(3), conns[2].weight.Load())
	})

	t.Run("non-power-of-2 mixed", func(t *testing.T) {
		conns := []*Connection{makeConn(8), makeConn(16), makeConn(32), makeConn(40)}
		computeWeights(conns)
		assert.Equal(t, int32(1), conns[0].weight.Load()) // 8/8
		assert.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
		assert.Equal(t, int32(4), conns[2].weight.Load()) // 32/8
		assert.Equal(t, int32(5), conns[3].weight.Load()) // 40/8
	})

	t.Run("larger non-power-of-2", func(t *testing.T) {
		conns := []*Connection{makeConn(24), makeConn(32), makeConn(40)}
		computeWeights(conns)
		assert.Equal(t, int32(3), conns[0].weight.Load()) // 24/8
		assert.Equal(t, int32(4), conns[1].weight.Load()) // 32/8
		assert.Equal(t, int32(5), conns[2].weight.Load()) // 40/8
	})

	t.Run("unknown cores get weight 1", func(t *testing.T) {
		conns := []*Connection{makeConn(0), makeConn(16), makeConn(8)}
		computeWeights(conns)
		assert.Equal(t, int32(1), conns[0].weight.Load()) // unknown -> 1
		assert.Equal(t, int32(2), conns[1].weight.Load()) // 16/8
		assert.Equal(t, int32(1), conns[2].weight.Load()) // 8/8
	})

	t.Run("all unknown leaves weights unchanged", func(t *testing.T) {
		conns := []*Connection{makeConn(0), makeConn(0)}
		conns[0].weight.Store(3) // pre-set
		conns[1].weight.Store(5)
		computeWeights(conns)
		assert.Equal(t, int32(3), conns[0].weight.Load()) // unchanged
		assert.Equal(t, int32(5), conns[1].weight.Load()) // unchanged
	})

	t.Run("empty slice is no-op", func(t *testing.T) {
		computeWeights(nil)
		computeWeights([]*Connection{})
	})
}

func TestCreateOrUpdateSingleNodePool(t *testing.T) {
	t.Run("single ready conn creates singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		conn.state.Store(int64(newConnState(lcActive)))

		client := &Client{}
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
		conn.state.Store(int64(newConnState(lcDead)))

		client := &Client{}
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
		conn.state.Store(int64(newConnState(lcActive)))

		msp := &multiServerPool{}
		msp.mu.ready = []*Connection{conn}
		msp.mu.activeCount = 1
		msp.mu.dead = []*Connection{}

		client := &Client{}
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
		newConn.state.Store(int64(newConnState(lcActive)))

		client := &Client{}
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
		client := &Client{}

		found := client.findConnectionByURL(pool, "http://node1:9200")
		require.Equal(t, conn, found)
	})

	t.Run("returns nil when not in singleServerPool", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		pool := &singleServerPool{connection: conn}
		client := &Client{}

		found := client.findConnectionByURL(pool, "http://other:9200")
		require.Nil(t, found)
	})

	t.Run("finds in multiServerPool ready list", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "node1:9200"}}
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{conn}
		pool.mu.dead = []*Connection{}
		client := &Client{}

		found := client.findConnectionByURL(pool, "http://node1:9200")
		require.Equal(t, conn, found)
	})

	t.Run("finds in multiServerPool dead list", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "dead:9200"}}
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{conn}
		client := &Client{}

		found := client.findConnectionByURL(pool, "http://dead:9200")
		require.Equal(t, conn, found)
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{}
		client := &Client{}

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

		client := &Client{
			serverMaxNewConnsPerSec: 0,
			clientsPerServer:        0,
			healthCheckRate:         0,
		}

		client.recalculateCapacityModel(conns)

		// Min cores = 4
		require.Equal(t, float64(4)*serverMaxNewConnsPerSecMultiplier, client.serverMaxNewConnsPerSec)
		require.Equal(t, float64(4), client.clientsPerServer)
		require.Equal(t, float64(4)*healthCheckRateMultiplier, client.healthCheckRate)
	})

	t.Run("no-op when no cores known", func(t *testing.T) {
		conns := []*Connection{
			makeConnWithCores("a:9200", 0),
		}

		client := &Client{
			serverMaxNewConnsPerSec: 99.0,
			clientsPerServer:        99.0,
			healthCheckRate:         99.0,
		}

		client.recalculateCapacityModel(conns)

		// Should remain unchanged
		require.Equal(t, 99.0, client.serverMaxNewConnsPerSec)
		require.Equal(t, 99.0, client.clientsPerServer)
		require.Equal(t, 99.0, client.healthCheckRate)
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

	newDiscoveryClient := func() *Client {
		ctx, cancel := context.WithCancel(context.Background())
		client := &Client{
			ctx:        ctx,
			cancelFunc: cancel,
			urls:       []*url.URL{{Scheme: "http", Host: "seed:9200"}},
			// Provide a transport that returns errors so background health checks
			// (from scheduleResurrect goroutines) don't panic on nil transport.
			transport: &http.Transport{},
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

		require.Greater(t, obs.count("discovery_add"), 0)
		require.Greater(t, obs.count("discovery_remove"), 0)
		require.Greater(t, obs.count("discovery_unchanged"), 0)
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
		conn.mu.deadSince = time.Now().Add(-5 * time.Second)
		conn.mu.Unlock()
		conn.failures.Store(3)

		// Re-discover with the SAME pointer (simulating nodeDiscovery reuse) and
		// healthCheckedAt AFTER the deadSince -> stale dead state should be cleared
		healthCheckedAt := time.Now()
		err = client.updateConnectionPool(t.Context(), healthCheckedAt, []*Connection{conn}, nil)
		require.NoError(t, err)

		// The old connection's dead state should have been cleared (resurrected)
		conn.mu.RLock()
		deadSince := conn.mu.deadSince
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
		conn.mu.deadSince = healthCheckedAt.Add(1 * time.Second)
		conn.mu.Unlock()

		// Re-discover with SAME pointer: dead state is newer than healthCheckedAt -> should stay dead
		err = client.updateConnectionPool(t.Context(), healthCheckedAt, []*Connection{conn}, nil)
		require.NoError(t, err)

		conn.mu.RLock()
		deadSince := conn.mu.deadSince
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
		require.True(t, dead.mu.deadSince.IsZero(), "deadSince should be zero before pool placement")
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
		require.False(t, deadConn.mu.deadSince.IsZero(), "deadSince must be set for dead-list connections")
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
		dead.state.Store(int64(newConnState(lcDead | lcNeedsWarmup | lcNeedsHardware)))

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
		existing.state.Store(int64(newConnState(lcActive)))
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
		existing.state.Store(int64(newConnState(lcActive)))
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
			allConns := append(p.mu.ready, p.mu.dead...)
			require.Len(t, allConns, 1)
			require.Equal(t, "new-id", allConns[0].ID)
			require.NotSame(t, existing, allConns[0], "should create new Connection when ID changes")
		default:
			t.Fatalf("unexpected pool type: %T", pool)
		}
	})
}
