// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Command osapifix migrates a Go module across opensearch-go major versions.
//
// It is a generic frontend over a registry of per-adjacent-hop migration tables
// (v4->v5 today; v3->v4, v2->v3 to follow). The source major is auto-detected
// from the consumer's imports and the target defaults to the newest known
// version, so the common invocation is simply `osapifix rewrite`.
//
// A migration has two fundamentally different kinds of change, needing two
// mechanisms - so the tool exposes two subcommands:
//
//	osapifix rewrite [-src=auto] [-dst=vN] [-w] [dir]   # SYNTACTIC pass (pre-compile)
//	osapifix vet     [-fix] pkgs                        # SEMANTIC pass  (post-compile)
//
// # rewrite - the API-shape migration
//
// Across a major bump many type names, method paths, and field spellings change
// (client.Get -> client.Doc.Get, ResponseShards -> ShardStatistics, DocumentID
// -> ID, Params -> *Params, ...). Source-version code using the old shapes does
// NOT compile against the target, so a type-checking analyzer cannot even load
// it. `rewrite` is therefore purely syntactic (go/parser + astutil + go/printer):
// it edits the AST from a type-aware delta and gets the module compiling against
// the target. Run it first, then bump the dependency and build.
//
// # vet - the runtime-hazard cleanup
//
// Once the module compiles against the target, a second class of bug remains:
// the target's precise types (*int64, *string, ...) flow into `any` sinks such as
// testify's Equal/Greater, which compile clean but fail at run time with
// "Elements should be the same type". `vet` runs go/analysis analyzers (see
// typedassert.go) that catch these statically, and with -fix rewrites the safe
// cases. Because these analyzers type-check, they only work AFTER `rewrite` +
// build succeed. `vet` targets the destination version and does not chain.
//
// # Architecture
//
// One binary, many transitions applied in series. Each adjacent hop (vN ->
// vN+1) is a Hop value in its own hop_vN_to_vN1.go, registered in the hops map
// (transitions.go), built from that hop's own two surfaces (surface_vN.json,
// surface_vN+1.json) diffed under its own tables. For a src->dst request,
// planChain (plan.go) produces one self-contained plan per hop; the driver
// applies them in order, rebuilding the module against the intermediate version
// between hops so the type-aware rewriter can resolve the next hop. Hops are NOT
// folded into a single pass - each is reproducible and coherent on its own, and
// adding a future hop is a purely local change (author its tables, drop in its
// surfaces). Intermediate versions are an implementation detail; the operator
// asked for src -> dst.
//
// Field changes are governed by an explicit, hand-authored table
// (FieldDispositions), never inferred: a field that vanishes on the target is
// renamed/removed/flagged per its ruling, and a vanished field with NO ruling is
// reported as an osapifix bug (the run fails loudly) rather than silently
// dropped. See README.md.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"
)

// The committed surfaces are embedded so the tool is self-contained: a consumer
// runs the binary without needing the surface files alongside. Regenerate with
// cmd/gensurface and re-embed when bumping the pinned opensearch-go versions.
// Register each embedded surface in the surfaces map (transitions.go).
//
//go:embed surface_v2.json
var surfaceV2JSON []byte

//go:embed surface_v3.json
var surfaceV3JSON []byte

//go:embed surface_v4.json
var surfaceV4JSON []byte

//go:embed surface_v5.json
var surfaceV5JSON []byte

// analyzers is the extensible set run by the `vet` subcommand. Add new semantic
// analyzers here; each is independently testable via analysistest.
//
//nolint:gochecknoglobals // const-ish analyzer registry, immutable after init
var analyzers = []*analysis.Analyzer{
	TypedAssertAnalyzer,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "rewrite":
		runRewrite(os.Args[2:])
	case "vet":
		// Hand the remaining args to multichecker, which owns -fix, -json, and
		// per-analyzer flags exactly as `go vet` tooling expects.
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		multichecker.Main(analyzers...)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// runRewrite plans the source->target migration (auto-detecting the source from
// the consumer's imports unless -src is given) and applies each hop in series
// with full source-version type resolution. Without -w it performs a dry run,
// printing intended edits for review before committing.
func runRewrite(args []string) {
	fs := flag.NewFlagSet("rewrite", flag.ExitOnError)
	write := fs.Bool("w", false, "write changes to files (default: dry run, print intended edits)")
	srcFlag := fs.String("src", "auto", "source major version (e.g. v4) or 'auto' to detect from the consumer's imports")
	dstFlag := fs.String("dst", "", "target major version (e.g. v5); default: newest known")
	// flag.ExitOnError makes Parse terminate the process on a bad flag, so the
	// returned error is always nil here; assign to _ to satisfy errcheck.
	_ = fs.Parse(args) //nolint:errcheck // ExitOnError: Parse never returns non-nil

	dir := "."
	if fs.NArg() > 0 {
		d, err := dirFromArg(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite:", err)
			os.Exit(1)
		}
		dir = d
	}

	// Resolve the target: explicit -dst, else the newest version the registry
	// can migrate to.
	dst := newestKnownTarget()
	if *dstFlag != "" {
		m, err := parseMajor(*dstFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite: -dst:", err)
			os.Exit(2)
		}
		dst = m
	}

	// Resolve the source: explicit -src, else auto-detect from imports.
	var src Major
	if *srcFlag == "auto" {
		m, warnings, err := detectSourceMajor(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite:", err)
			os.Exit(1)
		}
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "note:", w)
		}
		src = m
		fmt.Printf("detected source v%d (target v%d)\n", src, dst)
	} else {
		m, err := parseMajor(*srcFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite: -src:", err)
			os.Exit(2)
		}
		src = m
	}

	// A no-op is a first-class, friendly outcome - auto-detection makes it common
	// (e.g. the consumer already migrated).
	if src >= dst {
		fmt.Printf("already at v%d (>= target v%d); nothing to do.\n", src, dst)
		return
	}

	plans, err := planChain(src, dst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rewrite:", err)
		os.Exit(1)
	}

	// Apply each hop in series. The rewriter is type-aware, so between hops the
	// module must actually compile against the intermediate version: after
	// writing a hop's changes we bump the dependency and build before the next
	// hop can load and resolve types. Intermediate versions are an implementation
	// detail - the operator asked for src -> dst.
	var followups []string
	for i, p := range plans {
		results, err := runTypeAwareRewrite(rewriteConfig{
			dir:            dir,
			patterns:       []string{"./..."},
			delta:          p.delta,
			renames:        p.renames,
			regroups:       p.regroups,
			removedHelpers: p.removedHelpers,
			importPrefixes: p.importPrefixes,
			write:          *write,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "rewrite (v%d -> v%d): %v\n", p.from, p.to, err)
			os.Exit(1)
		}

		if len(plans) > 1 {
			fmt.Printf("== v%d -> v%d ==\n", p.from, p.to)
		}
		for _, r := range results {
			fmt.Printf("%s\n", r.path)
			for _, e := range r.edits {
				fmt.Printf("  - %s\n", e)
			}
		}
		if !*write {
			fmt.Printf("[dry run] %d file(s) would change.\n", len(results))
		} else {
			fmt.Printf("rewrote %d file(s).\n", len(results))
		}
		followups = append(followups, p.followups...)

		// Between hops, make the module compile against the next version so the
		// next hop can type-resolve. Not needed after the final hop (the operator
		// bumps to dst and builds themselves) or in a dry run (nothing written).
		isLast := i == len(plans)-1
		if *write && !isLast {
			if err := bumpAndBuild(context.Background(), dir, p.to); err != nil {
				fmt.Fprintf(os.Stderr, "rewrite: preparing v%d before the next hop: %v\n", p.to, err)
				os.Exit(1)
			}
		}
	}

	if !*write {
		fmt.Printf("\nre-run with -w to apply.\n")
	}

	if len(followups) > 0 {
		fmt.Println("\nManual follow-ups (behavioral changes not rewritten automatically):")
		for _, m := range dedupe(followups) {
			fmt.Printf("  * %s\n", m)
		}
	}
}

// bumpAndBuild points the consumer module at the given opensearch-go major and
// builds it, so a subsequent type-aware hop can load and resolve types against
// the intermediate version. Runs in the consumer module directory.
func bumpAndBuild(ctx context.Context, dir string, to Major) error {
	// #nosec G204 -- the command and its arguments are internally constructed
	// (a fixed "go get" of a version-derived module path), not user input.
	get := exec.CommandContext(ctx, "go", "get", modulePath(to)+"@latest")
	get.Dir = dir
	if out, err := get.CombinedOutput(); err != nil {
		return fmt.Errorf("go get %s: %w\n%s", modulePath(to), err, out)
	}
	build := exec.CommandContext(ctx, "go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build after bumping to v%d: %w\n%s", to, err, out)
	}
	return nil
}

// dedupe returns s with duplicates removed, preserving first-seen order.
func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// parseMajor parses a version label like "v4", "V4", or "4" into a Major.
func parseMajor(s string) (Major, error) {
	t := strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	n, err := strconv.Atoi(t)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid version %q (want e.g. v4)", s)
	}
	return Major(n), nil
}

// dirFromArg resolves the positional argument to a module directory and
// validates it. The rewrite pass always loads "./..." within that directory, so
// a Go package pattern ("./...") is not a valid target here - but callers
// reflexively type it like they do for `go test`. When the arg is a "..."
// wildcard, strip it back to its base directory (filepath.Dir drops the trailing
// "..." element) so `rewrite ./...` means `rewrite .`. The result is cleaned and
// stat-checked so a bogus path fails with a clear message rather than a cryptic
// chdir error deep in package loading.
func dirFromArg(arg string) (string, error) {
	if strings.HasSuffix(arg, "...") {
		arg = filepath.Dir(arg)
	}
	dir := filepath.Clean(arg)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("target directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("target %q is not a directory", dir)
	}
	return dir, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `osapifix - migrate a Go module across opensearch-go major versions

Usage:
  osapifix rewrite [-src=auto] [-dst=vN] [-w] [dir]
        Syntactic API-shape migration (run first, pre-compile). Dry run by
        default; -w writes changes. Source auto-detected from imports; target
        defaults to the newest known version.
  osapifix vet [-fix] pkgs...
        Semantic runtime-hazard analyzers (run after build). -fix applies safe
        suggested fixes. Targets the destination version; does not chain.

Typical flow (v4 -> v5):
  osapifix rewrite -w ./...
  go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
  osapifix vet -fix ./...
`)
}
