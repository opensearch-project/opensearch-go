// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"slices"
	"strconv"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestResolveUnionType(t *testing.T) {
	t.Parallel()

	// inlineObj builds an inline (non-$ref) object branch whose declared
	// properties are all required, so its content name is the first sorted key.
	inlineObj := func(props ...string) *openapi3.SchemaRef {
		p := openapi3.Schemas{}
		for _, name := range props {
			p[name] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		}
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Required:   props,
			Properties: p,
		}}
	}

	tests := []struct {
		name              string
		schema            *openapi3.Schema
		schemaKey         string
		wantName          string
		wantAmbiguousWire bool
		wantCount         int
		wantBranch        []string // expected branch names
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
			schemaKey:         "_common___HitsTotal",
			wantName:          "HitsTotal",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"TotalHits", "Int64"},
		},
		{
			name: "string and integer",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: openapi3.NewStringSchema()},
					{Value: openapi3.NewIntegerSchema()},
				},
			},
			schemaKey:         "test___MixedField",
			wantName:          "TestMixedField",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"String", "Int"},
		},
		{
			name: "bool and integer",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: openapi3.NewBoolSchema()},
					{Value: openapi3.NewIntegerSchema()},
				},
			},
			schemaKey:         "test___TrackHits",
			wantName:          "TestTrackHits",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"Bool", "Int"},
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
			schemaKey:         "test___AOrB",
			wantName:          "TestAOrB",
			wantAmbiguousWire: true,
			wantCount:         2,
			// Branch names come from the $ref key's local segment, not the
			// qualified Go type, so they do not restate the union's group prefix.
			wantBranch: []string{"TypeA", "TypeB"},
		},
		{
			name: "anyOf treated like oneOf",
			schema: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: openapi3.NewStringSchema()},
					{Value: openapi3.NewBoolSchema()},
				},
			},
			schemaKey:         "test___AnyField",
			wantName:          "TestAnyField",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"String", "Bool"},
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
			schemaKey:         "test___SeedLike",
			wantName:          "TestSeedLike",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"Int64", "String"},
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
			schemaKey:         "test___FloatLike",
			wantName:          "TestFloatLike",
			wantAmbiguousWire: false,
			wantCount:         2,
			wantBranch:        []string{"Float64", "String"},
		},
		{
			// Inline (non-$ref) object branches are named from their content, not
			// their spec position: each branch here is named for its (sole,
			// required) property key. This exercises the objectBranchNames pre-pass
			// wired into resolveUnionType, so a spec reorder can no longer rename
			// the generated type.
			name: "distinct inline objects named from content",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					inlineObj("task"),
					inlineObj("acknowledged"),
				},
			},
			schemaKey:         "test___InlineDistinct",
			wantName:          "TestInlineDistinct",
			wantAmbiguousWire: true,
			wantCount:         2,
			wantBranch:        []string{"Task", "Acknowledged"},
		},
		{
			// Two structurally identical inline objects (same properties and
			// required set) cannot be told apart by content, so both fall back to
			// positional Object<idx> suffixes and stay two distinct types. This is
			// the anti-collapse invariant: collapsing them to one type would
			// silently drop a union branch (the real SegmentReplication case).
			name: "identical inline objects stay distinct via positional fallback",
			schema: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					inlineObj("max_bytes_behind"),
					inlineObj("max_bytes_behind"),
				},
			},
			schemaKey:         "test___InlineIdentical",
			wantName:          "TestInlineIdentical",
			wantAmbiguousWire: true,
			wantCount:         2,
			wantBranch:        []string{"Object0", "Object1"},
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
			require.Equal(t, tt.wantAmbiguousWire, registered.IsAmbiguousWire)
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

func TestResolveUnionTypeKeepsDuplicateGoTypes(t *testing.T) {
	t.Parallel()

	// The Parse phase keeps same-Go-type branches. Whether a duplicate is
	// reachable depends on the union's decode state, which is not assigned until
	// the IR phase, so dropUnreachableBranches makes that call later. Dropping
	// here would delete the distinct As<Branch>() accessors a caller-keyed lazy
	// union legitimately exposes over one Go type.
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
	require.Len(t, registered.Branches, 3, "Parse phase must not judge branch reachability")
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
			branches: []unionBranch{{TokenClass: ir.TokenObject}},
			want:     false,
		},
		{
			name: "different tokens",
			branches: []unionBranch{
				{TokenClass: ir.TokenObject},
				{TokenClass: ir.TokenNumber},
			},
			want: false,
		},
		{
			name: "same token object",
			branches: []unionBranch{
				{TokenClass: ir.TokenObject},
				{TokenClass: ir.TokenObject},
			},
			want: true,
		},
		{
			name: "same token string",
			branches: []unionBranch{
				{TokenClass: ir.TokenString},
				{TokenClass: ir.TokenString},
			},
			want: true,
		},
		{
			name: "three mixed with collision",
			branches: []unionBranch{
				{TokenClass: ir.TokenObject},
				{TokenClass: ir.TokenObject},
				{TokenClass: ir.TokenString},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := branchesCollideOnTokenClass(tt.branches)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTokenClassForPrimitive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goType string
		want   ir.TokenClass
	}{
		{"string", ir.TokenString},
		{"bool", ir.TokenBool},
		{"int", ir.TokenNumber},
		{"int32", ir.TokenNumber},
		{"int64", ir.TokenNumber},
		{"float32", ir.TokenNumber},
		{"float64", ir.TokenNumber},
		{"[]string", ir.TokenArray},
		{"[]int", ir.TokenArray},
		{"map[string]int", ir.TokenObject},
		{"SomeStruct", ir.TokenObject},
	}

	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			t.Parallel()
			got := tokenClassForPrimitive(tt.goType)
			require.Equal(t, tt.want, got)
		})
	}
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
		wantToken ir.TokenClass
	}{
		{
			name:      "string",
			schema:    openapi3.NewStringSchema(),
			wantName:  "String",
			wantType:  "string",
			wantToken: ir.TokenString,
		},
		{
			name:      "boolean",
			schema:    openapi3.NewBoolSchema(),
			wantName:  "Bool",
			wantType:  "bool",
			wantToken: ir.TokenBool,
		},
		{
			name:      "integer",
			schema:    openapi3.NewIntegerSchema(),
			wantName:  "Int",
			wantType:  "int",
			wantToken: ir.TokenNumber,
		},
		{
			name:      "int64",
			schema:    openapi3.NewInt64Schema(),
			wantName:  "Int64",
			wantType:  "int64",
			wantToken: ir.TokenNumber,
		},
		{
			name:      "float64",
			schema:    openapi3.NewFloat64Schema(),
			wantName:  "Float64",
			wantType:  "float64",
			wantToken: ir.TokenNumber,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			ref := &openapi3.SchemaRef{Value: tt.schema}
			b := w.classifyBranch(ref, "test___Parent", "test", 0, "")
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
	b := w.classifyBranch(ref, "test___Parent", "test", 0, "")
	require.Equal(t, "Array", b.Name)
	require.Equal(t, "[]string", b.GoType)
	require.Equal(t, ir.TokenArray, b.TokenClass)
}

func TestClassifyBranchNilRef(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	b := w.classifyBranch(nil, "test___Parent", "test", 0, "")
	require.Empty(t, b.GoType)
}

// TestObjectBranchName covers the content-based naming of an inline object
// oneOf/anyOf branch: a titled member uses its (normalized) title, a branch
// with required keys is named for its first sorted required key, and a
// permissive branch is named for its sorted property keys joined together.
func TestObjectBranchName(t *testing.T) {
	t.Parallel()

	obj := func(title string, required []string, props ...string) *openapi3.Schema {
		p := openapi3.Schemas{}
		for _, name := range props {
			p[name] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		}
		return &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Title:      title,
			Required:   required,
			Properties: p,
		}
	}

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   string
	}{
		// Titled members keep their spec title (hyphens normalize to PascalCase).
		{name: "title", schema: obj("keyed", nil, "field"), want: "Keyed"},
		{name: "hyphenated title", schema: obj("score-ranker-processor", nil, "field"), want: "ScoreRankerProcessor"},
		// Required-keyed branches are named for the first sorted required key --
		// the field a decoder probes to select the branch.
		{name: "single required key", schema: obj("", []string{"acknowledged"}, "acknowledged", "shards_acknowledged"), want: "Acknowledged"},
		{name: "required key sorted first", schema: obj("", []string{"memory"}, "memory", "cpu"), want: "Memory"},
		{name: "required key underscore normalized", schema: obj("", []string{"_source"}, "_source"), want: "Source"},
		// Permissive branches (no required keys) join their sorted property keys.
		{name: "permissive single prop", schema: obj("", nil, "task"), want: "Task"},
		{name: "permissive multi prop sorted", schema: obj("", nil, "includes", "excludes"), want: "ExcludesIncludes"},
		// An object with no properties has no content name (open map branch).
		{name: "no properties", schema: obj("", nil), want: ""},
		// flattenRequired reaches through allOf, so a composed branch is named for
		// the key it requires even though its root declares no properties.
		{
			name: "composed branch takes its required key",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Value: obj("", []string{"value"}, "value", "case_insensitive")},
			}},
			want: "Value",
		},
		// A titled composed branch still uses its title.
		{
			name: "titled composed branch",
			schema: &openapi3.Schema{Title: "completion", AllOf: openapi3.SchemaRefs{
				{Value: obj("", nil, "options")},
			}},
			want: "Completion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, objectBranchName(tt.schema))
		})
	}
}

// TestObjectBranchNamesCollision verifies that when two inline object branches
// resolve to the same content name (structurally identical branches that cannot
// be told apart), both drop out of the name map so classifyBranch falls back to
// distinct positional Object<idx> suffixes rather than collapsing to one type.
func TestObjectBranchNamesCollision(t *testing.T) {
	t.Parallel()

	obj := func(required ...string) *openapi3.SchemaRef {
		p := openapi3.Schemas{}
		for _, name := range required {
			p[name] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		}
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Required:   required,
			Properties: p,
		}}
	}

	t.Run("distinct names kept", func(t *testing.T) {
		t.Parallel()
		names := objectBranchNames([]*openapi3.SchemaRef{obj("task"), obj("acknowledged")})
		require.Equal(t, map[int]string{0: "Task", 1: "Acknowledged"}, names)
	})

	t.Run("identical branches collide to positional", func(t *testing.T) {
		t.Parallel()
		// Both branches require the same key: same content name -> both dropped.
		names := objectBranchNames([]*openapi3.SchemaRef{obj("max_bytes_behind"), obj("max_bytes_behind")})
		require.Empty(t, names)
	})

	t.Run("null branch does not shift ordinals", func(t *testing.T) {
		t.Parallel()
		null := &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"null"}}}
		names := objectBranchNames([]*openapi3.SchemaRef{null, obj("task")})
		// The null branch is skipped, so the object branch is Ordinal 0.
		require.Equal(t, map[int]string{0: "Task"}, names)
	})
}

// TestClassifyBranchInlineObject covers classifyBranch for inline objects given
// a resolved name: a supplied content name is used for both the accessor name
// and the generated type suffix (kept union-relative so accessors and
// constructors don't stutter the parent prefix), and an empty name falls back to
// a positional Object<idx> suffix.
func TestClassifyBranchInlineObject(t *testing.T) {
	t.Parallel()

	objectSchema := func() *openapi3.SchemaRef {
		s := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: openapi3.Schemas{"field": {Value: openapi3.NewStringSchema()}},
		}
		return &openapi3.SchemaRef{Value: s}
	}

	tests := []struct {
		name       string
		branchIdx  int
		objName    string
		wantName   string
		wantGoType string
	}{
		{name: "content name", branchIdx: 1, objName: "Field", wantName: "Field", wantGoType: "ParentField"},
		{name: "positional fallback", branchIdx: 1, objName: "", wantName: "Object1", wantGoType: "ParentObject1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}
			b := w.classifyBranch(objectSchema(), "_common___Parent", "_common", tt.branchIdx, tt.objName)
			require.Equal(t, tt.wantName, b.Name)
			require.Equal(t, tt.wantGoType, b.GoType)
			require.Equal(t, ir.TokenObject, b.TokenClass)
		})
	}
}

// TestClassifyBranchInlineComposed covers an inline branch that composes its
// shape with allOf and so carries no `type` keyword: an allOf of objects is an
// object, and the branch must classify as one. A titled composed branch keeps
// its title-derived name; an unnamed one falls back to the positional suffix.
func TestClassifyBranchInlineComposed(t *testing.T) {
	t.Parallel()

	// Shaped like the spec's field-scoped queries: a $ref'd base merged with an
	// inline object that adds the branch's own properties.
	composed := func() *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			AllOf: openapi3.SchemaRefs{
				{
					Ref: "#/components/schemas/_common.query_dsl___QueryBase",
					Value: &openapi3.Schema{
						Type:       &openapi3.Types{"object"},
						Properties: openapi3.Schemas{"boost": {Value: openapi3.NewFloat64Schema()}},
					},
				},
				{Value: &openapi3.Schema{
					Type:       &openapi3.Types{"object"},
					Required:   []string{"value"},
					Properties: openapi3.Schemas{"value": {Value: openapi3.NewStringSchema()}},
				}},
			},
		}}
	}

	tests := []struct {
		name       string
		objName    string
		wantName   string
		wantGoType string
	}{
		{name: "content name", objName: "Value", wantName: "Value", wantGoType: "CommonQueryDSLTermQueryValue"},
		{name: "positional fallback", objName: "", wantName: "Object1", wantGoType: "CommonQueryDSLTermQueryObject1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			b := w.classifyBranch(composed(), "_common.query_dsl___TermQuery", "_common.query_dsl", 1, tt.objName)
			require.Equal(t, tt.wantName, b.Name)
			require.Equal(t, tt.wantGoType, b.GoType)
			require.Equal(t, ir.TokenObject, b.TokenClass)
			// Required comes through the allOf members, which is what a decoder
			// probes to tell the branch from its siblings.
			require.Equal(t, []string{"value"}, b.Required)
		})
	}
}

// TestResolveUnionTypeKeepsComposedBranch covers the two unions an inline
// composed (allOf) branch appears in: a shorthand scalar paired with the
// full-form object, and a union whose every branch is composed. Dropping the
// composed branch would silently collapse the first to the scalar alone and
// degrade the second to json.RawMessage.
func TestResolveUnionTypeKeepsComposedBranch(t *testing.T) {
	t.Parallel()

	composed := func(required string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			AllOf: openapi3.SchemaRefs{
				{Value: &openapi3.Schema{
					Type:       &openapi3.Types{"object"},
					Required:   []string{required},
					Properties: openapi3.Schemas{required: {Value: openapi3.NewStringSchema()}},
				}},
			},
		}}
	}

	tests := []struct {
		name       string
		schema     *openapi3.Schema
		schemaKey  string
		wantGoType string
		wantBranch []string
	}{
		{
			name: "scalar shorthand and composed full form",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				{Value: &openapi3.Schema{Title: "value", Type: &openapi3.Types{"string"}}},
				composed("value"),
			}},
			schemaKey:  "_common.query_dsl___TermQuery",
			wantGoType: "CommonQueryDSLTermQuery",
			wantBranch: []string{"String", "Value"},
		},
		{
			name: "every branch composed",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				composed("origin"),
				composed("pivot"),
			}},
			schemaKey:  "_common.query_dsl___DistanceFeatureQuery",
			wantGoType: "CommonQueryDSLDistanceFeatureQuery",
			wantBranch: []string{"Origin", "Pivot"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			got := w.resolveUnionType(tt.schema, tt.schemaKey, "_common.query_dsl")
			require.Equal(t, tt.wantGoType, got)

			registered, ok := reg.lookup(tt.schemaKey)
			require.True(t, ok)
			names := make([]string, 0, len(registered.Branches))
			for _, b := range registered.Branches {
				names = append(names, b.Name)
			}
			require.ElementsMatch(t, tt.wantBranch, names)
		})
	}
}

// TestSortBranchesNewestFirstOrderIndependent verifies the sort is a total
// order keyed on (VersionAdded desc, Ordinal asc), independent of the incoming
// slice order. Ordinal (spec-array position) is the tiebreaker, so no consumer
// needs to parse a branch Name to recover order.
func TestSortBranchesNewestFirstOrderIndependent(t *testing.T) {
	t.Parallel()

	// Ordinals are the spec-array positions; versions are intentionally varied,
	// including two unversioned branches that must fall back to Ordinal order.
	base := []unionBranch{
		{Name: "Object0", Ordinal: 0, VersionAdded: ""},
		{Name: "B", Ordinal: 1, VersionAdded: "2.5.0"},
		{Name: "C", Ordinal: 2, VersionAdded: "2.10.0"},
		{Name: "Object3", Ordinal: 3, VersionAdded: ""},
		{Name: "E", Ordinal: 4, VersionAdded: "2.5.0"},
	}
	// Newest first; equal versions and the unversioned pair break on Ordinal.
	want := []string{"C", "B", "E", "Object0", "Object3"}

	orderings := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
	}
	for _, order := range orderings {
		in := make([]unionBranch, len(order))
		for i, idx := range order {
			in[i] = base[idx]
		}
		sortBranchesNewestFirst(in)
		got := make([]string, len(in))
		for i, b := range in {
			got[i] = b.Name
		}
		require.Equal(t, want, got, "input order %v should not change the result", order)
	}
}

// TestSortBranchesNewestFirstDoubleDigitOrdinals guards the case Object10 would
// sort before Object2 under lexical ordering: with Ordinal an int the tie-break
// is numeric, so a union with more than ten inline-object branches still orders
// by spec position. Fails if branch ordering ever reverts to parsing the Name.
func TestSortBranchesNewestFirstDoubleDigitOrdinals(t *testing.T) {
	t.Parallel()

	// 12 unversioned branches named/positioned so the lexical order
	// (Object0, Object1, Object10, Object11, Object2, ...) differs from the
	// numeric order. Shuffle the input to prove Ordinal, not slice position,
	// drives the result.
	const n = 12
	in := make([]unionBranch, n)
	for i := range n {
		// Reverse the input so slice order can't accidentally match.
		ord := n - 1 - i
		in[i] = unionBranch{Name: "Object" + strconv.Itoa(ord), Ordinal: ord}
	}

	sortBranchesNewestFirst(in)

	want := make([]string, n)
	for i := range n {
		want[i] = "Object" + strconv.Itoa(i)
	}
	got := make([]string, n)
	for i, b := range in {
		got[i] = b.Name
	}
	require.Equal(t, want, got, "double-digit ordinals must sort numerically, not lexically")
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
			{Name: "BranchType", GoType: "BranchType", TokenClass: ir.TokenObject},
		},
	}
	reg.register(unionType)

	reg.promoteSharedDeps()

	promoted, ok := reg.lookup("group___BranchType")
	require.True(t, ok)
	require.True(t, promoted.IsShared, "branch type should be promoted to shared")
}

// TestRenameBranchesShadowingTypeNames pins the fix for a package that would not
// compile: a branch const "<Union><Branch>Type" lands in the same package-level
// namespace as every union's "<Union>Type" enum type, so a union X with branch B
// beside a sibling union named XB emits XBType twice -- once as a const, once as
// that sibling's type.
//
// The spec hits this three times, all shaped
// `<Thing>` = string | `<Thing>Definition` (CharFilter, TokenFilter, Tokenizer).
func TestRenameBranchesShadowingTypeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		unionName string
		branches  []unionBranch
		wantNames []string
	}{
		{
			// The real case: CommonAnalysisCharFilter + "Definition" would emit
			// CommonAnalysisCharFilterDefinitionType, which is also the enum type
			// name of the CommonAnalysisCharFilterDefinition union. The replacement
			// comes from the schema key's local segment, NOT the Go type name --
			// the latter would give the 62-char stuttered
			// CommonAnalysisCharFilterCommonAnalysisCharFilterDefinitionType.
			name:      "definition branch shadowing sibling union takes local schema name",
			unionName: "CommonAnalysisCharFilter",
			branches: []unionBranch{
				{Name: "String", GoType: "string"},
				{
					Name:      "Definition",
					GoType:    "CommonAnalysisCharFilterDefinition",
					SchemaKey: "_common.analysis___CharFilterDefinition",
					IsRef:     true,
				},
			},
			wantNames: []string{"String", "CharFilterDefinition"},
		},
		{
			// An INLINE object branch's Go type is "<Union><Branch>" by
			// construction, so it matches the shadow test every time -- but that
			// type is the union's own child, not an independent sibling union.
			// Renaming it would corrupt every inline branch name, so non-$ref
			// branches are exempt.
			name:      "inline branch is exempt despite matching the shadow test",
			unionName: "TestInlineDistinct",
			branches: []unionBranch{
				{Name: "Task", GoType: "TestInlineDistinctTask"},
			},
			wantNames: []string{"Task"},
		},
		{
			// No shadowing: the const stem does not equal the branch type name.
			name:      "unrelated branch keeps its name",
			unionName: "CommonAnalysisAnalyzer",
			branches: []unionBranch{
				{Name: "CustomAnalyzer", GoType: "CommonAnalysisCustomAnalyzer"},
			},
			wantNames: []string{"CustomAnalyzer"},
		},
		{
			// A primitive branch can never name a union type.
			name:      "primitive branches untouched",
			unionName: "Foo",
			branches: []unionBranch{
				{Name: "Int64", GoType: "int64"},
				{Name: "String", GoType: "string"},
			},
			wantNames: []string{"Int64", "String"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			branches := slices.Clone(tt.branches)
			renameBranchesShadowingTypeNames(tt.unionName, branches)

			got := make([]string, len(branches))
			for i, b := range branches {
				got[i] = b.Name
			}
			require.Equal(t, tt.wantNames, got)

			// The invariant that matters: no $ref branch's const stem equals its own
			// branch type name, so no const shadows a sibling union's enum type
			// name. Inline branches are exempt by design (their type IS
			// "<Union><Branch>").
			for _, b := range branches {
				if !b.IsRef {
					continue
				}
				require.NotEqual(t, unwrapTypeName(b.GoType), tt.unionName+b.Name,
					"branch %q const still shadows a union type name", b.Name)
			}
		})
	}
}
