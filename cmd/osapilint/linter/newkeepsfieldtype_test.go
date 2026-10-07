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
)

// TestNewKeepsFieldType pins when a pointerWrap value v may become new(v): only
// when new(v) points at the field's own type. go/types records an untyped
// constant with the type it converts to, so the check must read the constant's
// own type (literal token kind, or the named constant's declared type).
func TestNewKeepsFieldType(t *testing.T) {
	t.Parallel()
	const src = `package pkg

const (
	typed   int64 = 1
	untyped       = 1
)

type S struct {
	UntypedIntOnInt64 int64
	UntypedIntOnInt   int
	RuneOnInt         int
	RuneOnInt32       int32
	FloatOnFloat64    float64
	FloatOnFloat32    float32
	ImagOnComplex128  complex128
	ImagOnComplex64   complex64
	StringOnString    string
	BoolOnBool        bool
	TypedConstOnInt64 int64
	UntypedConstOnInt int
	UntypedConstOnI64 int64
	ConversionOnInt64 int64
	VarOnInt64        int64
}

func use(n int64) S {
	return S{
		UntypedIntOnInt64: 1,
		UntypedIntOnInt:   1,
		RuneOnInt:         'a',
		RuneOnInt32:       'a',
		FloatOnFloat64:    1.5,
		FloatOnFloat32:    1.5,
		ImagOnComplex128:  1i,
		ImagOnComplex64:   1i,
		StringOnString:    "s",
		BoolOnBool:        true,
		TypedConstOnInt64: typed,
		UntypedConstOnInt: untyped,
		UntypedConstOnI64: untyped,
		ConversionOnInt64: int64(1),
		VarOnInt64:        n,
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	require.NoError(t, err)
	info := &types.Info{
		Uses:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/pkg", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	elts := map[string]*ast.KeyValueExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			elts[kv.Key.(*ast.Ident).Name] = kv
		}
		return true
	})

	cases := []struct {
		field string
		want  bool
	}{
		{"UntypedIntOnInt64", false}, // new(1) is *int
		{"UntypedIntOnInt", true},
		{"RuneOnInt", false}, // new('a') is *int32
		{"RuneOnInt32", true},
		{"FloatOnFloat64", true},
		{"FloatOnFloat32", false}, // new(1.5) is *float64
		{"ImagOnComplex128", true},
		{"ImagOnComplex64", false}, // new(1i) is *complex128
		{"StringOnString", true},
		{"BoolOnBool", true},
		{"TypedConstOnInt64", true}, // new(typed) is *int64
		{"UntypedConstOnInt", true},
		{"UntypedConstOnI64", false}, // new(untyped) is *int
		{"ConversionOnInt64", false}, // other constant expressions stay MANUAL
		{"VarOnInt64", true},         // a non-constant already has the field's type
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			kv, ok := elts[tc.field]
			require.Truef(t, ok, "no literal element for %q", tc.field)
			require.Equal(t, tc.want, newKeepsFieldType(kv, info))
		})
	}
}

// TestNewKeepsFieldTypeGuards pins the three shapes newKeepsFieldType refuses
// before it can reason about a type: a key that is not an identifier, a key that
// resolves to something other than a field, and a value go/types recorded no
// type for. The first two reach it from a map literal, whose keys are values
// rather than field names; the third cannot arise from type-checked source, so
// it is built directly.
func TestNewKeepsFieldTypeGuards(t *testing.T) {
	t.Parallel()

	const src = `package pkg

const mapKey = "k"

type T struct{ N int }

func use(n int) []any {
	return []any{
		T{N: n},
		map[string]int{"literal": 1},
		map[string]int{mapKey: 1},
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	require.NoError(t, err)
	info := &types.Info{
		Uses:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	_, err = (&types.Config{Importer: importer.Default()}).Check("example.com/pkg", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	// In source order: the struct field, the string-literal map key, then the
	// named-constant map key.
	var kvs []*ast.KeyValueExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			kvs = append(kvs, kv)
		}
		return true
	})
	require.Len(t, kvs, 3)
	structField, literalKey, constKey := kvs[0], kvs[1], kvs[2]

	// A real field key paired with a value no type was recorded for.
	untypedValue := &ast.KeyValueExpr{Key: structField.Key, Value: ast.NewIdent("absent")}

	tests := []struct {
		name string
		kv   *ast.KeyValueExpr
		want bool
	}{
		{name: "struct field is the covered case", kv: structField, want: true},
		{name: "key is not an identifier", kv: literalKey, want: false},
		{name: "key resolves to a constant, not a field", kv: constKey, want: false},
		{name: "value has no recorded type", kv: untypedValue, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, newKeepsFieldType(tt.kv, info))
		})
	}
}
