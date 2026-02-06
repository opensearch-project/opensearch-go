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

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// TestDiscoveryIntegration tests discovery functionality against a real OpenSearch cluster
func TestDiscoveryIntegration(t *testing.T) {
	// Test discovery from different nodes in the cluster
	nodeEndpoints := []string{
		"http://localhost:9200",
		"http://localhost:9201",
		"http://localhost:9202",
	}

	for _, endpoint := range nodeEndpoints {
		t.Run(fmt.Sprintf("DiscoverNodes from %s", endpoint), func(t *testing.T) {
			u, _ := url.Parse(endpoint)
			client, err := opensearchtransport.New(opensearchtransport.Config{
				URLs: []*url.URL{u},
			})
			require.NoError(t, err, "Failed to create client")

			err = client.DiscoverNodes()
			if err != nil {
				t.Skipf("Discovery failed from %s - cluster may not be running: %v", endpoint, err)
				return
			}

			// Test that discovery worked by making a request and verifying we can reach different nodes
			// The client should now know about all 3 nodes and can route requests appropriately
			req, err := http.NewRequest("GET", "/", nil)
			require.NoError(t, err)

			// Make several requests to see if we're hitting different nodes (due to round-robin)
			nodeNames := make(map[string]int)
			for i := 0; i < 6; i++ { // Make enough requests to hit all nodes
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

			// Based on the cluster output, we expect to see requests distributed across multiple nodes
			// Since they all have data+ingest roles in addition to cluster_manager,
			// none should be filtered out as "dedicated cluster managers"
			if testutil.IsDebugEnabled(t) {
				t.Logf("Discovered from %s, requests distributed across nodes: %v", endpoint, nodeNames)
			}

			// We should see at least one node name (potentially all 3: opensearch-node1, opensearch-node2, opensearch-node3)
			assert.GreaterOrEqual(t, len(nodeNames), 1, "Should discover at least one node")

			// Verify we're seeing the expected node names from the cluster
			expectedNodeNames := map[string]bool{
				"opensearch-node1": true,
				"opensearch-node2": true,
				"opensearch-node3": true,
			}

			for nodeName := range nodeNames {
				assert.True(t, expectedNodeNames[nodeName],
					"Discovered unexpected node name: %s", nodeName)
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

		err = client.DiscoverNodes()
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

		// We expect to see all 3 nodes because they have work roles (data + ingest)
		// Expected URLs: localhost:9200, localhost:9201, localhost:9202
		assert.Equal(t, 3, len(urls), "All nodes should be included - they have data+ingest roles, not just cluster_manager")

		// Test with IncludeDedicatedClusterManagers = true
		clientIncludeAll, err := opensearchtransport.New(opensearchtransport.Config{
			URLs:                            []*url.URL{u},
			IncludeDedicatedClusterManagers: true,
		})
		require.NoError(t, err)

		err = clientIncludeAll.DiscoverNodes()
		require.NoError(t, err)

		urlsIncludeAll := clientIncludeAll.URLs()
		if testutil.IsDebugEnabled(t) {
			t.Logf("URLs after discovery with include all: %v", urlsIncludeAll)
		}

		// Should still get 3 nodes since there are no dedicated cluster managers to filter
		assert.Equal(t, 3, len(urlsIncludeAll), "Should still get all 3 nodes when including dedicated cluster managers")
	})
}
