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

func TestGroupFromSpec(t *testing.T) {
	t.Parallel()

	spec := &openapi3.T{
		Paths: openapi3.NewPaths(
			openapi3.WithPath("/_cluster/health", &openapi3.PathItem{
				Get: &openapi3.Operation{
					Extensions:  map[string]any{extOperationGroup: "cluster.health"},
					Description: "Returns cluster health status.",
				},
			}),
			openapi3.WithPath("/{index}/_refresh", &openapi3.PathItem{
				Post: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "indices.refresh"},
					Parameters: openapi3.Parameters{
						{Value: &openapi3.Parameter{Name: "index", In: "path", Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"array"}},
						}}},
					},
				},
			}),
			openapi3.WithPath("/_refresh", &openapi3.PathItem{
				Post: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "indices.refresh"},
				},
			}),
		),
	}

	groups := groupFromSpec(spec, nil)

	require.Len(t, groups, 2)

	// Groups are sorted by name.
	require.Equal(t, "cluster.health", groups[0].name)
	require.Equal(t, "indices.refresh", groups[1].name)

	// cluster.health has one path variant.
	require.Len(t, groups[0].pathSpecs, 1)
	require.Equal(t, "/_cluster/health", groups[0].pathSpecs[0].path)

	// indices.refresh has two path variants.
	require.Len(t, groups[1].pathSpecs, 2)
}

func TestGroupFromSpec_Filter(t *testing.T) {
	t.Parallel()

	spec := &openapi3.T{
		Paths: openapi3.NewPaths(
			openapi3.WithPath("/_cluster/health", &openapi3.PathItem{
				Get: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "cluster.health"},
				},
			}),
			openapi3.WithPath("/_search", &openapi3.PathItem{
				Post: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "search"},
				},
			}),
		),
	}

	filter := map[string]bool{"search": true}
	groups := groupFromSpec(spec, filter)

	require.Len(t, groups, 1)
	require.Equal(t, "search", groups[0].name)
}

func TestGroupFromSpec_SkipsNoGroup(t *testing.T) {
	t.Parallel()

	spec := &openapi3.T{
		Paths: openapi3.NewPaths(
			openapi3.WithPath("/no-group", &openapi3.PathItem{
				Get: &openapi3.Operation{},
			}),
		),
	}

	groups := groupFromSpec(spec, nil)
	require.Empty(t, groups)
}

func TestGroupFromSpec_DeduplicatesPath(t *testing.T) {
	t.Parallel()

	// Same path with GET (deprecated) and POST (not deprecated).
	spec := &openapi3.T{
		Paths: openapi3.NewPaths(
			openapi3.WithPath("/{index}/_search", &openapi3.PathItem{
				Get: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "search"},
					Deprecated: true,
				},
				Post: &openapi3.Operation{
					Extensions: map[string]any{extOperationGroup: "search"},
				},
			}),
		),
	}

	groups := groupFromSpec(spec, nil)
	require.Len(t, groups, 1)
	// The path should not be duplicated; the non-deprecated operation wins.
	require.Len(t, groups[0].pathSpecs, 1)
	require.False(t, groups[0].pathSpecs[0].deprecated)
}

func TestPathParamInfo(t *testing.T) {
	t.Parallel()

	pathItem := &openapi3.PathItem{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "index", In: "path", Schema: &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"array"}},
			}}},
		},
	}
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "id", In: "path", Schema: &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
			}}},
			{Value: &openapi3.Parameter{Name: "q", In: "query", Schema: &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
			}}},
		},
	}

	params, arrayParams := pathParamInfo(pathItem, op, "/{index}/_doc/{id}")

	require.Equal(t, []string{"index", "id"}, params)
	require.True(t, arrayParams["index"])
	require.False(t, arrayParams["id"])
}

func TestIsArrayParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		param *openapi3.Parameter
		want  bool
	}{
		{name: "nil schema", param: &openapi3.Parameter{}, want: false},
		{name: "string type", param: &openapi3.Parameter{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
		}, want: false},
		{name: "array type", param: &openapi3.Parameter{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}},
		}, want: true},
		{name: "oneOf with array", param: &openapi3.Parameter{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
					{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}},
				},
			}},
		}, want: true},
		{name: "anyOf with array", param: &openapi3.Parameter{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
					{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}},
				},
			}},
		}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isArrayParam(tt.param))
		})
	}
}

func TestSchemaIsArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{name: "direct array", schema: &openapi3.Schema{Type: &openapi3.Types{"array"}}, want: true},
		{name: "string", schema: &openapi3.Schema{Type: &openapi3.Types{"string"}}, want: false},
		{name: "oneOf array", schema: &openapi3.Schema{
			OneOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}}},
		}, want: true},
		{name: "anyOf array", schema: &openapi3.Schema{
			AnyOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}}},
		}, want: true},
		{name: "no type", schema: &openapi3.Schema{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, schemaIsArray(tt.schema))
		})
	}
}

func TestFindPath(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "test",
		pathSpecs: []pathVariant{
			{path: "/a"},
			{path: "/b"},
		},
	}

	require.NotNil(t, g.findPath("/a"))
	require.NotNil(t, g.findPath("/b"))
	require.Nil(t, g.findPath("/c"))
}
