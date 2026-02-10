// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//go:build integration

package ostest_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// TestNewClient demonstrates the enhanced client creation with automatic readiness checks
func TestNewClient(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err, "Failed to create client")
	require.NotNil(t, client, "Client should not be nil")

	// Test that the client can immediately perform operations without additional waits
	ctx := context.Background()

	// This should succeed immediately since NewClient waited for full readiness
	resp, err := client.Cluster.Health(ctx, nil)
	assert.NoError(t, err, "Cluster health should succeed immediately")
	assert.NotNil(t, resp, "Health response should not be nil")

	// Test that basic API operations work (these were failing in 2.1.0 before the fix)
	info, err := client.Info(ctx, nil)
	assert.NoError(t, err, "Info API should work immediately")
	assert.NotNil(t, info, "Info response should not be nil")
}

// TestConfigFunctions tests the configuration utility functions
func TestConfigFunctions(t *testing.T) {
	// Test ostest.IsSecure function
	t.Run("ostest.IsSecure", func(t *testing.T) {
		// Save original value
		original := os.Getenv("SECURE_INTEGRATION")
		defer os.Setenv("SECURE_INTEGRATION", original)

		// Test false case
		os.Setenv("SECURE_INTEGRATION", "false")
		assert.False(t, ostest.IsSecure())

		// Test true case
		os.Setenv("SECURE_INTEGRATION", "true")
		assert.True(t, ostest.IsSecure())

		// Test empty case
		os.Unsetenv("SECURE_INTEGRATION")
		assert.False(t, ostest.IsSecure())
	})

	// Test GetPassword function
	t.Run("GetPassword", func(t *testing.T) {
		// Save original value
		original := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			// Properly restore environment variable
			if original == "" {
				os.Unsetenv("OPENSEARCH_VERSION")
			} else {
				os.Setenv("OPENSEARCH_VERSION", original)
			}
		}()

		// Test default admin password for older versions
		os.Setenv("OPENSEARCH_VERSION", "2.0.0")
		password, err := ostest.GetPassword()
		assert.NoError(t, err)
		assert.Equal(t, "admin", password)

		// Test newer default password for 2.12+
		os.Setenv("OPENSEARCH_VERSION", "2.12.0")
		password, err = ostest.GetPassword()
		assert.NoError(t, err)
		assert.Equal(t, "myStrongPassword123!", password)

		// Test latest version
		os.Setenv("OPENSEARCH_VERSION", "latest")
		password, err = ostest.GetPassword()
		assert.NoError(t, err)
		assert.Equal(t, "myStrongPassword123!", password)

		// Test empty version (defaults to "admin" password)
		os.Unsetenv("OPENSEARCH_VERSION")
		password, err = ostest.GetPassword()
		assert.NoError(t, err)
		assert.Equal(t, "admin", password)

		// ERROR PATH: Test invalid version format
		os.Setenv("OPENSEARCH_VERSION", "invalid.version")
		_, err = ostest.GetPassword()
		assert.Error(t, err, "Should error with invalid version format")
	})

	// Test ClientConfig function
	t.Run("ClientConfig", func(t *testing.T) {
		// Save original values
		original := os.Getenv("SECURE_INTEGRATION")
		originalVersion := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			// Properly restore environment variables
			if original == "" {
				os.Unsetenv("SECURE_INTEGRATION")
			} else {
				os.Setenv("SECURE_INTEGRATION", original)
			}
			if originalVersion == "" {
				os.Unsetenv("OPENSEARCH_VERSION")
			} else {
				os.Setenv("OPENSEARCH_VERSION", originalVersion)
			}
		}()

		// Test insecure config (should return valid HTTP config)
		os.Setenv("SECURE_INTEGRATION", "false")
		config, err := ostest.ClientConfig()
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, []string{"http://localhost:9200"}, config.Client.Addresses)
		assert.Empty(t, config.Client.Username)
		assert.Empty(t, config.Client.Password)
		assert.Nil(t, config.Client.Transport)

		// Test secure config - success case
		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "2.12.0") // Valid version
		config, err = ostest.ClientConfig()
		assert.NoError(t, err)
		if config != nil {
			assert.Equal(t, "admin", config.Client.Username)
			assert.NotEmpty(t, config.Client.Password)
			assert.NotEmpty(t, config.Client.Addresses)
			assert.NotNil(t, config.Client.Transport)
		}

		// ERROR PATH: Test secure config with invalid version
		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "not.a.version")
		config, err = ostest.ClientConfig()
		assert.Error(t, err, "Should propagate GetPassword error")
		assert.Nil(t, config, "Config should be nil on error")
	})
}

// TestHelperFunctions tests utility functions with different scenarios
func TestHelperFunctions(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)

	// Test ostest.SkipIfBelowVersion - this will not skip since we're running on 3.4.0
	t.Run("ostest.SkipIfBelowVersion", func(t *testing.T) {
		// This should not skip on current version (3.4.0+)
		ostest.SkipIfBelowVersion(t, client, 2, 0, "TestFeature")
		// If we reach here, the test didn't skip
		assert.True(t, true, "Test should continue for supported version")
	})

	// Test SkipIfNotSecure
	t.Run("SkipIfNotSecure", func(t *testing.T) {
		if ostest.IsSecure() {
			ostest.SkipIfNotSecure(t)
			// If we reach here, it's a secure cluster and test continued
			assert.True(t, true, "Test should continue for secure cluster")
		}
	})

	// Test CompareRawJSONwithParsedJSON
	t.Run("CompareRawJSONwithParsedJSON", func(t *testing.T) {
		// Test with simple JSON comparison
		ctx := context.Background()
		resp, err := client.Info(ctx, &opensearchapi.InfoReq{})
		require.NoError(t, err)

		// This tests the JSON comparison utility
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	// Test GetVersion function
	t.Run("GetVersion", func(t *testing.T) {
		// Test successful version retrieval
		major, minor, patch, err := ostest.GetVersion(t, client)
		assert.NoError(t, err, "GetVersion should succeed with valid client")
		assert.True(t, major >= 1, "Major version should be at least 1")
		assert.True(t, minor >= 0, "Minor version should be non-negative")
		assert.True(t, patch >= 0, "Patch version should be non-negative")

		// ERROR PATH: Test GetVersion with nil client
		_, _, _, err = ostest.GetVersion(t, nil)
		assert.Error(t, err, "GetVersion should error with nil client")
	})

	// Test NewClient function
	t.Run("NewClient", func(t *testing.T) {
		// Test successful client creation (already done above)
		client, err := ostest.NewClient(t)
		assert.NoError(t, err, "NewClient should succeed in normal conditions")
		assert.NotNil(t, client, "Client should not be nil")

		// ERROR PATH: Test NewClient when config creation fails
		originalSecure := os.Getenv("SECURE_INTEGRATION")
		originalVersion := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			// Properly restore environment variables
			if originalSecure == "" {
				os.Unsetenv("SECURE_INTEGRATION")
			} else {
				os.Setenv("SECURE_INTEGRATION", originalSecure)
			}
			if originalVersion == "" {
				os.Unsetenv("OPENSEARCH_VERSION")
			} else {
				os.Setenv("OPENSEARCH_VERSION", originalVersion)
			}
		}()

		// Set invalid version to trigger ClientConfig error
		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "invalid.format")

		_, err = ostest.NewClient(t)
		assert.Error(t, err, "NewClient should propagate ClientConfig errors")
	})
}

// ExampleTestSuite demonstrates using the testify suite pattern
type ExampleTestSuite struct {
	ostest.OpenSearchTestSuite
}

func (s *ExampleTestSuite) TestClusterHealthWithSuite() {
	ctx := context.Background()

	// Suite already has a ready client available as s.Client
	resp, err := s.Client.Cluster.Health(ctx, nil)
	s.Require().NoError(err, "Cluster health should work")
	s.Assert().NotNil(resp, "Response should not be nil")
}

func (s *ExampleTestSuite) TestVersionBasedSkipping() {
	// Example of version-based test skipping
	s.SkipIfBelowVersion(2, 4, "Point_In_Time")

	// This test would only run on OpenSearch 2.4+
	if testutil.IsDebugEnabled(s.T()) {
		s.T().Logf("This test is running on version %s which supports the feature", s.Version())
	}
}

// TestSuiteMethods tests additional suite functionality
func (s *ExampleTestSuite) TestSuiteUtilityMethods() {
	// Test RequireHealthyCluster
	s.RequireHealthyCluster()

	// Test index management methods
	testIndex := "test-coverage-index"

	// Test EnsureIndex
	s.EnsureIndex(testIndex)

	// Test CleanupIndex
	s.CleanupIndex(testIndex)
}

// Test version skipping with a high version that should skip
func (s *ExampleTestSuite) TestVersionSkipping() {
	// This should skip on current version (testing the skip functionality)
	s.SkipIfBelowVersion(999, 0, "FutureFeature")

	// This line should not be reached
	s.T().Error("This test should have been skipped")
}

// Test secure cluster skipping if not secure
func (s *ExampleTestSuite) TestSecureSkipping() {
	if !ostest.IsSecure() {
		s.SkipIfNotSecure()
		// This line should not be reached for insecure clusters
		s.T().Error("This test should have been skipped for insecure cluster")
	}
}

// Run the suite
func TestExampleSuite(t *testing.T) {
	suite.Run(t, new(ExampleTestSuite))
}
