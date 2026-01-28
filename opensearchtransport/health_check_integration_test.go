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