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
	"net/url"
	"os"
	"testing"
)

func TestDiscoveryIntegration(t *testing.T) {
	// Get OpenSearch URL from environment or use default
	opensearchURL := os.Getenv("OPENSEARCH_URL")
	if opensearchURL == "" {
		opensearchURL = "http://localhost:9200"
	}

	u, err := url.Parse(opensearchURL)
	if err != nil {
		t.Fatalf("Failed to parse OpenSearch URL: %v", err)
	}

	t.Run("DiscoverNodes with health validation", func(t *testing.T) {
		client, err := New(Config{
			URLs: []*url.URL{u},
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Discovery should work with health validation
		err = client.DiscoverNodes()
		if err != nil {
			t.Errorf("DiscoverNodes() failed: %v", err)
		}

		// Should have at least one connection after discovery
		urls := client.URLs()
		if len(urls) == 0 {
			t.Error("Expected at least one URL after discovery")
		}

		t.Logf("Discovered %d nodes", len(urls))
	})

	t.Run("Role based nodes discovery with health validation", func(t *testing.T) {
		client, err := New(Config{
			URLs: []*url.URL{u},
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Test discovery with role filtering
		err = client.DiscoverNodes()
		if err != nil {
			t.Errorf("DiscoverNodes() failed: %v", err)
		}

		// Get the actual discovered connections for role testing
		urls := client.URLs()
		t.Logf("Role-based discovery found %d nodes", len(urls))

		// In a real cluster, we should have at least one data/coordinator node
		// (cluster_manager-only nodes are filtered out)
		if len(urls) == 0 {
			t.Error("Expected at least one non-cluster_manager-only node")
		}
	})
}