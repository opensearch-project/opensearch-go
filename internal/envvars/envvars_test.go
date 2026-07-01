// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package envvars_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// TestTruthyAndFalsy covers the two boolean env-var helpers. Note the
// asymmetry: unset, empty, and unparseable values all return false
// from BOTH Truthy and Falsy. Callers that need to distinguish
// "explicitly opted in" from "explicitly opted out" from "unset" use
// both helpers in tandem (e.g. `if !envvars.Falsy(name) { ... }` to
// mean "unset OR truthy").
func TestTruthyAndFalsy(t *testing.T) {
	const key = "OS_GO_TEST_BOOL_HELPER"

	tests := []struct {
		name       string
		setValue   string
		set        bool // false means "unset"
		wantTruthy bool
		wantFalsy  bool
	}{
		{name: "unset", set: false, wantTruthy: false, wantFalsy: false},
		{name: "empty string", set: true, setValue: "", wantTruthy: false, wantFalsy: false},
		{name: "true", set: true, setValue: "true", wantTruthy: true, wantFalsy: false},
		{name: "TRUE", set: true, setValue: "TRUE", wantTruthy: true, wantFalsy: false},
		{name: "1", set: true, setValue: "1", wantTruthy: true, wantFalsy: false},
		{name: "false", set: true, setValue: "false", wantTruthy: false, wantFalsy: true},
		{name: "FALSE", set: true, setValue: "FALSE", wantTruthy: false, wantFalsy: true},
		{name: "0", set: true, setValue: "0", wantTruthy: false, wantFalsy: true},
		{name: "unparseable", set: true, setValue: "garbage", wantTruthy: false, wantFalsy: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.setValue)
			} else {
				// Truly unset (not empty) so the LookupEnv ok==false path is
				// exercised rather than duplicating the "empty string" row.
				// t.Setenv first registers cleanup to restore any inherited
				// value; os.Unsetenv then removes it for the test body.
				t.Setenv(key, "")
				os.Unsetenv(key) //nolint:usetesting // t.Setenv cannot unset; this test mutates process env and must not run in parallel
			}
			require.Equal(t, tt.wantTruthy, envvars.Truthy(key))
			require.Equal(t, tt.wantFalsy, envvars.Falsy(key))
			// Truthy and Falsy must never both return true for the same value.
			require.False(t, envvars.Truthy(key) && envvars.Falsy(key),
				"Truthy and Falsy must be mutually exclusive")
		})
	}
}

// TestParseDefaultClientTTL covers the pure parser behind DefaultClientTTLValue.
// The public accessor is sync.Once-cached and cannot be re-driven per case, so
// the parse logic is tested directly.
func TestParseDefaultClientTTL(t *testing.T) {
	tests := []struct {
		name         string
		val          string
		ok           bool
		wantTTL      time.Duration
		wantDisabled bool
	}{
		{"unset", "", false, 6 * time.Minute, false},
		{"empty", "", true, 6 * time.Minute, false},
		{"invalid", "notaduration", true, 6 * time.Minute, false},
		{"negative disables", "-1s", true, 0, true},
		{"zero indefinite", "0", true, 0, false},
		{"positive", "90s", true, 90 * time.Second, false},
		{"positive minutes", "10m", true, 10 * time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl, disabled := envvars.ParseDefaultClientTTL(tt.val, tt.ok)
			require.Equal(t, tt.wantTTL, ttl)
			require.Equal(t, tt.wantDisabled, disabled)
		})
	}
}
