// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
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
			input:       []*ir.Type{{Name: "A", Kind: ir.TypeLazyUnion}, {Name: "B", Kind: ir.TypeLazyUnion}},
			wantStructs: 0,
			wantUnions:  2,
		},
		{
			name: "mixed -- the ReindexSourceSort case",
			input: []*ir.Type{
				{Name: "ReindexSource", Kind: ir.TypeStruct},
				{Name: "ReindexSourceSort", Kind: ir.TypeLazyUnion},
				{Name: "ReindexRemoteSource", Kind: ir.TypeStruct},
				{Name: "ReindexSourceSlice", Kind: ir.TypeStruct},
			},
			wantStructs: 3,
			wantUnions:  1,
		},
		{
			name: "mixed strict + lazy unions",
			input: []*ir.Type{
				{Name: "Lazy", Kind: ir.TypeLazyUnion},
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
				require.NotEqual(t, ir.TypeLazyUnion, s.Kind, "struct slice contains lazy union %q", s.Name)
			}
			for _, u := range unions {
				require.Contains(t, []ir.TypeKind{ir.TypeUnion, ir.TypeLazyUnion}, u.Kind, "union slice contains non-union %q", u.Name)
			}
		})
	}
}
