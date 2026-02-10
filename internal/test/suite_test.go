// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package ostest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
)

// TestSuiteVersion tests the Version method of OpenSearchTestSuite
type TestSuiteVersion struct {
	ostest.OpenSearchTestSuite
}

func (s *TestSuiteVersion) TestVersion() {
	// Version() should return a properly formatted version string
	version := s.Version()
	assert.NotEmpty(s.T(), version)

	// Log the version information
	s.T().Logf("OpenSearch version: %s (Major: %d, Minor: %d, Patch: %d)",
		version, s.Major, s.Minor, s.Patch)

	// Verify version components are reasonable
	assert.GreaterOrEqual(s.T(), s.Major, int64(1), "Major version should be at least 1")
	assert.GreaterOrEqual(s.T(), s.Minor, int64(0), "Minor version should be non-negative")
	assert.GreaterOrEqual(s.T(), s.Patch, int64(0), "Patch version should be non-negative")

	// Verify version string format matches components
	assert.Contains(s.T(), version, ".")
}

func TestVersionSuite(t *testing.T) {
	suite.Run(t, new(TestSuiteVersion))
}
