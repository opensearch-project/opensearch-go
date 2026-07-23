// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWalkLoadErrorGate covers the load gate: a consumer that does not compile
// against its current deps cannot be resolved for a type-aware rewrite, so Walk
// refuses the run rather than editing from incomplete type info.
func TestWalkLoadErrorGate(t *testing.T) {
	silenceOutput(t) // packages.PrintErrors writes the load errors to stderr
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{
		"go.mod": "module example.com/broken\n\ngo 1.24\n",
		"bad.go": "package broken\n\nfunc F() int { return undefinedSymbol }\n",
	})

	_, err := Walk(WalkConfig{Dir: dir}, func(File) ([]string, []string) {
		t.Fatal("visitor must not run when the module fails to load")
		return nil, nil
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must compile")
}

// TestWalkUnclassifiedAborts covers the abort-before-write invariant: when a
// visitor reports an unclassified reference (a field that vanished on the target
// with no disposition), Walk fails the whole run with the osapilint-bug error and
// writes nothing, even for files it did produce edits for.
func TestWalkUnclassifiedAborts(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{
		"go.mod": "module example.com/smoke\n\ngo 1.24\n",
		"p.go":   "package smoke\n\nvar Answer = 42\n",
	})

	// Omit Patterns to also cover the default-to-"./..." branch.
	results, err := Walk(WalkConfig{Dir: dir, Write: true},
		func(File) ([]string, []string) {
			return []string{"some edit"}, []string{"opensearchapi.SomeType.VanishedField"}
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "osapilint bug")
	require.NotEmpty(t, results, "edits are reported alongside the abort error")
}
