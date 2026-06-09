// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestResolveUnionType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schema     *openapi3.Schema
		schemaKey  string
		wantName   string
		wantLazy   bool
		wantCount  int
		wantBranch []string // expected branch names
	}{
		{
			name: "object and primitive",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{
						Ref:   "#/components/schemas/_common___TotalHits",
						Value: &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{"value": {Value: openapi3.NewInt64Schema()}}},
					},
					{Value: openapi3.NewInt64Schema()},
				},
			},
			schemaKey:  "_common___HitsTotal",
			wantName:   "HitsTotal",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"TotalHits", "Int64"},
		},
		{
			name: "string and integer",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: openapi3.NewStringSchema()},
					{Value: openapi3.NewIntegerSchema()},
				},
			},
			schemaKey:  "test___MixedField",
			wantName:   "TestMixedField",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"String", "Int"},
		},
		{
			name: "bool and integer",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: openapi3.NewBoolSchema()},
					{Value: openapi3.NewIntegerSchema()},
				},
			},
			schemaKey:  "test___TrackHits",
			wantName:   "TestTrackHits",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"Bool", "Int"},
		},
		{
			name: "two objects same token class is lazy",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{
						Ref:   "#/components/schemas/test___TypeA",
						Value: &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{"a": {Value: openapi3.NewStringSchema()}}},
					},
					{
						Ref:   "#/components/schemas/test___TypeB",
						Value: &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{"b": {Value: openapi3.NewStringSchema()}}},
					},
				},
			},
			schemaKey:  "test___AOrB",
			wantName:   "TestAOrB",
			wantLazy:   true,
			wantCount:  2,
			wantBranch: []string{"TestTypeA", "TestTypeB"},
		},
		{
			name: "anyOf treated like oneOf",
			schema: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: openapi3.NewStringSchema()},
					{Value: openapi3.NewBoolSchema()},
				},
			},
			schemaKey:  "test___AnyField",
			wantName:   "TestAnyField",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"String", "Bool"},
		},
		{
			// int and int64 decode from the same JSON integer token, so the
			// narrower int branch is unreachable in try-each order. Only the
			// widest integer survives, keeping its original position.
			name: "int and int64 collapse to widest",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: openapi3.NewIntegerSchema()},
					{Value: openapi3.NewInt64Schema()},
					{Value: openapi3.NewStringSchema()},
				},
			},
			schemaKey:  "test___SeedLike",
			wantName:   "TestSeedLike",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"Int64", "String"},
		},
		{
			// float32/float64 collapse the same way as the integer class.
			name: "float32 and float64 collapse to widest",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Type: &openapi3.Types{"number"}, Format: "float"}},
					{Value: openapi3.NewFloat64Schema()},
					{Value: openapi3.NewStringSchema()},
				},
			},
			schemaKey:  "test___FloatLike",
			wantName:   "TestFloatLike",
			wantLazy:   false,
			wantCount:  2,
			wantBranch: []string{"Float64", "String"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := newTypeRegistry(opensearchAPIPkgName)
			spec := &openapi3.T{
				Components: &openapi3.Components{
					Schemas: openapi3.Schemas{},
				},
			}
			w := &walker{registry: reg, spec: spec, inFlight: make(map[string]struct{})}

			got := w.resolveUnionType(tt.schema, tt.schemaKey, "test")
			require.Equal(t, tt.wantName, got)

			registered, ok := reg.lookup(tt.schemaKey)
			require.True(t, ok, "union type should be registered")
			require.True(t, registered.IsUnion)
			require.Equal(t, tt.wantLazy, registered.IsLazy)
			require.Len(t, registered.Branches, tt.wantCount)

			for i, name := range tt.wantBranch {
				require.Equal(t, name, registered.Branches[i].Name)
			}
		})
	}
}

func TestResolveUnionTypeNullableSingleBranch(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{
			{Value: openapi3.NewStringSchema()},
			{Value: &openapi3.Schema{Type: &openapi3.Types{"null"}}},
		},
	}

	got := w.resolveUnionType(schema, "test___Nullable", "test")
	require.Equal(t, "string", got, "nullable with one non-null branch returns the primitive")

	_, ok := reg.lookup("test___Nullable")
	require.False(t, ok, "single non-null branch should not register a union")
}

func TestResolveUnionTypeDeduplicates(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{
			{Value: openapi3.NewStringSchema()},
			{Value: openapi3.NewStringSchema()},
			{Value: openapi3.NewIntegerSchema()},
		},
	}

	got := w.resolveUnionType(schema, "test___Dedup", "test")
	require.Equal(t, "TestDedup", got)

	registered, ok := reg.lookup("test___Dedup")
	require.True(t, ok)
	require.Len(t, registered.Branches, 2, "duplicate string branches should be deduplicated")
}

func TestResolveUnionTypeCollapsesToSingle(t *testing.T) {
	t.Parallel()

	intSchema := func() *openapi3.SchemaRef { return &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()} }
	int32Schema := func() *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}, Format: "int32"}}
	}
	int64Schema := func() *openapi3.SchemaRef { return &openapi3.SchemaRef{Value: openapi3.NewInt64Schema()} }
	float32Schema := func() *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"number"}, Format: "float"}}
	}
	float64Schema := func() *openapi3.SchemaRef { return &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()} }

	tests := []struct {
		name     string
		branches openapi3.SchemaRefs
		want     string
	}{
		{name: "int and int64", branches: openapi3.SchemaRefs{intSchema(), int64Schema()}, want: "int64"},
		{name: "int32 and int64", branches: openapi3.SchemaRefs{int32Schema(), int64Schema()}, want: "int64"},
		{name: "int and int32 keeps wider int", branches: openapi3.SchemaRefs{intSchema(), int32Schema()}, want: "int"},
		{name: "all three integers", branches: openapi3.SchemaRefs{int32Schema(), intSchema(), int64Schema()}, want: "int64"},
		{name: "float32 and float64", branches: openapi3.SchemaRefs{float32Schema(), float64Schema()}, want: "float64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}
			key := "test___" + tt.name

			got := w.resolveUnionType(&openapi3.Schema{OneOf: tt.branches}, key, "test")
			require.Equal(t, tt.want, got, "same-class numeric branches collapse to the widest; not a union")

			_, ok := reg.lookup(key)
			require.False(t, ok, "a union that collapses to one branch should not register")
		})
	}
}

func TestUnionNeedsTryEach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		branches []unionBranch
		want     bool
	}{
		{
			name:     "single branch",
			branches: []unionBranch{{TokenClass: "object"}},
			want:     false,
		},
		{
			name: "different tokens",
			branches: []unionBranch{
				{TokenClass: "object"},
				{TokenClass: "number"},
			},
			want: false,
		},
		{
			name: "same token object",
			branches: []unionBranch{
				{TokenClass: "object"},
				{TokenClass: "object"},
			},
			want: true,
		},
		{
			name: "same token string",
			branches: []unionBranch{
				{TokenClass: "string"},
				{TokenClass: "string"},
			},
			want: true,
		},
		{
			name: "three mixed with collision",
			branches: []unionBranch{
				{TokenClass: "object"},
				{TokenClass: "object"},
				{TokenClass: "string"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := unionNeedsTryEach(tt.branches)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTokenClassForPrimitive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goType string
		want   string
	}{
		{"string", "string"},
		{"bool", "bool"},
		{"int", "number"},
		{"int32", "number"},
		{"int64", "number"},
		{"float32", "number"},
		{"float64", "number"},
		{"[]string", "array"},
		{"[]int", "array"},
		{"map[string]int", "object"},
		{"SomeStruct", "object"},
	}

	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			t.Parallel()
			got := tokenClassForPrimitive(tt.goType)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDeduplicateBranches(t *testing.T) {
	t.Parallel()

	branches := []unionBranch{
		{Name: "String", GoType: "string"},
		{Name: "String", GoType: "string"},
		{Name: "Int", GoType: "int"},
		{Name: "Int", GoType: "int"},
	}

	result := deduplicateBranches(branches)
	require.Len(t, result, 2)
	require.Equal(t, "string", result[0].GoType)
	require.Equal(t, "int", result[1].GoType)
}

func TestDeduplicateAccessorNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		branches []unionBranch
		want     []string
	}{
		{
			name: "no duplicates unchanged",
			branches: []unionBranch{
				{Name: "String", GoType: "string"},
				{Name: "Int", GoType: "int"},
			},
			want: []string{"String", "Int"},
		},
		{
			name: "duplicate Map disambiguated",
			branches: []unionBranch{
				{Name: "Map", GoType: "map[string]string"},
				{Name: "Map", GoType: "map[string]FieldSort"},
			},
			want: []string{"StringMap", "FieldSortMap"},
		},
		{
			name: "duplicate Array disambiguated",
			branches: []unionBranch{
				{Name: "Array", GoType: "[]string"},
				{Name: "Array", GoType: "[]int"},
			},
			want: []string{"StringArray", "IntArray"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deduplicateAccessorNames(tt.branches)
			for i, wantName := range tt.want {
				require.Equal(t, wantName, tt.branches[i].Name)
			}
		})
	}
}

func TestClassifyBranchInlinePrimitives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schema    *openapi3.Schema
		wantName  string
		wantType  string
		wantToken string
	}{
		{
			name:      "string",
			schema:    openapi3.NewStringSchema(),
			wantName:  "String",
			wantType:  "string",
			wantToken: "string",
		},
		{
			name:      "boolean",
			schema:    openapi3.NewBoolSchema(),
			wantName:  "Bool",
			wantType:  "bool",
			wantToken: "bool",
		},
		{
			name:      "integer",
			schema:    openapi3.NewIntegerSchema(),
			wantName:  "Int",
			wantType:  "int",
			wantToken: "number",
		},
		{
			name:      "int64",
			schema:    openapi3.NewInt64Schema(),
			wantName:  "Int64",
			wantType:  "int64",
			wantToken: "number",
		},
		{
			name:      "float64",
			schema:    openapi3.NewFloat64Schema(),
			wantName:  "Float64",
			wantType:  "float64",
			wantToken: "number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			ref := &openapi3.SchemaRef{Value: tt.schema}
			b := w.classifyBranch(ref, "test___Parent", "test", 0)
			require.Equal(t, tt.wantName, b.Name)
			require.Equal(t, tt.wantType, b.GoType)
			require.Equal(t, tt.wantToken, b.TokenClass)
		})
	}
}

func TestClassifyBranchInlineArray(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := &openapi3.Schema{
		Type:  &openapi3.Types{"array"},
		Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
	}

	ref := &openapi3.SchemaRef{Value: schema}
	b := w.classifyBranch(ref, "test___Parent", "test", 0)
	require.Equal(t, "Array", b.Name)
	require.Equal(t, "[]string", b.GoType)
	require.Equal(t, "array", b.TokenClass)
}

func TestClassifyBranchNilRef(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	b := w.classifyBranch(nil, "test___Parent", "test", 0)
	require.Empty(t, b.GoType)
}

func TestPromoteSharedDepsIncludesUnionBranches(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)

	branchType := &goType{
		Name:      "BranchType",
		Pkg:       ir.DefaultCoreImportPath,
		SchemaRef: "group___BranchType",
		IsShared:  false,
	}
	reg.register(branchType)

	unionType := &goType{
		Name:      "SharedUnion",
		Pkg:       ir.DefaultCoreImportPath,
		SchemaRef: "_common___SharedUnion",
		IsShared:  true,
		IsUnion:   true,
		Branches: []unionBranch{
			{Name: "BranchType", GoType: "BranchType", TokenClass: "object"},
		},
	}
	reg.register(unionType)

	reg.promoteSharedDeps()

	promoted, ok := reg.lookup("group___BranchType")
	require.True(t, ok)
	require.True(t, promoted.IsShared, "branch type should be promoted to shared")
}
