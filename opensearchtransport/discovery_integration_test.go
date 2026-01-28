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