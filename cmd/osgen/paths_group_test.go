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

	groups, _ := groupFromSpec(spec, nil, VersionRange{})

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
	groups, _ := groupFromSpec(spec, filter, VersionRange{})

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

	groups, _ := groupFromSpec(spec, nil, VersionRange{})
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

	groups, _ := groupFromSpec(spec, nil, VersionRange{})
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

func TestSchemaUnionMembers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   []string
	}{
		{
			name: "anyOf with two titled members",
			schema: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Title: "node_id"}},
					{Value: &openapi3.Schema{Title: "metric"}},
				},
			},
			want: []string{"node_id", "metric"},
		},
		{
			name:   "no anyOf",
			schema: &openapi3.Schema{Type: &openapi3.Types{"string"}},
			want:   nil,
		},
		{
			name: "single anyOf member is not a union",
			schema: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Title: "only"}},
				},
			},
			want: nil,
		},
		{
			name: "anyOf with anonymous member returns nil",
			schema: &openapi3.Schema{
				AnyOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Title: "node_id"}},
					{Value: &openapi3.Schema{}}, // no Title
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, schemaUnionMembers(tt.schema))
		})
	}
}

func TestExpandUnionPaths(t *testing.T) {
	t.Parallel()

	// Helper: build a pathVariant with the given path and pathParams,
	// using empty methods/arrayParams maps. The expansion logic must
	// deep-copy these so mutations on synthetic variants don't bleed
	// back into the original.
	mkVariant := func(path string, params ...string) pathVariant {
		return pathVariant{
			path:        path,
			pathParams:  append([]string(nil), params...),
			methods:     map[string]struct{}{"GET": {}},
			arrayParams: map[string]bool{},
		}
	}

	tests := []struct {
		name        string
		group       opGroup
		wantPaths   []string
		wantParams  map[string][]string // path -> ordered pathParams
		description string
	}{
		{
			name: "nodes.info-style union expands into per-member variants",
			group: opGroup{
				name: "nodes.info",
				pathSpecs: []pathVariant{
					mkVariant("/_nodes"),
					mkVariant("/_nodes/{node_id_or_metric}", "node_id_or_metric"),
					mkVariant("/_nodes/{node_id}/{metric}", "node_id", "metric"),
				},
				unionParams: map[string][]string{
					"node_id_or_metric": {"node_id", "metric"},
				},
			},
			wantPaths: []string{
				"/_nodes",
				"/_nodes/{node_id}",
				"/_nodes/{metric}",
				"/_nodes/{node_id}/{metric}",
			},
			wantParams: map[string][]string{
				"/_nodes":                    nil,
				"/_nodes/{node_id}":          {"node_id"},
				"/_nodes/{metric}":           {"metric"},
				"/_nodes/{node_id}/{metric}": {"node_id", "metric"},
			},
			description: "synthetic variants replace the union variant; member names appear as path params",
		},
		{
			name: "no union params -- group passes through untouched",
			group: opGroup{
				name: "indices.refresh",
				pathSpecs: []pathVariant{
					mkVariant("/_refresh"),
					mkVariant("/{index}/_refresh", "index"),
				},
			},
			wantPaths: []string{"/_refresh", "/{index}/_refresh"},
			wantParams: map[string][]string{
				"/_refresh":         nil,
				"/{index}/_refresh": {"index"},
			},
			description: "expandUnionPaths is a no-op when unionParams is nil",
		},
		{
			name: "union member missing from any other variant -- expansion is suppressed",
			group: opGroup{
				name: "synthetic",
				pathSpecs: []pathVariant{
					mkVariant("/{maybe}", "maybe"),
				},
				unionParams: map[string][]string{
					"maybe": {"a", "b"}, // neither "a" nor "b" appears in any pathParams
				},
			},
			wantPaths: []string{"/{maybe}"},
			wantParams: map[string][]string{
				"/{maybe}": {"maybe"},
			},
			description: "without member-name evidence in another variant, the synthetic param is left in place",
		},
		{
			name: "synthetic path collides with an existing variant -- collision is skipped",
			group: opGroup{
				name: "collide",
				pathSpecs: []pathVariant{
					// Already-present synthetic shape.
					mkVariant("/_p/{a}", "a"),
					mkVariant("/_p/{u}", "u"),
					mkVariant("/_p/{a}/{b}", "a", "b"),
				},
				unionParams: map[string][]string{
					"u": {"a", "b"},
				},
			},
			wantPaths: []string{
				"/_p/{a}",       // original
				"/_p/{b}",       // synthetic from union member b
				"/_p/{a}/{b}",   // original
			},
			wantParams: map[string][]string{
				"/_p/{a}":     {"a"},
				"/_p/{b}":     {"b"},
				"/_p/{a}/{b}": {"a", "b"},
			},
			description: "the would-be /_p/{a} synthetic is suppressed because /_p/{a} already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := tt.group
			expandUnionPaths(&g)

			gotPaths := make([]string, 0, len(g.pathSpecs))
			gotParams := make(map[string][]string, len(g.pathSpecs))
			for _, pv := range g.pathSpecs {
				gotPaths = append(gotPaths, pv.path)
				if len(pv.pathParams) == 0 {
					gotParams[pv.path] = nil
				} else {
					gotParams[pv.path] = pv.pathParams
				}
			}

			require.ElementsMatch(t, tt.wantPaths, gotPaths, tt.description)
			for path, want := range tt.wantParams {
				require.Equal(t, want, gotParams[path], "params mismatch for %s", path)
			}
		})
	}
}

func TestExpandUnionPaths_DeepCopiesMaps(t *testing.T) {
	t.Parallel()

	original := pathVariant{
		path:        "/_nodes/{node_id_or_metric}",
		pathParams:  []string{"node_id_or_metric"},
		methods:     map[string]struct{}{"GET": {}},
		arrayParams: map[string]bool{"node_id_or_metric": true},
	}
	g := opGroup{
		name: "nodes.info",
		pathSpecs: []pathVariant{
			{path: "/_nodes", methods: map[string]struct{}{"GET": {}}},
			original,
			{
				path:        "/_nodes/{node_id}/{metric}",
				pathParams:  []string{"node_id", "metric"},
				methods:     map[string]struct{}{"GET": {}},
				arrayParams: map[string]bool{},
			},
		},
		unionParams: map[string][]string{
			"node_id_or_metric": {"node_id", "metric"},
		},
	}

	expandUnionPaths(&g)

	// Find the synthetic variants and confirm their maps are independent
	// of the original, so mutations on one don't ripple to the others.
	var syn1, syn2 *pathVariant
	for i := range g.pathSpecs {
		switch g.pathSpecs[i].path {
		case "/_nodes/{node_id}":
			syn1 = &g.pathSpecs[i]
		case "/_nodes/{metric}":
			syn2 = &g.pathSpecs[i]
		}
	}
	require.NotNil(t, syn1, "expected /_nodes/{node_id} synthetic variant")
	require.NotNil(t, syn2, "expected /_nodes/{metric} synthetic variant")

	syn1.methods["POST"] = struct{}{}
	require.NotContains(t, syn2.methods, "POST", "synthetic variants share no method map")
	require.NotContains(t, original.methods, "POST", "synthetic mutation leaked into original")
}
