// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHealthCheckConfiguration(t *testing.T) {
	serverURL, _ := url.Parse("http://localhost:9200")

	t.Run("Default health check (nil)", func(t *testing.T) {
		// When HealthCheck is nil (or omitted), the built-in health check is used
		client, err := New(Config{
			URLs: []*url.URL{serverURL},
			// HealthCheck: nil, // Default - uses built-in health check
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Verify the client was created successfully
		if len(client.URLs()) == 0 {
			t.Error("Expected at least one URL")
		}

		// Check that health check function was assigned
		if pool, ok := client.mu.connectionPool.(*statusConnectionPool); ok {
			if pool.healthCheck == nil {
				t.Error("Expected health check function to be assigned when HealthCheck is nil")
			}
		}
	})

	t.Run("NoOpHealthCheck - disables health checking", func(t *testing.T) {
		// Use NoOpHealthCheck to disable health checking
		client, err := New(Config{
			URLs:        []*url.URL{serverURL},
			HealthCheck: NoOpHealthCheck,
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Verify the client was created successfully
		if len(client.URLs()) == 0 {
			t.Error("Expected at least one URL")
		}

		// Check that NoOpHealthCheck was assigned
		if pool, ok := client.mu.connectionPool.(*statusConnectionPool); ok {
			if pool.healthCheck == nil {
				t.Error("Expected NoOpHealthCheck to be assigned")
			}

			// Test the NoOpHealthCheck function directly
			ctx := context.Background()
			resp, err := pool.healthCheck(ctx, serverURL) //nolint:bodyclose // NoOpHealthCheck returns nil response
			if err != nil {
				t.Errorf("NoOpHealthCheck should never fail, got error: %v", err)
			}
			if resp != nil {
				t.Error("NoOpHealthCheck should return nil response")
			}
		}
	})

	t.Run("Custom health check function", func(t *testing.T) {
		customHealthCheckCalled := false

		customHealthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			customHealthCheckCalled = true

			// Custom logic: just check if URL contains "localhost"
			if !strings.Contains(u.Host, "localhost") {
				return nil, http.ErrServerClosed
			}

			// Return a successful response
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       http.NoBody,
			}, nil
		}

		client, err := New(Config{
			URLs:        []*url.URL{serverURL},
			HealthCheck: customHealthCheck,
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Verify the client was created successfully
		if len(client.URLs()) == 0 {
			t.Error("Expected at least one URL")
		}

		// Test the custom health check function directly
		testCustomHealthCheckFunction(t, client, serverURL, &customHealthCheckCalled)
	})

	t.Run("Custom health check with failure", func(t *testing.T) {
		customHealthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			// Always fail for demonstration
			return nil, http.ErrServerClosed
		}

		client, err := New(Config{
			URLs:        []*url.URL{serverURL},
			HealthCheck: customHealthCheck,
		})
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Test the failing health check
		if pool, ok := client.mu.connectionPool.(*statusConnectionPool); ok {
			ctx := context.Background()
			resp, err := pool.healthCheck(ctx, serverURL)
			if err == nil {
				t.Error("Expected custom health check to fail")
			}
			if resp != nil {
				if resp.Body != nil {
					resp.Body.Close()
				}
				t.Error("Expected nil response on health check failure")
			}
		}
	})
}

// testCustomHealthCheckFunction tests a custom health check function.
func testCustomHealthCheckFunction(t *testing.T, client *Client, serverURL *url.URL, customHealthCheckCalled *bool) {
	t.Helper()
	pool, ok := client.mu.connectionPool.(*statusConnectionPool)
	if !ok {
		return
	}

	if pool.healthCheck == nil {
		t.Error("Expected custom health check to be assigned")
		return
	}

	ctx := context.Background()
	resp, err := pool.healthCheck(ctx, serverURL)
	if err != nil {
		t.Errorf("Custom health check should succeed for localhost, got error: %v", err)
	}

	if resp == nil {
		t.Error("Custom health check should return a response")
		return
	}

	if resp.Body != nil {
		resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if !*customHealthCheckCalled {
		t.Error("Custom health check function was not called")
	}
}

func TestNoOpHealthCheck(t *testing.T) {
	ctx := context.Background()
	serverURL, _ := url.Parse("http://example.com:9200")

	t.Run("Always succeeds", func(t *testing.T) {
		resp, err := NoOpHealthCheck(ctx, serverURL) //nolint:bodyclose // NoOpHealthCheck returns nil response
		if err != nil {
			t.Errorf("NoOpHealthCheck should never fail, got: %v", err)
		}
		if resp != nil {
			t.Error("NoOpHealthCheck should return nil response")
		}
	})

	t.Run("Works with cancelled context", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		resp, err := NoOpHealthCheck(cancelCtx, serverURL) //nolint:bodyclose // NoOpHealthCheck returns nil response
		if err != nil {
			t.Errorf("NoOpHealthCheck should ignore context cancellation, got: %v", err)
		}
		if resp != nil {
			t.Error("NoOpHealthCheck should return nil response")
		}
	})

	t.Run("Works with nil URL", func(t *testing.T) {
		resp, err := NoOpHealthCheck(ctx, nil) //nolint:bodyclose // NoOpHealthCheck returns nil response
		if err != nil {
			t.Errorf("NoOpHealthCheck should work with nil URL, got: %v", err)
		}
		if resp != nil {
			t.Error("NoOpHealthCheck should return nil response")
		}
	})
}
