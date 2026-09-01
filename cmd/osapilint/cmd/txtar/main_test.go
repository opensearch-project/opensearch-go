// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/txtar"
)

// sectionNames flattens an archive's section names for order assertions.
func sectionNames(t *testing.T, archive string) []string {
	t.Helper()
	ar, err := txtar.ParseFile(archive)
	require.NoError(t, err)
	names := make([]string, len(ar.Files))
	for i, f := range ar.Files {
		names[i] = f.Name
	}
	return names
}

// seedArchive is deliberately unsorted (go.mod first, like the corpus
// archives) and carries a comment, so the round-trip tests exercise both
// invariants pack must preserve.
const seedArchive = "seed comment line 1\nseed comment line 2\n" +
	"-- mod/go.mod --\nmodule example.com/mod\n" +
	"-- mod/a.go --\npackage mod\n" +
	"-- mod/b.go --\npackage mod\n\nvar B = 1\n"

func writeSeed(t *testing.T) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "seed.txtar")
	require.NoError(t, os.WriteFile(archive, []byte(seedArchive), 0o600))
	return archive
}

// TestUnpackPackRoundTrip is the core promise: unpack then pack with no edits
// reproduces the archive byte for byte, comment and section order included.
func TestUnpackPackRoundTrip(t *testing.T) {
	t.Parallel()
	archive := writeSeed(t)
	dir := filepath.Join(t.TempDir(), "tree")
	require.NoError(t, unpack(archive, dir))
	require.NoError(t, pack(dir, archive))
	got, err := os.ReadFile(archive)
	require.NoError(t, err)
	require.Equal(t, seedArchive, string(got))
}

// TestCommittedArchivesRoundTrip runs the same promise over every committed
// fixture archive, so the tool is known to work on the archives it exists for.
func TestCommittedArchivesRoundTrip(t *testing.T) {
	t.Parallel()
	archives, err := filepath.Glob(filepath.Join("..", "..", "linter", "testdata", "*", "*.txtar"))
	require.NoError(t, err)
	more, err := filepath.Glob(filepath.Join("..", "..", "linter", "testdata", "*.txtar"))
	require.NoError(t, err)
	archives = append(archives, more...)
	require.NotEmpty(t, archives)

	for _, committed := range archives {
		t.Run(filepath.Base(committed), func(t *testing.T) {
			t.Parallel()
			want, err := os.ReadFile(committed)
			require.NoError(t, err)

			scratch := t.TempDir()
			archiveCopy := filepath.Join(scratch, "copy.txtar")
			//nolint:gosec // G703 false positive: both path and data derive from t.TempDir and the committed testdata glob
			require.NoError(t, os.WriteFile(archiveCopy, want, 0o600))
			dir := filepath.Join(scratch, "tree")
			require.NoError(t, unpack(archiveCopy, dir))
			require.NoError(t, pack(dir, archiveCopy))

			got, err := os.ReadFile(archiveCopy)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got))
		})
	}
}

// TestPackEditAddDelete covers the in-place update semantics: surviving
// sections keep their slot with new content, deleted files drop out, new
// files append in sorted order, and the comment survives.
func TestPackEditAddDelete(t *testing.T) {
	t.Parallel()
	archive := writeSeed(t)
	dir := filepath.Join(t.TempDir(), "tree")
	require.NoError(t, unpack(archive, dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod", "a.go"), []byte("package mod\n\nvar A = 2\n"), 0o600))
	require.NoError(t, os.Remove(filepath.Join(dir, "mod", "b.go")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod", "c.go"), []byte("package mod\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod", "aa.go"), []byte("package mod\n"), 0o600))

	require.NoError(t, pack(dir, archive))

	require.Equal(t, []string{"mod/go.mod", "mod/a.go", "mod/aa.go", "mod/c.go"}, sectionNames(t, archive))
	ar, err := txtar.ParseFile(archive)
	require.NoError(t, err)
	require.Equal(t, "seed comment line 1\nseed comment line 2\n", string(ar.Comment))
	require.Equal(t, "package mod\n\nvar A = 2\n", string(ar.Files[1].Data))
}

// TestPackNewArchive covers packing into a path with no existing archive:
// no comment, all sections sorted.
func TestPackNewArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600))

	archive := filepath.Join(t.TempDir(), "new.txtar")
	require.NoError(t, pack(dir, archive))
	require.Equal(t, []string{"a.txt", "b.txt"}, sectionNames(t, archive))
}

// TestPackRejectsMarkerInBody covers the corruption guard: txtar has no
// escaping, so a body containing a section-marker line must fail the pack
// instead of silently splitting the archive.
func TestPackRejectsMarkerInBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "evil.txt"), []byte("before\n-- phantom --\nafter\n"), 0o600))

	archive := filepath.Join(t.TempDir(), "out.txtar")
	err := pack(dir, archive)
	require.ErrorContains(t, err, "does not round-trip")
	require.NoFileExists(t, archive)
}

func TestPackEmptyDir(t *testing.T) {
	t.Parallel()
	err := pack(t.TempDir(), filepath.Join(t.TempDir(), "out.txtar"))
	require.ErrorContains(t, err, "no files under")
}

// TestRunUsageErrors covers the argument shapes that must print usage and exit
// 2 without touching the filesystem.
func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "too few arguments", args: []string{cmdUnpack, "only-one"}},
		{name: "too many arguments", args: []string{cmdUnpack, "a", "b", "c"}},
		{name: "unknown subcommand", args: []string{"squash", "a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			require.Equal(t, 2, run(tc.args, &stderr))
			require.Contains(t, stderr.String(), "usage:")
		})
	}
}

// TestRunDispatch covers the exit status run reports for each subcommand, since
// that status is the tool's contract with whoever is fixing a failing archive.
func TestRunDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     func(t *testing.T) []string
		wantCode int
		// wantStderr is a required substring; empty means stderr must stay empty.
		wantStderr string
	}{
		{
			name: cmdUnpack,
			args: func(t *testing.T) []string {
				t.Helper()
				return []string{cmdUnpack, writeSeed(t), filepath.Join(t.TempDir(), "tree")}
			},
			wantCode: 0,
		},
		{
			name: cmdPack,
			args: func(t *testing.T) []string {
				t.Helper()
				archive := writeSeed(t)
				dir := filepath.Join(t.TempDir(), "tree")
				require.NoError(t, unpack(archive, dir))
				return []string{cmdPack, dir, archive}
			},
			wantCode: 0,
		},
		{
			name: "missing archive",
			args: func(t *testing.T) []string {
				t.Helper()
				return []string{cmdUnpack, filepath.Join(t.TempDir(), "absent.txtar"), filepath.Join(t.TempDir(), "tree")}
			},
			wantCode:   1,
			wantStderr: "txtar:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			require.Equal(t, tc.wantCode, run(tc.args(t), &stderr))
			if tc.wantStderr == "" {
				require.Empty(t, stderr.String())
				return
			}
			require.Contains(t, stderr.String(), tc.wantStderr)
		})
	}
}
