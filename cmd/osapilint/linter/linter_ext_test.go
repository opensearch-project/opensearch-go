// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/linter"
)

// TestWalkImportableSeam proves the linter is importable by an external module
// and that Walk loads a consumer module and hands each file to the visitor,
// without the visitor needing any linter internals. It also transitively proves
// the internal/apirev boundary holds: linter_test imports linter, linter imports
// internal/apirev, and the immediate-importer internal rule is satisfied.
func TestWalkImportableSeam(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/smoke\n\ngo 1.25\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package smoke\n\nvar Answer = 42\n"), 0o600))

	var visited int
	results, err := linter.Walk(
		linter.WalkConfig{Dir: dir, Patterns: []string{"./..."}, Write: false},
		func(f linter.File) ([]string, []string) {
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
	require.Empty(t, linter.QualifiedType(nil))
	require.Nil(t, linter.NamedOf(nil))
	require.False(t, linter.FileImportsPath(&ast.File{}, "x"))
	require.Empty(t, linter.RewriteImports(&ast.File{}, nil))
	require.NotNil(t, linter.MarkerExpr("migrate by hand"))
}

// TestMigrateSDKReachable proves the opensearch-go SDK migration seam is exported
// and callable from an external module, alongside the Walk seam above. It locks
// linter.MigrateSDK/SDKConfig/Major as the surface an overlay dispatches to; the
// migration itself is exercised by the in-package migratesdk_test.go.
func TestMigrateSDKReachable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/ext\n\ngo 1.25\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package ext\n\nvar X = 1\n"), 0o600))

	// No opensearch-go import, so auto-detection fails: the value here is proving
	// the exported call reaches the engine from outside the package.
	_, err := linter.MigrateSDK(t.Context(), linter.SDKConfig{Dir: dir})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no opensearch-go imports")
}
