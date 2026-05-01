// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

	if err := removeGenFiles(outDir); err != nil {
		return err
	}
	if pluginsDir != "" {
		if err := removeGenFiles(pluginsDir); err != nil {
			return err
		}
	}

	for _, op := range ops {
		pkg, dir := routeOperation(op.Group, outDir, pluginsDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %q: %w", dir, err)
		}

		basename := operationFilename(op.Group)

		apiFile := filepath.Join(dir, basename+genFileSuffix)
		apiSrc, err := renderAPIFile(op, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %q: %v\n", op.Group, err)
			continue
		}
		if err := os.WriteFile(apiFile, []byte(apiSrc), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", apiFile, err)
		}

		paramsFile := filepath.Join(dir, basename+"-params"+genFileSuffix)
		paramsSrc, err := renderParamsFile(op, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render params %q: %v\n", op.Group, err)
			continue
		}
		if err := os.WriteFile(paramsFile, []byte(paramsSrc), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", paramsFile, err)
		}

		fmt.Fprintf(os.Stderr, "  %q -> %q\n", op.Group, apiFile)
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
		compatFile := filepath.Join(dir, "compat"+genFileSuffix)
		src, err := renderCompatFile(pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render compat %q: %v\n", pkg, err)
			continue
		}
		if err := os.WriteFile(compatFile, []byte(src), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", compatFile, err)
		}
	}

	fmt.Fprintf(os.Stderr, "generated %d operations\n", len(ops))
	return nil
}

// removeGenFiles removes all *_gen.go files under root, recursively.
// It cleans and resolves the path, verifies the target is inside the git
// working tree, and uses os.OpenRoot to confine removal to the directory.
func removeGenFiles(root string) error {
	abs, err := resolveGenRoot(root)
	if err != nil {
		return err
	}

	dir, err := os.OpenRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening root %q: %w", abs, err)
	}
	defer dir.Close()

	return fs.WalkDir(dir.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), genFileSuffix) {
			if err := dir.Remove(path); err != nil {
				return fmt.Errorf("removing stale %q: %w", path, err)
			}
		}
		return nil
	})
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
// it is a subdirectory of the git working tree. Returns an error if the path
// is the filesystem root, the user's home directory, or outside the repo.
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

	gitTop, err := repoRoot()
	if err != nil {
		return "", err
	}
	if abs != gitTop && !strings.HasPrefix(abs, gitTop+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to operate on %q: outside git root %q", abs, gitTop)
	}

	return abs, nil
}
