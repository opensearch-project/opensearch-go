// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// detect.go determines the source opensearch-go major version from the
// consumer's actual .go imports - not from go.mod. The import path carries the
// major (.../opensearch-go/v4/... vs .../v5/...), which is the ground truth of
// what the code uses. go.mod is the wrong source: mid-migration a module can
// require both majors simultaneously (different module path per major, both
// legal), and go.mod may already name the target while every call site is still
// source-shaped.
//
// The minor has no such ambiguity: one major's module is required at one
// version, so detectSource reads it from the module graph once the major is
// known.

// importMajorRe extracts the major version from an opensearch-go import path.
// Group 1 is the "N" of "/vN" when present; absent means v1 (no suffix, per Go
// module major-version rules).
var importMajorRe = regexp.MustCompile(`^` + regexp.QuoteMeta(openSearchGoBase) + `(?:/v([0-9]+))?(?:/|$)`)

// moduleMinorRe extracts the minor from a module version such as v5.1.3 or a
// pseudo-version built on it.
var moduleMinorRe = regexp.MustCompile(`^v[0-9]+\.([0-9]+)\.`)

// detectSource returns the release line the consumer under dir is on: the major
// from its imports (detectSourceMajor), and the minor of that major's module as
// the module graph requires it. When the minor cannot be read, the major's
// first line is assumed and a warning says so: applying a hop the module already
// passed is safer than skipping one it needs.
func detectSource(ctx context.Context, dir string) (version, []string, error) {
	m, warnings, err := detectSourceMajor(dir)
	if err != nil {
		return version{}, nil, err
	}

	minor, err := requiredMinor(ctx, dir, m)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("cannot read the required v%d version (%v); assuming its first release line", m, err))
		minor = 0
	}
	v, ok := lineOf(m, minor)
	if !ok {
		return version{}, nil, fmt.Errorf("no embedded surface for v%d", m)
	}
	return v, warnings, nil
}

// requiredMinor returns the minor of the opensearch-go module for major m that
// the module under dir requires.
func requiredMinor(ctx context.Context, dir string, m major) (int, error) {
	// #nosec G204 -- the command and its arguments are internally constructed
	// (a fixed "go list -m" of a version-derived module path), not user input.
	list := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Version}}", modulePath(m))
	list.Dir = dir
	out, err := list.Output()
	if err != nil {
		return 0, fmt.Errorf("go list -m %s: %w", modulePath(m), err)
	}
	ver := strings.TrimSpace(string(out))
	sub := moduleMinorRe.FindStringSubmatch(ver)
	if sub == nil {
		return 0, fmt.Errorf("unrecognized version %q", ver)
	}
	return strconv.Atoi(sub[1])
}

// detectSourceMajor loads the consumer packages under dir (imports only, no
// type-checking) and returns the opensearch-go major they import. When multiple
// majors are present (a partially migrated module), the lowest is chosen as the
// source and the others are reported for visibility.
func detectSourceMajor(dir string) (major, []string, error) {
	cfg := &packages.Config{
		Dir:   dir,
		Mode:  packages.NeedName | packages.NeedImports,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return 0, nil, fmt.Errorf("load packages under %s: %w", dir, err)
	}

	found := map[major]bool{}
	for _, pkg := range pkgs {
		for importPath := range pkg.Imports {
			if m, ok := majorOfImport(importPath); ok {
				found[m] = true
			}
		}
	}

	if len(found) == 0 {
		return 0, nil, fmt.Errorf("no opensearch-go imports found under %s (nothing to migrate)", dir)
	}

	majors := make([]major, 0, len(found))
	for m := range found {
		majors = append(majors, m)
	}
	slices.Sort(majors)

	src := majors[0]
	var warnings []string
	if len(majors) > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"consumer imports multiple opensearch-go majors %v; migrating from the lowest (v%d)", majorList(majors), src))
	}
	return src, warnings, nil
}

// majorOfImport reports the opensearch-go major an import path belongs to, and
// whether the path is an opensearch-go import at all.
func majorOfImport(importPath string) (major, bool) {
	m := importMajorRe.FindStringSubmatch(importPath)
	if m == nil {
		return 0, false
	}
	if m[1] == "" {
		return 1, true // no "/vN" segment => v1
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return major(n), true
}

// majorList formats majors as v-prefixed labels for messages.
func majorList(majors []major) []string {
	out := make([]string, len(majors))
	for i, m := range majors {
		out[i] = fmt.Sprintf("v%d", m)
	}
	return out
}
