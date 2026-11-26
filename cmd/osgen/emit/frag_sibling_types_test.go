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

func siblingKeysType(valueType string) *ir.Type {
	return &ir.Type{
		Name: "Own", Kind: ir.TypeStruct, Scope: ir.ScopeLocal,
		Fields:    []ir.Field{{GoName: "Boost", JSONName: "boost", GoType: "float32", IsPointer: true, OmitEmpty: true}},
		ExtraKeys: &ir.ExtraKeys{GoName: "Extra", ValueType: valueType, Known: []string{"boost"}},
	}
}

func siblingRegistry() *ir.TypeRegistry {
	reg := newRegistry()
	regType(reg, &ir.Type{Name: "SharedVal", Kind: ir.TypeStruct, Scope: ir.ScopeShared})
	return reg
}

func TestSiblingTypesFragment_Imports(t *testing.T) {
	t.Parallel()

	keysImports := []emit.Import{{Path: "fmt"}, {Path: emit.JSONImportPath, Alias: "json"}, {Path: "strings"}}
	coreImport := emit.Import{Path: "github.com/opensearch-project/opensearch-go/v5/opensearchapi"}

	tests := []struct {
		name     string
		isPlugin bool
		types    []*ir.Type
		want     []emit.Import
	}{
		{
			name:  "type with extra keys",
			types: []*ir.Type{siblingKeysType("string")},
			want:  keysImports,
		},
		{
			name: "type embedding an extra-keys type",
			types: []*ir.Type{
				siblingKeysType("string"),
				{Name: "Outer", Kind: ir.TypeStruct, Fields: []ir.Field{{GoType: "Own", IsEmbed: true}}},
			},
			want: keysImports,
		},
		{
			name: "plain types",
			types: []*ir.Type{
				{Name: "Plain", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "string"}}},
			},
			want: nil,
		},
		{
			name: "raw message field",
			types: []*ir.Type{
				{Name: "Raw", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "json.RawMessage"}}},
			},
			want: []emit.Import{{Path: emit.JSONImportPath, Alias: "json"}},
		},
		{
			name:     "plugin field of a shared type",
			isPlugin: true,
			types: []*ir.Type{
				{Name: "Plain", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "[]SharedVal"}}},
			},
			want: []emit.Import{coreImport},
		},
		{
			name:     "plugin extra keys of a shared value type",
			isPlugin: true,
			types:    []*ir.Type{siblingKeysType("SharedVal")},
			want:     keysImports,
		},
		{
			name: "core package ignores shared field types",
			types: []*ir.Type{
				{Name: "Plain", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "SharedVal"}}},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			frag := &emit.SiblingTypesFragment{
				Op:       &ir.Operation{Group: "widgets", IsPlugin: tt.isPlugin},
				Types:    tt.types,
				Registry: siblingRegistry(),
			}
			require.Equal(t, tt.want, frag.Imports())
		})
	}
}

func TestSiblingTypesFragment_ExtraKeysBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		isPlugin bool
		types    []*ir.Type
		contains []string
		absent   []string
	}{
		{
			name:  "no types",
			types: nil,
		},
		{
			name:  "extra keys field and codec",
			types: []*ir.Type{siblingKeysType("SharedVal")},
			contains: []string{
				"type Own struct {",
				"Extra map[string]SharedVal `json:\"-\"`",
				"func (v Own) MarshalJSON() ([]byte, error)",
				"func (v *Own) UnmarshalJSON(data []byte) error",
				`[...]string{"boost"}`,
			},
			absent: []string{"opensearchapi."},
		},
		{
			name:     "plugin qualifies a shared value type",
			isPlugin: true,
			types:    []*ir.Type{siblingKeysType("SharedVal")},
			contains: []string{
				"Extra map[string]opensearchapi.SharedVal `json:\"-\"`",
				"func (v Own) MarshalJSON() ([]byte, error)",
				"var val opensearchapi.SharedVal",
			},
		},
		{
			name:     "plugin leaves a non-shared value type",
			isPlugin: true,
			types:    []*ir.Type{siblingKeysType("string")},
			contains: []string{"Extra map[string]string `json:\"-\"`"},
			absent:   []string{"opensearchapi."},
		},
		{
			name:  "plain type has no codec",
			types: []*ir.Type{{Name: "Plain", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "string"}}}},
			contains: []string{
				"type Plain struct {",
			},
			absent: []string{"MarshalJSON", "UnmarshalJSON"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			frag := &emit.SiblingTypesFragment{
				Op:       &ir.Operation{Group: "widgets", IsPlugin: tt.isPlugin},
				Types:    tt.types,
				Registry: siblingRegistry(),
			}
			body, err := frag.Body()
			require.NoError(t, err)
			if len(tt.types) == 0 {
				require.Empty(t, body)
			}
			for _, s := range tt.contains {
				require.Contains(t, body, s)
			}
			for _, s := range tt.absent {
				require.NotContains(t, body, s)
			}
		})
	}
}

func TestSharedTypesFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		types []*ir.Type
		want  []emit.Import
	}{
		{
			name:  "raw message field only",
			types: []*ir.Type{{Name: "Raw", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "json.RawMessage"}}}},
			want:  []emit.Import{{Path: emit.JSONImportPath, Alias: "json"}},
		},
		{
			name:  "plain fields",
			types: []*ir.Type{{Name: "Plain", Kind: ir.TypeStruct, Fields: []ir.Field{{GoName: "A", JSONName: "a", GoType: "string"}}}},
			want:  nil,
		},
		{
			name:  "no types",
			types: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			frag := &emit.SharedTypesFragment{Types: tt.types}
			require.Equal(t, tt.want, frag.Imports())
			body, err := frag.Body()
			require.NoError(t, err)
			if tt.types == nil {
				require.Empty(t, body)
			}
		})
	}
}
