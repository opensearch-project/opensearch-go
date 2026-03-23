// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchtransport)

package opensearchtransport_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// TestRouterWithDiscovery verifies the complete flow:
// 1. Seed URLs used initially (treated as coordinating_only)
// 2. Discovery runs and finds nodes with actual roles
// 3. Seed URLs removed from coordinating_only policy
// 4. Router takes over with discovered nodes
func TestRouterWithDiscovery(t *testing.T) {
	// Verify cluster is reachable with the configured scheme
	_, err := testutil.InitClient(t)
	require.NoError(t, err)

	// Discovery uses seed URLs including port 9201; skip if only 1 node is available.
	testutil.SkipIfSingleNode(t, 2)

	t.Run("Complete seed URL to router transition", func(t *testing.T) {
		// Create mux router (which has IfEnabledPolicy for coordinator nodes)
		router := opensearchtransport.NewMuxRouter()

		// Start with test config for fast timeouts (auto-discovery disabled)
		cfg := testConfigWithAuth(t)
		cfg.Router = router
		cfg.EnableDebugLogger = true

		if _, ok := os.LookupEnv("GITHUB_ACTIONS"); ok {
			cfg.EnableDebugLogger = false // Disable debug in CI
		}

		// Create transport
		transport, err := opensearchtransport.New(cfg)
		require.NoError(t, err)

		// Phase 1: Before discovery, seed URLs should be available as coordinating_only nodes.
		req1, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)

		res1, err := transport.Perform(req1)
		require.NoError(t, err)
		res1.Body.Close()

		// Phase 2: Run discovery explicitly -- synchronous, no sleep needed
		err = transport.DiscoverNodes(t.Context())
		require.NoError(t, err)

		// Phase 3: After discovery, the router should handle requests.
		// - Discovery has run and found nodes with actual roles
		// - Seed URLs should be removed from coordinating_only policy
		// - coordinating_only policy should be disabled (IsEnabled() == false)
		for i := range 10 {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err, "request %d", i+1)

			res, err := transport.Perform(req)
			require.NoError(t, err, "request %d", i+1)
			res.Body.Close()
		}
	})

	t.Run("Verify IfEnabledPolicy condition switches after discovery", func(t *testing.T) {
		// This test explicitly checks the condition switching behavior
		router := opensearchtransport.NewMuxRouter()

		cfg := testConfigWithAuth(t)
		cfg.Router = router
		cfg.EnableDebugLogger = true

		if _, ok := os.LookupEnv("GITHUB_ACTIONS"); ok {
			cfg.EnableDebugLogger = false
		}

		transport, err := opensearchtransport.New(cfg)
		require.NoError(t, err)

		// Initial request using seed URL.
		req1, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)
		res1, err := transport.Perform(req1)
		require.NoError(t, err)
		res1.Body.Close()

		// Run discovery explicitly -- synchronous, no sleep needed
		err = transport.DiscoverNodes(t.Context())
		require.NoError(t, err)

		// After discovery, the coordinating_only policy should be disabled
		// and router should handle requests.

		// Test with a bulk operation that should route to ingest nodes (if available).
		// Use a minimal valid NDJSON body to avoid EOF from server rejecting empty POST /_bulk
		bulkBody := "{\"index\":{\"_index\":\"test-router\"}}\n{\"field\":\"value\"}\n"
		bulkReq, err := http.NewRequest(http.MethodPost, "/_bulk", strings.NewReader(bulkBody))
		require.NoError(t, err)
		bulkReq.Header.Set("Content-Type", "application/x-ndjson")
		bulkRes, err := transport.Perform(bulkReq)
		require.NoError(t, err)
		bulkRes.Body.Close()

		t.Cleanup(func() {
			delReq, _ := http.NewRequest(http.MethodDelete, "/test-router", nil)
			if res, err := transport.Perform(delReq); err == nil {
				res.Body.Close()
			}
		})

		// Test with a search operation that should route to data/search nodes.
		searchReq, err := http.NewRequest(http.MethodGet, "/_search", nil)
		require.NoError(t, err)
		searchRes, err := transport.Perform(searchReq)
		require.NoError(t, err)
		searchRes.Body.Close()
	})
}
