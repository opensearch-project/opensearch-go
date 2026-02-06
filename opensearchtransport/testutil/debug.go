// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
// Package testutil provides common utilities for testing across the opensearch-go codebase.
package testutil

import (
	"os"
	"strconv"
	"testing"
)

// IsDebugEnabled returns true if DEBUG environment variable is set to true.
// This function should be used consistently across all test files to determine
// when to enable verbose logging or debug output.
//
// Usage:
//
//	if testutil.IsDebugEnabled(t) {
//	    t.Logf("debug info: %v", someData)
//	}
func IsDebugEnabled(t *testing.T) bool {
	t.Helper()
	val, found := os.LookupEnv("DEBUG")
	if found && val == "" { // preserve current behavior - empty DEBUG= enables debug
		return true
	}
	debug, err := strconv.ParseBool(val)
	if err != nil {
		return false
	}
	return debug
}
