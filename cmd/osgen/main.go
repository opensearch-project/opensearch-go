// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// osgen reads the OpenSearch API specification and generates typed path
// builder structs grouped by x-operation-group.
//
// It uses kin-openapi to parse the spec and resolve $refs, then groups
// operations by x-operation-group and emits builder code.
//
// Usage:
//
//	go run ./cmd/osgen -spec /path/to/opensearch-openapi.yaml
//	go run ./cmd/osgen -spec /path/to/opensearch-openapi.yaml -groups indices.get_alias,search
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	specPath := flag.String("spec", "", "path to OpenAPI spec YAML (single combined file)")
	groups := flag.String("groups", "", "comma-separated x-operation-group names (empty = all)")
	pkg := flag.String("pkg", "path", "output package name")
	outFile := flag.String("o", "", "output file for builders (default: stdout)")
	testFile := flag.String("test-out", "", "output file for tests (empty = no tests)")
	flag.Parse()

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "usage: osgen -spec <openapi-spec.yaml> [-groups group1,group2] [-pkg path] [-o builders.go] [-test-out builders_test.go]")
		os.Exit(1)
	}

	var filter map[string]bool
	if *groups != "" {
		filter = make(map[string]bool)
		for g := range strings.SplitSeq(*groups, ",") {
			filter[strings.TrimSpace(g)] = true
		}
	}

	grouped, err := loadAndGroup(*specPath, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	builders := make([]builder, 0, len(grouped))
	for _, g := range grouped {
		b, err := analyzeGroup(g)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", g.name, err)
			continue
		}
		b.export()
		builders = append(builders, b)
	}

	out, err := render(builders, *pkg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		os.Exit(1)
	}

	if *outFile != "" {
		if err := os.WriteFile(*outFile, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "writing %s: %v\n", *outFile, err)
			os.Exit(1)
		}
	} else {
		fmt.Print(out)
	}

	if *testFile != "" {
		testOut, err := generateTests(builders, *pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "test generation error: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*testFile, []byte(testOut), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "writing %s: %v\n", *testFile, err)
			os.Exit(1)
		}
	}
}
