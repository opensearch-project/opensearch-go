// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//go:build integration

package ostest

import (
	"context"
	"fmt"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// OpenSearchTestSuite provides a testify suite with automatic client setup and readiness checking
type OpenSearchTestSuite struct {
	suite.Suite
	Client *opensearchapi.Client

	// Version information for test logic
	Major, Minor, Patch int64
}

// SetupSuite is called once before any tests in the suite run
func (s *OpenSearchTestSuite) SetupSuite() {
	t := s.T()

	// Create client with automatic readiness checking
	client, err := NewClient(t)
	require.NoError(t, err, "Failed to create OpenSearch client")
	s.Client = client

	// Get and store version information for test use
	major, minor, patch, err := GetVersion(s.Client, t)
	require.NoError(t, err, "Failed to get OpenSearch version")

	s.Major, s.Minor, s.Patch = major, minor, patch
}

// TearDownSuite is called once after all tests in the suite have run
func (s *OpenSearchTestSuite) TearDownSuite() {
	// Currently no cleanup needed for the client
}

// SetupTest is called before each individual test
func (s *OpenSearchTestSuite) SetupTest() {
	// Ensure cluster is still healthy before each test
	ctx := context.Background()
	_, err := s.Client.Cluster.Health(ctx, nil)
	require.NoError(s.T(), err, "Cluster health check failed before test")
}

// SkipIfBelowVersion skips the current test if the cluster version is below the specified version
func (s *OpenSearchTestSuite) SkipIfBelowVersion(majorVersion, patchVersion int64, testName string) {
	if s.Major < majorVersion || (s.Major == majorVersion && s.Patch < patchVersion) {
		s.T().Skipf("Skipping %s test as it requires OpenSearch %d.x.%d+, current version: %d.%d.%d",
			testName, majorVersion, patchVersion, s.Major, s.Minor, s.Patch)
	}
}

// SkipIfNotSecure skips the current test if running against an insecure cluster
func (s *OpenSearchTestSuite) SkipIfNotSecure() {
	if !IsSecure() {
		s.T().Skip("Skipping test as it requires a secured cluster")
	}
}

// Version returns the OpenSearch version as a formatted string
func (s *OpenSearchTestSuite) Version() string {
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// RequireHealthyCluster ensures the cluster is in a healthy state
func (s *OpenSearchTestSuite) RequireHealthyCluster() {
	ctx := context.Background()
	resp, err := s.Client.Cluster.Health(ctx, nil)
	require.NoError(s.T(), err, "Failed to get cluster health")
	require.NotNil(s.T(), resp, "Cluster health response is nil")
}

// CleanupIndex ensures an index is deleted, useful for test cleanup
func (s *OpenSearchTestSuite) CleanupIndex(indexName string) {
	ctx := context.Background()
	_, _ = s.Client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
		Indices: []string{indexName},
	})
}

// EnsureIndex creates an index if it doesn't exist, useful for test setup
func (s *OpenSearchTestSuite) EnsureIndex(indexName string) {
	ctx := context.Background()
	_, _ = s.Client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: indexName,
	})
}
