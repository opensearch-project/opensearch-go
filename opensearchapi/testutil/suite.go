// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
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
	major, minor, patch, err := GetVersion(t, t.Context(), s.Client)
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

// SkipIfVersion skips a test when the cluster version satisfies the given
// operator and version constraint. See the package-level SkipIfVersion for
// full documentation and examples.
func (s *OpenSearchTestSuite) SkipIfVersion(operator string, version string, testName string) {
	t := s.T()
	t.Helper()

	cMajor, cMinor, cPatch, hasPatch := parseVersion(t, version)

	serverSemver := fmt.Sprintf("v%d.%d.%d", s.Major, s.Minor, s.Patch)
	targetSemver := fmt.Sprintf("v%d.%d.%d", cMajor, cMinor, cPatch)

	var matches bool
	switch {
	case operator == "=" && !hasPatch:
		matches = s.Major == cMajor && s.Minor == cMinor
	case operator == "!=" && !hasPatch:
		matches = s.Major != cMajor || s.Minor != cMinor
	default:
		cmp := semver.Compare(serverSemver, targetSemver)
		switch operator {
		case "=":
			matches = cmp == 0
		case "!=":
			matches = cmp != 0
		case "<":
			matches = cmp < 0
		case "<=":
			matches = cmp <= 0
		case ">":
			matches = cmp > 0
		case ">=":
			matches = cmp >= 0
		default:
			t.Fatalf("SkipIfVersion: unsupported operator %q: must be =, !=, <, <=, >, or >=", operator)
		}
	}

	if matches {
		t.Skipf("Skipping %s: server version %s matches constraint %q %s", testName, serverSemver[1:], operator, version)
	}
}

// SkipIfNotSecure skips a test that runs against an insecure cluster
func (s *OpenSearchTestSuite) SkipIfNotSecure() {
	t := s.T()
	t.Helper()
	if !tptestutil.IsSecure(t) {
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
	t := s.T()
	t.Helper()
	ctx := context.Background()

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
