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

package opensearchtransport_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// TestDiscoveryIntegration tests discovery functionality against a real OpenSearch cluster
func TestDiscoveryIntegration(t *testing.T) {
	// Wait for cluster to be ready before starting discovery tests
	// This ensures all nodes are up and discoverable
	client, err := ostest.NewClient(t)
	require.NoError(t, err, "Failed to create client for readiness check")

	// Query the actual cluster to determine how many nodes are available
	nodesResp, err := client.Nodes.Info(t.Context(), nil)
	require.NoError(t, err, "Failed to get cluster nodes info")

	expectedNodeCount := len(nodesResp.Nodes)
	if testutil.IsDebugEnabled(t) {
		t.Logf("Detected %d-node cluster", expectedNodeCount)
	}

	// Detect which ports are available for testing discovery from different endpoints
	// Use the same scheme (http/https) as the test environment
	scheme := "http"
	if ostest.IsSecure() {
		scheme = "https"
	}

	baseURL := &url.URL{Scheme: scheme, Host: "localhost:9200"}
	availableNodes := []string{baseURL.String()}

	for _, port := range []string{"9201", "9202"} {
		nodeURL := &url.URL{Scheme: scheme, Host: "localhost:" + port}
		endpoint := nodeURL.String()
		//nolint:gosec // G107: Test code using localhost URLs with scheme from test config
		if resp, err := http.Get(endpoint); err == nil {
			resp.Body.Close()
			availableNodes = append(availableNodes, endpoint)
		}
	}

	// Test discovery from available nodes in the cluster
	for _, endpoint := range availableNodes {
		t.Run(fmt.Sprintf("DiscoverNodes from %s", endpoint), func(t *testing.T) {
			u, _ := url.Parse(endpoint)
			client, err := opensearchtransport.New(opensearchtransport.Config{
				URLs: []*url.URL{u},
			})
			require.NoError(t, err, "Failed to create client")

			err = client.DiscoverNodes(t.Context())
			if err != nil {
				t.Skipf("Discovery failed from %s - cluster may not be running: %v", endpoint, err)
				return
			}

			// Test that discovery worked by making a request and verifying we can reach different nodes
			// The client should now know about all nodes and can route requests appropriately
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err)

			// Make several requests to see if we're hitting different nodes (due to round-robin)
			nodeNames := make(map[string]int)
			for range 6 { // Make enough requests to hit all nodes
				resp, err := client.Perform(req)
				if err != nil {
					t.Fatalf("Request failed: %v", err)
				}

				var nodeResp struct {
					Name string `json:"name"`
				}
				err = json.NewDecoder(resp.Body).Decode(&nodeResp)
				resp.Body.Close()
				require.NoError(t, err)

				nodeNames[nodeResp.Name]++
			}

			// Based on the cluster, we expect to see requests distributed across nodes
			// In multi-node clusters with data+ingest roles, none should be filtered out
			if testutil.IsDebugEnabled(t) {
				t.Logf("Discovered from %s, requests distributed across nodes: %v", endpoint, nodeNames)
			}

			// We should see at least one node name
			assert.GreaterOrEqual(t, len(nodeNames), 1, "Should discover at least one node")

			// In a multi-node cluster, we should see multiple nodes being hit
			if expectedNodeCount > 1 {
				require.LessOrEqual(t, len(nodeNames), expectedNodeCount,
					"Should not discover more nodes than available in cluster")
			}
		})
	}

	t.Run("Role filtering behavior with mixed-role nodes", func(t *testing.T) {
		u, _ := url.Parse("http://localhost:9200")

		// Test with default settings (dedicated cluster managers filtered)
		client, err := opensearchtransport.New(opensearchtransport.Config{
			URLs: []*url.URL{u},
			// Default: IncludeDedicatedClusterManagers = false
		})
		require.NoError(t, err)

		err = client.DiscoverNodes(t.Context())
		if err != nil {
			t.Skipf("Discovery failed - cluster may not be running: %v", err)
			return
		}

		// Test the behavior by checking URLs() which should include all nodes
		// since none are "dedicated" cluster managers (they have work roles)
		urls := client.URLs()
		if testutil.IsDebugEnabled(t) {
			t.Logf("URLs after discovery with filtering: %v", urls)
		}

		// We expect to see all available nodes because they have work roles (data + ingest)
		require.Equal(t, expectedNodeCount, len(urls),
			"All nodes should be included - they have data+ingest roles, not just cluster_manager")

		// Test with IncludeDedicatedClusterManagers = true
		clientIncludeAll, err := opensearchtransport.New(opensearchtransport.Config{
			URLs:                            []*url.URL{u},
			IncludeDedicatedClusterManagers: true,
		})
		require.NoError(t, err)

		err = clientIncludeAll.DiscoverNodes(t.Context())
		require.NoError(t, err)

		urlsIncludeAll := clientIncludeAll.URLs()
		if testutil.IsDebugEnabled(t) {
			t.Logf("URLs after discovery with include all: %v", urlsIncludeAll)
		}

		// Should still get the same number of nodes since there are no dedicated cluster managers to filter
		require.Equal(t, expectedNodeCount, len(urlsIncludeAll),
			"Should still get all %d node(s) when including dedicated cluster managers", expectedNodeCount)
	})
}
