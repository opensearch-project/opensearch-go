// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// patternAll is the go/packages load pattern for a whole module.
const patternAll = "./..."

// TestRewriteCorpus runs the real type-aware rewrite over a fixture module and
// checks three things per hop: each golden-backed fixture matches its committed
// .golden sibling, the emitted report lines cover the expected rewrites and
// MANUAL diagnostics, and each fixture listed in compileClean is import-clean
// pure target-version output (no marker, no unused import). A fixture the hop
// ONLY reports on (pure idiom 1, e.g. bulk_idiom1.go) has no golden and is
// covered by report assertions alone. Not every golden is a promise of compiling
// v3: seedops.go rewrites its idiom-2 seed ops to compiling v3 but also carries
// an idiom-1 reference to a removed type (opensearchapi.PingRequest), which stays
// put as a reported MANUAL item, so its golden deliberately does not compile as
// pure v3 - only the compileClean subset makes that promise. Fixtures compile
// against a local stub of the source-version API (testdata/corpus/stub-vN), so no
// opensearch-go download is needed.
//
// Regenerate goldens after an intentional rewrite change with:
//
//	UPDATE_GOLDEN=1 go test ./cmd/osapifix -run TestRewriteCorpus
func TestRewriteCorpus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     Major
		dst     Major
		corpus  string   // dir under testdata/corpus holding go.mod + fixtures
		stub    string   // replace-target dir under testdata/corpus
		goldens []string // fixture files diffed against <file>.golden
		// compileClean lists goldens that must be pure compiling target-version
		// output: no _OSAPIFIX_RESOLVE marker and no unused import. The corpus does
		// not run `go build` (the stubs are a minimal API surface, not the real
		// package), so this is the syntactic stand-in that guards the class of bug
		// where a rewrite leaves an import dangling - e.g. the root import going
		// dead once *opensearch.Client is repointed to *opensearchapi.Client.
		compileClean []string
		edits        []string // substrings that must appear in the report
	}{
		{
			name:   "v2_to_v3",
			src:    2,
			dst:    3,
			corpus: "v2",
			stub:   "stub-v2",
			// seedops: idiom-2 seed ops -> compiling v3. aliasedimport: aliased
			// opensearchapi import stays single. paramsemit: destParams nests under
			// Params. carriedrootclient: a carried v2-root-client arg -> marker.
			goldens: []string{"seedops.go", "aliasedimport.go", "paramsemit.go", "carriedrootclient.go"},
			// paramsemit is the one fixture with no marker and no removed-type ref,
			// so its golden must be import-clean compiling v3. seedops carries an
			// idiom-1 removed-type ref; carriedrootclient plants a marker; both are
			// non-compiling by design and excluded.
			compileClean: []string{"paramsemit.go"},
			edits: []string{
				// idiom 2 (seed ops): rewritten best-effort
				"rewrite client.Ping",
				"rewrite resp.Status() -> fmt.Sprintf/http.StatusText",
				"rewrite client.Indices.Exists",
				"reshape v2 Config{...} -> opensearchapi.Config{Client: ...}",
				"repoint opensearchv2.NewClient -> opensearchapi.NewClient",
				`MANUAL "github.com/opensearch-project/opensearch-go/v2/opensearchapi.PingRequest" removed`,
				// idiom 1 (function API): removed-type diagnostic, report-only
				`MANUAL "github.com/opensearch-project/opensearch-go/v2/opensearchapi.BulkRequest" removed`,
			},
		},
		{
			name:    "v3_to_v4",
			src:     3,
			dst:     4,
			corpus:  "v3",
			stub:    "stub-v3",
			goldens: []string{"client.go"}, // quiet hop: only the import path bumps
			edits: []string{
				"import github.com/opensearch-project/opensearch-go/v3",
			},
		},
		{
			// Cross-hop removed-type diagnostic: flagRemovedTypeRef fires on every
			// hop, not just v2->v3. AliasDeleteResp exists in v4 but is removed in
			// v5, so a reference to it must be reported as a MANUAL worklist item.
			name:    "v4_to_v5",
			src:     4,
			dst:     5,
			corpus:  "v4",
			stub:    "stub-v4",
			goldens: []string{"removedtype.go"}, // import bumps; the removed-type ref stays put
			edits: []string{
				"import github.com/opensearch-project/opensearch-go/v4",
				`MANUAL "github.com/opensearch-project/opensearch-go/v4/opensearchapi.AliasDeleteResp" removed`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := stageCorpus(t, tc.corpus, tc.stub)

			plans, err := planChain(tc.src, tc.dst)
			require.NoError(t, err)
			require.Len(t, plans, 1, "corpus test covers a single hop")
			p := plans[0]

			results, err := runTypeAwareRewrite(rewriteConfig{
				dir:            dir,
				patterns:       []string{patternAll},
				delta:          p.delta,
				renames:        p.renames,
				regroups:       p.regroups,
				removedHelpers: p.removedHelpers,
				importPrefixes: p.importPrefixes,
				write:          true,
			})
			require.NoError(t, err)

			report := reportText(results)
			for _, want := range tc.edits {
				require.Contains(t, report, want, "report must mention %q\nfull report:\n%s", want, report)
			}

			for _, file := range tc.goldens {
				got, err := os.ReadFile(filepath.Join(dir, file))
				require.NoError(t, err)

				goldenPath := filepath.Join("testdata", "corpus", tc.corpus, file+".golden")
				if os.Getenv("UPDATE_GOLDEN") != "" {
					// goldenPath is built from hardcoded test-table fields, not
					// external input; this dev-only branch never runs under CI.
					require.NoError(t, os.WriteFile(goldenPath, got, 0o600)) //nolint:gosec // G703: path from test constants
					continue
				}
				want, err := os.ReadFile(goldenPath)
				require.NoError(t, err, "missing golden for %s; regenerate with UPDATE_GOLDEN=1", file)
				require.Equal(t, string(want), string(got), "rewritten %s does not match golden", file)
			}

			for _, file := range tc.compileClean {
				got, err := os.ReadFile(filepath.Join(dir, file))
				require.NoError(t, err)
				require.NotContains(t, string(got), markerPrefix,
					"%s is declared compile-clean but carries an _OSAPIFIX_RESOLVE marker", file)
				assertNoUnusedImports(t, file, got)
			}
		})
	}
}

// assertNoUnusedImports parses src as Go and fails if any import is unreferenced
// (the "imported and not used" compile error, in syntactic form). It resolves
// each import's local name - the alias, or the last path segment when unnamed -
// and checks a matching pkg.Sel selector appears somewhere in the file. This is
// the corpus's compile stand-in for goldens meant to be pure target-version
// output, since the test does not build against the real packages.
func assertNoUnusedImports(t *testing.T, file string, src []byte) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	require.NoError(t, err, "parse %s", file)

	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var name string
		if imp.Name != nil {
			name = imp.Name.Name
		} else if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		} else {
			name = path
		}
		if name == "_" || name == "." {
			continue // blank/dot imports are used by side effect
		}
		require.Truef(t, used[name], "%s imports %q (as %q) but never references it", file, path, name)
	}
}

// stageCorpus copies a corpus module and its stub into a temp dir so the rewrite
// mutates a throwaway tree rather than the committed fixture. The committed
// go.mod uses "replace ... => ../<stub>", which still resolves because both dirs
// are copied as siblings under the temp root.
func stageCorpus(t *testing.T, corpus, stub string) string {
	t.Helper()
	root := t.TempDir()
	srcBase := filepath.Join("testdata", "corpus")
	require.NoError(t, os.CopyFS(filepath.Join(root, corpus), os.DirFS(filepath.Join(srcBase, corpus))))
	require.NoError(t, os.CopyFS(filepath.Join(root, stub), os.DirFS(filepath.Join(srcBase, stub))))
	return filepath.Join(root, corpus)
}

// reportText flattens per-file edit lines into one string for substring checks.
func reportText(results []rewriteResult) string {
	var b strings.Builder
	for _, r := range results {
		for _, e := range r.edits {
			b.WriteString(e)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
