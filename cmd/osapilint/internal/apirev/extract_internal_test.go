// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"
)

const extractSrc = `package p

type Base struct{ Pretty bool }
type Alias = Base

type Plain struct {
	Base
	Q string
}
type ByAlias struct {
	Alias
	Q string
}
type ByAliasPointer struct {
	*Alias
	Q string
}
`

// TestExtractStructEmbeddedAlias verifies that fields promoted through an
// embedded type alias are kept, as they are through a plain embedded type.
func TestExtractStructEmbeddedAlias(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", extractSrc, 0)
	require.NoError(t, err)
	pkg, err := (&types.Config{}).Check("p", fset, []*ast.File{file}, nil)
	require.NoError(t, err)

	tests := []struct {
		name   string
		typ    string
		fields []string
	}{
		{name: "plain embed", typ: "Plain", fields: []string{"Pretty", "Q"}},
		{name: "alias embed", typ: "ByAlias", fields: []string{"Pretty", "Q"}},
		{name: "pointer to alias embed", typ: "ByAliasPointer", fields: []string{"Pretty", "Q"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := pkg.Scope().Lookup(tt.typ).Type().Underlying().(*types.Struct)
			var got []string
			for _, f := range extractStruct("p", tt.typ, st).Fields {
				got = append(got, f.Name)
			}
			require.Equalf(t, tt.fields, got, "fields of %q", tt.typ)
		})
	}
}
