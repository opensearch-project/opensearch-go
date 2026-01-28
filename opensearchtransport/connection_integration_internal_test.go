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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

func TestStatusConnectionPool(t *testing.T) {
	t.Run("Real cluster connection pool", func(t *testing.T) {
		// Test against real OpenSearch cluster
		u := getTestURL(t)

		// Get test config with secure/insecure settings
		cfg := getTestConfig(t, []*url.URL{u, u}) // Use duplicate URLs to force statusConnectionPool

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
		pool := transport.mu.connectionPool.(*statusConnectionPool)
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
		require.NoError(t, err, "Failed to create server pool")

		// Track request counts per server to verify round-robin distribution
		requestCounts := make(map[string]*atomic.Int64)
		for _, u := range serverPool.URLs() {
			requestCounts[u.Host] = &atomic.Int64{}
		}

		// Create a handler that tracks requests and returns a valid OpenSearch response
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Track which server received this request (lock-free)
			if counter, ok := requestCounts[r.Host]; ok {
				counter.Add(1)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockhttp.CreateMockOpenSearchResponse()))
		})

		// Start all mock servers
		err = serverPool.Start(t, handler)
		require.NoError(t, err, "Failed to start server pool")

		// Create transport with mock server URLs
		// Note: This will trigger health checks on all connections
		urls := serverPool.URLs()

		cfg := Config{URLs: urls}
		transport, err := New(cfg)
		require.NoError(t, err, "Failed to create transport")

		// Verify we get a statusConnectionPool with multiple URLs
		transport.mu.RLock()
		pool, ok := transport.mu.connectionPool.(*statusConnectionPool)
		transport.mu.RUnlock()
		require.True(t, ok, "Expected statusConnectionPool, got %T", transport.mu.connectionPool)

		// Perform 9 requests (3x round-robin cycles with 3 servers)
		for i := 1; i <= 9; i++ {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err, "Failed to create request %d", i)

			res, err := transport.Perform(req)
			require.NoError(t, err, "Request %d failed", i)
			require.NotNil(t, res, "Request %d returned nil response", i)

			if res.Body != nil {
				res.Body.Close()
			}
			require.Equal(t, http.StatusOK, res.StatusCode, "Request %d unexpected status code", i)
		}

		// Verify all servers are marked as live
		pool.mu.Lock()
		liveCount := len(pool.mu.live)
		deadCount := len(pool.mu.dead)
		pool.mu.Unlock()

		require.Equal(t, 3, liveCount, "Expected 3 live connections")
		require.Equal(t, 0, deadCount, "Expected 0 dead connections")

		// Verify requests were distributed reasonably (accounting for server readiness checks)
		// Server readiness checks add 1 request per server during startup
		// So we expect: 1 (readiness) + 3 (round-robin from 9 requests) = 4 per server
		// OR if readiness checks happen before handler is set: 3 requests per server
		serversSeen := 0
		for host, counter := range requestCounts {
			count := counter.Load()
			if count > 0 {
				serversSeen++
			}
			// Allow some flexibility: 3-4 requests per server is acceptable
			// (3 if readiness check missed, 4 if readiness check counted)
			require.GreaterOrEqual(t, count, int64(3), "Expected server %s to receive at least 3 requests, got %d", host, count)
			require.LessOrEqual(t, count, int64(4), "Expected server %s to receive at most 4 requests, got %d", host, count)
		}

		require.Equal(t, 3, serversSeen, "Expected requests to be distributed across all 3 servers")
	})
}
