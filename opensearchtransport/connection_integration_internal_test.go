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
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

func TestStatusConnectionPool(t *testing.T) {
	t.Run("Real cluster connection pool", func(t *testing.T) {
		// Test against real OpenSearch cluster
		u := mockhttp.GetOpenSearchURL(t)

		// Test basic connection pool functionality
		cfg := Config{URLs: []*url.URL{u, u}} // Use duplicate URLs to force statusConnectionPool

		if _, ok := os.LookupEnv("GITHUB_ACTIONS"); !ok {
			cfg.Logger = &TextLogger{Output: os.Stdout}
			cfg.EnableDebugLogger = true
		}

		transport, err := New(cfg)
		if err != nil {
			t.Fatalf("Failed to create transport: %v", err)
		}

		// Test basic request functionality
		for i := 1; i <= 5; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/", nil)
			res, err := transport.Perform(req)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				continue
			}
			if res == nil {
				t.Errorf("Response is nil")
				continue
			}
			if res.Body != nil {
				res.Body.Close()
			}
			if res.StatusCode != http.StatusOK {
				t.Errorf("Unexpected status code, want=200, got=%d", res.StatusCode)
			}
		}

		// Verify pool has the expected connection
		transport.mu.RLock()
		pool := transport.mu.pool.(*statusConnectionPool)
		transport.mu.RUnlock()

		pool.mu.Lock()
		if len(pool.mu.live) != 2 {
			t.Errorf("Unexpected number of live connections, want=2, got=%d", len(pool.mu.live))
		}
		if len(pool.mu.dead) != 0 {
			t.Errorf("Unexpected number of dead connections, want=0, got=%d", len(pool.mu.dead))
		}
		pool.mu.Unlock()
	})

	t.Run("Mock server connection pool behavior", func(t *testing.T) {
		// Test connection pool behavior with controlled mock servers
		serverPool, err := mockhttp.NewServerPool(t, "connection-pool-test", 3)
		if err != nil {
			t.Fatalf("Failed to create server pool: %v", err)
		}

		// Create a handler that returns a valid OpenSearch response
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockhttp.CreateMockOpenSearchResponse()))
		})

		// Start all mock servers
		if err := serverPool.Start(t, handler); err != nil {
			t.Fatalf("Failed to start server pool: %v", err)
		}

		// Create transport with mock server URLs
		cfg := Config{URLs: serverPool.URLs()}
		transport, err := New(cfg)
		if err != nil {
			t.Fatalf("Failed to create transport: %v", err)
		}

		// Verify we get a statusConnectionPool with multiple URLs
		transport.mu.RLock()
		pool, ok := transport.mu.pool.(*statusConnectionPool)
		transport.mu.RUnlock()

		if !ok {
			t.Fatalf("Expected statusConnectionPool, got %T", transport.mu.pool)
		}

		// Test requests are distributed across servers
		for i := 1; i <= 9; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/", nil)
			res, err := transport.Perform(req)
			if err != nil {
				t.Errorf("Request %d failed: %v", i, err)
			}
			if res != nil && res.Body != nil {
				res.Body.Close()
			}
			if res.StatusCode != http.StatusOK {
				t.Errorf("Request %d unexpected status code, want=200, got=%d", i, res.StatusCode)
			}
		}

		// Verify all servers are marked as live
		pool.mu.Lock()
		liveCount := len(pool.mu.live)
		deadCount := len(pool.mu.dead)
		pool.mu.Unlock()

		if liveCount != 3 {
			t.Errorf("Expected 3 live connections, got %d", liveCount)
		}
		if deadCount != 0 {
			t.Errorf("Expected 0 dead connections, got %d", deadCount)
		}
	})
}
