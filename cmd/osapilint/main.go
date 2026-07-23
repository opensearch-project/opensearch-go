// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Command osapilint migrates a Go module across opensearch-go major versions.
//
// It is a generic frontend over a registry of per-adjacent-hop migration tables
// (v4->v5 today; v3->v4, v2->v3 to follow). The source major is auto-detected
// from the consumer's imports and the target defaults to the newest known
// version, so the common invocation is simply `osapilint rewrite`.
//
// A migration has two fundamentally different kinds of change, needing two
// mechanisms - so the tool exposes two subcommands:
//
//	osapilint rewrite [-src=auto] [-dst=vN] [-w] [dir]   # SYNTACTIC pass (pre-compile)
//	osapilint vet     [-fix] pkgs                        # SEMANTIC pass  (post-compile)
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
// reported as an osapilint bug (the run fails loudly) rather than silently
// dropped. See README.md.
package main

import (
	"fmt"
	"os"

	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/linter"
)

func main() {
	if len(os.Args) < 2 {
		linter.Usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "rewrite":
		if err := linter.Rewrite(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "rewrite:", err)
			os.Exit(1)
		}
	case "vet":
		// Hand the remaining args to multichecker, which owns -fix, -json, and
		// per-analyzer flags exactly as `go vet` tooling expects.
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		multichecker.Main(linter.Analyzers()...)
	case "-h", "--help", "help":
		linter.Usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		linter.Usage()
		os.Exit(2)
	}
}
