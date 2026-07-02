// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Command gensurface extracts the exported-struct surface of one or more
// opensearch-go packages (at a specific module checkout) to a committed JSON
// file.
//
// It is run once per version. Because each opensearch-go major version is its
// own module, resolution is done by pointing -dir at that module's root and
// loading the relevant package patterns within it. Multiple patterns are joined
// with commas so the surface spans every package a consumer might touch
// (opensearchapi, opensearch, opensearchtransport) - the same field name fans in
// across packages (EnableMetrics on opensearch.Config AND
// opensearchtransport.Config), so the surface must cover all of them:
//
//	go run ./cmd/gensurface -dir <module-dir> -version v5 \
//	    -patterns ./opensearchapi,.,./opensearchtransport -out surface_v5.json
//
// The resulting JSON is committed so the delta the rewriter uses is auditable in
// review, not recomputed opaquely at run time.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
)

func main() {
	var (
		dir      = flag.String("dir", ".", "module directory to load packages from")
		patterns = flag.String("patterns", "./opensearchapi,.,./opensearchtransport", "comma-separated go/packages load patterns")
		version  = flag.String("version", "", "version label recorded for provenance (v4 or v5)")
		out      = flag.String("out", "", "output JSON path (default: stdout)")
	)
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "gensurface: -version is required")
		os.Exit(2)
	}

	pats := strings.Split(*patterns, ",")
	snap, err := apirev.ExtractFromDir(*dir, *version, pats...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gensurface:", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gensurface:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if *out == "" {
		os.Stdout.Write(data)
		return
	}
	if err := writeThroughRoot(*out, data); err != nil {
		fmt.Fprintln(os.Stderr, "gensurface:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %d structs to %s\n", len(snap.Structs), *out)
}

// writeThroughRoot writes data to out via an os.Root anchored at out's
// directory, so the write is confined there and cannot escape via symlinks or
// "..". It is a separate function so the root is closed before any os.Exit.
func writeThroughRoot(out string, data []byte) error {
	outDir, outName := filepath.Split(out)
	if outDir == "" {
		outDir = "."
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(outName, data, 0o644)
}
