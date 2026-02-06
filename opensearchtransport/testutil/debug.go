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
	debug, _ := strconv.ParseBool(val)
	return debug
}
