// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/emit"
	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// TestSplitUnionsFromSiblings is a regression guard for the
// ReindexSourceSort empty-struct bug: SiblingTypesFragment renders types
// as structs (using their Fields), so a union sibling fed through it
// emits as `type Foo struct {}` because the union's Branches aren't
// Fields. The codegen must split unions out and route them to
// UnionFragment instead.
//
// This affects request-body subtrees (op.ReqBodySiblings) the same way
// it affects response subtrees (op.SiblingTypes); the bug surfaced when
// the request-body path lacked the split.
func TestSplitUnionsFromSiblings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       []*ir.Type
		wantStructs int
		wantUnions  int
	}{
		{
			name:        "nil input",
			input:       nil,
			wantStructs: 0,
			wantUnions:  0,
		},
		{
			name:        "all structs",
			input:       []*ir.Type{{Name: "A", Kind: ir.TypeStruct}, {Name: "B", Kind: ir.TypeStruct}},
			wantStructs: 2,
			wantUnions:  0,
		},
		{
			name:        "all unions (strict)",
			input:       []*ir.Type{{Name: "A", Kind: ir.TypeUnion}, {Name: "B", Kind: ir.TypeUnion}},
			wantStructs: 0,
			wantUnions:  2,
		},
		{
			name:        "all unions (lazy)",
			input:       []*ir.Type{{Name: "A", Kind: ir.TypeAmbiguousWire}, {Name: "B", Kind: ir.TypeAmbiguousWire}},
			wantStructs: 0,
			wantUnions:  2,
		},
		{
			name: "mixed -- the ReindexSourceSort case",
			input: []*ir.Type{
				{Name: "ReindexSource", Kind: ir.TypeStruct},
				{Name: "ReindexSourceSort", Kind: ir.TypeAmbiguousWire},
				{Name: "ReindexRemoteSource", Kind: ir.TypeStruct},
				{Name: "ReindexSourceSlice", Kind: ir.TypeStruct},
			},
			wantStructs: 3,
			wantUnions:  1,
		},
		{
			name: "mixed strict + lazy unions",
			input: []*ir.Type{
				{Name: "RequestSel", Kind: ir.TypeAmbiguousWire},
				{Name: "Struct", Kind: ir.TypeStruct},
				{Name: "Strict", Kind: ir.TypeUnion},
			},
			wantStructs: 1,
			wantUnions:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			structs, unions := emit.SplitUnionsFromSiblings(tt.input)
			require.Len(t, structs, tt.wantStructs, "structs")
			require.Len(t, unions, tt.wantUnions, "unions")

			// Verify the partition is exhaustive: every input ends up
			// in exactly one of the output slices.
			require.Equal(t, len(tt.input), len(structs)+len(unions), "partition exhaustive")

			// Verify no struct ended up in unions and vice versa.
			for _, s := range structs {
				require.NotEqual(t, ir.TypeUnion, s.Kind, "struct slice contains union %q", s.Name)
				require.NotEqual(t, ir.TypeAmbiguousWire, s.Kind, "struct slice contains lazy union %q", s.Name)
			}
			for _, u := range unions {
				require.Contains(t, []ir.TypeKind{ir.TypeUnion, ir.TypeAmbiguousWire}, u.Kind, "union slice contains non-union %q", u.Name)
			}
		})
	}
}

// TestParamTestCases pins the per-kind branching of paramTestCases: pointer
// kinds emit a second row for the zero value so a `!= 0`/`!= nil` guard
// regression that drops a deliberate 0/false is caught, while value-typed and
// string params collapse to the single happy-path case from paramTestValues.
func TestParamTestCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		param ir.QueryParam
		want  []emit.ParamTestCase
	}{
		{
			name:  "*int emits 42 and a deliberate 0",
			param: ir.QueryParam{GoName: "Version", WireName: "version", GoType: "*int", Kind: ir.ParamInt},
			want: []emit.ParamTestCase{
				{Name: "version", FieldAssign: "Version: func(i int) *int { return &i }(42)", WantAssign: `"version": "42"`},
				{Name: "version=0", FieldAssign: "Version: func(i int) *int { return &i }(0)", WantAssign: `"version": "0"`},
			},
		},
		{
			name:  "*bool emits true and false",
			param: ir.QueryParam{GoName: "Pretty", WireName: "pretty", GoType: "*bool", Kind: ir.ParamBool},
			want: []emit.ParamTestCase{
				{Name: "pretty=true", FieldAssign: "Pretty: func(b bool) *bool { return &b }(true)", WantAssign: `"pretty": "true"`},
				{Name: "pretty=false", FieldAssign: "Pretty: func(b bool) *bool { return &b }(false)", WantAssign: `"pretty": "false"`},
			},
		},
		{
			name:  "*float64 emits 1.5 and a deliberate 0",
			param: ir.QueryParam{GoName: "RequestsPerSecond", WireName: "requests_per_second", GoType: "*float64", Kind: ir.ParamFloat},
			want: []emit.ParamTestCase{
				{
					Name:        "requests_per_second",
					FieldAssign: "RequestsPerSecond: func(f float64) *float64 { return &f }(1.5)",
					WantAssign:  `"requests_per_second": "1.5"`,
				},
				{
					Name:        "requests_per_second=0",
					FieldAssign: "RequestsPerSecond: func(f float64) *float64 { return &f }(0)",
					WantAssign:  `"requests_per_second": "0"`,
				},
			},
		},
		{
			name:  "value-typed int stays a single case",
			param: ir.QueryParam{GoName: "Version", WireName: "version", GoType: "int", Kind: ir.ParamInt},
			want: []emit.ParamTestCase{
				{Name: "version", FieldAssign: "Version: 42", WantAssign: `"version": "42"`},
			},
		},
		{
			name:  "string param stays a single case",
			param: ir.QueryParam{GoName: "Routing", WireName: "routing", GoType: "string", Kind: ir.ParamString},
			want: []emit.ParamTestCase{
				{Name: "routing", FieldAssign: `Routing: "test-value"`, WantAssign: `"routing": "test-value"`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.ParamTestCases(tt.param))
		})
	}
}
