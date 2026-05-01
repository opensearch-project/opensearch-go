// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/renameio/v2"
)

// runAPI implements the "osgen api" subcommand. It parses flags and delegates
// to generateAPI for the actual work.
func runAPI() error {
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to OpenAPI spec YAML (single combined file)")
	groups := fs.String("groups", "", "comma-separated x-operation-group names (empty = all)")
	outDir := fs.String("out", "", "output directory for core API files (opensearchapi/)")
	pluginsDir := fs.String("plugins-out", "", "output directory for plugin files (plugins/)")
	fs.Parse(os.Args[1:])

	if *specPath == "" || *outDir == "" {
		return fmt.Errorf("usage: osgen api -spec <openapi-spec.yaml> -out <opensearchapi/> -plugins-out <plugins/>")
	}

	var filter map[string]bool
	if *groups != "" {
		filter = make(map[string]bool)
		for g := range strings.SplitSeq(*groups, ",") {
			filter[strings.TrimSpace(g)] = true
		}
	}

	return generateAPI(*specPath, filter, *outDir, *pluginsDir)
}

// generateAPI extracts operations from the spec and writes Req/Params/Resp
// files. This is the testable core of the "api" subcommand.
func generateAPI(specPath string, filter map[string]bool, outDir, pluginsDir string) error {
	ops, err := extractOperations(specPath, filter)
	if err != nil {
		return err
	}

	// Track every file we write so we can remove stale ones afterward.
	written := make(map[string]struct{})

	var wrote int
	for _, op := range ops {
		pkg, dir := routeOperation(op.Group, outDir, pluginsDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %q: %w", dir, err)
		}

		basename := operationFilename(op.Group)

		apiFile, err := filepath.Abs(filepath.Join(dir, basename+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		apiSrc, err := renderAPIFile(op, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %q: %v\n", op.Group, err)
			continue
		}
		if changed, werr := writeIfChanged(apiFile, []byte(apiSrc)); werr != nil {
			return werr
		} else if changed {
			fmt.Fprintf(os.Stderr, "  %q -> %s\n", op.Group, repoRelPath(apiFile))
			wrote++
		}
		written[apiFile] = struct{}{}
	}

	// Generate compat.go for each plugin package.
	pluginPkgs := make(map[string]string)
	for _, op := range ops {
		pkg, dir := routeOperation(op.Group, outDir, pluginsDir)
		if pkg != opensearchAPIPkgName {
			pluginPkgs[pkg] = dir
		}
	}
	for pkg, dir := range pluginPkgs {
		compatFile, err := filepath.Abs(filepath.Join(dir, "compat"+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		src, err := renderCompatFile(pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render compat %q: %v\n", pkg, err)
			continue
		}
		if changed, werr := writeIfChanged(compatFile, []byte(src)); werr != nil {
			return fmt.Errorf("writing %q: %w", compatFile, werr)
		} else if changed {
			fmt.Fprintf(os.Stderr, "  compat -> %s\n", repoRelPath(compatFile))
			wrote++
		}
		written[compatFile] = struct{}{}
	}

	// Remove stale _gen.go files not produced by this run.
	removed, err := removeStaleGenFiles(outDir, written)
	if err != nil {
		return err
	}
	if pluginsDir != "" {
		n, err := removeStaleGenFiles(pluginsDir, written)
		if err != nil {
			return err
		}
		removed += n
	}

	fmt.Fprintf(os.Stderr, "generated %d operations (%d files written, %d stale removed)\n", len(ops), wrote, removed)
	return nil
}

// removeStaleGenFiles removes *_gen.go files under root that are not in
// the written set. It resolves root, verifies it is inside the git working
// tree, and uses os.OpenRoot to confine removal.
func removeStaleGenFiles(root string, written map[string]struct{}) (int, error) {
	abs, err := resolveGenRoot(root)
	if err != nil {
		return 0, err
	}

	dir, err := os.OpenRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("opening root %q: %w", abs, err)
	}
	defer dir.Close()

	var removed int
	err = fs.WalkDir(dir.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), genFileSuffix) {
			return nil
		}
		full := filepath.Join(abs, path)
		if _, ok := written[full]; ok {
			return nil
		}
		if err := dir.Remove(path); err != nil {
			return fmt.Errorf("removing stale %q: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "  removed stale %s\n", repoRelPath(full))
		removed++
		return nil
	})
	return removed, err
}

var (
	gitTopOnce  sync.Once
	gitTopValue string
	gitTopErr   error
)

// repoRoot returns the absolute path of the git working tree root,
// cached for the lifetime of the process. Tests may override
// repoRootFunc to bypass the git check.
var repoRootFunc = repoRootGit

func repoRoot() (string, error) {
	return repoRootFunc()
}

func repoRootGit() (string, error) {
	gitTopOnce.Do(func() {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			gitTopErr = fmt.Errorf("not inside a git repository: %w", err)
			return
		}
		gitTopValue = strings.TrimSpace(string(out))
	})
	return gitTopValue, gitTopErr
}

// resolveGenRoot cleans and resolves root to an absolute path, then verifies
// it is a subdirectory of the git working tree (unless OSGEN_SKIP_GIT_CHECK
// is set). Returns an error if the path is the filesystem root, the user's
// home directory, or outside the repo.
func resolveGenRoot(root string) (string, error) {
	cleaned := filepath.Clean(root)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", root, err)
	}

	if abs == "/" || abs == filepath.Dir(abs) {
		return "", fmt.Errorf("refusing to operate on filesystem root %q", abs)
	}
	if home, err := os.UserHomeDir(); err == nil && abs == home {
		return "", fmt.Errorf("refusing to operate on home directory %q", abs)
	}

	if !skipGitCheck() {
		gitTop, err := repoRoot()
		if err != nil {
			return "", fmt.Errorf("%w (set %s=1 to bypass)", err, envSkipGitCheck)
		}
		if abs != gitTop && !strings.HasPrefix(abs, gitTop+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing to operate on %q: outside git root %q (set %s=1 to bypass)", abs, gitTop, envSkipGitCheck)
		}
	}

	return abs, nil
}

// skipGitCheck returns true when envSkipGitCheck is set to a truthy value
// (per strconv.ParseBool: 1, t, true, yes, on).
func skipGitCheck() bool {
	v, _ := strconv.ParseBool(os.Getenv(envSkipGitCheck))
	return v
}

// writeIfChanged compares data against the existing file at path. If the
// content differs (or the file does not exist), it atomically writes data
// using renameio and returns true. If the content is identical, it returns
// false without touching the file.
func writeIfChanged(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	if err := renameio.WriteFile(path, data, 0o644); err != nil {
		return false, fmt.Errorf("writing %q: %w", path, err)
	}
	return true, nil
}

// repoRelPath returns path relative to the git working tree root for
// display purposes. Falls back to the absolute path if the root is
// unavailable or the path is not under it.
func repoRelPath(abs string) string {
	root, err := repoRoot()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}
