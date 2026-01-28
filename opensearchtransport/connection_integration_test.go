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

//nolint:testpackage // Tests internal implementation details (statusConnectionPool, etc.)
package opensearchtransport

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

const serverReadinessTimeout = 30 * time.Second

func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// waitForServerReady waits for a server to be ready by trying to connect to it
func waitForServerReady(t *testing.T, addr string, timeout time.Duration) error {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("server %s not ready after %v", addr, timeout)
}

func TestStatusConnectionPool(t *testing.T) {
	const numServers = 3

	var (
		server     *http.Server
		servers    []*http.Server
		serverURLs = make([]*url.URL, 0, numServers)

		defaultHandler = func(w http.ResponseWriter, r *http.Request) {
			// Return proper OpenSearch root endpoint JSON response
			w.Header().Set("Content-Type", "application/json")
			response := `{
  "name": "test-node",
  "cluster_name": "test-cluster",
  "cluster_uuid": "test-cluster-uuid",
  "version": {
    "number": "3.4.0",
    "build_type": "tar",
    "build_hash": "test-hash",
    "build_date": "2024-01-01T00:00:00.000Z",
    "build_snapshot": false,
    "lucene_version": "9.11.0",
    "minimum_wire_compatibility_version": "7.10.0",
    "minimum_index_compatibility_version": "7.0.0",
    "distribution": "opensearch"
  },
  "tagline": "The OpenSearch Project: https://opensearch.org/"
}`
			fmt.Fprint(w, response)
		}
	)

	for i := 1; i <= numServers; i++ {
		addr := fmt.Sprintf("localhost:1000%d", i)
		s := NewServer(addr, http.HandlerFunc(defaultHandler))

		go func(s *http.Server) {
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				t.Errorf("Unable to start server: %s", err)
			}
		}(s)

		defer func(s *http.Server) { s.Close() }(s)

		servers = append(servers, s)

		// Wait for server to be ready before proceeding
		if err := waitForServerReady(t, addr, serverReadinessTimeout); err != nil {
			t.Fatalf("Server not ready: %v", err)
		}
	}

	for _, s := range servers {
		u, _ := url.Parse("http://" + s.Addr)
		serverURLs = append(serverURLs, u)
	}

	cfg := Config{URLs: serverURLs}

	transport, _ := New(cfg)

	transport.mu.RLock()
	// Access the connection pool directly
	connectionPool := transport.mu.connectionPool
	if connectionPool == nil {
		transport.mu.RUnlock()
		t.Fatalf("Expected connection pool for multi-node setup")
	}

	// For status connection pool, set the resurrect timeout
	if statusPool, ok := connectionPool.(*statusConnectionPool); ok {
		statusPool.resurrectTimeoutInitial = time.Second
	}
	transport.mu.RUnlock()

	// Helper function to get the status connection pool
	getPool := func() *statusConnectionPool {
		transport.mu.RLock()
		defer transport.mu.RUnlock()
		if statusPool, ok := transport.mu.connectionPool.(*statusConnectionPool); ok {
			return statusPool
		}
		return nil
	}

	for i := 1; i <= 9; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		res, err := transport.Perform(req)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code, want=200, got=%d", res.StatusCode)
		}
	}

	pool := getPool()
	if pool != nil {
		pool.mu.Lock()
		if len(pool.mu.live) != 3 {
			t.Errorf("Unexpected number of live connections, want=3, got=%d", len(pool.mu.live))
		}
		pool.mu.Unlock()
	}

	server = servers[1]
	if err := server.Close(); err != nil {
		t.Fatalf("Unable to close server: %s", err)
	}

	for i := 1; i <= 9; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		res, err := transport.Perform(req)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code, want=200, got=%d", res.StatusCode)
		}
	}

	pool = getPool()
	if pool != nil {
		pool.mu.Lock()
		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of live connections, want=2, got=%d", len(pool.mu.live))
		}
		pool.mu.Unlock()

		pool.mu.Lock()
		if len(pool.mu.dead) != 1 {
			t.Errorf("Unexpected number of dead connections, want=1, got=%d", len(pool.mu.dead))
		}
		pool.mu.Unlock()
	}

	server = NewServer("localhost:10002", http.HandlerFunc(defaultHandler))
	servers[1] = server
	go func() {
		if err := server.ListenAndServe(); err != nil {
			t.Errorf("Unable to start server: %s", err)
		}
	}()

	// Wait for server to be ready before proceeding
	if err := waitForServerReady(t, "localhost:10002", serverReadinessTimeout); err != nil {
		t.Fatalf("Restarted server not ready: %v", err)
	}

	resurrectWait := 1250 * time.Millisecond
	time.Sleep(resurrectWait)

	for i := 1; i <= 9; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		res, err := transport.Perform(req)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code, want=200, got=%d", res.StatusCode)
		}
	}

	pool = getPool()
	if pool != nil {
		pool.mu.Lock()
		if len(pool.mu.live) != 3 {
			t.Errorf("Unexpected number of live connections, want=3, got=%d", len(pool.mu.live))
		}
		pool.mu.Unlock()

		pool.mu.Lock()
		if len(pool.mu.dead) != 0 {
			t.Errorf("Unexpected number of dead connections, want=0, got=%d", len(pool.mu.dead))
		}
		pool.mu.Unlock()
	}
}
