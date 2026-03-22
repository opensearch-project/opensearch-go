// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil //nolint:testpackage // tests unexported parseVersion, classifyConnError

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		major    int64
		minor    int64
		patch    int64
		hasPatch bool
	}{
		{name: "two-part 2.15", version: "2.15", major: 2, minor: 15, patch: 0, hasPatch: false},
		{name: "three-part 2.4.0", version: "2.4.0", major: 2, minor: 4, patch: 0, hasPatch: true},
		{name: "three-part 3.0.1", version: "3.0.1", major: 3, minor: 0, patch: 1, hasPatch: true},
		{name: "large patch 1.3.19", version: "1.3.19", major: 1, minor: 3, patch: 19, hasPatch: true},
		{name: "zero major 0.9", version: "0.9", major: 0, minor: 9, patch: 0, hasPatch: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			major, minor, patch, hasPatch := parseVersion(t, tc.version)
			require.Equal(t, tc.major, major, "major")
			require.Equal(t, tc.minor, minor, "minor")
			require.Equal(t, tc.patch, patch, "patch")
			require.Equal(t, tc.hasPatch, hasPatch, "hasPatch")
		})
	}
}

func TestClassifyConnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		err            error
		everConnected  bool
		initialEOF     int
		expectFatal    bool
		expectEOFCount int
	}{
		{
			name:           "Unauthorized is fatal",
			err:            errors.New("Get https://localhost:9200: Unauthorized"),
			everConnected:  false,
			initialEOF:     0,
			expectFatal:    true,
			expectEOFCount: 0,
		},
		{
			name:           "invalid character U is fatal",
			err:            errors.New("invalid character 'U' looking for beginning of value"),
			everConnected:  false,
			initialEOF:     0,
			expectFatal:    true,
			expectEOFCount: 0,
		},
		{
			name:           "EOF with count below threshold is transient",
			err:            errors.New("read: connection reset by peer: EOF"),
			everConnected:  false,
			initialEOF:     0,
			expectFatal:    false,
			expectEOFCount: 1,
		},
		{
			name:           "EOF reaching threshold is fatal",
			err:            errors.New("EOF"),
			everConnected:  false,
			initialEOF:     4,
			expectFatal:    true,
			expectEOFCount: 5,
		},
		{
			name:           "EOF when everConnected resets count",
			err:            errors.New("unexpected EOF"),
			everConnected:  true,
			initialEOF:     4,
			expectFatal:    false,
			expectEOFCount: 0,
		},
		{
			name:           "non-EOF error resets count",
			err:            errors.New("connection refused"),
			everConnected:  false,
			initialEOF:     3,
			expectFatal:    false,
			expectEOFCount: 0,
		},
		{
			name:           "EOF at count 3 not yet fatal",
			err:            errors.New("EOF"),
			everConnected:  false,
			initialEOF:     3,
			expectFatal:    false,
			expectEOFCount: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eofCount := tc.initialEOF
			result := classifyConnError(tc.err, tc.everConnected, &eofCount)
			if tc.expectFatal {
				require.Error(t, result, "expected a fatal error")
			} else {
				require.NoError(t, result, "expected nil (transient/retryable)")
			}
			require.Equal(t, tc.expectEOFCount, eofCount, "eofCount after call")
		})
	}
}

func TestOpenSearchTestSuiteVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		major    int64
		minor    int64
		patch    int64
		expected string
	}{
		{name: "typical version", major: 2, minor: 15, patch: 0, expected: "2.15.0"},
		{name: "patch version", major: 3, minor: 0, patch: 1, expected: "3.0.1"},
		{name: "all zeros", major: 0, minor: 0, patch: 0, expected: "0.0.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &OpenSearchTestSuite{
				Major: tc.major,
				Minor: tc.minor,
				Patch: tc.patch,
			}
			got := s.Version()
			require.Equal(t, tc.expected, got,
				"Version() for %d.%d.%d", tc.major, tc.minor, tc.patch)
		})
	}
}
