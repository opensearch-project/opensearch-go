// Command osupgrade-v4-to-v5 migrates a Go module from opensearch-go v4 to v5.
//
// The v4 -> v5 jump has two fundamentally different kinds of change, which need
// two fundamentally different mechanisms — so the tool exposes two subcommands:
//
//	osupgrade-v4-to-v5 rewrite [-w] [dir]   # SYNTACTIC pass (pre-compile)
//	osupgrade-v4-to-v5 vet     [-fix] pkgs  # SEMANTIC pass  (post-compile)
//
// # rewrite — the API-shape migration
//
// v5's opensearchapi is generated from the OpenAPI spec, so many v4 type names,
// method paths, and field spellings changed (client.Get -> client.Doc.Get,
// ResponseShards -> ShardStatistics, DocumentID -> ID, Params -> *Params, …).
// v4 code using the old shapes does NOT compile against v5, so a type-checking
// analyzer cannot even load it. `rewrite` is therefore purely syntactic
// (go/parser + astutil + go/printer): it edits the AST from a declarative rule
// table (see rules_v4_to_v5.go) and gets the module compiling against v5. Run it
// first, then `go get github.com/opensearch-project/opensearch-go/v5 && go build`.
//
// # vet — the runtime-hazard cleanup
//
// Once the module compiles against v5, a second class of bug remains: v5's
// precise types (*int64, *string, …) flow into `any` sinks such as testify's
// Equal/Greater, which compile clean but fail at run time with "Elements should
// be the same type". `vet` runs go/analysis analyzers (see typedassert.go) that
// catch these statically, and with -fix rewrites the safe cases. Because these
// analyzers type-check, they only work AFTER `rewrite` + `go build` succeed.
//
// Semantic behavior changes that cannot be mechanically rewritten (errmask
// default flip, Router injection, EnableMetrics removal) are reported as
// advisories by `rewrite`; see reportOnly in rules_v4_to_v5.go.
//
// Each major transition gets its own tool (osupgrade-v5-to-v6, …) so the rule
// tables stay migration-specific instead of accreting version flags.
package main

import (
	"encoding/json"
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osupgrade-v4-to-v5/internal/surface"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"
)

// The committed surfaces are embedded so the tool is self-contained: a consumer
// runs the binary without needing the surface files alongside. Regenerate with
// cmd/gensurface and re-embed when bumping the pinned opensearch-go versions.
//
//go:embed surface_v4.json
var surfaceV4JSON []byte

//go:embed surface_v5.json
var surfaceV5JSON []byte

// analyzers is the extensible set run by the `vet` subcommand. Add new semantic
// analyzers here; each is independently testable via analysistest.
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

// semanticFollowups are v4->v5 changes that cannot be mechanically rewritten
// (behavioral, not shape). The rewrite subcommand prints them so the operator
// knows the automated rewrite is necessary but not sufficient.
var semanticFollowups = []string{
	"errmask default flipped: Config.Errors == nil now reports every partial-failure category (v4 masked all).",
	"opensearchapi.NewClient now injects a default Router when Config.Client.Router is nil — verify OPENSEARCH_GO_ROUTER expectations.",
	"EnableMetrics removed: Metrics() no longer errors when disabled — drop any code that branched on that error.",
	"Timeout/Pretty/Human/ErrorTrace moved into embedded TimeoutParams/DebugParams — restructure those assignments by hand.",
}

// runRewrite loads the committed v4/v5 surfaces, derives the delta, and applies
// it to the consumer module with full v4 type resolution. Without -w it performs
// a dry run, printing intended edits for review before committing.
func runRewrite(args []string) {
	fs := flag.NewFlagSet("rewrite", flag.ExitOnError)
	write := fs.Bool("w", false, "write changes to files (default: dry run, print intended edits)")
	_ = fs.Parse(args)

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	v4, v5, err := loadSurfaces()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rewrite:", err)
		os.Exit(1)
	}
	delta := surface.DeriveDelta(v4, v5, typeRenamesV4toV5)

	results, err := runTypeAwareRewrite(rewriteConfig{
		dir:      dir,
		patterns: []string{"./..."},
		delta:    delta,
		renames:  typeRenamesV4toV5,
		write:    *write,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rewrite:", err)
		os.Exit(1)
	}

	for _, r := range results {
		fmt.Printf("%s\n", r.path)
		for _, e := range r.edits {
			fmt.Printf("  - %s\n", e)
		}
	}

	if !*write {
		fmt.Printf("\n[dry run] %d file(s) would change; re-run with -w to apply.\n", len(results))
	} else {
		fmt.Printf("\nrewrote %d file(s).\n", len(results))
	}

	fmt.Println("\nManual follow-ups (behavioral changes not rewritten automatically):")
	for _, m := range semanticFollowups {
		fmt.Printf("  * %s\n", m)
	}
}

// loadSurfaces decodes the embedded committed surfaces.
func loadSurfaces() (*surface.Snapshot, *surface.Snapshot, error) {
	var v4, v5 surface.Snapshot
	if err := json.Unmarshal(surfaceV4JSON, &v4); err != nil {
		return nil, nil, fmt.Errorf("decode surface_v4.json: %w", err)
	}
	if err := json.Unmarshal(surfaceV5JSON, &v5); err != nil {
		return nil, nil, fmt.Errorf("decode surface_v5.json: %w", err)
	}
	return &v4, &v5, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `osupgrade-v4-to-v5 — migrate a Go module from opensearch-go v4 to v5

Usage:
  osupgrade-v4-to-v5 rewrite [-w] [dir]   Syntactic API-shape migration (run first, pre-compile).
                                          Dry run by default; -w writes changes.
  osupgrade-v4-to-v5 vet [-fix] pkgs...   Semantic runtime-hazard analyzers (run after go build).
                                          -fix applies safe suggested fixes.

Typical flow:
  osupgrade-v4-to-v5 rewrite -w ./...
  go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
  osupgrade-v4-to-v5 vet -fix ./...
`)
}
