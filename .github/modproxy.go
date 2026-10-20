// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build ignore

// modproxy builds a module zip for an unpublished root version from a git
// revision and lays it out as a file-based Go module proxy, so `go mod tidy`
// in a nested module can record the version's go.sum checksums before the
// version is fetchable from the real proxy. With -pseudo it derives the
// version itself, as Go would for the commit, and prints it. See
// .github/release.sh.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	modzip "golang.org/x/mod/zip"
)

func main() {
	var (
		repo    = flag.String("repo", "", "absolute path to the git worktree")
		rev     = flag.String("rev", "", "commit (or, without -pseudo, tree) to build the zip from")
		modPath = flag.String("module", "", "root module path")
		version = flag.String("version", "", "version to publish, e.g. v5.1.0; ignored with -pseudo")
		pseudo  = flag.Bool("pseudo", false, "publish the canonical pseudo-version of -rev and print it")
		out     = flag.String("out", "", "proxy directory to write")
	)
	flag.Parse()

	if err := run(*repo, *rev, *modPath, *version, *pseudo, *out); err != nil {
		fmt.Fprintln(os.Stderr, "modproxy:", err)
		os.Exit(1)
	}
}

func run(repo, rev, modPath, version string, pseudo bool, out string) error {
	if repo == "" || rev == "" || modPath == "" || out == "" || (version == "" && !pseudo) {
		return fmt.Errorf("-repo, -rev, -module, -out and one of -version or -pseudo are required")
	}

	if pseudo {
		v, err := pseudoVersion(repo, rev, modPath)
		if err != nil {
			return err
		}
		version = v
	}

	m := module.Version{Path: modPath, Version: version}
	escPath, err := module.EscapePath(modPath)
	if err != nil {
		return err
	}
	escVersion, err := module.EscapeVersion(version)
	if err != nil {
		return err
	}

	dir := filepath.Join(out, filepath.FromSlash(escPath), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	zf, err := os.Create(filepath.Join(dir, escVersion+".zip"))
	if err != nil {
		return err
	}
	vcsRoot, err := mainWorktree(repo)
	if err != nil {
		zf.Close()
		return err
	}
	if err := modzip.CreateFromVCS(zf, m, vcsRoot, rev, ""); err != nil {
		zf.Close()
		return fmt.Errorf("create zip from %s: %w", rev, err)
	}
	if err := zf.Close(); err != nil {
		return err
	}

	gomod, err := exec.Command("git", "-C", repo, "show", rev+":go.mod").Output()
	if err != nil {
		return fmt.Errorf("read go.mod at %s: %w", rev, err)
	}
	if err := os.WriteFile(filepath.Join(dir, escVersion+".mod"), gomod, 0o644); err != nil {
		return err
	}

	info, err := json.Marshal(struct {
		Version string
		Time    time.Time
	}{version, time.Now().UTC()})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, escVersion+".info"), info, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "list"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}

	if pseudo {
		fmt.Println(version)
	}
	return nil
}

// pseudoVersion mirrors how the go command names a commit: the highest semver
// tag of the module's major version merged into the commit is the base, and
// the commit's committer time and 12-character hash follow it.
func pseudoVersion(repo, rev, modPath string) (string, error) {
	_, major, ok := module.SplitPathVersion(modPath)
	if !ok || major == "" {
		return "", fmt.Errorf("module path %q has no /vN suffix", modPath)
	}
	major = strings.TrimPrefix(major, "/")

	hash, err := git(repo, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	ct, err := git(repo, "show", "-s", "--format=%ct", hash)
	if err != nil {
		return "", err
	}
	secs, err := strconv.ParseInt(ct, 10, 64)
	if err != nil {
		return "", fmt.Errorf("committer time %q: %w", ct, err)
	}

	tags, err := git(repo, "tag", "--merged", hash, "--list", major+".*")
	if err != nil {
		return "", err
	}
	base := ""
	for _, t := range strings.Fields(tags) {
		if semver.IsValid(t) && semver.Canonical(t) == t && semver.Major(t) == major && semver.Compare(t, base) > 0 {
			base = t
		}
	}
	return module.PseudoVersion(major, base, time.Unix(secs, 0).UTC(), hash[:12]), nil
}

// mainWorktree returns the main worktree of repo's repository. CreateFromVCS
// recognizes a repository only by a .git directory, which a linked worktree
// lacks; the main worktree has one and shares the linked worktree's objects.
func mainWorktree(repo string) (string, error) {
	common, err := git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("repository at %s has no main worktree (git common dir %s)", repo, common)
	}
	return filepath.Dir(common), nil
}

func git(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
