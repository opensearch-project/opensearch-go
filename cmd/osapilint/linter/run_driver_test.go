// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// silenceOutput redirects os.Stdout and os.Stderr to /dev/null for the duration
// of a test. Rewrite and packages.PrintErrors write progress and load errors to
// the process streams; the driver tests exercise those paths for coverage, not
// for their text, so the noise is discarded. Tests using this must not run in
// parallel: it swaps process-global streams.
func silenceOutput(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() {
		os.Stdout, os.Stderr = savedOut, savedErr
		devnull.Close()
	})
}

// TestRewriteDriver drives the full rewrite command over a staged corpus module,
// covering the flag/detect/plan/apply flow that sits above runTypeAwareRewrite:
// auto-detection of the source major, an explicit -src, a multi-hop chain, and a
// -w write. The per-file rewrite correctness is asserted by TestRewriteCorpus;
// here the assertion is only that the driver runs the flow without error.
func TestRewriteDriver(t *testing.T) {
	tests := []struct {
		name string
		args func(dir string) []string
	}{
		{"auto-detect source, dry run to v3", func(d string) []string { return []string{"-dst=v3", d} }},
		{"explicit -src, dry run to v3", func(d string) []string { return []string{"-src=v2", "-dst=v3", d} }},
		{"multi-hop chain, dry run to v5", func(d string) []string { return []string{"-src=v2", "-dst=v5", d} }},
		{"write to v3", func(d string) []string { return []string{"-src=v2", "-dst=v3", "-w", d} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			silenceOutput(t)
			dir := stageCorpus(t, "v2", "stub-v2")
			require.NoError(t, Rewrite(tt.args(dir)))
		})
	}
}

// TestRewriteAlreadyAtTarget covers the src >= dst short-circuit: when the source
// already meets or exceeds the target, the driver reports "nothing to do" and
// returns nil without loading or planning.
func TestRewriteAlreadyAtTarget(t *testing.T) {
	silenceOutput(t)
	require.NoError(t, Rewrite([]string{"-src=v5", "-dst=v3", t.TempDir()}))
}

// TestRewriteBadDir covers the positional-argument validation path: a target that
// does not exist fails in dirFromArg before any package load, as an operational
// error (not a UsageError).
func TestRewriteBadDir(t *testing.T) {
	silenceOutput(t)
	err := Rewrite([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	require.Error(t, err)
}

// TestDetectSourceMajorMultiMajor covers detectSourceMajor over a module that
// imports two opensearch-go majors: the lowest is chosen as the source and the
// others are reported through majorList in a warning.
func TestDetectSourceMajorMultiMajor(t *testing.T) {
	dir := stageTwoMajor(t)
	src, warnings, err := detectSourceMajor(dir)
	require.NoError(t, err)
	require.Equal(t, major(2), src)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "v2")
	require.Contains(t, warnings[0], "v3")
}

// TestDetectSourceMajorNoImports covers the empty-result branch: a module with no
// opensearch-go import has nothing to migrate and detection fails.
func TestDetectSourceMajorNoImports(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{
		"go.mod": "module example.com/none\n\ngo 1.24\n",
		"p.go":   "package none\n\nvar X = 1\n",
	})
	_, _, err := detectSourceMajor(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no opensearch-go imports")
}

func TestMajorList(t *testing.T) {
	require.Equal(t, []string{"v2", "v4", "v5"}, majorList([]major{2, 4, 5}))
	require.Empty(t, majorList(nil))
}

func TestAnalyzersNonEmpty(t *testing.T) {
	require.NotEmpty(t, Analyzers(), "vet must expose at least one analyzer")
}

func TestUsageErrorMessage(t *testing.T) {
	require.Equal(t, "invalid command line", (&UsageError{}).Error())
	require.Equal(t, "-dst: bad value", (&UsageError{Msg: "-dst: bad value"}).Error())
}

// TestUsageWritesText covers Usage: it names the binary and both subcommands so a
// bare or unknown invocation is self-explanatory.
func TestUsageWritesText(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved := os.Stderr
	os.Stderr = w
	Usage()
	os.Stderr = saved
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "osapilint rewrite")
	require.Contains(t, string(out), "osapilint vet")
}

// writeModule writes the given files (relative path -> content) under dir,
// creating parent directories as needed.
func writeModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
}

// stageTwoMajor stages a consumer module that imports two opensearch-go majors
// (v2 and v3), each resolved to its committed stub via a replace so the import
// graph loads offline. It returns the consumer module directory.
func stageTwoMajor(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	srcBase := filepath.Join("testdata", "corpus")
	require.NoError(t, os.CopyFS(filepath.Join(root, "stub-v2"), os.DirFS(filepath.Join(srcBase, "stub-v2"))))
	require.NoError(t, os.CopyFS(filepath.Join(root, "stub-v3"), os.DirFS(filepath.Join(srcBase, "stub-v3"))))

	consumer := filepath.Join(root, "consumer")
	writeModule(t, consumer, map[string]string{
		"go.mod": "module example.com/twomajor\n\ngo 1.24\n\n" +
			"require (\n" +
			"\tgithub.com/opensearch-project/opensearch-go/v2 v2.0.0\n" +
			"\tgithub.com/opensearch-project/opensearch-go/v3 v3.0.0\n" +
			")\n\n" +
			"replace github.com/opensearch-project/opensearch-go/v2 => ../stub-v2\n" +
			"replace github.com/opensearch-project/opensearch-go/v3 => ../stub-v3\n",
		"consumer.go": "package consumer\n\nimport (\n" +
			"\t_ \"github.com/opensearch-project/opensearch-go/v2\"\n" +
			"\t_ \"github.com/opensearch-project/opensearch-go/v3\"\n" +
			")\n",
	})
	return consumer
}
