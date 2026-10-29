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

// UsageError is an invocation error - a bad flag or a bad -src/-dst value - as
// distinct from an operational failure. The CLI maps it to exit code 2 (the flag
// convention for a malformed command line) and reserves exit 1 for migration
// failures. Msg is the text the caller should print; it is empty when the flag
// package has already written the error and usage itself (parse failures), so the
// caller must not print it a second time.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return "invalid command line"
}

// Rewrite plans the source->target migration (auto-detecting the source from the
// consumer's imports unless -src is given) and applies each hop in series with
// full source-version type resolution. Without -w it performs a dry run,
// printing intended edits for review before committing. Failures are returned as
// errors: invocation errors carry a *UsageError (exit 2), operational failures
// map to exit 1. The caller owns the process exit code.
func Rewrite(args []string) error {
	fs := flag.NewFlagSet("rewrite", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	write := fs.Bool("w", false, "write changes to files (default: dry run, print intended edits)")
	srcFlag := fs.String("src", "auto", "source version (e.g. v4 or v5.1) or 'auto' to detect from the consumer's imports and go.mod")
	dstFlag := fs.String("dst", "", "target version (e.g. v5 or v5.2; a bare major means its newest release line); default: newest known")
	if err := fs.Parse(args); err != nil {
		// -h/-help is not an error; exit cleanly. For any other parse failure the
		// flag package already wrote the message and usage to os.Stderr (set
		// above), so return a message-less UsageError: exit 2, do not reprint.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &UsageError{}
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
		v, err := parseLine(*dstFlag, newestLineOf)
		if err != nil {
			return &UsageError{Msg: "-dst: " + err.Error()}
		}
		dst = v
	}

	var src version
	var warnings []string
	if *srcFlag == "auto" {
		v, w, err := detectSource(context.Background(), dir)
		if err != nil {
			return err
		}
		for _, n := range w {
			fmt.Fprintln(os.Stderr, "note:", n)
		}
		src, warnings = v, w
		fmt.Printf("detected source %s (target %s)\n", src, dst)
	} else {
		v, err := parseLine(*srcFlag, func(m major) (version, bool) { return lineOf(m, 0) })
		if err != nil {
			return &UsageError{Msg: "-src: " + err.Error()}
		}
		src = v
	}

	if !src.less(dst) {
		fmt.Printf("already at %s (>= target %s); nothing to do.\n", src, dst)
		return nil
	}

	// The engine (planning, per-hop type-aware rewrite, between-hop rebuild) is
	// shared with MigrateSDK; Rewrite is the CLI shell over it. src/dst are already
	// resolved above, so pass them explicitly rather than re-detecting.
	results, err := migrate(context.Background(), dir, src, dst, *write, warnings)
	if err != nil {
		return err
	}

	var followups []string
	for _, r := range results {
		if len(results) > 1 {
			fmt.Printf("== %s -> %s ==\n", r.FromVersion, r.ToVersion)
		}
		for _, f := range r.Files {
			fmt.Printf("%s\n", f.Path)
			for _, e := range f.Edits {
				fmt.Printf("  - %s\n", e)
			}
		}
		if !*write {
			fmt.Printf("[dry run] %d file(s) would change.\n", len(r.Files))
		} else {
			fmt.Printf("rewrote %d file(s).\n", len(r.Files))
		}
		followups = append(followups, r.Followups...)
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

// bumpAndBuild points the consumer module at the newest release on line to and
// builds it, so a subsequent type-aware hop can load and resolve types against
// the intermediate version. Runs in the consumer module directory.
func bumpAndBuild(ctx context.Context, dir string, to version) error {
	// The newest release on the line: below the next line of the same major, or
	// the major's latest when to is its newest line.
	query := "latest"
	if next, ok := nextLineOf(to); ok {
		query = fmt.Sprintf("<v%d.%d.0", next.major, next.minor)
	}
	target := modulePath(to.major) + "@" + query
	// #nosec G204 -- the command and its arguments are internally constructed
	// (a fixed "go get" of a version-derived module query), not user input.
	get := exec.CommandContext(ctx, "go", "get", target)
	get.Dir = dir
	if out, err := get.CombinedOutput(); err != nil {
		return fmt.Errorf("go get %s: %w\n%s", target, err, out)
	}
	build := exec.CommandContext(ctx, "go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build after bumping to %s: %w\n%s", to, err, out)
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

// parseLine parses a version label like "v4", "4", "v5.1" or "V5.2" into the
// release line it names. A label with a minor names the line that release is on;
// a bare major is resolved by forMajor.
func parseLine(s string, forMajor func(major) (version, bool)) (version, error) {
	t := strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	majorPart, minorPart, hasMinor := strings.Cut(t, ".")
	n, err := strconv.Atoi(majorPart)
	if err != nil || n < 1 {
		return version{}, fmt.Errorf("invalid version %q (want e.g. v4 or v5.2)", s)
	}
	m := major(n)

	var v version
	var ok bool
	if hasMinor {
		minor, err := strconv.Atoi(minorPart)
		if err != nil || minor < 0 {
			return version{}, fmt.Errorf("invalid version %q (want e.g. v4 or v5.2)", s)
		}
		v, ok = lineOf(m, minor)
	} else {
		v, ok = forMajor(m)
	}
	if !ok {
		return version{}, fmt.Errorf("no embedded surface for %s", s)
	}
	return v, nil
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
	fmt.Fprint(os.Stderr, `osapilint - migrate a Go module across opensearch-go versions

Usage:
  osapilint rewrite [-src=auto] [-dst=vN[.M]] [-w] [dir]
        Syntactic API-shape migration (run first, pre-compile). Dry run by
        default; -w writes changes. Source auto-detected from imports and the
        required module version; target defaults to the newest known version.
  osapilint vet [-fix] pkgs...
        Semantic runtime-hazard analyzers (run after build). -fix applies safe
        suggested fixes. Targets the destination version; does not chain.

Typical flow (v4 -> v5):
  osapilint rewrite -w ./...
  go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
  osapilint vet -fix ./...
`)
}
