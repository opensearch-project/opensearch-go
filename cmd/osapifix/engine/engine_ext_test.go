// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/engine"
)

// TestWalkImportableSeam proves the engine is importable by an external module
// and that Walk loads a consumer module and hands each file to the visitor,
// without the visitor needing any engine internals. It also transitively proves
// the internal/apirev boundary holds: engine_test imports engine, engine imports
// internal/apirev, and the immediate-importer internal rule is satisfied.
func TestWalkImportableSeam(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/smoke\n\ngo 1.25\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package smoke\n\nvar Answer = 42\n"), 0o600))

	var visited int
	results, err := engine.Walk(
		engine.WalkConfig{Dir: dir, Patterns: []string{"./..."}, Write: false},
		func(f engine.File) ([]string, []string) {
			visited++
			require.NotNil(t, f.Syntax)
			require.NotNil(t, f.Info)
			require.NotNil(t, f.Fset)
			return nil, nil // no edits
		})
	require.NoError(t, err)
	require.GreaterOrEqual(t, visited, 1, "visitor must see the module's file")
	require.Empty(t, results, "a no-edit visitor reports no results")
}

// TestExportedHelpersReachable is a compile-and-call proof that the five AST
// mutation helpers are exported and safe to call with trivial inputs. It locks
// the exported surface the external opensearchtools visitor depends on.
func TestExportedHelpersReachable(t *testing.T) {
	require.Empty(t, engine.QualifiedType(nil))
	require.Nil(t, engine.NamedOf(nil))
	require.False(t, engine.FileImportsPath(&ast.File{}, "x"))
	require.Empty(t, engine.RewriteImports(&ast.File{}, nil))
	require.NotNil(t, engine.MarkerExpr("migrate by hand"))
}
