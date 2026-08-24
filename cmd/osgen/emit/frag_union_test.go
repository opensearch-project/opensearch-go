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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
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
	require.Contains(t, body, "u.value.(*SearchTotalHits)")
	require.Contains(t, body, "u.value.(*int64)")
}

func TestUnionFragment_TryEach(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "TryEachValue",
			Kind: ir.TypeAmbiguousWire,
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
	require.Contains(t, body, "func (u *TryEachValue) AsMap() (map[string]any, error)")
	require.Contains(t, body, "&UnionBranchError{Union: \"TryEachValue\", Want: \"AsMap\"")
	require.Contains(t, body, "u.value.(*map[string]any)")
	require.Contains(t, body, "if err := json.Unmarshal(data, &v); err == nil")
	require.Contains(t, body, "RawJSON")
}

// A try-each union decodes by probing required keys, so a branch whose key set
// duplicates an earlier branch's is queued behind it and the generator records that
// in ProbeCollisionBranches. The type's own doc comment has to say so: the branch stays
// constructible and marshals correctly, only Type() can never report it. Without
// these assertions the whole feature can be deleted with the suite still green.
func TestUnionFragment_TryEachProbeCollisionCaveat(t *testing.T) {
	t.Parallel()

	branches := func(firstKey, secondKey string) []ir.UnionBranch {
		return []ir.UnionBranch{
			{Name: "First", GoType: "FirstShape", TokenClass: ir.TokenObject, Required: []string{firstKey}},
			{Name: "Second", GoType: "SecondShape", TokenClass: ir.TokenObject, Required: []string{secondKey}},
		}
	}

	tests := []struct {
		name    string
		typ     *ir.Type
		want    []string
		notWant []string
	}{
		{
			name: "colliding branch is named on the type",
			typ: &ir.Type{
				Name:                   "CollidingValue",
				Kind:                   ir.TypeAmbiguousWire,
				Branches:               branches("field", "field"),
				ProbeCollisionBranches: []string{"Second (same required keys as First)"},
			},
			want: []string{
				"// The branches below are probed only after an earlier branch that declares the",
				"read RawJSON() when you need the payload exactly as it arrived, and",
				// Rendered as a godoc list item, not interpolated into the prose line.
				"//   - Second (same required keys as First)",
				// The caveat sits in the doc block, so it must stay attached to the type.
				"//   - Second (same required keys as First)\ntype CollidingValue struct",
				// Both branches keep their full surface: only Type() is affected.
				"func NewCollidingValueFromSecond(v SecondShape) CollidingValue",
				"func (u *CollidingValue) Second() (SecondShape, error)",
			},
		},
		{
			name: "branches with distinct required keys carry no caveat",
			typ: &ir.Type{
				Name:     "PlainValue",
				Kind:     ir.TypeAmbiguousWire,
				Branches: branches("a", "b"),
			},
			notWant: []string{"probed only after an earlier branch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := (&emit.UnionFragment{Types: []*ir.Type{tt.typ}}).Body()
			require.NoError(t, err)

			for _, want := range tt.want {
				require.Contains(t, body, want)
			}
			for _, notWant := range tt.notWant {
				require.NotContains(t, body, notWant)
			}
		})
	}
}

func TestUnionFragment_MergedDecode(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "DocsItem",
			Kind: ir.TypeAmbiguousWire,
			Branches: []ir.UnionBranch{
				{Name: "GetResult", GoType: "GetResult", TokenClass: ir.TokenObject},
				{Name: "MultiGetError", GoType: "MultiGetError", TokenClass: ir.TokenObject, Required: []string{"error"}},
			},
			Merge: &ir.UnionMerge{
				PrimaryGoType: "GetResult",
				PrimaryConst:  "DocsItemGetResultType",
				PrimaryName:   "GetResult",
				Probes:        []ir.MergeProbe{{GoName: "Disc0", JSONKey: "error"}},
				Branches: []ir.MergeBranch{
					{GoType: "MultiGetError", Const: "DocsItemMultiGetErrorType", Name: "MultiGetError", PresentProbes: []string{"Disc0"}},
				},
			},
		},
	}

	body, err := (&emit.UnionFragment{Types: types}).Body()
	require.NoError(t, err)

	// Single-pass merge: embeds the primary, probes for the discriminator,
	// and never calls the try-each HasJSONKeys probe.
	require.Contains(t, body, "type merged struct")
	require.Contains(t, body, "GetResult\n") // embedded primary
	require.Contains(t, body, "Disc0 json.RawMessage `json:\"error\"`")
	require.Contains(t, body, "if len(m.Disc0) > 0 {")
	require.Contains(t, body, "u.value = &m.GetResult")
	require.NotContains(t, body, "build.HasJSONKeys")
	require.NotContains(t, body, "append(u.raw")
	require.Contains(t, body, "u.raw = data")
}

func TestUnionFragment_RequestSelected(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name:            "AggValue",
			Kind:            ir.TypeAmbiguousWire,
			RequestSelected: true,
			Branches: []ir.UnionBranch{
				{Name: "Avg", GoType: "AvgAggregate", TokenClass: ir.TokenObject},
				{Name: "Sum", GoType: "SumAggregate", TokenClass: ir.TokenObject},
			},
		},
	}

	body, err := (&emit.UnionFragment{Types: types}).Body()
	require.NoError(t, err)

	// Request-selected: UnmarshalJSON only aliases raw; per-branch As<T>() decode on demand.
	require.Contains(t, body, "func (u *AggValue) AsAvg() (AvgAggregate, error)")
	require.Contains(t, body, "func (u *AggValue) AsSum() (SumAggregate, error)")
	require.Contains(t, body, "err := json.Unmarshal(u.raw, &v)")
	require.Contains(t, body, "u.raw = data")
	require.NotContains(t, body, "build.HasJSONKeys")
	require.NotContains(t, body, "no branch matched")
}

func TestUnionFragment_Imports(t *testing.T) {
	t.Parallel()

	// crossPkgRegistry is shared across the cross-pkg cases: the
	// branch GoType "shared.FieldSort" lives in the core package, so
	// the registry's CoreImport must be set for hasCrossPkgBranch to
	// flag it.
	crossPkgRegistry := ir.NewTypeRegistry("opensearchapi",
		"github.com/opensearch-project/opensearch-go/v5/opensearchapi")
	crossPkgRegistry.Register(&ir.Type{
		Name:      "FieldSort",
		SchemaRef: "#/test/FieldSort",
		Scope:     ir.ScopeShared,
	})

	tests := []struct {
		name           string
		op             *ir.Operation
		registry       *ir.TypeRegistry
		types          []*ir.Type
		wantFmt        bool
		wantCoreImport bool
	}{
		{
			name:    "strict needs fmt",
			types:   []*ir.Type{{Kind: ir.TypeUnion, Branches: []ir.UnionBranch{{TokenClass: ir.TokenObject}}}},
			wantFmt: true,
		},
		{
			name:    "try-each needs fmt",
			types:   []*ir.Type{{Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{{TokenClass: ir.TokenObject}}}},
			wantFmt: true,
		},
		{
			name:    "no types yields no imports",
			types:   nil,
			wantFmt: false,
		},
		{
			name:     "plugin op with cross-pkg branch adds core import",
			op:       &ir.Operation{IsPlugin: true},
			registry: crossPkgRegistry,
			types: []*ir.Type{{
				Name:     "PluginUnion",
				Kind:     ir.TypeUnion,
				Branches: []ir.UnionBranch{{Name: "Sort", GoType: "FieldSort"}},
			}},
			wantFmt:        true,
			wantCoreImport: true,
		},
		{
			name:     "plugin op without cross-pkg branch omits core import",
			op:       &ir.Operation{IsPlugin: true},
			registry: crossPkgRegistry,
			types: []*ir.Type{{
				Name:     "LocalUnion",
				Kind:     ir.TypeUnion,
				Branches: []ir.UnionBranch{{Name: "Local", GoType: "LocalOnlyType"}},
			}},
			wantFmt:        true,
			wantCoreImport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			frag := &emit.UnionFragment{Op: tt.op, Types: tt.types, Registry: tt.registry}
			imps := frag.Imports()

			hasFmt := false
			hasJSON := false
			hasCore := false
			for _, imp := range imps {
				switch imp.Path {
				case "fmt":
					hasFmt = true
				case "encoding/json":
					hasJSON = true
				case "github.com/opensearch-project/opensearch-go/v5/opensearchapi":
					hasCore = true
				}
			}
			if len(tt.types) == 0 {
				require.Empty(t, imps, "no types should yield no imports")
				return
			}
			require.True(t, hasJSON, "all union fragments need encoding/json")
			require.Equal(t, tt.wantFmt, hasFmt, "fmt import mismatch")
			require.Equal(t, tt.wantCoreImport, hasCore, "core import mismatch")
		})
	}
}

func TestQuotedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []string
		want string
	}{
		{name: "empty slice", keys: nil, want: ""},
		{name: "single key", keys: []string{"error"}, want: `"error"`},
		{name: "multiple keys", keys: []string{"index", "type", "id"}, want: `"index", "type", "id"`},
		{name: "key with embedded quote", keys: []string{`he said "hi"`}, want: `"he said \"hi\""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.QuotedKeys(tt.keys))
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
