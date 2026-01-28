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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock transport for tests that need HTTP mocking even in integration context
type mockTransp struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func (t *mockTransp) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.RoundTripFunc(req)
}

func TestDiscovery(t *testing.T) {
	var (
		httpPort1, httpPort2, httpPort3, httpPort4 int
		tlsPort1, tlsPort2                         int
	)

	dynamicNodesHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{
			"_nodes": {
				"total": 4,
				"successful": 4,
				"failed": 0
			},
			"cluster_name": "opensearch",
			"nodes": {
				"8g1UNpQNS06tlH1DUMBNhg": {
					"name": "es1",
					"transport_address": "127.0.0.1:9300",
					"host": "127.0.0.1",
					"ip": "127.0.0.1",
					"version": "7.4.2",
					"roles": ["ingest", "cluster_manager", "data"],
					"http": {
						"publish_address": "127.0.0.1:%d"
					}
				},
				"8YR2EBk_QvWI4guQK292RA": {
					"name": "es2",
					"transport_address": "127.0.0.1:9302",
					"host": "127.0.0.1",
					"ip": "127.0.0.1",
					"version": "7.4.2",
					"roles": ["ingest", "cluster_manager", "data"],
					"http": {
						"publish_address": "localhost/127.0.0.1:%d"
					}
				},
				"oSVIMafYQD-4kD0Lz6H4-g": {
					"name": "es3",
					"transport_address": "127.0.0.1:9301",
					"host": "127.0.0.1",
					"ip": "127.0.0.1",
					"version": "7.4.2",
					"roles": ["cluster_manager"],
					"http": {
						"publish_address": "127.0.0.1:%d"
					}
				},
				"4uJ-108zTz27ISgkmAQgfw": {
					"name": "es4",
					"transport_address": "[fc99:3528::a04:812c]:9303",
					"host": "fc99:3528:0:0:0:0:a04:812c",
					"ip": "fc99:3528::a04:812c",
					"version": "7.4.2",
					"roles": ["cluster_manager"],
					"http": {
						"publish_address": "localhost:%d"
					}
				}
			}
		}`, httpPort1, httpPort2, httpPort3, httpPort4)
	}

	tlsNodesHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{
			"_nodes": {
				"total": 2,
				"successful": 2,
				"failed": 0
			},
			"cluster_name": "opensearch",
			"nodes": {
				"8g1UNpQNS06tlH1DUMBNhg": {
					"name": "es1",
					"transport_address": "127.0.0.1:9300",
					"host": "127.0.0.1",
					"ip": "127.0.0.1",
					"version": "7.4.2",
					"roles": ["ingest", "cluster_manager", "data"],
					"http": {
						"publish_address": "127.0.0.1:%d"
					}
				},
				"8YR2EBk_QvWI4guQK292RA": {
					"name": "es2",
					"transport_address": "127.0.0.1:9302",
					"host": "127.0.0.1",
					"ip": "127.0.0.1",
					"version": "7.4.2",
					"roles": ["ingest", "cluster_manager", "data"],
					"http": {
						"publish_address": "localhost/127.0.0.1:%d"
					}
				}
			}
		}`, tlsPort1, tlsPort2)
	}

	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{
			"name": "test-node",
			"cluster_name": "opensearch-cluster",
			"cluster_uuid": "test-cluster-uuid",
			"version": {
				"distribution": "opensearch",
				"number": "1.3.0",
				"build_type": "tar",
				"build_hash": "test-build-hash",
				"build_date": "2023-01-01T00:00:00.000000000Z",
				"build_snapshot": false,
				"lucene_version": "8.10.1",
				"minimum_wire_compatibility_version": "6.8.0",
				"minimum_index_compatibility_version": "6.0.0-beta1"
			},
			"tagline": "The OpenSearch Project: https://opensearch.org/"
		}`)
	}

	createMux := func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("/_nodes/http", dynamicNodesHandler)
		mux.HandleFunc("/", healthHandler)
		return mux
	}

	createTLSMux := func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("/_nodes/http", tlsNodesHandler)
		mux.HandleFunc("/", healthHandler)
		return mux
	}

	// Start servers on dynamic ports
	srv1 := &http.Server{Addr: "localhost:0", Handler: createMux(), ReadTimeout: 1 * time.Second}
	srv2 := &http.Server{Addr: "localhost:0", Handler: createMux(), ReadTimeout: 1 * time.Second}
	srv3 := &http.Server{Addr: "localhost:0", Handler: createMux(), ReadTimeout: 1 * time.Second}
	srv4 := &http.Server{Addr: "localhost:0", Handler: createMux(), ReadTimeout: 1 * time.Second}
	srvTLS1 := &http.Server{Addr: "localhost:0", Handler: createTLSMux(), ReadTimeout: 1 * time.Second}
	srvTLS2 := &http.Server{Addr: "localhost:0", Handler: createTLSMux(), ReadTimeout: 1 * time.Second}

	// Start HTTP servers and get assigned ports
	l1, _ := net.Listen("tcp", "localhost:0")
	l2, _ := net.Listen("tcp", "localhost:0")
	l3, _ := net.Listen("tcp", "localhost:0")
	l4, _ := net.Listen("tcp", "localhost:0")
	httpPort1 = l1.Addr().(*net.TCPAddr).Port
	httpPort2 = l2.Addr().(*net.TCPAddr).Port
	httpPort3 = l3.Addr().(*net.TCPAddr).Port
	httpPort4 = l4.Addr().(*net.TCPAddr).Port

	// Start TLS servers and get assigned ports
	tl1, _ := net.Listen("tcp", "localhost:0")
	tl2, _ := net.Listen("tcp", "localhost:0")
	tlsPort1 = tl1.Addr().(*net.TCPAddr).Port
	tlsPort2 = tl2.Addr().(*net.TCPAddr).Port

	go func() { srv1.Serve(l1) }()
	go func() { srv2.Serve(l2) }()
	go func() { srv3.Serve(l3) }()
	go func() { srv4.Serve(l4) }()
	go func() { srvTLS1.ServeTLS(tl1, "testdata/cert.pem", "testdata/key.pem") }()
	go func() { srvTLS2.ServeTLS(tl2, "testdata/cert.pem", "testdata/key.pem") }()

	defer func() { srv1.Close() }()
	defer func() { srv2.Close() }()
	defer func() { srv3.Close() }()
	defer func() { srv4.Close() }()
	defer func() { srvTLS1.Close() }()
	defer func() { srvTLS2.Close() }()

	time.Sleep(100 * time.Millisecond)

	t.Run("getNodesInfo()", func(t *testing.T) {
		u := &url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort1))}
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
				require.Equal(t, "http://"+net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", httpPort1)), node.url.String())
			case "es2":
				require.Equal(t, "http://"+net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort2)), node.url.String())
			case "es3":
				require.Equal(t, "http://"+net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", httpPort3)), node.url.String())
			case "es4":
				require.Equal(t, "http://"+net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort4)), node.url.String())
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

		_, err = tp.getNodesInfo(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected empty body")
	})

	t.Run("DiscoverNodes()", func(t *testing.T) {
		u := &url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort1))}
		tp, _ := New(Config{URLs: []*url.URL{u}})

		tp.DiscoverNodes(t.Context())

		pool, ok := tp.mu.connectionPool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.mu.connectionPool)
		}

		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.mu.live))
		}

		for _, conn := range pool.mu.live {
			switch conn.Name {
			case "es1":
				require.Equal(t, "http://"+net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", httpPort1)), conn.URL.String())
			case "es2":
				require.Equal(t, "http://"+net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort2)), conn.URL.String())
			default:
				t.Errorf("Unexpected node: %s", conn.Name)
			}
		}
	})

	t.Run("DiscoverNodes() with SSL and authorization", func(t *testing.T) {
		u := &url.URL{Scheme: "https", Host: net.JoinHostPort("localhost", fmt.Sprintf("%d", tlsPort1))}
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

		tp.DiscoverNodes(t.Context())

		pool, ok := tp.mu.connectionPool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.mu.connectionPool)
		}

		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.mu.live))
		}

		for _, conn := range pool.mu.live {
			switch conn.Name {
			case "es1":
				require.Equal(t, "https://"+net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", tlsPort1)), conn.URL.String())
			case "es2":
				require.Equal(t, "https://"+net.JoinHostPort("localhost", fmt.Sprintf("%d", tlsPort2)), conn.URL.String())
			default:
				t.Errorf("Unexpected node: %s", conn.Name)
			}
		}
	})

	t.Run("scheduleDiscoverNodes()", func(t *testing.T) {
		t.Skip("Skip") // TODO(karmi): Investigate the intermittent failures of this test

		var numURLs int
		u := &url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", fmt.Sprintf("%d", httpPort1))}

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
							// Handle health check requests
							if req.URL.Path == "/" {
								healthResponse := `{
									"name": "test-node",
									"cluster_name": "opensearch-cluster",
									"cluster_uuid": "test-cluster-uuid",
									"version": {
										"distribution": "opensearch",
										"number": "1.3.0",
										"build_type": "tar",
										"build_hash": "test-build-hash",
										"build_date": "2023-01-01T00:00:00.000000000Z",
										"build_snapshot": false,
										"lucene_version": "8.10.1",
										"minimum_wire_compatibility_version": "6.8.0",
										"minimum_index_compatibility_version": "6.0.0-beta1"
									},
									"tagline": "The OpenSearch Project: https://opensearch.org/"
								}`
								return &http.Response{
									Status:        "200 OK",
									StatusCode:    http.StatusOK,
									ContentLength: int64(len(healthResponse)),
									Header:        http.Header{"Content-Type": []string{"application/json"}},
									Body:          io.NopCloser(bytes.NewReader([]byte(healthResponse))),
								}, nil
							}

							// Handle nodes info requests
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
				c.DiscoverNodes(t.Context())

				pool, ok := c.mu.connectionPool.(*statusConnectionPool)
				if !ok {
					t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", c.mu.connectionPool)
				}

				if len(pool.mu.live) != tt.want.wantsNConn {
					t.Errorf("Unexpected number of nodes, want=%d, got=%d", tt.want.wantsNConn, len(pool.mu.live))
				}

				for _, conn := range pool.mu.live {
					if !reflect.DeepEqual(tt.args.Nodes[conn.ID].Roles, conn.Roles) {
						t.Errorf("Unexpected roles for node %s, want=%s, got=%s", conn.Name, tt.args.Nodes[conn.ID], conn.Roles)
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
			err = c.DiscoverNodes(t.Context())
			assert.NoError(t, err)

			// Verify results
			pool, ok := c.mu.connectionPool.(*statusConnectionPool)
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
			err = c.DiscoverNodes(t.Context())
			assert.NoError(t, err)

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
			assert.Equal(t, expectedTotal, len(actualNodes),
				"Expected %d nodes but got %d", expectedTotal, len(actualNodes))
		})
	}
}

// TestRoleBasedSelectors tests the role-based selector with various configurations
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
