// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestUnionFragment_Strict(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "TotalHits",
			Kind: ir.TypeUnion,
			Branches: []ir.UnionBranch{
				{Name: "SearchTotalHits", GoType: "SearchTotalHits", TokenClass: ir.TokenObject},
				{Name: "Int64", GoType: "int64", TokenClass: ir.TokenNumber},
			},
		},
	}

	frag := &emit.UnionFragment{Types: types}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type TotalHits struct")
	require.Contains(t, body, "value any")
	require.Contains(t, body, "TotalHitsType")
	require.Contains(t, body, "TotalHitsSearchTotalHitsType")
	require.Contains(t, body, "TotalHitsInt64Type")
	require.Contains(t, body, "case data[0] == '{'")
	require.Contains(t, body, "case data[0] >= '0'")
	require.Contains(t, body, "MarshalJSON")
	require.Contains(t, body, "u.value.(SearchTotalHits)")
	require.Contains(t, body, "u.value.(int64)")
}

func TestUnionFragment_TryEach(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "TryEachValue",
			Kind: ir.TypeLazyUnion,
			Branches: []ir.UnionBranch{
				{Name: "AsMap", GoType: "map[string]any", TokenClass: ir.TokenObject},
				{Name: "AsSlice", GoType: "[]any", TokenClass: ir.TokenArray},
			},
		},
	}

	frag := &emit.UnionFragment{Types: types}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type TryEachValue struct")
	require.Contains(t, body, "value any")
	require.Contains(t, body, "TryEachValueType")
	require.Contains(t, body, "func (u *TryEachValue) AsMap() map[string]any")
	require.Contains(t, body, "u.value.(map[string]any)")
	require.Contains(t, body, "if err := json.Unmarshal(data, &v); err == nil")
	require.Contains(t, body, "RawJSON")
}

func TestUnionFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		types   []*ir.Type
		wantFmt bool
	}{
		{
			name:    "strict needs fmt",
			types:   []*ir.Type{{Kind: ir.TypeUnion, Branches: []ir.UnionBranch{{TokenClass: ir.TokenObject}}}},
			wantFmt: true,
		},
		{
			name:    "try-each needs fmt",
			types:   []*ir.Type{{Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{{TokenClass: ir.TokenObject}}}},
			wantFmt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			frag := &emit.UnionFragment{Types: tt.types}
			imps := frag.Imports()

			hasFmt := false
			hasJSON := false
			for _, imp := range imps {
				if imp.Path == "fmt" {
					hasFmt = true
				}
				if imp.Path == "encoding/json" {
					hasJSON = true
				}
			}
			require.True(t, hasJSON, "all union fragments need encoding/json")
			require.Equal(t, tt.wantFmt, hasFmt, "fmt import mismatch")
		})
	}
}

func TestUnionFragment_FileAssembly(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name:  "TotalHits",
			Kind:  ir.TypeUnion,
			Scope: ir.ScopeShared,
			Branches: []ir.UnionBranch{
				{Name: "Object", GoType: "TotalHitsObject", TokenClass: ir.TokenObject},
				{Name: "Int64", GoType: "int64", TokenClass: ir.TokenNumber},
			},
		},
	}

	target := emit.NewUnionTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package "+ir.DefaultCorePkgName)
	require.Contains(t, output, `"encoding/json"`)
	require.Contains(t, output, `"fmt"`)

	jsonIdx := strings.Index(output, `"encoding/json"`)
	fmtIdx := strings.Index(output, `"fmt"`)
	require.Positive(t, jsonIdx)
	require.Positive(t, fmtIdx)
}

func TestSharedTypesFragment_Body(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "ShardStatistics",
			Kind: ir.TypeStruct,
			Fields: []ir.Field{
				{GoName: "Total", JSONName: "total", GoType: "int"},
				{GoName: "Successful", JSONName: "successful", GoType: "int"},
				{GoName: "Failed", JSONName: "failed", GoType: "int"},
			},
		},
	}

	frag := &emit.SharedTypesFragment{Types: types}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type ShardStatistics struct")
	require.Contains(t, body, `json:"total"`)
}

func TestNewSharedTypesFile_FiltersStructs(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{Name: "StructType", Kind: ir.TypeStruct, Scope: ir.ScopeShared},
		{Name: "LocalStruct", Kind: ir.TypeStruct, Scope: ir.ScopeLocal},
		{Name: "UnionType", Kind: ir.TypeUnion, Scope: ir.ScopeShared},
	}

	target := emit.NewSharedTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "StructType")
	require.NotContains(t, output, "LocalStruct")
	require.NotContains(t, output, "UnionType")
}
