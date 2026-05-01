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
	fs.Parse(os.Args[1:])

	if *specPath == "" {
		return fmt.Errorf("usage: osgen paths -spec <openapi-spec.yaml> [-groups group1,group2] [-pkg path] [-o builders.go] [-test-out builders_test.go]")
	}

	var filter map[string]bool
	if *groups != "" {
		filter = make(map[string]bool)
		for g := range strings.SplitSeq(*groups, ",") {
			filter[strings.TrimSpace(g)] = true
		}
	}

	return generatePaths(*specPath, filter, *pkg, *outFile, *testFile)
}

// generatePaths loads the spec, analyzes operation groups, and writes path
// builder source and optional test files. This is the testable core of the
// "paths" subcommand.
func generatePaths(specPath string, filter map[string]bool, pkg, outFile, testFile string) error {
	grouped, err := loadAndGroup(specPath, filter)
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

	out, err := render(builders, pkg, true)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, []byte(out), 0o644); err != nil {
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
		if err := os.WriteFile(testFile, []byte(testOut), 0o644); err != nil {
			return fmt.Errorf("writing %q: %w", testFile, err)
		}
	}

	return nil
}
