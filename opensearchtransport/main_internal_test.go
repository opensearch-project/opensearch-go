// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"os"
	"testing"
)

// TestMain disables the default router for the transport unit-test package.
//
// In v5 the router is on by default (OPENSEARCH_GO_ROUTER defaults to enabled),
// which makes New() spawn background health-check and discovery goroutines.
// Most unit tests here drive a mock transport and count or shape its calls;
// those background requests would race with the test body and perturb counts
// and pool shapes. Tests that specifically exercise router behavior set
// Config.Router programmatically (which always wins) or override the env var
// per-test, so this package-wide opt-out does not weaken them.
func TestMain(m *testing.M) {
	if _, ok := os.LookupEnv(envRouter); !ok {
		os.Setenv(envRouter, "false")
	}
	os.Exit(m.Run())
}
