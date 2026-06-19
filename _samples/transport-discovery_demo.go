// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// This demo shows the complete node discovery flow:
// 1. Start with seed URLs (treated as coordinating_only nodes)
// 2. Seed URLs used for initial queries
// 3. Discovery runs immediately
// 4. After discovery, seed URLs removed from coordinating_only policy
// 5. Router takes over with discovered nodes
// 6. Requests routed by role (bulk->ingest, search->data/search)

func main() {
	ctx := context.Background()

	// Create default router for role-based routing
	router, err := opensearchtransport.NewDefaultRouter()
	if err != nil {
		log.Fatalf("Error creating router: %s", err)
	}

	// Configure transport with fast discovery for demo
	cfg := opensearchtransport.Config{
		URLs: []*url.URL{
			{Scheme: "http", Host: "localhost:9200"},
			{Scheme: "http", Host: "localhost:9201"},
		},
		DiscoverNodesInterval: 5 * time.Second, // Fast for demo
		EnableMetrics:         true,
		EnableDebugLogger:     !isCI(), // Enable locally, disable in CI
		Logger:                &opensearchtransport.ColorLogger{Output: os.Stdout, EnableRequestBody: false, EnableResponseBody: false},
		Router:                router,

		// Fast timeouts for demo
		ResurrectTimeoutInitial:      1 * time.Second,
		ResurrectTimeoutMax:          10 * time.Second,
		ResurrectTimeoutFactorCutoff: 3,
		MinimumResurrectTimeout:      500 * time.Millisecond,
		HealthCheckTimeout:           2 * time.Second,
		HealthCheckMaxRetries:        3,
		DiscoveryHealthCheckRetries:  2,
	}

	transport, err := opensearchtransport.New(cfg)
	if err != nil {
		log.Fatalf("Error creating transport: %s", err)
	}

	fmt.Println("=== OpenSearch Discovery Demo ===")
	fmt.Println()

	// PHASE 1: Initial request using seed URLs
	fmt.Println("PHASE 1: Making initial request (using seed URLs as coordinator_only)")
	if err := makeRequest(ctx, transport, "GET", "/", "initial"); err != nil {
		log.Fatalf("Initial request failed: %s", err)
	}
	fmt.Println("ok: Initial request succeeded using seed URL")

	printMetrics(transport, "After initial request")

	// PHASE 2: Block until discovery completes. DiscoverNodes performs a
	// synchronous discovery pass (and joins any pass already in flight), so we
	// observe a fully-populated topology without guessing at a sleep duration.
	fmt.Println("\nPHASE 2: Running discovery to completion...")
	if err := transport.DiscoverNodes(ctx); err != nil {
		log.Fatalf("Discovery failed: %s", err)
	}

	printMetrics(transport, "After discovery")

	// PHASE 3: Make requests that use the router
	fmt.Println("\nPHASE 3: Making requests with router")

	// Test bulk operation (should route to ingest nodes)
	fmt.Println("\n-> Bulk request (should route to ingest nodes)")
	if err := makeRequest(ctx, transport, "POST", "/_bulk", "bulk"); err != nil {
		log.Printf("Bulk request error (expected with empty body): %s", err)
	} else {
		fmt.Println("ok: Bulk request succeeded")
	}

	// Test search operation (should route to data/search nodes)
	fmt.Println("\n-> Search request (should route to data/search nodes)")
	if err := makeRequest(ctx, transport, "GET", "/_search", "search"); err != nil {
		log.Printf("Search error: %s", err)
	} else {
		fmt.Println("ok: Search request succeeded")
	}

	// Test general operation (uses round-robin fallback)
	fmt.Println("\n-> Info request (uses round-robin fallback)")
	if err := makeRequest(ctx, transport, "GET", "/", "info"); err != nil {
		log.Fatalf("Info error: %s", err)
	}
	fmt.Println("ok: Info request succeeded")

	printMetrics(transport, "After routed requests")

	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("\nKey Observations:")
	fmt.Println("1. Started with seed URLs as coordinator_only nodes")
	fmt.Println("2. Discovery found nodes with actual roles")
	fmt.Println("3. Seed URLs removed from coordinator_only policy")
	fmt.Println("4. Router now handles requests based on operation type")
	fmt.Println("5. Bulk -> ingest nodes, Search -> data/search nodes")
}

func makeRequest(ctx context.Context, transport *opensearchtransport.Transport, method, path, label string) error {
	req, err := http.NewRequestWithContext(ctx, method, path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	res, err := transport.Stream(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer res.Body.Close()

	// Read and discard body
	io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 400 {
		return fmt.Errorf("status %d", res.StatusCode)
	}

	return nil
}

func printMetrics(transport *opensearchtransport.Transport, phase string) {
	m, err := transport.Metrics()
	if err != nil {
		log.Printf("Failed to get metrics: %s", err)
		return
	}

	fmt.Printf("\n--- Metrics: %s ---\n", phase)
	fmt.Printf("Requests:             %d\n", m.Requests)
	fmt.Printf("Failures:             %d\n", m.Failures)
	fmt.Printf("Live Connections:     %d\n", m.LiveConnections)
	fmt.Printf("Dead Connections:     %d\n", m.DeadConnections)
	fmt.Printf("Connections Promoted: %d\n", m.ConnectionsPromoted)
	fmt.Printf("Connections Demoted:  %d\n", m.ConnectionsDemoted)
	fmt.Printf("Zombie Connections:   %d\n", m.ZombieConnections)
	fmt.Printf("Health Checks:        %d\n", m.HealthChecks)
	fmt.Printf("Cluster Health Checks:%d\n", m.ClusterHealthChecks)
	fmt.Printf("Health Check Success: %d\n", m.HealthChecksSuccess)
	fmt.Printf("Health Check Failed:  %d\n", m.HealthChecksFailed)
	fmt.Printf("Overloaded Servers:   %d\n", m.OverloadedServers)

	if len(m.Connections) > 0 {
		fmt.Println("\nConnections:")
		for _, conn := range m.Connections {
			fmt.Printf("  %s\n", conn)
		}
	}

	if len(m.Responses) > 0 {
		fmt.Print("Response codes: ")
		data, _ := json.Marshal(m.Responses)
		fmt.Println(string(data))
	}
}

func isCI() bool {
	_, ok := os.LookupEnv("GITHUB_ACTIONS")
	return ok
}
