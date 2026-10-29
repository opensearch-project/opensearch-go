// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// TestRewriteSymbolRef pins which selector references follow a rename: a
// package-level type, constant or function qualified through an import. Methods,
// package variables and unqualified references (own declarations, dot imports)
// are never renamed, even when their name collides with a rename key.
func TestRewriteSymbolRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		src        string
		renames    map[string]apirev.TypeRename
		wantEdit   string // edit of the selector named target; "" when untouched
		target     string // selector text before rewriting, as "X.Sel"
		wantSel    string // sel.Sel.Name of the target after rewriting
		noSelector bool   // the file has no selector, so nothing reaches the rewriter
	}{
		{
			name:     "func renamed",
			src:      "package p\nimport \"strings\"\nvar _ = strings.Compare(\"a\", \"b\")\n",
			renames:  map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}},
			target:   "strings.Compare",
			wantEdit: "func Compare -> Cmp",
			wantSel:  "Cmp",
		},
		{
			name:     "const renamed",
			src:      "package p\nimport \"io\"\nvar _ = io.SeekStart\n",
			renames:  map[string]apirev.TypeRename{"io.SeekStart": {FromPkgPath: "io", FromName: "SeekStart", ToName: "Start"}},
			target:   "io.SeekStart",
			wantEdit: "const SeekStart -> Start",
			wantSel:  "Start",
		},
		{
			name:     "type renamed with type text",
			src:      "package p\nimport \"bytes\"\nvar _ bytes.Buffer\n",
			renames:  map[string]apirev.TypeRename{"bytes.Buffer": {FromPkgPath: "bytes", FromName: "Buffer", ToName: "Buf"}},
			target:   "bytes.Buffer",
			wantEdit: "type Buffer -> Buf",
			wantSel:  "Buf",
		},
		{
			name:     "aliased import renamed",
			src:      "package p\nimport str \"strings\"\nvar _ = str.Compare(\"a\", \"b\")\n",
			renames:  map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}},
			target:   "str.Compare",
			wantEdit: "func Compare -> Cmp",
			wantSel:  "Cmp",
		},
		{
			name:       "own unqualified func untouched",
			src:        "package p\nfunc Compare(a, b string) int { return 0 }\nvar _ = Compare(\"a\", \"b\")\n",
			renames:    map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}},
			noSelector: true,
		},
		{
			name:       "dot import untouched",
			src:        "package p\nimport . \"strings\"\nvar _ = Compare(\"a\", \"b\")\n",
			renames:    map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}},
			noSelector: true,
		},
		{
			name:    "method untouched",
			src:     "package p\nimport \"strings\"\nvar sb strings.Builder\nvar _ = sb.String()\n",
			renames: map[string]apirev.TypeRename{"strings.String": {FromPkgPath: "strings", FromName: "String", ToName: "Str"}},
			target:  "sb.String",
			wantSel: "String",
		},
		{
			name:    "package var untouched",
			src:     "package p\nimport \"io\"\nvar _ = io.EOF\n",
			renames: map[string]apirev.TypeRename{"io.EOF": {FromPkgPath: "io", FromName: "EOF", ToName: "End"}},
			target:  "io.EOF",
			wantSel: "EOF",
		},
		{
			name:    "other package same name untouched",
			src:     "package p\nimport \"bytes\"\nvar _ = bytes.Compare(nil, nil)\n",
			renames: map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}},
			target:  "bytes.Compare",
			wantSel: "Compare",
		},
		{
			name:    "no entry untouched",
			src:     "package p\nimport \"strings\"\nvar _ = strings.Compare(\"a\", \"b\")\n",
			renames: map[string]apirev.TypeRename{},
			target:  "strings.Compare",
			wantSel: "Compare",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "src.go", tt.src, 0)
			require.NoError(t, err)
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
			_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/p", fset, []*ast.File{file}, info)
			require.NoError(t, err)

			var edits []string
			var sels []*ast.SelectorExpr
			byText := map[string]*ast.SelectorExpr{}
			ast.Inspect(file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					sels = append(sels, sel)
					if x, ok := sel.X.(*ast.Ident); ok {
						byText[x.Name+"."+sel.Sel.Name] = sel
					}
					if e := rewriteSymbolRef(sel, info, tt.renames); e != "" {
						edits = append(edits, e)
					}
				}
				return true
			})

			if tt.noSelector {
				require.Empty(t, sels)
				require.Empty(t, edits)
				return
			}

			got := byText[tt.target]
			require.NotNil(t, got, "selector %q not found", tt.target)
			require.Equal(t, tt.wantSel, got.Sel.Name)
			if tt.wantEdit == "" {
				require.Empty(t, edits)
			} else {
				require.Equal(t, []string{tt.wantEdit}, edits)
			}
		})
	}
}

// TestFlagUnqualifiedSymbolRef pins which unqualified identifiers are reported
// as MANUAL: only a use that resolves to a renamed package-level symbol of
// another package, which in practice means a dot import. The AST is never
// mutated.
func TestFlagUnqualifiedSymbolRef(t *testing.T) {
	t.Parallel()

	renames := map[string]apirev.TypeRename{"strings.Compare": {FromPkgPath: "strings", FromName: "Compare", ToName: "Cmp"}}

	tests := []struct {
		name    string
		src     string
		renames map[string]apirev.TypeRename
		want    []string
	}{
		{
			name:    "dot imported func flagged",
			src:     "package p\nimport . \"strings\"\nvar _ = Compare(\"a\", \"b\")\n",
			renames: renames,
			want:    []string{"MANUAL func Compare -> Cmp: unqualified use (dot import) is not rewritten; rename it by hand"},
		},
		{
			name:    "dot imported const flagged",
			src:     "package p\nimport . \"io\"\nvar _ = SeekStart\n",
			renames: map[string]apirev.TypeRename{"io.SeekStart": {FromPkgPath: "io", FromName: "SeekStart", ToName: "Start"}},
			want:    []string{"MANUAL const SeekStart -> Start: unqualified use (dot import) is not rewritten; rename it by hand"},
		},
		{
			name:    "dot imported type flagged",
			src:     "package p\nimport . \"bytes\"\nvar _ Buffer\n",
			renames: map[string]apirev.TypeRename{"bytes.Buffer": {FromPkgPath: "bytes", FromName: "Buffer", ToName: "Buf"}},
			want:    []string{"MANUAL type Buffer -> Buf: unqualified use (dot import) is not rewritten; rename it by hand"},
		},
		{
			name:    "own func silent",
			src:     "package p\nfunc Compare(a, b string) int { return 0 }\nvar _ = Compare(\"a\", \"b\")\n",
			renames: renames,
		},
		{
			name:    "local var silent",
			src:     "package p\nfunc f() int {\n\tCompare := 1\n\treturn Compare\n}\n",
			renames: renames,
		},
		{
			name:    "dot import without entry silent",
			src:     "package p\nimport . \"strings\"\nvar _ = Compare(\"a\", \"b\")\n",
			renames: map[string]apirev.TypeRename{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "src.go", tt.src, 0)
			require.NoError(t, err)
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
			_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/p", fset, []*ast.File{file}, info)
			require.NoError(t, err)

			var got []string
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if e := flagUnqualifiedSymbolRef(id, info, tt.renames); e != "" {
						got = append(got, e)
					}
				}
				return true
			})
			require.Equal(t, tt.want, got)
		})
	}
}
