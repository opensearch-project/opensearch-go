// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"go/ast"
	"go/importer"
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

const symbolSrc = `package p

import "encoding/json"

type Relation string

const RelationEq Relation = "eq"

type OptionsType int

const (
	OptionsUnknownType OptionsType = iota
	OptionsTextType
)

type Options struct {
	typ OptionsType
	raw json.RawMessage
}

func (u *Options) Text() (string, error) { return "", nil }

func NewOptionsFromText(v string) Options { return Options{} }

func (u Options) Raw() []byte { return nil }

type Opts = Options
`

// TestExtractSymbol pins what a symbol records: a type's underlying shape with
// its unexported fields and its methods, a constant's type and exact value, and
// a function's signature, all relative to the symbol's own package.
func TestExtractSymbol(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", symbolSrc, 0)
	require.NoError(t, err)
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("p", fset, []*ast.File{file}, nil)
	require.NoError(t, err)

	tests := []struct {
		name string
		want Symbol
	}{
		{name: "Relation", want: Symbol{PkgPath: "p", Name: "Relation", Kind: SymbolType, Type: "string"}},
		{name: "RelationEq", want: Symbol{PkgPath: "p", Name: "RelationEq", Kind: SymbolConst, Type: "@.Relation", Value: `"eq"`}},
		{name: "OptionsTextType", want: Symbol{PkgPath: "p", Name: "OptionsTextType", Kind: SymbolConst, Type: "@.OptionsType", Value: "1"}},
		{name: "Options", want: Symbol{
			PkgPath: "p", Name: "Options", Kind: SymbolType,
			Type: "struct{typ @.OptionsType; raw encoding/json.RawMessage}",
			Methods: []Method{
				{Name: "Raw", Signature: "func() []byte"},
				{Name: "Text", Signature: "func() (string, error)", PointerReceiver: true},
			},
		}},
		{name: "Opts", want: Symbol{PkgPath: "p", Name: "Opts", Kind: SymbolAlias, Type: "@.Options"}},
		{name: "NewOptionsFromText", want: Symbol{PkgPath: "p", Name: "NewOptionsFromText", Kind: SymbolFunc, Type: "func(v string) @.Options"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := extractSymbol(pkg, pkg.Scope().Lookup(tt.name))
			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
