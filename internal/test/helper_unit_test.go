// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package ostest_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// TestWaitForClusterReady_NilClient tests the nil client validation
func TestWaitForClusterReady_NilClient(t *testing.T) {
	err := ostest.WaitForClusterReady(t, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client and client.Client must not be nil")
}

// TestWaitForClusterReady_NilInnerClient tests the nil inner client validation
func TestWaitForClusterReady_NilInnerClient(t *testing.T) {
	client := &opensearchapi.Client{}
	err := ostest.WaitForClusterReady(t, client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client and client.Client must not be nil")
}

// TestGetVersion tests the GetVersion function
func TestGetVersion(t *testing.T) {
	t.Run("nil client returns error", func(t *testing.T) {
		major, minor, patch, err := ostest.GetVersion(t, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "client cannot be nil")
		assert.Equal(t, int64(0), major)
		assert.Equal(t, int64(0), minor)
		assert.Equal(t, int64(0), patch)
	})

	t.Run("successful version parsing", func(t *testing.T) {
		// Create a mock server that returns version info
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{
					"name": "test-node",
					"cluster_name": "test-cluster",
					"version": {
						"number": "2.11.0",
						"distribution": "opensearch"
					}
				}`)
			}
		}))
		defer server.Close()

		cfg := opensearchapi.Config{
			Client: opensearch.Config{
				Addresses: []string{server.URL},
			},
		}
		client, err := opensearchapi.NewClient(cfg)
		require.NoError(t, err)

		major, minor, patch, err := ostest.GetVersion(t, client)
		require.NoError(t, err)
		assert.Equal(t, int64(2), major)
		assert.Equal(t, int64(11), minor)
		assert.Equal(t, int64(0), patch)
	})
}

// TestSkipIfNotSecure tests the SkipIfNotSecure function
func TestSkipIfNotSecure(t *testing.T) {
	// Save original env value
	originalValue := os.Getenv("SECURE_INTEGRATION")
	defer func() {
		if originalValue != "" {
			os.Setenv("SECURE_INTEGRATION", originalValue)
		} else {
			os.Unsetenv("SECURE_INTEGRATION")
		}
	}()

	t.Run("skips when not secure", func(t *testing.T) {
		os.Setenv("SECURE_INTEGRATION", "false")

		// Use a sub-test that we expect to be skipped
		skipped := false
		t.Run("should_skip", func(t *testing.T) {
			defer func() {
				if t.Skipped() {
					skipped = true
				}
			}()
			ostest.SkipIfNotSecure(t)
		})

		assert.True(t, skipped)
	})

	t.Run("does not skip when secure", func(t *testing.T) {
		os.Setenv("SECURE_INTEGRATION", "true")

		skipped := false
		t.Run("should_not_skip", func(t *testing.T) {
			defer func() {
				if t.Skipped() {
					skipped = true
				}
			}()
			ostest.SkipIfNotSecure(t)
			// If we get here, test was not skipped
		})

		assert.False(t, skipped)
	})
}
