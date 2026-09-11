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
)

// aliasRefTo builds a bare `allOf: [$ref target]` schema, the shape the spec uses
// to give a mangled generic instantiation a readable alias.
func aliasRefTo(target string) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
		{Ref: "#/components/schemas/" + target},
	}}}
}

// objectSchema builds a plain object schema with one field.
func objectSchema() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Properties: openapi3.Schemas{
		"value": {Value: openapi3.NewFloat64Schema()},
	}}}
}

// collapsedRegistry registers target under its own key and points every alias at
// it, mirroring what walker.resolveCollapsedBase leaves behind.
func collapsedRegistry(t *testing.T, target string, aliases ...string) (*typeRegistry, *goType) {
	t.Helper()
	reg := newTypeRegistry(opensearchAPIPkgName)
	typ, ok := reg.register(&goType{Name: schemaTypeName(target, false), SchemaRef: target})
	require.True(t, ok)
	for _, a := range aliases {
		reg.aliasRef(a, typ)
	}
	return reg, typ
}

// TestRenameCollapsedAliases covers the pass that repairs the regression the
// collapse work introduced: collapsing a bare `allOf: [$ref]` alias onto its
// target kept the target's mangled name and discarded the readable alias, so
// AsAdjacencyMatrix() returned
// CommonAggregationsMultiBucketAggregateBaseAdjacencyMatrixBucket.
//
// The two guards are what make it safe. A target several aliases share has no
// single better name, and renaming one anyway dangles the siblings' references --
// that is what broke the build when this was attempted mid-walk. A target the spec
// leans on more heavily than its alias keeps its own name, or SearchResult would be
// retired in favor of SearchResponse.
func TestRenameCollapsedAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "alias name replaces the mangled instantiation name",
			check: func(t *testing.T) {
				t.Helper()
				const (
					alias  = "_common.aggregations___AdjacencyMatrixAggregate"
					target = "_common.aggregations___MultiBucketAggregateBaseAdjacencyMatrixBucket"
				)
				spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
					alias:  aliasRefTo(target),
					target: objectSchema(),
				}}}
				reg, typ := collapsedRegistry(t, target, alias)

				renameCollapsedAliases(spec, reg)

				require.Equal(t, "CommonAggregationsAdjacencyMatrixAggregate", typ.Name)
				// Both refs still resolve to the one type, under the new name.
				viaAlias, ok := reg.lookup(alias)
				require.True(t, ok)
				require.Same(t, typ, viaAlias)
				byName, ok := reg.lookupByName("CommonAggregationsAdjacencyMatrixAggregate")
				require.True(t, ok)
				require.Same(t, typ, byName)
			},
		},
		{
			// SearchResponse aliases SearchResult, but the spec $refs SearchResult
			// far more often; promoting the alias retires the name callers use.
			name: "the more heavily referenced name survives",
			check: func(t *testing.T) {
				t.Helper()
				const (
					alias  = "_core.search___SearchResponse"
					target = "_core.search___SearchResult"
				)
				schemas := openapi3.Schemas{alias: aliasRefTo(target), target: objectSchema()}
				for _, n := range []string{"a", "b", "c"} {
					schemas["_core.search___User"+n] = &openapi3.SchemaRef{Value: &openapi3.Schema{
						Properties: openapi3.Schemas{"r": {Ref: "#/components/schemas/" + target}},
					}}
				}
				spec := &openapi3.T{Components: &openapi3.Components{Schemas: schemas}}
				reg, typ := collapsedRegistry(t, target, alias)

				renameCollapsedAliases(spec, reg)

				require.Equal(t, "SearchResult", typ.Name)
			},
		},
		{
			// Eight schemas from AvgAggregate to WeightedAvgAggregate collapse onto
			// SingleMetricAggregateBase, so no one alias name is the right one.
			name: "a target several aliases share keeps its own name",
			check: func(t *testing.T) {
				t.Helper()
				const target = "_common.aggregations___SingleMetricAggregateBase"
				aliases := []string{
					"_common.aggregations___AvgAggregate",
					"_common.aggregations___SumAggregate",
					"_common.aggregations___MinAggregate",
				}
				schemas := openapi3.Schemas{target: objectSchema()}
				for _, a := range aliases {
					schemas[a] = aliasRefTo(target)
				}
				spec := &openapi3.T{Components: &openapi3.Components{Schemas: schemas}}
				reg, typ := collapsedRegistry(t, target, aliases...)

				renameCollapsedAliases(spec, reg)

				require.Equal(t, "CommonAggregationsSingleMetricAggregateBase", typ.Name)
			},
		},
		{
			// Type references are plain strings, which is why the rename cannot
			// happen mid-walk: renaming the registry entry without rewriting them
			// emits code naming a type that no longer exists.
			name: "every reference form is rewritten",
			check: func(t *testing.T) {
				t.Helper()
				const (
					alias  = "_common.aggregations___AdjacencyMatrixAggregate"
					target = "_common.aggregations___MultiBucketAggregateBaseAdjacencyMatrixBucket"
					oldGo  = "CommonAggregationsMultiBucketAggregateBaseAdjacencyMatrixBucket"
					newGo  = "CommonAggregationsAdjacencyMatrixAggregate"
				)
				spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
					alias:  aliasRefTo(target),
					target: objectSchema(),
				}}}
				reg, _ := collapsedRegistry(t, target, alias)

				holder, ok := reg.register(&goType{
					Name:      "Holder",
					SchemaRef: "_common.aggregations___Holder",
					Fields: []goField{
						{GoName: "Bare", GoType: oldGo},
						{GoName: "Slice", GoType: "[]" + oldGo},
						{GoName: "Ptr", GoType: "*" + oldGo},
						{GoName: "Map", GoType: "map[string]" + oldGo},
						{GoName: "Other", GoType: "SomethingElse"},
					},
					Branches: []unionBranch{{Name: "Agg", GoType: oldGo}},
				})
				require.True(t, ok)

				renameCollapsedAliases(spec, reg)

				require.Equal(t, newGo, holder.Fields[0].GoType)
				require.Equal(t, "[]"+newGo, holder.Fields[1].GoType)
				require.Equal(t, "*"+newGo, holder.Fields[2].GoType)
				require.Equal(t, "map[string]"+newGo, holder.Fields[3].GoType)
				require.Equal(t, "SomethingElse", holder.Fields[4].GoType,
					"unrelated types are untouched")
				require.Equal(t, newGo, holder.Branches[0].GoType)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}

// TestDescendantRenames pins how a nested type is identified. Matching by Go name
// prefix dragged SearchResultJSONValue along when SearchResult was renamed, even
// though it is a separate spec schema that merely shares the prefix; only types
// keyed BENEATH the collapsed schema are descendants.
func TestDescendantRenames(t *testing.T) {
	t.Parallel()

	const target = "_core.search___SearchResult"

	tests := []struct {
		name      string
		typeName  string
		schemaRef string
		wantNew   string // "" means it must not be renamed
	}{
		{
			name:      "type keyed beneath the target is a descendant",
			typeName:  "SearchResultHits",
			schemaRef: target + ".hits",
			wantNew:   "SearchResponseHits",
		},
		{
			name:      "separate schema sharing the name prefix is not",
			typeName:  "SearchResultJSONValue",
			schemaRef: "_core.search___SearchResultJSONValue",
		},
		{
			name:      "the target itself is not its own descendant",
			typeName:  "SearchResult",
			schemaRef: target,
		},
		{
			name:      "nested type whose name does not carry the prefix is left alone",
			typeName:  "ShardStatistics",
			schemaRef: target + ".shards",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			typ, ok := reg.register(&goType{Name: tt.typeName, SchemaRef: tt.schemaRef})
			require.True(t, ok)

			got := descendantRenames(reg, target, "SearchResult", "SearchResponse", make(set[string]))

			if tt.wantNew == "" {
				require.Empty(t, got)
				require.Equal(t, tt.typeName, typ.Name)
				return
			}
			require.Len(t, got, 1)
			require.Same(t, typ, got[0].typ)
			require.Equal(t, tt.wantNew, got[0].newName)
		})
	}
}

// TestDescendantRenamesYieldsOnNameConflict covers the two guards that make a
// descendant keep its old name. A descendant renamed onto a name another type
// already holds would collide in the registry, and register drops the loser --
// silently degrading that type's output. Keeping the old name is the safe half.
func TestDescendantRenamesYieldsOnNameConflict(t *testing.T) {
	t.Parallel()

	const (
		target  = "_core.search___SearchResult"
		oldName = "SearchResult"
		newName = "SearchResponse"
	)

	tests := []struct {
		name string
		// setup registers whatever already holds the candidate name and returns
		// the names claimed by an earlier planned rename.
		setup func(t *testing.T, reg *typeRegistry) set[string]
	}{
		{
			name: "an earlier planned rename already claimed the name",
			setup: func(t *testing.T, _ *typeRegistry) set[string] {
				t.Helper()
				return newSet("SearchResponseHits")
			},
		},
		{
			name: "a registered type already holds the name",
			setup: func(t *testing.T, reg *typeRegistry) set[string] {
				t.Helper()
				_, ok := reg.register(&goType{
					Name:      "SearchResponseHits",
					SchemaRef: "_core.search___SearchResponseHits",
				})
				require.True(t, ok)
				return make(set[string])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			descendant, ok := reg.register(&goType{
				Name:      "SearchResultHits",
				SchemaRef: target + ".hits",
			})
			require.True(t, ok)
			taken := tt.setup(t, reg)

			require.Empty(t, descendantRenames(reg, target, oldName, newName, taken))
			require.Equal(t, "SearchResultHits", descendant.Name,
				"the descendant keeps its old name rather than colliding")
		})
	}
}

// TestPlannedRenamesGuards pins the conditions under which no rename is planned
// at all. Each is a case where renaming would either dangle a reference or
// retire a name something else already answers to, so the collapse keeps the
// base's name and the readable alias is simply not recovered.
func TestPlannedRenamesGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		alias  string
		target string
		// setup builds the registry for the scenario. The default registers the
		// target and aliases the alias's ref onto it.
		setup func(t *testing.T, alias, target string) *typeRegistry
	}{
		{
			// The collapse never happened (the walk resolved the base to a
			// primitive, say), so there is no registry entry to rename.
			name:   "the target is not registered",
			alias:  "_core.search___SearchResponse",
			target: "_core.search___SearchResult",
			setup: func(t *testing.T, _, _ string) *typeRegistry {
				t.Helper()
				return newTypeRegistry(opensearchAPIPkgName)
			},
		},
		{
			// Without aliasRef the two refs are independent types that merely
			// look like a collapse pair in the spec; renaming one would rename a
			// type the alias never resolved to.
			name:   "the alias resolved to a different type",
			alias:  "_core.search___SearchResponse",
			target: "_core.search___SearchResult",
			setup: func(t *testing.T, alias, target string) *typeRegistry {
				t.Helper()
				reg, _ := collapsedRegistry(t, target)
				_, ok := reg.register(&goType{Name: schemaTypeName(alias, false), SchemaRef: alias})
				require.True(t, ok)
				return reg
			},
		},
		{
			// Both keys derive the same Go name, so the rename would be a no-op.
			name:   "the alias derives the name the target already has",
			alias:  "a.b___X",
			target: "a___BX",
		},
		{
			// An unrelated schema already emits SearchResponse; taking the name
			// would collide in the registry and drop one of the two types.
			name:   "another registered type already holds the alias's name",
			alias:  "_core.search___SearchResponse",
			target: "_core.search___SearchResult",
			setup: func(t *testing.T, alias, target string) *typeRegistry {
				t.Helper()
				reg, _ := collapsedRegistry(t, target, alias)
				_, ok := reg.register(&goType{
					Name:      schemaTypeName(alias, false),
					SchemaRef: "_core.search___Unrelated",
				})
				require.True(t, ok)
				return reg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
				tt.alias:  aliasRefTo(tt.target),
				tt.target: objectSchema(),
			}}}
			reg := tt.setup
			if reg == nil {
				reg = func(t *testing.T, alias, target string) *typeRegistry {
					t.Helper()
					r, _ := collapsedRegistry(t, target, alias)
					return r
				}
			}

			require.Empty(t, plannedRenames(spec, reg(t, tt.alias, tt.target)))
		})
	}
}

// TestPlannedRenamesOneNameWins covers the cross-pair guard: two unrelated
// collapse pairs whose aliases derive the SAME Go name. Only one can take it,
// because the second would collide in the registry once the first is applied.
func TestPlannedRenamesOneNameWins(t *testing.T) {
	t.Parallel()

	// "a.b___X" and "a___BX" both derive "ABX".
	const (
		aliasOne  = "a.b___X"
		targetOne = "a___T1"
		aliasTwo  = "a___BX"
		targetTwo = "a___T2"
	)
	spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		aliasOne:  aliasRefTo(targetOne),
		targetOne: objectSchema(),
		aliasTwo:  aliasRefTo(targetTwo),
		targetTwo: objectSchema(),
	}}}
	reg, _ := collapsedRegistry(t, targetOne, aliasOne)
	typTwo, ok := reg.register(&goType{Name: schemaTypeName(targetTwo, false), SchemaRef: targetTwo})
	require.True(t, ok)
	reg.aliasRef(aliasTwo, typTwo)

	renames := plannedRenames(spec, reg)

	require.Len(t, renames, 1, "the contested name is planned exactly once")
	require.Equal(t, "ABX", renames[0].newName)
}

// TestPlannedRenamesSortsByOldName pins the ordering. Generation must be
// reproducible, and plannedRenames walks a map, so the result is sorted rather
// than left in iteration order.
func TestPlannedRenamesSortsByOldName(t *testing.T) {
	t.Parallel()

	const (
		aggAlias     = "_common.aggregations___AdjacencyMatrixAggregate"
		aggTarget    = "_common.aggregations___MultiBucketAggregateBaseAdjacencyMatrixBucket"
		searchAlias  = "_core.search___SearchResponse"
		searchTarget = "_core.search___SearchResult"
	)
	spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		aggAlias:     aliasRefTo(aggTarget),
		aggTarget:    objectSchema(),
		searchAlias:  aliasRefTo(searchTarget),
		searchTarget: objectSchema(),
	}}}
	reg, _ := collapsedRegistry(t, aggTarget, aggAlias)
	searchTyp, ok := reg.register(&goType{Name: schemaTypeName(searchTarget, false), SchemaRef: searchTarget})
	require.True(t, ok)
	reg.aliasRef(searchAlias, searchTyp)

	renames := plannedRenames(spec, reg)

	require.Len(t, renames, 2)
	require.Equal(t, []string{
		"CommonAggregationsMultiBucketAggregateBaseAdjacencyMatrixBucket",
		"SearchResult",
	}, []string{renames[0].oldName, renames[1].oldName})
}

// jsonContent wraps a schema ref as the JSON media type of a request or response
// body component.
func jsonContent(ref *openapi3.SchemaRef) openapi3.Content {
	return openapi3.Content{"application/json": &openapi3.MediaType{Schema: ref}}
}

// TestRefCountSpansEverySite pins where a name can be depended upon. refCount is
// the tiebreaker that decides whether SearchResult or SearchResponse survives a
// collapse, so a site it fails to count makes the busier name look idle and
// retires a type callers name directly.
func TestRefCountSpansEverySite(t *testing.T) {
	t.Parallel()

	const (
		key  = "_core.search___SearchResult"
		skip = "_core.search___SearchResponse"
	)
	refTo := func(target string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"r": {Ref: "#/components/schemas/" + target},
		}}}
	}

	tests := []struct {
		name       string
		components *openapi3.Components
		want       int
	}{
		{
			name: "component schemas",
			components: &openapi3.Components{Schemas: openapi3.Schemas{
				"_core.search___A": refTo(key),
				"_core.search___B": refTo(key),
			}},
			want: 2,
		},
		{
			// The key's own schema cannot depend on itself, and the alias's $ref
			// to its target IS the collapse: counting it would make every target
			// look busier than its alias and block every rename.
			name: "the key's own schema and the skipped alias are excluded",
			components: &openapi3.Components{Schemas: openapi3.Schemas{
				key:  refTo(key),
				skip: refTo(key),
			}},
			want: 0,
		},
		{
			name: "request bodies",
			components: &openapi3.Components{
				RequestBodies: openapi3.RequestBodies{
					"search": {Value: &openapi3.RequestBody{Content: jsonContent(refTo(key))}},
				},
			},
			want: 1,
		},
		{
			name: "responses",
			components: &openapi3.Components{
				Responses: openapi3.ResponseBodies{
					"search": {Value: &openapi3.Response{Content: jsonContent(refTo(key))}},
				},
			},
			want: 1,
		},
		{
			name: "every site is summed",
			components: &openapi3.Components{
				Schemas: openapi3.Schemas{"_core.search___A": refTo(key)},
				RequestBodies: openapi3.RequestBodies{
					"search": {Value: &openapi3.RequestBody{Content: jsonContent(refTo(key))}},
				},
				Responses: openapi3.ResponseBodies{
					"search": {Value: &openapi3.Response{Content: jsonContent(refTo(key))}},
				},
			},
			want: 3,
		},
		{
			name: "unresolved bodies and empty media entries are skipped",
			components: &openapi3.Components{
				RequestBodies: openapi3.RequestBodies{
					"nilRef":   nil,
					"unloaded": {Ref: "#/components/requestBodies/other"},
					"noSchema": {Value: &openapi3.RequestBody{Content: openapi3.Content{
						"application/json": nil,
					}}},
				},
				Responses: openapi3.ResponseBodies{
					"nilRef":   nil,
					"unloaded": {Ref: "#/components/responses/other"},
					"noSchema": {Value: &openapi3.Response{Content: openapi3.Content{
						"application/json": nil,
					}}},
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, refCount(&openapi3.T{Components: tt.components}, key, skip))
		})
	}
}

// TestCountRefsTo pins the descent. It reaches every place a $ref can sit inside
// one schema but never follows a $ref into its target, so a name is credited
// once per site that mentions it rather than once per path that reaches it.
func TestCountRefsTo(t *testing.T) {
	t.Parallel()

	const key = "_core.search___SearchResult"
	keyRef := &openapi3.SchemaRef{Ref: "#/components/schemas/" + key}

	tests := []struct {
		name string
		ref  *openapi3.SchemaRef
		want int
	}{
		{
			name: "a nil ref",
			ref:  nil,
		},
		{
			name: "a SchemaRef with neither ref nor value",
			ref:  &openapi3.SchemaRef{},
		},
		{
			name: "a direct $ref",
			ref:  keyRef,
			want: 1,
		},
		{
			name: "a $ref to something else",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/_core.search___Other"},
		},
		{
			name: "allOf, oneOf, and anyOf members",
			ref: &openapi3.SchemaRef{Value: &openapi3.Schema{
				AllOf: openapi3.SchemaRefs{keyRef},
				OneOf: openapi3.SchemaRefs{keyRef},
				AnyOf: openapi3.SchemaRefs{keyRef},
			}},
			want: 3,
		},
		{
			name: "array items and additionalProperties",
			ref: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Items:                keyRef,
				AdditionalProperties: openapi3.AdditionalProperties{Schema: keyRef},
			}},
			want: 2,
		},
		{
			name: "properties",
			ref: &openapi3.SchemaRef{Value: &openapi3.Schema{Properties: openapi3.Schemas{
				"a": keyRef,
				"b": keyRef,
			}}},
			want: 2,
		},
		{
			// The loader resolves a $ref's Value, so descending through one
			// would count the target's own references as the referrer's.
			name: "the descent stops at a $ref rather than entering it",
			ref: &openapi3.SchemaRef{
				Ref:   "#/components/schemas/_core.search___Other",
				Value: &openapi3.Schema{Properties: openapi3.Schemas{"a": keyRef}},
			},
		},
		{
			// The descent stops at every $ref, so it only ever walks one
			// schema's inline subschemas: a finite tree, however deep. A bound
			// here would silently undercount a deeply nested reference and skew
			// the tiebreaker toward the other name.
			name: "deep inline nesting is counted in full",
			ref:  nestedItems(keyRef, 20),
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, countRefsTo(tt.ref, key))
		})
	}
}

// nestedItems wraps ref in depth levels of inline array containers.
func nestedItems(ref *openapi3.SchemaRef, depth int) *openapi3.SchemaRef {
	out := ref
	for range depth {
		out = &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{openapi3.TypeArray},
			Items: out,
		}}
	}
	return out
}

// TestCollapseTargetOf pins the spec-only mirror of walker.collapsesToBase used
// by the rename pass. It runs without a walker, so it cannot consult the erased
// type-parameter marker: any member that declares a property at all is treated
// as contributing, which makes it strictly more conservative than the walk. A
// disagreement in the other direction would plan a rename for a type that never
// collapsed, which plannedRenames catches by requiring both refs to resolve to
// the same registered type.
func TestCollapseTargetOf(t *testing.T) {
	t.Parallel()

	baseRef := &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Base"}

	tests := []struct {
		name       string
		schema     *openapi3.Schema
		wantTarget string
	}{
		{
			name:       "a bare $ref rename",
			schema:     &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef}},
			wantTarget: "pkg___Base",
		},
		{
			name: "a $ref plus an empty override",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Type:       &openapi3.Types{openapi3.TypeObject},
				Properties: openapi3.Schemas{},
			}}}},
			wantTarget: "pkg___Base",
		},
		{
			name:       "a member with neither ref nor value is skipped",
			schema:     &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {}}},
			wantTarget: "pkg___Base",
		},
		{
			name:   "a nil member makes the shape undecidable",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, nil}},
		},
		{
			name: "two $ref members compose distinct schemas",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				baseRef,
				{Ref: "#/components/schemas/pkg___Other"},
			}},
		},
		{
			name: "a member declaring a property contributes",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Properties: openapi3.Schemas{"extra": {Value: openapi3.NewStringSchema()}},
			}}}},
		},
		{
			name: "an allOf with no $ref member has nothing to collapse onto",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{
				Type: &openapi3.Types{openapi3.TypeObject},
			}}}},
		},
		{
			name: "a schema without an allOf is not a wrapper",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{
				"extra": {Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name: "a wrapper declaring its own properties adds something",
			schema: &openapi3.Schema{
				AllOf:      openapi3.SchemaRefs{baseRef},
				Properties: openapi3.Schemas{"extra": {Value: openapi3.NewStringSchema()}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := collapseTargetOf(tt.schema)
			require.Equal(t, tt.wantTarget != "", ok)
			require.Equal(t, tt.wantTarget, got)
		})
	}
}

// TestCollapseAliasesFollowsChains pins the chain resolution. The spec chains
// these collapses (RangeAggregate -> RangeAggregateBase ->
// MultiBucketAggregateBaseRangeBucket), and every alias in a chain must resolve
// to the same terminal target or the "exactly one alias" test in plannedRenames
// counts the same family twice and plans nothing.
func TestCollapseAliasesFollowsChains(t *testing.T) {
	t.Parallel()

	const (
		outer    = "_common.aggregations___RangeAggregate"
		middle   = "_common.aggregations___RangeAggregateBase"
		terminal = "_common.aggregations___MultiBucketAggregateBaseRangeBucket"
	)

	tests := []struct {
		name    string
		schemas openapi3.Schemas
		want    map[string]string
	}{
		{
			name: "a chain resolves every hop to the terminal target",
			schemas: openapi3.Schemas{
				outer:    aliasRefTo(middle),
				middle:   aliasRefTo(terminal),
				terminal: objectSchema(),
			},
			want: map[string]string{outer: terminal, middle: terminal},
		},
		{
			// The loader can leave a component unresolved; treating a nil Value
			// as a collapse would map an alias onto an empty target key.
			name: "unresolved and non-collapsing schemas are skipped",
			schemas: openapi3.Schemas{
				"_common.aggregations___NilRef":   nil,
				"_common.aggregations___Unloaded": {Ref: "#/components/schemas/" + terminal},
				"_common.aggregations___Plain":    objectSchema(),
				terminal:                          objectSchema(),
			},
			want: map[string]string{},
		},
		{
			// A cycle has no terminal target; breaking out at the repeat is what
			// keeps the resolution from spinning.
			name: "a cycle terminates",
			schemas: openapi3.Schemas{
				"_common.aggregations___A": aliasRefTo("_common.aggregations___B"),
				"_common.aggregations___B": aliasRefTo("_common.aggregations___A"),
			},
			want: map[string]string{
				"_common.aggregations___A": "_common.aggregations___B",
				"_common.aggregations___B": "_common.aggregations___A",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := &openapi3.T{Components: &openapi3.Components{Schemas: tt.schemas}}
			require.Equal(t, tt.want, collapseAliases(spec))
		})
	}
}

// TestRewriteTypeRefsLeavesUnnameableExpressionsAlone covers the guards on the
// splice. A type expression whose base name is empty or absent from the rename
// set has nothing to rewrite, and rewriting it anyway would corrupt the emitted
// type.
func TestRewriteTypeRefsLeavesUnnameableExpressionsAlone(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	old, ok := reg.register(&goType{Name: "OldName", SchemaRef: "pkg___Old"})
	require.True(t, ok)
	holder, ok := reg.register(&goType{
		Name:      "Holder",
		SchemaRef: "pkg___Holder",
		Fields: []goField{
			{GoName: "Empty", GoType: ""},
			{GoName: "Renamed", GoType: "OldName"},
			{GoName: "Unrelated", GoType: "SomethingElse"},
		},
	})
	require.True(t, ok)

	// An empty rename set is a no-op, not a pass that clears every type.
	reg.rewriteTypeRefs(nil)
	require.Equal(t, "OldName", holder.Fields[1].GoType)

	reg.rewriteTypeRefs([]typeRename{{typ: old, oldName: "OldName", newName: "NewName"}})

	require.Empty(t, holder.Fields[0].GoType, "an empty type expression names nothing")
	require.Equal(t, "NewName", holder.Fields[1].GoType)
	require.Equal(t, "SomethingElse", holder.Fields[2].GoType)
}

// TestContributesNothing pins what an inline allOf member has to say before the
// rename pass refuses to treat its wrapper as a pure alias. Anything it declares
// -- properties, composition, a container, an enumeration, a required field, a
// non-object type -- would be lost if the wrapper collapsed onto its base.
func TestContributesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			name:   "an empty schema",
			schema: &openapi3.Schema{},
			want:   true,
		},
		{
			// The spec's spelling for "same shape as the base": `type: object`
			// with nothing else to say.
			name:   "a bare object declaration",
			schema: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}},
			want:   true,
		},
		{
			name: "properties",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{
				"extra": {Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name:   "allOf",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "oneOf",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "anyOf",
			schema: &openapi3.Schema{AnyOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "array items",
			schema: &openapi3.Schema{Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
		},
		{
			name: "an additionalProperties schema",
			schema: &openapi3.Schema{AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name:   "an enumeration",
			schema: &openapi3.Schema{Enum: []any{"asc", "desc"}},
		},
		{
			name: "a newly required field",
			schema: &openapi3.Schema{
				Type:     &openapi3.Types{openapi3.TypeObject},
				Required: []string{"hits"},
			},
		},
		{
			name:   "a non-object type",
			schema: openapi3.NewStringSchema(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, contributesNothing(tt.schema))
		})
	}
}
