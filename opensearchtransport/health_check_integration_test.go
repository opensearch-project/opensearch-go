//go:build integration && (core || opensearchtransport)

package opensearchtransport

import (
	"net/url"
	"os"
	"testing"
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
		version, err := client.isHealthyOpenSearchNode(u)
		if err != nil {
			t.Fatalf("Health check failed: %v", err)
		}

		t.Logf("OpenSearch version: %s", version)

		if version == "" {
			t.Error("Version should not be empty")
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