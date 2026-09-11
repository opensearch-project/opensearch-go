// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// debugEnvChild marks the re-executed copy of this test binary and carries what
// that copy should expect: "on" if a logger must be installed, "off" if not.
const debugEnvChild = "OSGO_TEST_DEBUG_ENV_CHILD"

// debugEnvTestName is the name of the test below, used to re-run exactly it in the
// child. Naming it as a constant keeps the -test.run pattern from having to be
// derived from t.Name(), which inside a subtest carries the subtest name too.
const debugEnvTestName = "TestDebugEnvInstallsBuiltinLogger"

// TestDebugEnvInstallsBuiltinLogger pins the end-to-end effect of
// OPENSEARCH_GO_LOG and OPENSEARCH_GO_DEBUG: whether the package's init installs
// the built-in logger, and which variable wins when both are set.
//
// envvars.DebugRequested is unit-tested on its own, but nothing connected it to
// the observable outcome. This does, and it is the only way to: init runs at
// package load, before a test can call t.Setenv, so the assertion has to happen
// in a process that already had the variable set. Each row therefore re-executes
// this binary with a scrubbed environment plus the row's own variables.
func TestDebugEnvInstallsBuiltinLogger(t *testing.T) {
	t.Parallel()

	if want, isChild := os.LookupEnv(debugEnvChild); isChild {
		require.Equal(t, want == "on", debugEnabled(),
			"init resolved %s=%q / %s=%q to the wrong state",
			envvars.Log, os.Getenv(envvars.Log), envvars.Debug, os.Getenv(envvars.Debug))
		return
	}

	tests := []struct {
		name string
		env  []string
		want bool
	}{
		{name: "neither set"},
		{name: "log debug installs", env: []string{envvars.Log + "=debug"}, want: true},
		{name: "log debug is case insensitive", env: []string{envvars.Log + "=DEBUG"}, want: true},
		{name: "log debug tolerates spaces", env: []string{envvars.Log + "= debug "}, want: true},
		{name: "log info does not install", env: []string{envvars.Log + "=info"}},
		{name: "log unrecognized does not install", env: []string{envvars.Log + "=verbose"}},
		{name: "debug truthy installs", env: []string{envvars.Debug + "=true"}, want: true},
		{name: "debug falsy does not install", env: []string{envvars.Debug + "=false"}},
		{
			name: "log debug overrides falsy debug",
			env:  []string{envvars.Log + "=debug", envvars.Debug + "=false"},
			want: true,
		},
		{
			// The reason Log wins rather than merely being consulted first: a
			// deployment can be quieted without unsetting the older boolean.
			name: "log info overrides truthy debug",
			env:  []string{envvars.Log + "=info", envvars.Debug + "=true"},
		},
		{
			name: "empty log falls through to debug",
			env:  []string{envvars.Log + "=", envvars.Debug + "=true"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := "off"
			if tt.want {
				want = "on"
			}

			//nolint:gosec // os.Args[0] is this test binary; the pattern is a constant
			cmd := exec.Command(os.Args[0], "-test.run=^"+debugEnvTestName+"$", "-test.count=1")
			cmd.Env = append(scrubDebugEnv(), tt.env...)
			cmd.Env = append(cmd.Env, debugEnvChild+"="+want)

			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "child process output:\n%s", out)
		})
	}
}

// scrubDebugEnv copies the environment with both debug variables removed, so a
// value inherited from the developer's shell cannot decide a row's outcome.
func scrubDebugEnv() []string {
	env := os.Environ()
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, envvars.Log+"="),
			strings.HasPrefix(kv, envvars.Debug+"="),
			strings.HasPrefix(kv, debugEnvChild+"="):
			continue
		default:
			kept = append(kept, kv)
		}
	}
	return kept
}
