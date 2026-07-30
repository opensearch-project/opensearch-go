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
// retired in favour of SearchResponse.
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
