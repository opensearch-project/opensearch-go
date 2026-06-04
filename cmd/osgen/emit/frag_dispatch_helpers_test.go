// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/errwrap"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// regType is a small helper that registers an ir.Type in the test
// registry with a unique SchemaRef so multi-type fixtures don't
// collide on the empty-string default.
func regType(reg *ir.TypeRegistry, t *ir.Type) {
	if t.SchemaRef == "" {
		t.SchemaRef = "test/" + t.Name
	}
	reg.Register(t)
}

// ---------------------------------------------------------------------------
// Scalar mappings: pure (string -> string) helpers
// ---------------------------------------------------------------------------

func TestWriteOperationConst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		group string
		want  string
	}{
		// Single-doc write groups map to the OperationXxx constants used
		// in ShardFailureError.Operation.
		{group: errwrap.GroupIndex, want: errwrap.WriteOpIndex},
		{group: errwrap.GroupCreate, want: errwrap.WriteOpCreate},
		{group: errwrap.GroupUpdate, want: errwrap.WriteOpUpdate},
		{group: errwrap.GroupDelete, want: errwrap.WriteOpDelete},

		// Dotted groups (e.g. "document.create") use the trailing
		// segment after the last "." as the lookup key.
		{group: "document.create", want: errwrap.WriteOpCreate},
		{group: "document.delete", want: errwrap.WriteOpDelete},

		// Unknown groups produce the empty string -- the caller's
		// template emits that and ends up with `Operation: ` (an
		// invalid identifier), which would surface as a build break.
		// That's fine for tests; we just confirm the mapping is empty.
		{group: "unknown", want: ""},
		{group: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.WriteOperationConst(tt.group))
		})
	}
}

func TestPerOpErrorTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		group string
		want  string
	}{
		// Multi-wrapper ops get a per-op error container.
		{group: errwrap.GroupMSearch, want: "MSearchErrors"},
		{group: errwrap.GroupMSearchTemplate, want: "MSearchTemplateErrors"},

		// Single-wrapper / unknown groups get "" -- the dispatch
		// template emits `nil` as the wrap closure in that case.
		{group: errwrap.GroupBulk, want: ""},
		{group: errwrap.GroupSearch, want: ""},
		{group: "unknown", want: ""},
		{group: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.PerOpErrorTypeName(tt.group))
		})
	}
}

func TestWrapperMethodName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wrapper string
		want    string
	}{
		// Standard rule: trim trailing "s", append "Failures".
		{wrapper: errwrap.WrapperBulkItems, want: "BulkItemFailures"},
		{wrapper: errwrap.WrapperSearchShards, want: "SearchShardFailures"},
		{wrapper: errwrap.WrapperWriteShards, want: "WriteShardFailures"},
		{wrapper: errwrap.WrapperBroadcastShards, want: "BroadcastShardFailures"},
		{wrapper: errwrap.WrapperMultiSearchItems, want: "MultiSearchItemFailures"},

		// Wrappers already ending in "Failures" keep their suffix.
		{wrapper: errwrap.WrapperNodeFailures, want: "NodeFailures"},
		{wrapper: errwrap.WrapperBulkByScrollFailures, want: "BulkByScrollFailures"},
	}

	for _, tt := range tests {
		t.Run(tt.wrapper, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.WrapperMethodName(tt.wrapper))
		})
	}
}

// ---------------------------------------------------------------------------
// Field-walk helpers: lookupResponseField, shardsIsPointer
// ---------------------------------------------------------------------------

func TestLookupResponseField(t *testing.T) {
	t.Parallel()

	// Embedded type registered for the embed-walking case.
	reg := newRegistry()
	embeddedType := &ir.Type{
		Name:  "EmbeddedShards",
		Scope: ir.ScopeLocal,
		Fields: []ir.Field{
			{GoName: "Shards", GoType: "ShardStatistics"},
		},
	}
	regType(reg, embeddedType)

	tests := []struct {
		name     string
		resp     *ir.Type
		goName   string
		wantOK   bool
		wantType string // GoType when found
	}{
		{
			name:   "nil resp returns false",
			resp:   nil,
			goName: "Shards",
			wantOK: false,
		},
		{
			name: "direct field match",
			resp: newRespType("X",
				ir.Field{GoName: "Shards", GoType: "ShardStatistics"},
			),
			goName:   "Shards",
			wantOK:   true,
			wantType: "ShardStatistics",
		},
		{
			name: "missing field returns false",
			resp: newRespType("X",
				ir.Field{GoName: "Foo", GoType: "string"},
			),
			goName: "Shards",
			wantOK: false,
		},
		{
			name: "embed-walked field match",
			resp: newRespType("X",
				ir.Field{GoType: "EmbeddedShards", IsEmbed: true},
			),
			goName:   "Shards",
			wantOK:   true,
			wantType: "ShardStatistics",
		},
		{
			name: "embed without registry hit returns false",
			resp: newRespType("X",
				ir.Field{GoType: "MissingFromRegistry", IsEmbed: true},
			),
			goName: "Shards",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := emit.LookupResponseField(tt.resp, tt.goName, reg)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantType, got.GoType)
			}
		})
	}
}

func TestShardsIsPointer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp *ir.Type
		want bool
	}{
		{
			name: "missing Shards field -> false (Applies guard would have skipped)",
			resp: newRespType("X",
				ir.Field{GoName: "Other", GoType: "int"},
			),
			want: false,
		},
		{
			name: "value-typed Shards -> false",
			resp: newRespType("X",
				ir.Field{GoName: "Shards", GoType: "ShardStatistics", IsPointer: false},
			),
			want: false,
		},
		{
			name: "pointer-typed Shards -> true (omitempty case)",
			resp: newRespType("X",
				ir.Field{GoName: "Shards", GoType: "*ShardStatistics", IsPointer: true},
			),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.ShardsIsPointer(tt.resp, newRegistry()))
		})
	}
}

// ---------------------------------------------------------------------------
// Element-type helpers: elementTypeHasShards
// ---------------------------------------------------------------------------

func TestElementTypeHasShards(t *testing.T) {
	t.Parallel()

	reg := newRegistry()

	// Plain struct with Shards.
	regType(reg, &ir.Type{
		Name:   "ShardItem",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
	})
	// Plain struct without Shards.
	regType(reg, &ir.Type{
		Name:   "PlainItem",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Foo", GoType: "string"}},
	})
	// Discriminated union: one branch has Shards, the other has Status+Error.
	regType(reg, &ir.Type{
		Name:   "ShardBranch",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
	})
	regType(reg, &ir.Type{
		Name:  "ErrBranch",
		Scope: ir.ScopeLocal,
		Fields: []ir.Field{
			{GoName: "Status", GoType: "int"},
			{GoName: "Error", GoType: "ErrorCause"},
		},
	})
	regType(reg, &ir.Type{
		Name:  "ItemUnion",
		Scope: ir.ScopeLocal,
		Kind:  ir.TypeLazyUnion,
		Branches: []ir.UnionBranch{
			{Name: "ShardBranch", GoType: "ShardBranch"},
			{Name: "ErrBranch", GoType: "ErrBranch"},
		},
	})

	tests := []struct {
		name   string
		goType string
		want   bool
	}{
		{name: "slice of Shards-bearing struct", goType: "[]ShardItem", want: true},
		{name: "slice of plain struct", goType: "[]PlainItem", want: false},
		{name: "pointer to Shards-bearing struct", goType: "*ShardItem", want: true},
		{name: "slice of pointer to Shards-bearing struct", goType: "[]*ShardItem", want: true},
		{name: "slice of union with Shards-bearing branch", goType: "[]ItemUnion", want: true},
		{name: "unknown type", goType: "[]NotRegistered", want: false},
		{name: "nil registry", goType: "[]ShardItem", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := reg
			if tt.name == "nil registry" {
				r = nil
			}
			require.Equal(t, tt.want, emit.ElementTypeHasShards(tt.goType, r))
		})
	}
}

// ---------------------------------------------------------------------------
// Applicability predicates
// ---------------------------------------------------------------------------

func TestApplicabilityPredicates(t *testing.T) {
	t.Parallel()

	reg := newRegistry()
	regType(reg, &ir.Type{
		Name:   "ShardItem",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
	})

	tests := []struct {
		name      string
		predicate func(*ir.Type, *ir.TypeRegistry) bool
		resp      *ir.Type
		want      bool
	}{
		{
			name:      "applyHasShards: present",
			predicate: emit.ApplyHasShards,
			resp:      shardsFixtureResp("X"),
			want:      true,
		},
		{
			name:      "applyHasShards: absent",
			predicate: emit.ApplyHasShards,
			resp:      newRespType("X"),
			want:      false,
		},
		{
			name:      "applyBulkItems: needs both Errors and Items",
			predicate: emit.ApplyBulkItems,
			resp:      bulkFixtureResp(),
			want:      true,
		},
		{
			name:      "applyBulkItems: missing Items",
			predicate: emit.ApplyBulkItems,
			resp:      newRespType("X", ir.Field{GoName: "Errors", GoType: "bool"}),
			want:      false,
		},
		{
			name:      "applyBulkItems: missing Errors",
			predicate: emit.ApplyBulkItems,
			resp:      newRespType("X", ir.Field{GoName: "Items", GoType: "[]Foo"}),
			want:      false,
		},
		{
			name:      "applySearchShards: top-level Shards present",
			predicate: emit.ApplySearchShards,
			resp:      shardsFixtureResp("X"),
			want:      true,
		},
		{
			name:      "applySearchShards: Responses element has Shards (msearch shape)",
			predicate: emit.ApplySearchShards,
			resp:      newRespType("X", ir.Field{GoName: "Responses", GoType: "[]ShardItem"}),
			want:      true,
		},
		{
			name:      "applySearchShards: neither -> false",
			predicate: emit.ApplySearchShards,
			resp:      newRespType("X"),
			want:      false,
		},
		{
			name:      "applyMultiSearchItems: Responses with Shards element",
			predicate: emit.ApplyMultiSearchItems,
			resp:      newRespType("X", ir.Field{GoName: "Responses", GoType: "[]ShardItem"}),
			want:      true,
		},
		{
			name:      "applyMultiSearchItems: missing Responses -> false",
			predicate: emit.ApplyMultiSearchItems,
			resp:      shardsFixtureResp("X"),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.predicate(tt.resp, reg))
		})
	}
}

// ---------------------------------------------------------------------------
// Union-shape resolution
// ---------------------------------------------------------------------------

func TestResolveUnionShape(t *testing.T) {
	t.Parallel()

	reg := newRegistry()

	// Branch types.
	regType(reg, &ir.Type{
		Name:   "ShardBranch",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
	})
	regType(reg, &ir.Type{
		Name:  "ErrBranch",
		Scope: ir.ScopeLocal,
		Fields: []ir.Field{
			{GoName: "Status", GoType: "int"},
			{GoName: "Error", GoType: "ErrorCause"},
		},
	})
	regType(reg, &ir.Type{
		Name:   "PlainBranch",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Foo", GoType: "string"}},
	})

	tests := []struct {
		name            string
		input           *ir.Type
		wantUnionName   string
		wantSuccess     string
		wantErrorBranch string
	}{
		{
			name:  "non-union returns zero-value",
			input: &ir.Type{Name: "Plain", Kind: ir.TypeStruct},
		},
		{
			name:  "nil input returns zero-value",
			input: nil,
		},
		{
			name: "union with Shards + Status/Error branches",
			input: &ir.Type{
				Name: "ItemUnion",
				Kind: ir.TypeLazyUnion,
				Branches: []ir.UnionBranch{
					{Name: "ShardBranch", GoType: "ShardBranch"},
					{Name: "ErrBranch", GoType: "ErrBranch"},
				},
			},
			wantUnionName:   "ItemUnion",
			wantSuccess:     "ShardBranch",
			wantErrorBranch: "ErrBranch",
		},
		{
			name: "union with only success branch",
			input: &ir.Type{
				Name: "OnlyShard",
				Kind: ir.TypeLazyUnion,
				Branches: []ir.UnionBranch{
					{Name: "ShardBranch", GoType: "ShardBranch"},
					{Name: "PlainBranch", GoType: "PlainBranch"},
				},
			},
			wantUnionName: "OnlyShard",
			wantSuccess:   "ShardBranch",
		},
		{
			name: "branch type not in registry is skipped",
			input: &ir.Type{
				Name: "Mixed",
				Kind: ir.TypeLazyUnion,
				Branches: []ir.UnionBranch{
					{Name: "Missing", GoType: "Missing"},
					{Name: "ShardBranch", GoType: "ShardBranch"},
				},
			},
			wantUnionName: "Mixed",
			wantSuccess:   "ShardBranch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotUnion, gotSucc, gotErr := emit.ResolveUnionShape(tt.input, reg)
			require.Equal(t, tt.wantUnionName, gotUnion)
			require.Equal(t, tt.wantSuccess, gotSucc)
			require.Equal(t, tt.wantErrorBranch, gotErr)
		})
	}
}

func TestUnionFromResponses(t *testing.T) {
	t.Parallel()

	reg := newRegistry()
	regType(reg, &ir.Type{
		Name:   "Item",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
	})
	regType(reg, &ir.Type{
		Name:  "MsearchItemUnion",
		Scope: ir.ScopeLocal,
		Kind:  ir.TypeLazyUnion,
		Branches: []ir.UnionBranch{
			{Name: "Item", GoType: "Item"},
		},
	})

	tests := []struct {
		name          string
		resp          *ir.Type
		wantUnionName string
	}{
		{
			name: "resp without Responses field",
			resp: shardsFixtureResp("X"),
		},
		{
			name: "Responses is plain slice (not a union)",
			resp: newRespType("X", ir.Field{GoName: "Responses", GoType: "[]Item"}),
		},
		{
			name:          "Responses is slice of union",
			resp:          newRespType("X", ir.Field{GoName: "Responses", GoType: "[]MsearchItemUnion"}),
			wantUnionName: "MsearchItemUnion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			unionName, _, _ := emit.UnionFromResponses(tt.resp, reg)
			require.Equal(t, tt.wantUnionName, unionName)
		})
	}
}

func TestBulkInnerItemType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resp     *ir.Type
		regBuild func(reg *ir.TypeRegistry)
		nilReg   bool
		wantName string
		wantOK   bool
	}{
		{
			name:   "nil registry returns false",
			resp:   newRespType("Bulk", ir.Field{GoName: "Items", GoType: "[]BulkItem"}),
			nilReg: true,
		},
		{
			name: "missing Items field returns false",
			resp: newRespType("Bulk", ir.Field{GoName: "Errors", GoType: "bool"}),
		},
		{
			name: "outer element type missing from registry returns false",
			resp: newRespType("Bulk", ir.Field{GoName: "Items", GoType: "[]MissingOuter"}),
		},
		{
			name: "outer with no pointer fields returns false",
			resp: newRespType("Bulk", ir.Field{GoName: "Items", GoType: "[]NoPointers"}),
			regBuild: func(reg *ir.TypeRegistry) {
				regType(reg, &ir.Type{
					Name:   "NoPointers",
					Scope:  ir.ScopeLocal,
					Fields: []ir.Field{{GoName: "Status", GoType: "int"}},
				})
			},
		},
		{
			name: "first pointer field's target wins",
			resp: newRespType("Bulk", ir.Field{GoName: "Items", GoType: "[]BulkItem"}),
			regBuild: func(reg *ir.TypeRegistry) {
				regType(reg, &ir.Type{
					Name:  "BulkItem",
					Scope: ir.ScopeLocal,
					Fields: []ir.Field{
						{GoName: "Errors", GoType: "bool", IsPointer: false},
						{GoName: "Index", GoType: "*BulkRespItem", IsPointer: true},
						{GoName: "Update", GoType: "*BulkRespItem", IsPointer: true},
					},
				})
			},
			wantName: "BulkRespItem",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var reg *ir.TypeRegistry
			if !tt.nilReg {
				reg = newRegistry()
				if tt.regBuild != nil {
					tt.regBuild(reg)
				}
			}
			got, ok := emit.BulkInnerItemType(tt.resp, reg)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantName, got)
		})
	}
}

func TestUnionFromResponses_FallbackBranches(t *testing.T) {
	t.Parallel()

	reg := newRegistry()
	regType(reg, &ir.Type{
		Name:   "ItemNotUnion",
		Scope:  ir.ScopeLocal,
		Fields: []ir.Field{{GoName: "Status", GoType: "int"}},
	})

	tests := []struct {
		name string
		resp *ir.Type
	}{
		{
			name: "Responses element type missing from registry",
			resp: newRespType("X", ir.Field{GoName: "Responses", GoType: "[]MissingType"}),
		},
		{
			name: "Responses element type is registered but not a union",
			resp: newRespType("X", ir.Field{GoName: "Responses", GoType: "[]ItemNotUnion"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			unionName, success, errBranch := emit.UnionFromResponses(tt.resp, reg)
			require.Empty(t, unionName)
			require.Empty(t, success)
			require.Empty(t, errBranch)
		})
	}
}
