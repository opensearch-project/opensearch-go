// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// analyzers is the extensible set run by the `vet` subcommand. Add new semantic
// analyzers here; each is independently testable via analysistest.
//
//nolint:gochecknoglobals // const-ish analyzer registry, immutable after init
var analyzers = []*analysis.Analyzer{
	typedAssertAnalyzer,
}

// Analyzers returns the semantic analyzers run by the `vet` subcommand.
func Analyzers() []*analysis.Analyzer { return analyzers }

// Rewrite plans the source->target migration (auto-detecting the source from the
// consumer's imports unless -src is given) and applies each hop in series with
// full source-version type resolution. Without -w it performs a dry run,
// printing intended edits for review before committing. All operational failures
// are returned; the caller owns the process exit code.
func Rewrite(args []string) error {
	fs := flag.NewFlagSet("rewrite", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	write := fs.Bool("w", false, "write changes to files (default: dry run, print intended edits)")
	srcFlag := fs.String("src", "auto", "source major version (e.g. v4) or 'auto' to detect from the consumer's imports")
	dstFlag := fs.String("dst", "", "target major version (e.g. v5); default: newest known")
	if err := fs.Parse(args); err != nil {
		// flag already printed usage (to os.Stderr, set above); -h/-help is
		// not an operational error, so exit cleanly rather than as a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	dir := "."
	if fs.NArg() > 0 {
		d, err := dirFromArg(fs.Arg(0))
		if err != nil {
			return err
		}
		dir = d
	}

	dst := newestKnownTarget()
	if *dstFlag != "" {
		m, err := parseMajor(*dstFlag)
		if err != nil {
			return fmt.Errorf("-dst: %w", err)
		}
		dst = m
	}

	var src major
	if *srcFlag == "auto" {
		m, warnings, err := detectSourceMajor(dir)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "note:", w)
		}
		src = m
		fmt.Printf("detected source v%d (target v%d)\n", src, dst)
	} else {
		m, err := parseMajor(*srcFlag)
		if err != nil {
			return fmt.Errorf("-src: %w", err)
		}
		src = m
	}

	if src >= dst {
		fmt.Printf("already at v%d (>= target v%d); nothing to do.\n", src, dst)
		return nil
	}

	plans, err := planChain(src, dst)
	if err != nil {
		return err
	}

	var followups []string
	for i, p := range plans {
		results, err := runTypeAwareRewrite(rewriteConfig{
			dir:            dir,
			patterns:       []string{patternAll},
			delta:          p.delta,
			renames:        p.renames,
			regroups:       p.regroups,
			removedHelpers: p.removedHelpers,
			importPrefixes: p.importPrefixes,
			write:          *write,
		})
		if err != nil {
			return fmt.Errorf("v%d -> v%d: %w", p.from, p.to, err)
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

		isLast := i == len(plans)-1
		if *write && !isLast {
			if err := bumpAndBuild(context.Background(), dir, p.to); err != nil {
				return fmt.Errorf("preparing v%d before the next hop: %w", p.to, err)
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
	return nil
}

// bumpAndBuild points the consumer module at the given opensearch-go major and
// builds it, so a subsequent type-aware hop can load and resolve types against
// the intermediate version. Runs in the consumer module directory.
func bumpAndBuild(ctx context.Context, dir string, to major) error {
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

// parseMajor parses a version label like "v4", "V4", or "4" into a major.
func parseMajor(s string) (major, error) {
	t := strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	n, err := strconv.Atoi(t)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid version %q (want e.g. v4)", s)
	}
	return major(n), nil
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

// Usage writes the command usage text to stderr.
func Usage() {
	fmt.Fprint(os.Stderr, `osapilint - migrate a Go module across opensearch-go major versions

Usage:
  osapilint rewrite [-src=auto] [-dst=vN] [-w] [dir]
        Syntactic API-shape migration (run first, pre-compile). Dry run by
        default; -w writes changes. Source auto-detected from imports; target
        defaults to the newest known version.
  osapilint vet [-fix] pkgs...
        Semantic runtime-hazard analyzers (run after build). -fix applies safe
        suggested fixes. Targets the destination version; does not chain.

Typical flow (v4 -> v5):
  osapilint rewrite -w ./...
  go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
  osapilint vet -fix ./...
`)
}
