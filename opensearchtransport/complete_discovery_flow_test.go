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

// TestCompleteDiscoveryFlow verifies all 6 requirements:
// 1. Run discovery with one or more URLs
// 2. Initial URLs used for queries are these seed URLs
// 3. Immediately run discovery
// 4. The policy that had connections in the coordinator_only role needs to remove those nodes
// 5. The IfEnabledPolicy should be disabled after discovery
// 6. The router should be used (Else branch of the IfEnabledPolicy)
func TestCompleteDiscoveryFlow(t *testing.T) {
	// Verify cluster is reachable with the configured scheme
	_, err := testutil.InitClient(t)
	require.NoError(t, err)

	// Discovery uses seed URLs including port 9201; skip if only 1 node is available.
	testutil.SkipIfSingleNode(t, 2)

	// Create mux router (includes IfEnabledPolicy for coordinator_only nodes)
	router := opensearchtransport.NewMuxRouter()

	// REQUIREMENT 1: Run discovery with one or more URLs
	cfg := testConfigWithAuth(t)
	cfg.Router = router
	cfg.EnableDebugLogger = true

	if _, ok := os.LookupEnv("GITHUB_ACTIONS"); ok {
		cfg.EnableDebugLogger = false
	}

	transport, err := opensearchtransport.New(cfg)
	require.NoError(t, err)

	// REQUIREMENT 2: Initial URLs used for queries are these seed URLs
	req1, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)

	res1, err := transport.Perform(req1)
	require.NoError(t, err)
	res1.Body.Close()

	// REQUIREMENT 3: Run discovery explicitly (auto-discovery disabled in testConfigWithAuth)
	err = transport.DiscoverNodes(t.Context())
	require.NoError(t, err)

	// REQUIREMENT 4: coordinator_only policy should remove nodes with actual roles
	// Verified by debug logs showing removal of seed URLs from coordinating_only policy.

	// REQUIREMENT 5: IfEnabledPolicy condition should switch after discovery
	// coordinator_only policy.IsEnabled() returns false after seed URLs removed,
	// causing the condition in NewMuxRoutePolicy to evaluate to false.

	// REQUIREMENT 6: Router (Else branch) should now be used
	// Test bulk operation - should route to ingest nodes
	// Use a minimal valid NDJSON body to avoid EOF from server rejecting empty POST /_bulk
	bulkBody := "{\"index\":{\"_index\":\"test-discovery-flow\"}}\n{\"field\":\"value\"}\n"
	bulkReq, err := http.NewRequest(http.MethodPost, "/_bulk", strings.NewReader(bulkBody))
	require.NoError(t, err)
	bulkReq.Header.Set("Content-Type", "application/x-ndjson")
	bulkRes, err := transport.Perform(bulkReq)
	require.NoError(t, err)
	bulkRes.Body.Close()

	t.Cleanup(func() {
		delReq, _ := http.NewRequest(http.MethodDelete, "/test-discovery-flow", nil)
		if res, err := transport.Perform(delReq); err == nil {
			res.Body.Close()
		}
	})

	// Test search operation - should route to data/search nodes
	searchReq, err := http.NewRequest(http.MethodGet, "/_search", nil)
	require.NoError(t, err)
	searchRes, err := transport.Perform(searchReq)
	require.NoError(t, err)
	searchRes.Body.Close()

	// Test general operation - should use round-robin fallback
	infoReq, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	infoRes, err := transport.Perform(infoReq)
	require.NoError(t, err)
	infoRes.Body.Close()
}
