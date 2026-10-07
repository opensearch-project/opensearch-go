// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// typeCheckCompositeLit type-checks src and returns the type info plus its first
// composite literal.
func typeCheckCompositeLit(t *testing.T, src string) (*types.Info, *ast.CompositeLit) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/pkg", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	var found *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok && found == nil {
			found = lit
		}
		return true
	})
	require.NotNil(t, found, "no composite literal in src")
	return info, found
}

// TestRewriteCompositeLit_FieldArms pins what each field-change kind does to a
// keyed literal. Dropping a key is correct only here: the field is a knob that
// no longer exists, where dropping a read of one would silently lose the value.
// A manual or unclassified change leaves the key in place for a human, and an
// unclassified one is reported as a bug rather than as an edit.
func TestRewriteCompositeLit_FieldArms(t *testing.T) {
	t.Parallel()

	const src = `package pkg

type Config struct {
	Metrics   bool
	Addresses []string
}

func use() Config { return Config{Metrics: true, Addresses: nil} }
`
	const qual = "example.com/pkg.Config"

	tests := []struct {
		name             string
		change           apirev.FieldChange
		wantKeys         []string
		wantEdits        []string
		wantUnclassified []string
	}{
		{
			name:      "rename rewrites the key",
			change:    apirev.FieldChange{Kind: apirev.KindRename, From: "Metrics", To: "Stats"},
			wantKeys:  []string{"Stats", "Addresses"},
			wantEdits: []string{`"` + qual + `": field Metrics -> Stats`},
		},
		{
			name:      "removal drops the key",
			change:    apirev.FieldChange{Kind: apirev.KindRemove, From: "Metrics"},
			wantKeys:  []string{"Addresses"},
			wantEdits: []string{`"` + qual + `": field Metrics removed`},
		},
		{
			name:      "manual keeps the key and reports it",
			change:    apirev.FieldChange{Kind: apirev.KindManual, From: "Metrics", Note: "decode it from Body instead"},
			wantKeys:  []string{"Metrics", "Addresses"},
			wantEdits: []string{`MANUAL "` + qual + `": field Metrics - decode it from Body instead`},
		},
		{
			name:             "unclassified keeps the key and is a bug",
			change:           apirev.FieldChange{Kind: apirev.KindUnclassified, From: "Metrics", Note: "classify it in the hop"},
			wantKeys:         []string{"Metrics", "Addresses"},
			wantUnclassified: []string{qual + "#Metrics (set in a literal) - classify it in the hop"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info, lit := typeCheckCompositeLit(t, src)
			delta := apirev.Delta{Structs: map[string]apirev.StructDelta{qual: {
				From: qual, Changes: []apirev.FieldChange{tt.change},
			}}}

			edits, unclassified := rewriteCompositeLit(lit, info, delta, nil)

			// rewriteCompositeLit rewrites the element list in place, so the
			// surviving keys are read back off the literal.
			keys := make([]string, 0, len(lit.Elts))
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				require.True(t, ok, "element is not keyed")
				key, ok := kv.Key.(*ast.Ident)
				require.True(t, ok, "key is not an identifier")
				keys = append(keys, key.Name)
			}

			require.Equal(t, tt.wantKeys, keys)
			require.Equal(t, tt.wantEdits, edits)
			require.Equal(t, tt.wantUnclassified, unclassified)
		})
	}
}

// typeCheckNamedLit type-checks src and returns the type info plus the composite
// literal whose resolved type is named want.
func typeCheckNamedLit(t *testing.T, src, want string) (*types.Info, *ast.CompositeLit) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/pkg", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	var found *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok && found == nil {
			if QualifiedType(info.TypeOf(lit)) == want {
				found = lit
			}
		}
		return true
	})
	require.NotNilf(t, found, "no composite literal of type %q in src", want)
	return info, found
}

// TestRewriteCompositeLit_ElementShapes pins the elements rewriteCompositeLit
// passes through untouched, and the one it renames by TYPE rather than by field.
// A positional element carries no key to match a field change against, and a
// named map type's keys are values rather than field names, so neither can be
// rewritten. An embedded field is the exception: its key IS its type's base
// name, so a type rename has to rename the key or the literal stops compiling.
func TestRewriteCompositeLit_ElementShapes(t *testing.T) {
	t.Parallel()

	const src = `package pkg

type Inner struct{ X int }

type Positional struct {
	Inner
	Y int
}

type Keyed struct {
	Inner
	Y int
}

type Lookup map[string]int

func positional() Positional { return Positional{Inner{}, 1} }
func keyedByType() Keyed     { return Keyed{Inner: Inner{}, Y: 1} }
func mapKeyed() Lookup       { return Lookup{"a": 1} }
`
	// A change on Positional.Y that must not fire, because no element the cases
	// below present is a keyed identifier naming Y.
	deltaOnPositional := apirev.Delta{Structs: map[string]apirev.StructDelta{
		"example.com/pkg.Positional": {
			From:    "example.com/pkg.Positional",
			Changes: []apirev.FieldChange{{Kind: apirev.KindRemove, From: "Y"}},
		},
	}}

	tests := []struct {
		name      string
		litType   string
		delta     apirev.Delta
		renames   map[string]apirev.TypeRename
		wantEdits []string
		// wantElts is the literal's elements after the rewrite, printed as source.
		wantElts []string
	}{
		{
			name:     "positional elements have no key to match",
			litType:  "example.com/pkg.Positional",
			delta:    deltaOnPositional,
			wantElts: []string{"Inner{}", "1"},
		},
		{
			name:     "a named map's keys are values, not fields",
			litType:  "example.com/pkg.Lookup",
			delta:    deltaOnPositional,
			wantElts: []string{`"a": 1`},
		},
		{
			name:    "an embedded key follows its type's rename",
			litType: "example.com/pkg.Keyed",
			delta:   apirev.Delta{Structs: map[string]apirev.StructDelta{}},
			renames: map[string]apirev.TypeRename{
				"example.com/pkg.Inner": {FromPkgPath: "example.com/pkg", FromName: "Inner", ToPkgPath: "example.com/pkg", ToName: "Renamed"},
			},
			wantEdits: []string{"embedded key Inner -> Renamed"},
			wantElts:  []string{"Renamed: Inner{}", "Y: 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info, lit := typeCheckNamedLit(t, src, tt.litType)

			edits, unclassified := rewriteCompositeLit(lit, info, tt.delta, tt.renames)

			require.Equal(t, tt.wantEdits, edits)
			require.Empty(t, unclassified)

			elts := make([]string, 0, len(lit.Elts))
			for _, elt := range lit.Elts {
				var buf bytes.Buffer
				require.NoError(t, printer.Fprint(&buf, token.NewFileSet(), elt))
				elts = append(elts, buf.String())
			}
			require.Equal(t, tt.wantElts, elts)
		})
	}
}
