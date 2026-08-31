// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/imports"
	"golang.org/x/tools/txtar"
)

// staged records, by testdata-relative path (e.g. "corpus/v2.txtar"), every
// archive extractTxtar has loaded during the test run. TestMain compares this
// against the archives on disk to catch a fixture nothing stages anymore.
//
//nolint:gochecknoglobals // recorder written by extractTxtar, read by TestMain
var staged struct {
	sync.Mutex
	names map[string]struct{}
}

func recordStaged(name string) {
	staged.Lock()
	defer staged.Unlock()
	if staged.names == nil {
		staged.names = make(map[string]struct{})
	}
	staged.names[name] = struct{}{}
}

// TestMain lets the completeness half of the zombie check - "does every
// archive on disk get staged by some test" - run once after the whole
// package's tests, rather than per-test, since it needs every extractTxtar
// call to have happened first.
func TestMain(m *testing.M) {
	code := m.Run()

	// A filtered run (make fix-txtar passes -run TestTxtarArchives; any
	// single-test debug run does the same) never reaches most extractTxtar
	// call sites, so every un-staged archive would misreport as a zombie.
	// Likewise, a failing suite may legitimately not have staged everything
	// it would have on a green run.
	if code == 0 && flag.Lookup("test.run").Value.String() == "" && !checkNoZombieArchives() {
		code = 1
	}
	os.Exit(code)
}

// checkNoZombieArchives compares every archive extractTxtar loaded during this
// run against every .txtar file on disk under this package's testdata/. It
// only knows about this package's own archives - not any other module's - so
// a clean report here does not vet those; a module that gains txtar fixtures
// needs its own recorder.
func checkNoZombieArchives() bool {
	staged.Lock()
	got := staged.names
	staged.Unlock()

	onDisk := map[string]struct{}{}
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".txtar") {
			return nil
		}
		rel, err := filepath.Rel("testdata", p)
		if err != nil {
			return err
		}
		onDisk[rel] = struct{}{}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkNoZombieArchives: walk testdata: %v\n", err)
		return false
	}

	ok := true
	for name := range onDisk {
		if _, isStaged := got[name]; !isStaged {
			fmt.Fprintf(os.Stderr,
				"zombie: testdata/%s was never staged by any test; delete it, or stage it from a test\n", name)
			ok = false
		}
	}
	for name := range got {
		if _, exists := onDisk[name]; !exists {
			fmt.Fprintf(os.Stderr, "testdata/%s was staged by a test but does not exist on disk\n", name)
			ok = false
		}
	}
	return ok
}

// TestTxtarArchives gates every txtar fixture archive tracked in git - not
// just this module's - against drift: canonical byte form, no CRLF, section
// names that cannot escape extractTxtar's sandbox, and goimports-clean
// Go/golden sections. Fixtures live in archives precisely so ordinary
// repo-wide tooling does not walk into them (see extractTxtar), which means
// nothing else in the build catches this drift. The zombie check - is this
// archive staged by anything - lives in TestMain instead, since it needs the
// whole package's tests to have run first.
//
// Fix mechanical drift (canonical form, CRLF, formatting) with:
//
//	UPDATE_TXTAR=1 go test ./linter -run TestTxtarArchives
//
// A bad section name is not mechanically fixable and fails even under
// UPDATE_TXTAR; edit the archive by hand with `go run ./cmd/txtar unpack
// <archive> <dir>` / `pack <dir> <archive>`.
func TestTxtarArchives(t *testing.T) {
	t.Parallel()
	root := gitRepoRoot(t)
	archives := gitTrackedTxtarArchives(t, root)
	// A gate that silently matches nothing is worse than no gate: it would
	// pass green forever even if every archive in the repo were deleted.
	require.NotEmpty(t, archives, "no tracked .txtar archives found under %s", root)

	for _, archive := range archives {
		rel, err := filepath.Rel(root, archive)
		require.NoError(t, err)
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			checkArchive(t, archive)
		})
	}
}

// gitRepoRoot returns the absolute path of the git working tree root,
// matching cmd/osgen/api_cmd.go's repoRootGit.
func gitRepoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "git rev-parse --show-toplevel")
	return strings.TrimSpace(string(out))
}

// gitTrackedTxtarArchives returns the absolute paths of every .txtar file
// tracked by git in the repo. Walking the filesystem instead would descend
// into any worktrees checked out under this repo - this one alone carries
// around a dozen, under .worktrees/ and .claude/worktrees/ - gating and, with
// UPDATE_TXTAR=1, rewriting their archives too; git ls-files only ever sees
// this worktree's own tracked files. Consequently this only gates *tracked*
// archives - a brand-new untracked one isn't gated until it's added, which is
// the right call, not an oversight.
func gitTrackedTxtarArchives(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "*.txtar").Output()
	require.NoError(t, err, "git ls-files *.txtar")

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	var archives []string
	for _, line := range strings.Split(trimmed, "\n") {
		archives = append(archives, filepath.Join(root, line))
	}
	return archives
}

func checkArchive(t *testing.T, archive string) {
	raw, err := os.ReadFile(archive)
	require.NoError(t, err)
	ar := txtar.Parse(raw)
	update := os.Getenv("UPDATE_TXTAR") != ""

	// Section names: not cosmetic. extractTxtar lays archives down with
	// os.CopyFS into a t.TempDir(), so a ".." section name is a write escape
	// out of the sandbox. Not mechanically fixable - a human has to decide
	// the intended name - so this hard-fails regardless of UPDATE_TXTAR.
	seen := map[string]struct{}{}
	for _, f := range ar.Files {
		require.Falsef(t, path.IsAbs(f.Name),
			"%s: section %q is an absolute path; fix by hand: go run ./cmd/txtar unpack %s <dir>, rename, go run ./cmd/txtar pack <dir> %s",
			archive, f.Name, archive, archive)
		require.Falsef(t, slices.Contains(strings.Split(f.Name, "/"), ".."),
			"%s: section %q contains a \"..\" element; fix by hand: go run ./cmd/txtar unpack %s <dir>, rename, go run ./cmd/txtar pack <dir> %s",
			archive, f.Name, archive, archive)
		_, dup := seen[f.Name]
		require.Falsef(t, dup,
			"%s: section %q is duplicated; fix by hand: go run ./cmd/txtar unpack %s <dir>, dedupe, go run ./cmd/txtar pack <dir> %s",
			archive, f.Name, archive, archive)
		seen[f.Name] = struct{}{}
	}

	// CRLF gets its own assertion so a CRLF-only archive names the actual
	// problem instead of a three-way guess; it stays auto-fixed below, since
	// canonicalize strips CRLF as part of computing the canonical form.
	require.Falsef(t, bytes.Contains(raw, []byte("\r\n")) && !update,
		"%s contains CRLF line endings; fix with: UPDATE_TXTAR=1 go test ./linter -run TestTxtarArchives", archive)

	// Canonical form and section formatting are both mechanical: the fix is
	// always "recompute and rewrite", so they share one remedy.
	canon := canonicalize(t, raw)
	if !bytes.Equal(canon, raw) {
		if update {
			require.NoError(t, os.WriteFile(archive, canon, 0o600))
			return
		}
		require.Equal(t, string(canon), string(raw),
			"%s is not canonical txtar.Format output or has non-goimports-clean .go/.golden sections; fix with: UPDATE_TXTAR=1 go test ./linter -run TestTxtarArchives",
			archive)
	}
}

// canonicalize returns the canonical bytes raw should have: LF-only,
// txtar.Format's exact section layout, and every .go/.golden section body run
// through imports.Process. Comparing this against raw covers the CRLF,
// section-layout, and formatting assertions in one shot, since all three
// share the same fix.
func canonicalize(t *testing.T, raw []byte) []byte {
	t.Helper()
	noCR := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	ar := txtar.Parse(noCR)
	for i, f := range ar.Files {
		if !isFormattable(f.Name) {
			continue
		}
		// FormatOnly is load-bearing: goldens are never compiled, and
		// assertNoUnusedImports only checks the compileClean subset, so a
		// non-compileClean golden may legitimately carry an unreferenced
		// import representing a reported MANUAL case mid-migration. Full
		// imports.Process would delete that import and break the fixture it
		// exists to test. Even so, imports.Process regroups imports into
		// std/non-std blocks the same way goimports does - that's goimports
		// behaviour, not gofmt - and runs the same printer as gofmt, so a
		// separate gofmt/format.Source assertion could never fire on its own
		// here.
		formatted, err := imports.Process(f.Name, f.Data, &imports.Options{
			FormatOnly: true,
			Comments:   true,
			TabIndent:  true,
			TabWidth:   8,
		})
		require.NoErrorf(t, err, "format section %q", f.Name)
		ar.Files[i].Data = formatted
	}
	return txtar.Format(ar)
}

func isFormattable(section string) bool {
	return strings.HasSuffix(section, ".go") || strings.HasSuffix(section, ".golden")
}
