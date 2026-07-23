// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteFormattedThroughSymlinkedDir guards the write-sandbox namespace match.
// go/packages reports file paths symlink-resolved, but the module dir arrives as
// the caller's logical path. When that logical dir is itself under a symlink, a
// naive Rel(logicalDir, resolvedPath) yields a ../.. escape that os.Root rejects
// (observed as "path escapes from parent" against a module under macOS
// /tmp -> /private/tmp). The corpus tests miss this because they pass an already
// absolute dir, so Abs is a no-op. Here dir is the logical symlink and the file
// path is pre-resolved, reproducing the real CLI's Dir="." from a symlinked cwd.
func TestWriteFormattedThroughSymlinkedDir(t *testing.T) {
	realDir := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(realDir, linkDir))

	const src = "package p\n\nfunc F() {}\n"
	realFile := filepath.Join(realDir, "a.go")
	require.NoError(t, os.WriteFile(realFile, []byte(src), 0o644))

	// The path the linter derives from a token position is symlink-resolved.
	resolvedFile, err := filepath.EvalSymlinks(realFile)
	require.NoError(t, err)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, resolvedFile, src, parser.SkipObjectResolution)
	require.NoError(t, err)

	// Root the sandbox at the logical (symlinked) dir, as Walk does from Dir.
	root, err := os.OpenRoot(linkDir)
	require.NoError(t, err)
	defer root.Close()

	require.NoError(t, writeFormatted(root, linkDir, resolvedFile, fset, file))

	got, err := os.ReadFile(realFile)
	require.NoError(t, err)
	require.Equal(t, src, string(got))
}
