// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sdkReport flattens the per-file edit lines of a MigrateSDK result into one
// string for substring and parity checks, mirroring reportText for rewriteResult.
func sdkReport(results []SDKResult) string {
	var b strings.Builder
	for _, r := range results {
		for _, f := range r.Files {
			for _, e := range f.Edits {
				b.WriteString(e)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// TestMigrateSDKSingleHop drives the library entrypoint over the v2 corpus for a
// single explicit hop and checks the returned SDKResult shape: one hop v2 -> v3
// with the documented edits.
func TestMigrateSDKSingleHop(t *testing.T) {
	dir := stageCorpus(t, "v2", "stub-v2")
	results, err := MigrateSDK(t.Context(), SDKConfig{Dir: dir, Src: 2, Dst: 3, Write: false})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, major(2), results[0].From)
	require.Equal(t, major(3), results[0].To)
	require.NotEmpty(t, results[0].Files, "the v2 corpus has rewritable files")

	report := sdkReport(results)
	require.Contains(t, report, "repoint opensearchv2.NewClient -> opensearchapi.NewClient")
	require.Contains(t, report, `MANUAL "github.com/opensearch-project/opensearch-go/v2/opensearchapi.BulkRequest" removed`)
}

// TestMigrateSDKMatchesEngine is the parity check from the design: for the same
// dir/src/dst, MigrateSDK's dry-run edits must match the low-level driver the CLI
// used before the extraction (runTypeAwareRewrite over the same plan).
func TestMigrateSDKMatchesEngine(t *testing.T) {
	dir := stageCorpus(t, "v2", "stub-v2")

	plans, err := planChain(2, 3)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	p := plans[0]
	engine, err := runTypeAwareRewrite(rewriteConfig{
		dir:            dir,
		patterns:       []string{patternAll},
		delta:          p.delta,
		renames:        p.renames,
		regroups:       p.regroups,
		removedHelpers: p.removedHelpers,
		importPrefixes: p.importPrefixes,
		write:          false,
	})
	require.NoError(t, err)

	sdk, err := MigrateSDK(t.Context(), SDKConfig{Dir: dir, Src: 2, Dst: 3, Write: false})
	require.NoError(t, err)
	require.Equal(t, reportText(engine), sdkReport(sdk), "library edits must match the engine edits")
}

// TestMigrateSDKAutoDetect covers Src == 0: the source is detected from the
// module's imports (v2 corpus imports v2), with no warnings for a single major.
func TestMigrateSDKAutoDetect(t *testing.T) {
	dir := stageCorpus(t, "v2", "stub-v2")
	results, err := MigrateSDK(t.Context(), SDKConfig{Dir: dir, Src: 0, Dst: 3, Write: false})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, major(2), results[0].From)
	require.Empty(t, results[0].Warnings, "single-major module has no detection warning")
}

// TestMigrateSDKNewestKnown covers Dst == 0: the target defaults to the newest
// registered version, producing a contiguous multi-hop chain from the source.
func TestMigrateSDKNewestKnown(t *testing.T) {
	dir := stageCorpus(t, "v2", "stub-v2")
	results, err := MigrateSDK(t.Context(), SDKConfig{Dir: dir, Src: 2, Dst: 0, Write: false})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, major(2), results[0].From)
	require.Equal(t, newestKnownTarget(), results[len(results)-1].To)
	for i := 1; i < len(results); i++ {
		require.Equal(t, results[i-1].To, results[i].From, "hops must be contiguous")
	}
}

// TestMigrateSDKAlreadyAtTarget covers src >= dst: nothing to migrate, so the
// library returns no hops and no error.
func TestMigrateSDKAlreadyAtTarget(t *testing.T) {
	results, err := MigrateSDK(t.Context(), SDKConfig{Dir: t.TempDir(), Src: 5, Dst: 3, Write: false})
	require.NoError(t, err)
	require.Nil(t, results)
}

// TestMigrateSDKWarnings covers the warning surface: a module importing two
// majors detects the lowest as the source and returns the multi-major note on
// the first hop rather than dropping it.
func TestMigrateSDKWarnings(t *testing.T) {
	dir := stageTwoMajor(t)
	results, err := MigrateSDK(t.Context(), SDKConfig{Dir: dir, Src: 0, Dst: 5, Write: false})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, major(2), results[0].From)
	require.Len(t, results[0].Warnings, 1)
	require.Contains(t, results[0].Warnings[0], "v2")
	require.Contains(t, results[0].Warnings[0], "v3")
}
