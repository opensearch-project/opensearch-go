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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// TestSeedURLsWithDiscovery reproduces the zombie connection issue
// where seed URLs get duplicated or end up in inconsistent state.
func TestSeedURLsWithDiscovery(t *testing.T) {
	// Verify cluster is reachable with the configured scheme
	_, err := testutil.InitClient(t)
	require.NoError(t, err)

	// Discovery uses seed URLs including port 9201; skip if only 1 node is available.
	testutil.SkipIfSingleNode(t, 2)

	t.Run("Seed URLs with role-based router and discovery", func(t *testing.T) {
		// Create router with data policy (since our test cluster nodes have data role)
		// Policy chain: try coordinating_only first, then fall back to data nodes
		dataPolicy, err := opensearchtransport.NewRolePolicy(opensearchtransport.RoleData)
		require.NoError(t, err)

		router := opensearchtransport.NewRouter(
			mustRolePolicy(opensearchtransport.RoleCoordinatingOnly), // Try coordinating-only first
			dataPolicy, // Fall back to data nodes
		)

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

		// Make initial request (should use seed URLs as coordinating_only since they have no roles yet)
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)

		res, err := transport.Perform(req)
		require.NoError(t, err)
		res.Body.Close()

		// Run discovery explicitly -- synchronous, no sleep needed
		err = transport.DiscoverNodes(t.Context())
		require.NoError(t, err)

		// Make several more requests to trigger OnSuccess multiple times.
		// After discovery, should use data policy since discovered nodes have data role.
		// This should expose the duplicate resurrection bug if it exists.
		for i := range 10 {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err, "request %d", i+1)

			res, err := transport.Perform(req)
			require.NoError(t, err, "request %d", i+1)
			res.Body.Close()
		}
	})
}
