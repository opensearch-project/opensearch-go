// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/renameio/v2/maybe"
)

// runPaths implements the "osgen paths" subcommand. It parses flags and
// delegates to generatePaths for the actual work.
func runPaths() error {
	fs := flag.NewFlagSet("paths", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to OpenAPI spec YAML (single combined file)")
	groups := fs.String("groups", "", "comma-separated x-operation-group names (empty = all)")
	pkg := fs.String("pkg", "path", "output package name")
	outFile := fs.String("o", "", "output file for builders (default: stdout)")
	testFile := fs.String("test-out", "", "output file for tests (empty = no tests)")
	minVer := fs.String("min-version", versionEpoch, "minimum OpenSearch version (default operator: >=)")
	maxVer := fs.String("max-version", versionLatest, "maximum OpenSearch version (default operator: <=)")
	removeDepr := fs.String("remove-deprecated", versionEpoch,
		"treat operations deprecated at or before this version as removed (default: epoch, meaning keep all)")
	preserveOpt := fs.Bool("min-version-preserve-optional", false,
		"keep version-gated fields as pointers even when min-version guarantees their presence")
	bcOpsFlag := fs.String("version-breadcrumb-operations", breadcrumbModeAll, "emit comments for excluded operations: all, older, newer")
	bcTypesFlag := fs.String("version-breadcrumb-types", breadcrumbModeAll, "emit comments for excluded types: all, older, newer")
	bcFieldsFlag := fs.String("version-breadcrumb-fields", breadcrumbModeAll, "emit comments for excluded struct fields: all, older, newer")
	bcPathsFlag := fs.String("version-breadcrumb-paths", breadcrumbModeAll, "emit comments for excluded path builders: all, older, newer")
	bcParamsFlag := fs.String("version-breadcrumb-params", breadcrumbModeAll, "emit comments for excluded query parameters: all, older, newer")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *specPath == "" {
		return fmt.Errorf(
			"usage: osgen paths -spec <openapi-spec.yaml> " +
				"[-groups group1,group2] [-pkg path] [-o builders.go] [-test-out builders_test.go]")
	}

	var filter map[string]bool
	if *groups != "" {
		filter = make(map[string]bool)
		for g := range strings.SplitSeq(*groups, ",") {
			filter[strings.TrimSpace(g)] = true
		}
	}

	vrange, err := ParseVersionRange(*minVer, *maxVer, *removeDepr, *preserveOpt)
	if err != nil {
		return err
	}

	bc, err := parseBreadcrumbFlags(*bcOpsFlag, *bcTypesFlag, *bcFieldsFlag, *bcPathsFlag, *bcParamsFlag)
	if err != nil {
		return err
	}

	return generatePaths(*specPath, filter, *pkg, *outFile, *testFile, vrange, bc)
}

// generatePaths loads the spec, analyzes operation groups, and writes path
// builder source and optional test files. This is the testable core of the
// "paths" subcommand.
func generatePaths(
	specPath string,
	filter map[string]bool,
	pkg, outFile, testFile string,
	vrange VersionRange,
	bc BreadcrumbConfig,
) error {
	grouped, exclusions, err := loadAndGroup(specPath, filter, vrange)
	if err != nil {
		return err
	}

	builders := make([]builder, 0, len(grouped))
	for _, g := range grouped {
		b, err := analyzeGroup(g)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %q: %v\n", g.name, err)
			continue
		}
		b.export()
		builders = append(builders, b)
	}

	var breadcrumbs []string
	seen := make(map[string]bool)
	for i := range exclusions {
		if bc.Paths.ShouldBreadcrumb(&exclusions[i]) {
			msg := exclusions[i].Name + "Path: " + exclusions[i].Reason + "."
			if !seen[msg] {
				seen[msg] = true
				breadcrumbs = append(breadcrumbs, msg)
			}
		}
	}

	out, err := render(builders, pkg, true, breadcrumbs)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if outFile != "" {
		if err := maybe.WriteFile(outFile, []byte(out), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", outFile, err)
		}
	} else {
		fmt.Print(out)
	}

	if testFile != "" {
		testOut, err := generateTests(builders, pkg)
		if err != nil {
			return fmt.Errorf("test generation: %w", err)
		}
		if err := maybe.WriteFile(testFile, []byte(testOut), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", testFile, err)
		}
	}

	return nil
}
