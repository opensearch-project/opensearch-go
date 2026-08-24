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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/internal/apirev"
)

// flagfieldaccess_test.go pins the promoted-field resolution in flagFieldAccess.
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

	manual, unclassified := flagFieldAccess(sel, info, delta)
	require.Empty(t, unclassified)
	require.Contains(t, manual, "access .Ping", "promoted field access must be flagged against the receiver type")
}
