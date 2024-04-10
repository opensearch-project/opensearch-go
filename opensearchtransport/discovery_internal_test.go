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
	"net/http"
	"net/url"
	"os"
	"reflect"
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

		pool, ok := tp.pool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.pool)
		}

		if len(pool.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.live))
		}

		for _, conn := range pool.live {
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

		pool, ok := tp.pool.(*statusConnectionPool)
		if !ok {
			t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", tp.pool)
		}

		if len(pool.live) != 2 {
			t.Errorf("Unexpected number of nodes, want=2, got=%d", len(pool.live))
		}

		for _, conn := range pool.live {
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

		tp.Lock()
		numURLs = len(tp.pool.URLs())
		tp.Unlock()
		if numURLs != 1 {
			t.Errorf("Unexpected number of nodes, want=1, got=%d", numURLs)
		}

		time.Sleep(18 * time.Millisecond) // Wait until (*Client).scheduleDiscoverNodes()
		tp.Lock()
		numURLs = len(tp.pool.URLs())
		tp.Unlock()
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
				var urls []*url.URL
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

				pool, ok := c.pool.(*statusConnectionPool)
				if !ok {
					t.Fatalf("Unexpected pool, want=statusConnectionPool, got=%T", c.pool)
				}

				if len(pool.live) != tt.want.wantsNConn {
					t.Errorf("Unexpected number of nodes, want=%d, got=%d", tt.want.wantsNConn, len(pool.live))
				}

				for _, conn := range pool.live {
					if !reflect.DeepEqual(tt.args.Nodes[conn.ID].Roles, conn.Roles) {
						t.Errorf("Unexpected roles for node %s, want=%s, got=%s", conn.Name, tt.args.Nodes[conn.ID], conn.Roles)
					}
				}

				if err := c.DiscoverNodes(); (err != nil) != tt.want.wantErr {
					t.Errorf("DiscoverNodes() error = %v, wantErr %v", err, tt.want.wantErr)
				}
			})
		}
	})
}
