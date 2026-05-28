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

	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// OpenSearchTestSuite provides a testify suite with automatic client setup and readiness checking
type OpenSearchTestSuite struct {
	suite.Suite
	Client *opensearchapi.Client

	Major, Minor, Patch int64
}

// SetupSuite is called once before any tests in the suite run
func (s *OpenSearchTestSuite) SetupSuite() {
	t := s.T()

	client, err := NewClient(t)
	require.NoError(t, err, "Failed to create OpenSearch client")
	s.Client = client

	major, minor, patch, err := GetVersion(t, t.Context(), s.Client)
	require.NoError(t, err, "Failed to get OpenSearch version")

	s.Major, s.Minor, s.Patch = major, minor, patch
}

// TearDownSuite is called once after all tests in the suite have run
func (s *OpenSearchTestSuite) TearDownSuite() {}

// Version returns a formatted version string for logging
func (s *OpenSearchTestSuite) Version() string {
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// SkipIfVersion skips a test when the cluster version satisfies the given
// operator and version constraint.
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
	s.T().Helper()

	t := s.T()
	t.Helper()
	ctx := t.Context()

	resp, err := s.Client.Indices.Exists(ctx, &opensearchapi.IndicesExistsReq{
		Index: []string{indexName},
	})
	if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
		return
	}

	_, err = s.Client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: indexName,
	})
	require.NoError(t, err, "Should be able to create index")
}

// CleanupIndex deletes an index if it exists
func (s *OpenSearchTestSuite) CleanupIndex(indexName string) {
	s.T().Helper()

	t := s.T()
	t.Helper()
	ctx := context.Background()

	resp, err := s.Client.Indices.Exists(ctx, &opensearchapi.IndicesExistsReq{
		Index: []string{indexName},
	})
	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		return
	}

	_, err = s.Client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
		Index: []string{indexName},
	})
	require.NoError(t, err, "Should be able to delete index")
}
