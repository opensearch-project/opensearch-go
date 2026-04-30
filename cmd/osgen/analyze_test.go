// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveStructName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"search", "searchPath"},
		{"indices.get_alias", "indicesGetAliasPath"},
		{"ism.explain_policy", "ismExplainPolicyPath"},
		{"indices.create", "indicesCreatePath"},
		{"indices.create_data_stream", "indicesCreateDataStreamPath"},
		{"cat.indices", "catIndicesPath"},
		{"nodes.info", "nodesInfoPath"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, deriveStructName(tt.input))
		})
	}
}

func TestGoFieldName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"index", "index"},
		{"name", "name"},
		{"id", "id"},
		{"new_index", "newIndex"},
		{"scroll_id", "scrollID"},
		{"node_id", "nodeID"},
		{"policy_id", "policyID"},
		{"type", "typeVal"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, goFieldName(tt.input))
		})
	}
}

func TestDeriveFields(t *testing.T) {
	t.Parallel()

	// Simulates indices.get_alias: /_alias, /_alias/{name}, /{index}/_alias, /{index}/_alias/{name}
	paths := []pathVariant{
		{path: "/_alias", pathParams: nil, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/_alias/{name}", pathParams: []string{"name"}, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/{index}/_alias", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/{index}/_alias/{name}", pathParams: []string{"index", "name"}, arrayParams: map[string]bool{"index": true, "name": true}},
	}

	fields := deriveFields(paths)

	require.Len(t, fields, 2)
	require.Equal(t, "index", fields[0].Name)
	require.False(t, fields[0].Required)
	require.True(t, fields[0].IsList)
	require.Equal(t, "name", fields[1].Name)
	require.False(t, fields[1].Required)
	require.True(t, fields[1].IsList)
}

func TestDeriveFieldsAllRequired(t *testing.T) {
	t.Parallel()

	paths := []pathVariant{
		{path: "/{index}", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
	}

	fields := deriveFields(paths)

	require.Len(t, fields, 1)
	require.Equal(t, "index", fields[0].Name)
	require.True(t, fields[0].Required)
	require.True(t, fields[0].IsList)
}

func TestBuildOps(t *testing.T) {
	t.Parallel()

	paths := []pathVariant{
		{path: "/_alias", pathParams: nil, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/_alias/{name}", pathParams: []string{"name"}, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/{index}/_alias", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true, "name": true}},
		{path: "/{index}/_alias/{name}", pathParams: []string{"index", "name"}, arrayParams: map[string]bool{"index": true, "name": true}},
	}
	fields := deriveFields(paths)
	ops := buildOps(paths, fields)

	// Subsumption: index is list-typed so no guard is needed.
	// Output should be linear: writeSegments(index), writeReq("_alias"), writeSegments(name)
	require.Equal(t, []emitOp{
		{Kind: opList, Value: "index"},
		{Kind: opLit, Value: "_alias"},
		{Kind: opList, Value: "name"},
	}, ops)
}

func TestBuildOpsScalarSubsumption(t *testing.T) {
	t.Parallel()

	// nodes.hot_threads: /_nodes/hot_threads, /_nodes/{node_id}/hot_threads
	// The scalar param's subtree contains the same literal suffix - hoist it.
	paths := []pathVariant{
		{path: "/_nodes/hot_threads", pathParams: nil},
		{path: "/_nodes/{node_id}/hot_threads", pathParams: []string{"node_id"}},
	}
	fields := deriveFields(paths)
	ops := buildOps(paths, fields)

	// Should emit: _nodes, ifStr(nodeID), field(nodeID), end, lit(hot_threads)
	require.Equal(t, []emitOp{
		{Kind: opLit, Value: "_nodes"},
		{Kind: opIfStr, Value: "nodeID"},
		{Kind: opField, Value: "nodeID"},
		{Kind: opEnd, Value: ""},
		{Kind: opLit, Value: "hot_threads"},
	}, ops)
}

func TestBuildOpsNodesInfo(t *testing.T) {
	t.Parallel()

	arrayParams := map[string]bool{"node_id": true, "metric": true, "node_id_or_metric": true}
	paths := []pathVariant{
		{path: "/_nodes", pathParams: nil, arrayParams: arrayParams},
		{path: "/_nodes/{node_id_or_metric}", pathParams: []string{"node_id_or_metric"}, arrayParams: arrayParams},
		{path: "/_nodes/{node_id}/{metric}", pathParams: []string{"node_id", "metric"}, arrayParams: arrayParams},
	}
	fields := deriveFields(paths)
	ops := buildOps(paths, fields)

	b := builder{StructName: "nodesInfoPath", Comment: "nodesInfoPath builds paths for the nodes.info operation group.", Fields: fields, Ops: ops}
	b.export()
	out, err := render([]builder{b}, "path", true)
	require.NoError(t, err)
	require.Contains(t, out, "NodeID")
	require.Contains(t, out, "Metric")
	require.Contains(t, out, "NodeIDOrMetric")
	require.Contains(t, out, "[]string")
}

func TestAnalyzeGroup(t *testing.T) {
	t.Parallel()

	arrayParams := map[string]bool{"index": true, "name": true}
	g := opGroup{
		name: "indices.get_alias",
		pathSpecs: []pathVariant{
			{path: "/_alias", pathParams: nil, arrayParams: arrayParams},
			{path: "/_alias/{name}", pathParams: []string{"name"}, arrayParams: arrayParams},
			{path: "/{index}/_alias", pathParams: []string{"index"}, arrayParams: arrayParams},
			{path: "/{index}/_alias/{name}", pathParams: []string{"index", "name"}, arrayParams: arrayParams},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "indicesGetAliasPath", b.StructName)
	require.Len(t, b.Fields, 2)
	require.False(t, b.Fields[0].Required)
	require.False(t, b.Fields[1].Required)
	require.NotEmpty(t, b.Ops)
}

func TestRender(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "search",
		pathSpecs: []pathVariant{
			{path: "/_search", pathParams: nil, arrayParams: map[string]bool{"index": true}},
			{path: "/{index}/_search", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
		},
	}
	b, err := analyzeGroup(g)
	require.NoError(t, err)
	b.export()

	out, err := render([]builder{b}, "path", true)
	require.NoError(t, err)
	require.Contains(t, out, "type SearchPath struct")
	require.Contains(t, out, "Index []string")
	require.Contains(t, out, "writeSegments(pb, p.Index)")
	require.Contains(t, out, `pb.writeReq("_search")`)
	require.Contains(t, out, "func (p SearchPath) Build()")
	require.NotContains(t, out, "errRequired")
}

func TestRenderRequired(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "indices.create",
		pathSpecs: []pathVariant{
			{path: "/{index}", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
		},
	}
	b, err := analyzeGroup(g)
	require.NoError(t, err)
	b.export()

	out, err := render([]builder{b}, "path", true)
	require.NoError(t, err)
	require.Contains(t, out, "len(p.Index) == 0")
	require.Contains(t, out, "errRequired")
}

func TestSegmentAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"canonical passthrough", "_alias", "_alias"},
		{"aliases to alias", "_aliases", "_alias"},
		{"hotthreads to hot_threads", "hotthreads", "hot_threads"},
		{"unknown passthrough", "_search", "_search"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, canonicalSegment(tt.input))
		})
	}
}

func TestPreferredLiteral(t *testing.T) {
	t.Parallel()

	children := map[string]*pathTrie{
		"_opendistro": {deprecated: true},
		"_plugins":    {deprecated: false},
	}
	seg, _ := preferredLiteral(children)
	require.Equal(t, "_plugins", seg)
}
