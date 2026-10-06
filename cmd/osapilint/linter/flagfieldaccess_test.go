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

// flagfieldaccess_test.go pins the promoted-field resolution in rewriteFieldAccess.
//
// gensurface flattens promoted fields onto the embedding struct, so a field
// declared on an embedded (and often removed) type is ruled on the OUTER type in
// the surface - exactly the v2 root opensearch.Client, whose API methods are
// promoted from an embedded *opensearchapi.API. The linter must therefore flag an
// access through the receiver (outer) type, not only through the type that
// literally declares the field. This is the fix validated against the real config
// consumer's client.Ping call.

// typeCheckSelector parses src, type-checks it, and returns the type info plus the
// first SelectorExpr whose selector name is field.
func typeCheckSelector(t *testing.T, src, field string) (*types.Info, *ast.SelectorExpr) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Uses:       map[*ast.Ident]types.Object{},
		Defs:       map[*ast.Ident]types.Object{},
		Types:      map[ast.Expr]types.TypeAndValue{},
	}
	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("example.com/pkg", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	var found *ast.SelectorExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == field && found == nil {
			if _, isSelection := info.Selections[sel]; isSelection {
				found = sel
			}
		}
		return true
	})
	require.NotNilf(t, found, "no promoted-field selection for .%s found", field)
	return info, found
}

// TestFlagFieldAccess_PromotedField verifies that an access to a field promoted
// from an embedded type is flagged against a disposition keyed on the OUTER
// (receiver) type - the gensurface-flattening case. Client embeds *api (which
// declares Ping); the delta rules Ping on Client, mirroring the v2 surface.
func TestFlagFieldAccess_PromotedField(t *testing.T) {
	const src = `package pkg

type api struct {
	Ping func()
}

type Client struct {
	*api
}

func use(c *Client) {
	c.Ping()
}
`
	info, sel := typeCheckSelector(t, src, "Ping")

	delta := apirev.Delta{Structs: map[string]apirev.StructDelta{
		"example.com/pkg.Client": {
			From: "example.com/pkg.Client",
			Changes: []apirev.FieldChange{
				{Kind: apirev.KindManual, From: "Ping", Note: "root client method removed"},
			},
		},
	}}

	manual, unclassified := rewriteFieldAccess(sel, info, delta)
	require.Empty(t, unclassified)
	require.Contains(t, manual, "access .Ping", "promoted field access must be flagged against the receiver type")
}

func TestRewriteFieldAccess_Rename(t *testing.T) {
	for _, tc := range []struct{ name, receiver, ruledType string }{
		{"value", "Response", "Response"},
		{"pointer", "*Response", "Response"},
		{"promoted declaring type", "*Wrapped", "Response"},
		{"promoted receiver type", "*Wrapped", "Wrapped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `package pkg
type Response struct { Shards []int }
type Wrapped struct { *Response }
func use(r ` + tc.receiver + `) { _ = r.Shards }
`
			info, sel := typeCheckSelector(t, src, "Shards")
			qual := "example.com/pkg." + tc.ruledType
			delta := apirev.Delta{Structs: map[string]apirev.StructDelta{
				qual: {
					From: qual,
					Changes: []apirev.FieldChange{
						{Kind: apirev.KindRename, From: "Shards", To: "Records"},
					},
				},
			}}
			edit, unclassified := rewriteFieldAccess(sel, info, delta)
			require.Empty(t, unclassified)
			require.Equal(t, "Records", sel.Sel.Name)
			require.Contains(t, edit, "field Shards -> Records")
		})
	}
}

func TestRewriteFieldAccess_RenameCollision(t *testing.T) {
	for _, tc := range []struct{ name, declarations, receiver string }{
		{"direct field", "type Wrapped struct { *Response; Records []int }", "*Wrapped"},
		{"promoted field", "type Other struct { Records []int }; type Wrapped struct { *Response; Other }", "Wrapped"},
		{"method", "type Wrapped struct { *Response }; func (*Wrapped) Records() {}", "*Wrapped"},
		{"addressable pointer method", "type Wrapped struct { *Response }; func (*Wrapped) Records() {}", "Wrapped"},
		{
			"ambiguous field",
			"type A struct { Records []int }; type B struct { Records []int }; type Wrapped struct { *Response; A; B }",
			"*Wrapped",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, sel := typeCheckSelector(t, `package pkg
type Response struct { Shards []int }
`+tc.declarations+`
func use(r `+tc.receiver+`) { _ = r.Shards }
`, "Shards")
			const qual = "example.com/pkg.Response"
			delta := apirev.Delta{Structs: map[string]apirev.StructDelta{qual: {
				From: qual, Changes: []apirev.FieldChange{{Kind: apirev.KindRename, From: "Shards", To: "Records"}},
			}}}
			edit, unclassified := rewriteFieldAccess(sel, info, delta)
			require.Empty(t, unclassified)
			require.Equal(t, "Shards", sel.Sel.Name, "a colliding selector must not be rewritten")
			require.Contains(t, edit, "MANUAL")
			require.Contains(t, edit, "Records")
		})
	}
}

func TestRewriteRename_TypeChangeWarning(t *testing.T) {
	info, sel := typeCheckSelector(t, `package pkg
type Response struct { Shards []string }
func use(r Response) { _ = r.Shards; _ = Response{Shards: nil} }
`, "Shards")
	const qual = "example.com/pkg.Response"
	const note = "type changed from []string to []*string"
	delta := apirev.Delta{Structs: map[string]apirev.StructDelta{qual: {
		From: qual, Changes: []apirev.FieldChange{{Kind: apirev.KindRename, From: "Shards", To: "Records", Note: note}},
	}}}
	edit, unclassified := rewriteFieldAccess(sel, info, delta)
	require.Empty(t, unclassified)
	require.Equal(t, "Records", sel.Sel.Name)
	require.Contains(t, edit, "MANUAL")
	require.Contains(t, edit, note)

	var lit *ast.CompositeLit
	for expr := range info.Types {
		if candidate, ok := expr.(*ast.CompositeLit); ok {
			lit = candidate
		}
	}
	require.NotNil(t, lit)
	edits, unknown := rewriteCompositeLit(lit, info, delta, nil)
	require.Empty(t, unknown)
	require.Equal(t, "Records", lit.Elts[0].(*ast.KeyValueExpr).Key.(*ast.Ident).Name)
	require.Contains(t, edits, `MANUAL "`+qual+`": field Records - `+note)
}
