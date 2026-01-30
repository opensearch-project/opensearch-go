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
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscovery(t *testing.T) {
	defaultHandler := func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open("testdata/nodes.info.json")
		if err != nil {
			http.Error(w, fmt.Sprintf("Fixture error: %s", err), http.StatusInternalServerError)
			return
		}
		io.Copy(w, f)
	}

	srv := &http.Server{Addr: "localhost:10001", Handler: http.HandlerFunc(defaultHandler), ReadTimeout: 1 * time.Second}
	srvTLS := &http.Server{Addr: "localhost:12001", Handler: http.HandlerFunc(defaultHandler), ReadTimeout: 1 * time.Second}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start server: %s", err)
			return
		}
	}()
	go func() {
		if err := srvTLS.ListenAndServeTLS("testdata/cert.pem", "testdata/key.pem"); err != nil && err != http.ErrServerClosed {
			t.Errorf("Unable to start server: %s", err)
			return
		}
	}()
	defer func() { srv.Close() }()
	defer func() { srvTLS.Close() }()

	time.Sleep(50 * time.Millisecond)

	t.Run("getNodesInfo()", func(t *testing.T) {
		u, _ := url.Parse("http://" + srv.Addr)
		tp, _ := New(Config{URLs: []*url.URL{u}})

		nodes, err := tp.getNodesInfo()
		if err != nil {
			t.Fatalf("ERROR: %s", err)
		}

		if len(nodes) != 4 {
			t.Errorf("Unexpected number of nodes, want=4, got=%d", len(nodes))
		}

		for _, node := range nodes {
			switch node.Name {
			case "es1":
				if node.URL.String() != "http://127.0.0.1:10001" {
					t.Errorf("Unexpected URL: %s", node.URL.String())
				}
			case "es2":
				if node.URL.String() != "http://localhost:10002" {
					t.Errorf("Unexpected URL: %s", node.URL.String())
				}
			case "es3":
				if node.URL.String() != "http://127.0.0.1:10003" {
					t.Errorf("Unexpected URL: %s", node.URL.String())
				}
			case "es4":
				if node.URL.String() != "http://[fc99:3528::a04:812c]:10004" {
					t.Errorf("Unexpected URL: %s", node.URL.String())
				}
			}
		}
	})

	t.Run("getNodesInfo() empty Body", func(t *testing.T) {
		newRoundTripper := func() http.RoundTripper {
			return &mockTransp{
				RoundTripFunc: func(req *http.Request) (*http.Response, error) {
					return &http.Response{Header: http.Header{}}, nil
				},
			}
		}

		u, _ := url.Parse("http://localhost:8080")
		tp, err := New(Config{URLs: []*url.URL{u}, Transport: newRoundTripper()})
		require.NoError(t, err)

		_, err = tp.getNodesInfo()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected empty body")
	})

	t.Run("DiscoverNodes()", func(t *testing.T) {
		u, _ := url.Parse("http://" + srv.Addr)
		tp, _ := New(Config{URLs: []*url.URL{u}})

		tp.DiscoverNodes()

		pool, ok := tp.mu.pool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.mu.pool)
		}

		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.mu.live))
		}

		for _, conn := range pool.mu.live {
			switch conn.Name {
			case "es1":
				if conn.URL.String() != "http://127.0.0.1:10001" {
					t.Errorf("Unexpected URL: %s", conn.URL.String())
				}
			case "es2":
				if conn.URL.String() != "http://localhost:10002" {
					t.Errorf("Unexpected URL: %s", conn.URL.String())
				}
			default:
				t.Errorf("Unexpected node: %s", conn.Name)
			}
		}
	})

	t.Run("DiscoverNodes() with SSL and authorization", func(t *testing.T) {
		u, _ := url.Parse("https://" + srvTLS.Addr)
		tp, _ := New(Config{
			URLs:     []*url.URL{u},
			Username: "foo",
			Password: "bar",
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		})

		tp.DiscoverNodes()

		pool, ok := tp.mu.pool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.mu.pool)
		}

		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.mu.live))
		}

		for _, conn := range pool.mu.live {
			switch conn.Name {
			case "es1":
				if conn.URL.String() != "https://127.0.0.1:10001" {
					t.Errorf("Unexpected URL: %s", conn.URL.String())
				}
			case "es2":
				if conn.URL.String() != "https://localhost:10002" {
					t.Errorf("Unexpected URL: %s", conn.URL.String())
				}
			default:
				t.Errorf("Unexpected node: %s", conn.Name)
			}
		}
	})

	t.Run("scheduleDiscoverNodes()", func(t *testing.T) {
		t.Skip("Skip") // TODO(karmi): Investigate the intermittent failures of this test

		var numURLs int
		u, _ := url.Parse("http://" + srv.Addr)

		tp, _ := New(Config{URLs: []*url.URL{u}, DiscoverNodesInterval: 10 * time.Millisecond})

		tp.mu.Lock()
		numURLs = len(tp.mu.pool.URLs())
		tp.mu.Unlock()
		if numURLs != 1 {
			t.Errorf("Unexpected number of nodes, want=1, got=%d", numURLs)
		}

		time.Sleep(18 * time.Millisecond) // Wait until (*Client).scheduleDiscoverNodes()
		tp.mu.Lock()
		numURLs = len(tp.mu.pool.URLs())
		tp.mu.Unlock()
		if numURLs != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", numURLs)
		}
	})

	t.Run("Role based nodes discovery", func(t *testing.T) {
		type Node struct {
			URL   string
			Roles []string
		}

		type fields struct {
			Nodes map[string]Node
		}
		type wants struct {
			wantErr    bool
			wantsNConn int
		}
		tests := []struct {
			name string
			args fields
			want wants
		}{
			{
				"Default roles should allow every node to be selected",
				fields{
					Nodes: map[string]Node{
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
				wants{
					false, 3,
				},
			},
			{
				"Cluster manager only node should not be selected",
				fields{
					Nodes: map[string]Node{
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

				wants{
					false, 2,
				},
			},
			{
				"Cluster manager and data only nodes should be selected",
				fields{
					Nodes: map[string]Node{
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

				wants{
					false, 2,
				},
			},
			{
				"Default roles should allow every node to be selected",
				fields{
					Nodes: map[string]Node{
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
				wants{
					false, 3,
				},
			},
			{
				"Master only node should not be selected",
				fields{
					Nodes: map[string]Node{
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

				wants{
					false, 2,
				},
			},
			{
				"Master and data only nodes should be selected",
				fields{
					Nodes: map[string]Node{
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

				wants{
					false, 2,
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				urls := make([]*url.URL, 0, len(tt.args.Nodes))
				for _, node := range tt.args.Nodes {
					u, _ := url.Parse(node.URL)
					urls = append(urls, u)
				}

				newRoundTripper := func() http.RoundTripper {
					return &mockTransp{
						RoundTripFunc: func(req *http.Request) (*http.Response, error) {
							nodes := make(map[string]map[string]nodeInfo)
							nodes["nodes"] = make(map[string]nodeInfo)
							for name, node := range tt.args.Nodes {
								nodes["nodes"][name] = nodeInfo{Roles: node.Roles}
							}

							b, _ := json.Marshal(nodes)

							return &http.Response{
								Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
								StatusCode:    http.StatusOK,
								ContentLength: int64(len(b)),
								Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
								Body:          io.NopCloser(bytes.NewReader(b)),
							}, nil
						},
					}
				}

				c, _ := New(Config{
					URLs:      urls,
					Transport: newRoundTripper(),
				})
				c.DiscoverNodes()

				pool, ok := c.mu.pool.(*statusConnectionPool)
				if !ok {
					t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", c.mu.pool)
				}

				if len(pool.mu.live) != tt.want.wantsNConn {
					t.Errorf("Unexpected number of nodes, want=%d, got=%d", tt.want.wantsNConn, len(pool.mu.live))
				}

				for _, conn := range pool.mu.live {
					expectedRoles := make([]string, len(tt.args.Nodes[conn.ID].Roles))
					copy(expectedRoles, tt.args.Nodes[conn.ID].Roles)
					slices.Sort(expectedRoles)

					actualRoles := conn.Roles.toSlice()

					if !reflect.DeepEqual(expectedRoles, actualRoles) {
						t.Errorf("Unexpected roles for node %s, want=%s, got=%s", conn.Name, expectedRoles, actualRoles)
					}
				}

				if err := c.DiscoverNodes(); (err != nil) != tt.want.wantErr {
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
			// Create mock transport that returns our test nodes
			newRoundTripper := func() http.RoundTripper {
				return &mockTransp{
					RoundTripFunc: func(req *http.Request) (*http.Response, error) {
						nodes := make(map[string]map[string]nodeInfo)
						nodes["nodes"] = make(map[string]nodeInfo)

						for name, roles := range tt.nodes {
							nodes["nodes"][name] = nodeInfo{
								ID:    name + "-id",
								Name:  name,
								Roles: roles,
							}
						}

						b, _ := json.Marshal(nodes)
						return &http.Response{
							Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
							StatusCode:    http.StatusOK,
							ContentLength: int64(len(b)),
							Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
							Body:          io.NopCloser(bytes.NewReader(b)),
						}, nil
					},
				}
			}

			u, _ := url.Parse("http://localhost:9200")
			c, err := New(Config{
				URLs:      []*url.URL{u},
				Transport: newRoundTripper(),
			})
			require.NoError(t, err)

			// Perform discovery
			err = c.DiscoverNodes()
			assert.NoError(t, err)

			// Verify results
			pool, ok := c.mu.pool.(*statusConnectionPool)
			require.True(t, ok, "Expected statusConnectionPool")

			// Check that expected nodes are included
			actualNodes := make(map[string]bool)
			for _, conn := range pool.mu.live {
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
			// Create mock transport
			newRoundTripper := func() http.RoundTripper {
				return &mockTransp{
					RoundTripFunc: func(req *http.Request) (*http.Response, error) {
						nodes := make(map[string]map[string]nodeInfo)
						nodes["nodes"] = make(map[string]nodeInfo)

						for name, roles := range tt.nodes {
							nodes["nodes"][name] = nodeInfo{
								ID:    name + "-id",
								Name:  name,
								Roles: roles,
							}
						}

						b, _ := json.Marshal(nodes)
						return &http.Response{
							Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
							StatusCode:    http.StatusOK,
							ContentLength: int64(len(b)),
							Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
							Body:          io.NopCloser(bytes.NewReader(b)),
						}, nil
					},
				}
			}

			u, _ := url.Parse("http://localhost:9200")
			c, err := New(Config{
				URLs:                            []*url.URL{u},
				Transport:                       newRoundTripper(),
				IncludeDedicatedClusterManagers: tt.includeDedicatedClusterManagers,
			})
			require.NoError(t, err)

			// Perform discovery
			err = c.DiscoverNodes()
			assert.NoError(t, err)

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
		{Name: "coordinating-node", Roles: newRoleSet([]string{})}, // No specific roles
	}

	fallback := &mockSelector{}

	t.Run("Generic selector with required roles", func(t *testing.T) {
		// Create a selector that requires both data and ingest roles in one group,
		// OR data and search roles in another group: (data && ingest) || (data && search)
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleData, RoleIngest),
			WithRequiredRoles(RoleData, RoleSearch),
		)

		conn, err := selector.Select(connections)
		require.NoError(t, err)
		// Should match only data-ingest-node (has both data and ingest roles)
		// Note: there's no data-search-node in test data
		require.Equal(t, "data-ingest-node", conn.Name)
	})

	t.Run("Generic selector with excluded roles", func(t *testing.T) {
		// Create a selector that excludes cluster manager roles
		selector := NewRoleBasedSelector(
			WithExcludedRoles(RoleClusterManager),
		)

		conn, err := selector.Select(connections)
		assert.NoError(t, err)
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
		assert.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "no connections found matching required role groups")
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

		assert.NoError(t, err1)
		assert.Error(t, err2) // Strict mode should fail with no warm nodes
		assert.Nil(t, conn2)
		// Fallback selector should return ingest-capable nodes
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn1.Name)
	})
}

// TestRoleBasedSelectors tests the role-based selector with various configurations
func TestRoleBasedSelectors(t *testing.T) {
	// Create test connections with different roles
	connections := []*Connection{
		{Name: "data-node", Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", Roles: newRoleSet([]string{RoleIngest})},
		{Name: "data-ingest-node", Roles: newRoleSet([]string{RoleData, RoleIngest})},
		{Name: "cluster-manager-node", Roles: newRoleSet([]string{RoleClusterManager})},
		{Name: "warm-node", Roles: newRoleSet([]string{RoleWarm})},
		{Name: "search-node", Roles: newRoleSet([]string{RoleSearch})},
		{Name: "coordinating-node", Roles: newRoleSet([]string{})}, // No specific roles
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
		assert.NoError(t, err)
		// Should get either "ingest-node" or "data-ingest-node"
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)

		// Should fall back when no ingest nodes available
		dataOnlyConns := []*Connection{connections[0], connections[3]} // data and cluster-manager
		conn, err = selector.Select(dataOnlyConns)
		assert.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Fallback should work
	})

	t.Run("DataPreferred", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleData),
			WithFallback(fallback),
		)

		conn, err := selector.Select(connections)
		assert.NoError(t, err)
		// Should get a data-capable node
		assert.Contains(t, []string{"data-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("WarmPreferred", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleWarm),
			WithFallback(fallback),
		)

		conn, err := selector.Select(connections)
		assert.NoError(t, err)
		assert.Equal(t, "warm-node", conn.Name)

		// Should fall back when no warm nodes available
		noWarmConns := []*Connection{connections[0], connections[1]} // data and ingest
		conn, err = selector.Select(noWarmConns)
		assert.NoError(t, err)
		assert.Equal(t, "data-node", conn.Name) // Fallback should work
	})

	t.Run("IngestOnly", func(t *testing.T) {
		selector := NewRoleBasedSelector(
			WithRequiredRoles(RoleIngest),
			WithStrictMode(),
		)

		// Should work when ingest nodes are available
		conn, err := selector.Select(connections)
		assert.NoError(t, err)
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)

		// Should fail when no ingest nodes available (strict mode)
		dataOnlyConns := []*Connection{connections[0], connections[3]} // data and cluster-manager
		conn, err = selector.Select(dataOnlyConns)
		assert.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "no connections found matching required role groups")
	})
}

// TestSmartSelector tests the request-aware smart selector
func TestSmartSelector(t *testing.T) {
	// Create test connections
	connections := []*Connection{
		{Name: "data-node", Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", Roles: newRoleSet([]string{RoleIngest})},
		{Name: "data-ingest-node", Roles: newRoleSet([]string{RoleData, RoleIngest})},
	}

	selector := NewSmartSelector()

	t.Run("IngestOperationRouting", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/my-index/_bulk", nil)

		conn, err := selector.SelectForRequest(connections, req)
		assert.NoError(t, err)
		// Should route to ingest-capable node for bulk operations
		assert.Contains(t, []string{"ingest-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("SearchOperationRouting", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/my-index/_search", nil)

		conn, err := selector.SelectForRequest(connections, req)
		assert.NoError(t, err)
		// Should route to data-capable node for search operations
		assert.Contains(t, []string{"data-node", "data-ingest-node"}, conn.Name)
	})

	t.Run("DefaultOperationRouting", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/_cluster/health", nil)

		conn, err := selector.SelectForRequest(connections, req)
		assert.NoError(t, err)
		// Should use default routing
		assert.Equal(t, "data-node", conn.Name) // Mock selector returns first connection
	})
}

// TestRequestRoutingConnectionPool tests the enhanced connection pool
func TestRequestRoutingConnectionPool(t *testing.T) {
	connections := []*Connection{
		{Name: "data-node", Roles: newRoleSet([]string{RoleData})},
		{Name: "ingest-node", Roles: newRoleSet([]string{RoleIngest})},
	}

	pool := NewConnectionPool(connections, NewDefaultSelector())

	racp, ok := pool.(RequestRoutingConnectionPool)
	assert.True(t, ok, "Should implement RequestRoutingConnectionPool")

	t.Run("NextForRequest", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/my-index/_bulk", nil)

		conn, err := racp.NextForRequest(req)
		assert.NoError(t, err)
		assert.Equal(t, "ingest-node", conn.Name) // Should route to ingest node for bulk
	})

	t.Run("BackwardCompatibilityNext", func(t *testing.T) {
		conn, err := pool.Next()
		assert.NoError(t, err)
		// With smart selector, Next() should use round-robin fallback
		assert.Contains(t, []string{"data-node", "ingest-node"}, conn.Name) // Could be either
	})
}

// Mock implementations for testing

type mockSelector struct{}

func (s *mockSelector) Select(connections []*Connection) (*Connection, error) {
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil // Always return first connection
}
