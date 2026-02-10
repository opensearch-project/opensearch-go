// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides utilities for testing OpenSearch Go client functionality.
// This package contains test helpers that are shared across integration tests
// and can be used by external test packages.
package testutil

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// MustUniqueString returns a unique string with the given prefix.
// This is useful for creating unique resource names in tests to avoid conflicts.
func MustUniqueString(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, rand.Int64()) // #nosec G404 -- Using math/rand for test resource names, not cryptographic purposes
}
