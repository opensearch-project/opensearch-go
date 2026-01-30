// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"fmt"
	"net/http"

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
	major, minor, patch, err := GetVersion(t, s.Client)
	require.NoError(t, err, "Failed to get OpenSearch version")

	s.Major, s.Minor, s.Patch = major, minor, patch
}

// TearDownSuite is called once after all tests in the suite have run
func (s *OpenSearchTestSuite) TearDownSuite() {
	// Currently no cleanup needed for the client
}

// Version returns a formatted version string for logging
func (s *OpenSearchTestSuite) Version() string {
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// SkipIfBelowVersion skips a test if the cluster version is below a given version
func (s *OpenSearchTestSuite) SkipIfBelowVersion(majorVersion, patchVersion int64, testName string) {
	t := s.T()
	t.Helper()
	if s.Major < majorVersion || (s.Major == majorVersion && s.Minor < patchVersion) {
		t.Skipf("Skipping %s as version %d.%d.x does not support this endpoint", testName, s.Major, s.Minor)
	}
}

// SkipIfNotSecure skips a test that runs against an insecure cluster
func (s *OpenSearchTestSuite) SkipIfNotSecure() {
	t := s.T()
	t.Helper()
	if !IsSecure(t) {
		t.Skipf("Skipping %s as it needs a secured cluster", t.Name())
	}
}

// RequireHealthyCluster ensures the cluster is in a healthy state
func (s *OpenSearchTestSuite) RequireHealthyCluster() {
	s.T().Helper()

	t := s.T()
	t.Helper()
	ctx := t.Context()

	resp, err := s.Client.Cluster.Health(ctx, nil)
	require.NoError(t, err, "Cluster health check should succeed")
	require.NotNil(t, resp, "Health response should not be nil")
}

// EnsureIndex creates an index if it doesn't exist
func (s *OpenSearchTestSuite) EnsureIndex(indexName string) {
	s.T().Helper()

	t := s.T()
	t.Helper()
	ctx := t.Context()

	// Check if index exists, create if it doesn't
	exists, err := s.Client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{
		Indices: []string{indexName},
	})

	// Only require no error if we got a nil response - 404s are expected
	if err != nil && exists == nil {
		require.NoError(t, err, "Should be able to check if index exists")
	}

	// If status is not 200, the index doesn't exist
	if exists == nil || exists.StatusCode != http.StatusOK {
		// Create the index
		_, err := s.Client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
			Index: indexName,
		})
		require.NoError(t, err, "Should be able to create index")
	}
}

// CleanupIndex deletes an index if it exists
func (s *OpenSearchTestSuite) CleanupIndex(indexName string) {
	s.T().Helper()

	t := s.T()
	t.Helper()
	ctx := t.Context()

	// Check if index exists
	exists, err := s.Client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{
		Indices: []string{indexName},
	})

	// Only require no error if we got a nil response - 404s are expected
	if err != nil && exists == nil {
		require.NoError(t, err, "Should be able to check if index exists")
	}

	// If status is 200, the index exists and should be deleted
	if exists != nil && exists.StatusCode == http.StatusOK {
		// Delete the index
		_, err := s.Client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
			Indices: []string{indexName},
		})
		require.NoError(t, err, "Should be able to delete index")
	}
}
