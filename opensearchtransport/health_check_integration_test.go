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

	"github.com/stretchr/testify/require"
)

func TestHealthCheckIntegration(t *testing.T) {
	// Get OpenSearch URL from environment or use default
	opensearchURL := os.Getenv("OPENSEARCH_URL")
	if opensearchURL == "" {
		opensearchURL = "http://localhost:9200"
	}

	u, err := url.Parse(opensearchURL)
	if err != nil {
		t.Fatalf("Failed to parse OpenSearch URL: %v", err)
	}

	// Create a client with default config
	client, err := New(Config{
		URLs: []*url.URL{u},
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	t.Run("Health check against real OpenSearch", func(t *testing.T) {
		ctx := t.Context()
		resp, err := client.defaultHealthCheck(ctx, u)
		require.NoError(t, err, "Health check should succeed")
		require.NotNil(t, resp, "Response should not be nil")
		require.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")
		if resp.Body != nil {
			resp.Body.Close()
		}
	})

	t.Run("Connection pool with health validation", func(t *testing.T) {
		// The client should have successfully initialized with health-validated connections
		urls := client.URLs()
		if len(urls) == 0 {
			t.Error("Expected at least one URL after health validation")
		}

		t.Logf("Client initialized with %d validated connections", len(urls))
	})
}