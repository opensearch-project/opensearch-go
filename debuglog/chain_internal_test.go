// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package debuglog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fieldMethods are the [Event] methods that return an Event. A chain ending in
// one of them, used as a statement, has discarded the Event without emitting:
// the record is silently dropped and, for the zerolog adapter, its pooled event
// is never returned to the pool.
//
// Msg is deliberately absent -- it is the terminator this guard looks for.
var fieldMethods = map[string]bool{
	"Str":      true,
	"Strs":     true,
	"Int":      true,
	"Int32":    true,
	"Int64":    true,
	"Uint32":   true,
	"Float64":  true,
	"Dur":      true,
	"Time":     true,
	"Stringer": true,
	"Err":      true,
}

// chainRoots are the names of the calls that begin a debug chain: the
// opensearchtransport accessor, and the per-message resolver the metrics
// registries hold.
//
// Matching the root is what keeps this off unrelated builders. A config hash
// builder chains Int the same way a debug record does, so the method names alone
// do not identify a debug chain.
var chainRoots = map[string]bool{
	"Debug": true,
	"log":   true,
}

// unterminatedChains reports every statement in f that builds a debug record and
// then throws it away.
//
// The shape it matches is a bare expression statement whose outermost call is a
// field method, and whose chain begins at one of [chainRoots]:
// `Debug().Str("k", v)` with no Msg.
//
// This is syntactic on purpose. Resolving the receiver's type would mean pulling
// golang.org/x/tools into the root module's test dependencies, and the point of
// this package is that it has none.
func unterminatedChains(fset *token.FileSet, f *ast.File) []string {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		stmt, ok := n.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !fieldMethods[sel.Sel.Name] || !chainRoots[chainRoot(call)] {
			return true
		}
		found = append(found, fset.Position(stmt.Pos()).String()+": chain ends in "+sel.Sel.Name+", never calls Msg")
		return true
	})
	return found
}

// chainRoot returns the name of the call that begins the chain ending at call,
// or "" when the chain does not begin at a call.
func chainRoot(call *ast.CallExpr) string {
	for {
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			// An unqualified call: Debug().
			return fun.Name
		case *ast.SelectorExpr:
			inner, ok := fun.X.(*ast.CallExpr)
			if !ok {
				// The receiver is not itself a call, so this selector is the root:
				// opensearchtransport.Debug() or r.log().
				return fun.Sel.Name
			}
			call = inner
		default:
			return ""
		}
	}
}

func TestUnterminatedChains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "terminated chain is fine",
			src: `package p
func f() { Debug().Str("a", "b").Int("n", 1).Msg("hello") }`,
			want: 0,
		},
		{
			name: "chain with no fields is fine",
			src: `package p
func f() { Debug().Msg("hello") }`,
			want: 0,
		},
		{
			name: "chain ending in a field method is caught",
			src: `package p
func f() { Debug().Str("a", "b").Int("n", 1) }`,
			want: 1,
		},
		{
			name: "single field with no terminator is caught",
			src: `package p
func f() { Debug().Err(err) }`,
			want: 1,
		},
		{
			name: "qualified accessor is caught too",
			src: `package p
func f() { opensearchtransport.Debug().Stringer("conn", u) }`,
			want: 1,
		},
		{
			name: "multiple bad chains are all reported",
			src: `package p
func f() {
	Debug().Str("a", "b")
	Debug().Int("n", 1).Msg("ok")
	Debug().Dur("d", d)
}`,
			want: 2,
		},
		{
			name: "field method on a plain value is not a chain",
			src: `package p
func f() { ctx.Err() }`,
			want: 0,
		},
		{
			// The config-cache hash builder chains Int exactly like a debug record
			// does. Matching on the method name alone flagged it.
			name: "unrelated builder chaining the same method names is not a chain",
			src: `package p
func f() {
	b.Int(int64(cfg.DiscoverNodesInterval)).
		Int(int64(cfg.VerifyDeadAfter)).
		Str(cfg.Username)
}`,
			want: 0,
		},
		{
			name: "registry per-message resolver is a chain root",
			src: `package p
func f() { r.log().Int("workers", n) }`,
			want: 1,
		},
		{
			name: "assigned chain is not a statement",
			src: `package p
func f() { e := Debug().Str("a", "b"); e.Msg("later") }`,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tt.src, 0)
			require.NoError(t, err)
			require.Len(t, unterminatedChains(fset, f), tt.want)
		})
	}
}

// TestRepoDebugChainsTerminate is the guard the compiler cannot be: a debug
// chain is a valid expression statement whether or not it ends in Msg, so
// forgetting the terminator produces working code that logs nothing.
//
// It walks every module in the repository rather than only this one, because the
// adapter and metrics-registry modules build chains too.
func TestRepoDebugChainsTerminate(t *testing.T) {
	t.Parallel()

	const repoRoot = ".."

	var findings []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata holds deliberately broken corpora for the migration tool.
			if name := d.Name(); name == ".git" || name == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		findings = append(findings, unterminatedChains(fset, f)...)
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, findings, "debug chains must end in Msg")
}
