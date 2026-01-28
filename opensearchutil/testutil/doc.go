// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides shared test utilities for the OpenSearch Go client.
//
// This is the authoritative package for shared test utilities that can be used
// across the entire project, including by external *_test packages that cannot
// import from internal/ directories due to Go's visibility restrictions.
//
// # Design Principles
//
// All exported functions in this package follow these design principles:
//
//   - MUST take `t *testing.T` as the first parameter
//   - MUST call `t.Helper()` as the first statement
//   - SHOULD be simple, focused utilities rather than complex orchestration
//
// The `t *testing.T` requirement prevents misuse of test utilities outside
// of test contexts and ensures proper test failure reporting. When test
// utilities call `t.Helper()`, test failures point to the calling test code
// rather than the utility implementation, making debugging easier.
//
// # When to Add Functions Here
//
// Add functions to this package when:
//   - The utility is needed by multiple packages across the project
//   - External *_test packages need access to the functionality
//   - The utility is a simple, reusable helper (like generating unique strings)
//   - The utility provides cross-cutting test configuration (like client setup)
//
// This package is the single source of truth for all shared test utilities.
// All test utilities should be centralized here to avoid duplication and
// ensure consistent behavior across the entire project.
//
// # Examples
//
//	// Generate unique test resource names
//	indexName := testutil.MustUniqueString(t, "test-index")
//
//	// Get test client configuration
//	config, err := testutil.ClientConfig(t)
//	require.NoError(t, err)
//
//	// Check if running against secure cluster
//	if testutil.IsSecure(t) {
//		// secure-specific test logic
//	}
package testutil
