// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want []string
	}{
		{name: "simple", path: "/_cluster/health", want: []string{"_cluster", "health"}},
		{name: "with param", path: "/{index}/_refresh", want: []string{"{index}", "_refresh"}},
		{name: "root", path: "/", want: []string{}},
		{name: "trailing slash", path: "/_cat/indices/", want: []string{"_cat", "indices"}},
		{name: "multiple params", path: "/{index}/_doc/{id}", want: []string{"{index}", "_doc", "{id}"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, splitPath(tt.path))
		})
	}
}

func TestDeriveFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []pathVariant
		want  []builderField
	}{
		{
			name: "single required param",
			paths: []pathVariant{
				{path: "/{index}/_refresh", pathParams: []string{"index"}, arrayParams: map[string]bool{}},
			},
			want: []builderField{
				{Name: "index", Param: "index", Required: true, IsList: false},
			},
		},
		{
			name: "optional param (not in all variants)",
			paths: []pathVariant{
				{path: "/_refresh", pathParams: []string{}, arrayParams: map[string]bool{}},
				{path: "/{index}/_refresh", pathParams: []string{"index"}, arrayParams: map[string]bool{}},
			},
			want: []builderField{
				{Name: "index", Param: "index", Required: false, IsList: false},
			},
		},
		{
			name: "array param",
			paths: []pathVariant{
				{path: "/{index}/_refresh", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
			},
			want: []builderField{
				{Name: "index", Param: "index", Required: true, IsList: true},
			},
		},
		{
			name: "multiple params ordered by longest path",
			paths: []pathVariant{
				{path: "/{index}/_doc/{id}", pathParams: []string{"index", "id"}, arrayParams: map[string]bool{}},
				{path: "/{index}/_doc", pathParams: []string{"index"}, arrayParams: map[string]bool{}},
			},
			want: []builderField{
				{Name: "index", Param: "index", Required: true, IsList: false},
				{Name: "id", Param: "id", Required: false, IsList: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deriveFields(tt.paths)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAnalyzeGroup_SinglePath(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "cluster.health",
		pathSpecs: []pathVariant{
			{
				path:         "/_cluster/health",
				methods:      map[string]struct{}{http.MethodGet: {}},
				pathParams:   []string{},
				arrayParams:  map[string]bool{},
				description:  "Returns cluster health.",
				versionAdded: "1.0",
			},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "ClusterHealthPath", b.StructName)
	require.Equal(t, "cluster.health", b.Group)
	require.Equal(t, "Returns cluster health.", b.Description)
	require.Equal(t, "1.0", b.VersionAdded)
	require.Empty(t, b.Fields)

	require.Len(t, b.Ops, 2)
	require.Equal(t, opLit, b.Ops[0].Kind)
	require.Equal(t, "_cluster", b.Ops[0].Value)
	require.Equal(t, opLit, b.Ops[1].Kind)
	require.Equal(t, "health", b.Ops[1].Value)
}

func TestAnalyzeGroup_RequiredParam(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "indices.delete",
		pathSpecs: []pathVariant{
			{
				path:        "/{index}",
				pathParams:  []string{"index"},
				arrayParams: map[string]bool{"index": true},
			},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "IndicesDeletePath", b.StructName)
	require.Len(t, b.Fields, 1)
	require.Equal(t, "index", b.Fields[0].Name)
	require.True(t, b.Fields[0].Required)
	require.True(t, b.Fields[0].IsList)

	// Should emit a list op for the required list param.
	require.NotEmpty(t, b.Ops)
	hasListOp := false
	for _, op := range b.Ops {
		if op.Kind == opList && op.Value == "index" {
			hasListOp = true
		}
	}
	require.True(t, hasListOp, "expected opList for index field")
}

func TestAnalyzeGroup_OptionalParam(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "indices.refresh",
		pathSpecs: []pathVariant{
			{path: "/_refresh", pathParams: []string{}, arrayParams: map[string]bool{}},
			{path: "/{index}/_refresh", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "IndicesRefreshPath", b.StructName)
	require.Len(t, b.Fields, 1)
	require.Equal(t, "index", b.Fields[0].Name)
	require.False(t, b.Fields[0].Required)
	require.True(t, b.Fields[0].IsList)
}

func TestAnalyzeGroup_DeprecatedVariant(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "nodes.hot_threads",
		pathSpecs: []pathVariant{
			{path: "/_nodes/hot_threads", pathParams: []string{}, arrayParams: map[string]bool{}, description: "Current desc."},
			{
				path:               "/_nodes/hotthreads",
				pathParams:         []string{},
				arrayParams:        map[string]bool{},
				deprecated:         true,
				deprecationMessage: "Use hot_threads.",
			},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.False(t, b.Deprecated, "group is not fully deprecated")
	require.Equal(t, "Use hot_threads.", b.DeprecationMessage)
}

func TestAnalyzeGroup_AllDeprecated(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "old.endpoint",
		pathSpecs: []pathVariant{
			{
				path:               "/_old",
				pathParams:         []string{},
				arrayParams:        map[string]bool{},
				deprecated:         true,
				deprecationMessage: "Removed.",
				versionDeprecated:  "2.0",
			},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.True(t, b.Deprecated)
	require.Equal(t, "2.0", b.VersionDeprecated)
}

func TestAnalyzeGroup_MultipleParams(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "indices.get_alias",
		pathSpecs: []pathVariant{
			{path: "/_alias", pathParams: []string{}, arrayParams: map[string]bool{}},
			{path: "/_alias/{name}", pathParams: []string{"name"}, arrayParams: map[string]bool{"name": true}},
			{path: "/{index}/_alias/{name}", pathParams: []string{"index", "name"}, arrayParams: map[string]bool{"index": true, "name": true}},
			{path: "/{index}/_alias", pathParams: []string{"index"}, arrayParams: map[string]bool{"index": true}},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "IndicesGetAliasPath", b.StructName)
	require.Len(t, b.Fields, 2)

	// Both fields should be optional (not in all variants).
	for _, f := range b.Fields {
		require.False(t, f.Required, "field %s should be optional", f.Name)
		require.True(t, f.IsList, "field %s should be a list", f.Name)
	}
}

func TestAnalyzeGroup_Empty(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name:      "empty",
		pathSpecs: nil,
	}

	_, err := analyzeGroup(g)
	require.Error(t, err)
}

func TestExport(t *testing.T) {
	t.Parallel()

	b := builder{
		Fields: []builderField{
			{Name: "index", Param: "index"},
			{Name: "node_id", Param: "node_id"},
		},
		Ops: []emitOp{
			{Kind: opField, Value: "index"},
			{Kind: opList, Value: "node_id"},
		},
	}

	b.export()

	require.Equal(t, "Index", b.Fields[0].Name)
	require.Equal(t, "NodeID", b.Fields[1].Name)
	require.Equal(t, "Index", b.Ops[0].Value)
	require.Equal(t, "NodeID", b.Ops[1].Value)
}

func TestCanonicalSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seg  string
		want string
	}{
		{name: "hotthreads mapping", seg: "hotthreads", want: "hot_threads"},
		{name: "no mapping", seg: "_cluster", want: "_cluster"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, canonicalSegment(tt.seg))
		})
	}
}

func TestOpKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind opKind
		want string
	}{
		{opLit, "lit"},
		{opField, "field"},
		{opList, "list"},
		{opSwitch, "switch"},
		{opCase, "case"},
		{opDefault, "default"},
		{opSwitchEnd, "switchEnd"},
		{opIf, "if"},
		{opIfEnd, "ifEnd"},
		{opExplainCheck, "explainCheck"},
		{opKind(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.kind.String())
		})
	}
}

func TestEmitOpPredicates(t *testing.T) {
	t.Parallel()

	ops := []emitOp{
		{Kind: opLit},
		{Kind: opField},
		{Kind: opList},
		{Kind: opSwitch},
		{Kind: opCase},
		{Kind: opDefault},
		{Kind: opSwitchEnd},
		{Kind: opIf},
		{Kind: opIfEnd},
		{Kind: opExplainCheck},
	}

	require.True(t, ops[0].IsLit())
	require.True(t, ops[1].IsField())
	require.True(t, ops[2].IsList())
	require.True(t, ops[3].IsSwitch())
	require.True(t, ops[4].IsCase())
	require.True(t, ops[5].IsDefault())
	require.True(t, ops[6].IsSwitchEnd())
	require.True(t, ops[7].IsIf())
	require.True(t, ops[8].IsIfEnd())
	require.True(t, ops[9].IsExplainCheck())
}

func TestAnalyzeGroup_OptionalStringParam(t *testing.T) {
	t.Parallel()

	// Optional non-list path param renders as a single if{} guard
	// since writeReq() does not self-skip empty strings.
	g := opGroup{
		name: "cat.indices",
		pathSpecs: []pathVariant{
			{path: "/_cat/indices", pathParams: []string{}, arrayParams: map[string]bool{}},
			{path: "/_cat/indices/{index}", pathParams: []string{"index"}, arrayParams: map[string]bool{}},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Len(t, b.Fields, 1)
	require.False(t, b.Fields[0].IsList)
	require.False(t, b.Fields[0].Required)

	// Should have an opIf wrapping the field write.
	var hasIf, hasIfEnd bool
	for _, op := range b.Ops {
		if op.Kind == opIf {
			hasIf = true
			require.Len(t, op.Conditions, 1)
			require.False(t, op.Conditions[0].IsList)
		}
		if op.Kind == opIfEnd {
			hasIfEnd = true
		}
	}
	require.True(t, hasIf, "expected opIf for optional string field")
	require.True(t, hasIfEnd, "expected opIfEnd to close the guard")
}

func TestAnalyzeGroup_DeprecatedAlias(t *testing.T) {
	t.Parallel()

	// Two literal paths for the same group, one deprecated. Exercises
	// dedupeEquivalentVariants' deprecation-aware selection.
	g := opGroup{
		name: "nodes.hot_threads",
		pathSpecs: []pathVariant{
			{path: "/_nodes/hot_threads", pathParams: []string{}, arrayParams: map[string]bool{}},
			{path: "/_nodes/hotthreads", pathParams: []string{}, arrayParams: map[string]bool{}, deprecated: true},
			{path: "/_nodes/{node_id}/hot_threads", pathParams: []string{"node_id"}, arrayParams: map[string]bool{}},
			{path: "/_nodes/{node_id}/hotthreads", pathParams: []string{"node_id"}, arrayParams: map[string]bool{}, deprecated: true},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "NodesHotThreadsPath", b.StructName)

	// Should prefer "hot_threads" over "hotthreads" (canonical segment).
	hasHotThreads := false
	for _, op := range b.Ops {
		if op.Kind == opLit && op.Value == "hot_threads" {
			hasHotThreads = true
		}
	}
	require.True(t, hasHotThreads, "expected preferred literal hot_threads")
}

func TestAnalyzeGroup_MultipleOptionalParams(t *testing.T) {
	t.Parallel()

	// Exercises elseIf branches when multiple optional params are at the same
	// trie level.
	g := opGroup{
		name: "cluster.state",
		pathSpecs: []pathVariant{
			{path: "/_cluster/state", pathParams: []string{}, arrayParams: map[string]bool{}},
			{path: "/_cluster/state/{metric}", pathParams: []string{"metric"}, arrayParams: map[string]bool{"metric": true}},
			{
				path:        "/_cluster/state/{metric}/{index}",
				pathParams:  []string{"metric", "index"},
				arrayParams: map[string]bool{"metric": true, "index": true},
			},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "ClusterStatePath", b.StructName)
	require.Len(t, b.Fields, 2)

	// Both fields should be optional lists.
	for _, f := range b.Fields {
		require.False(t, f.Required)
		require.True(t, f.IsList)
	}
}

func TestAnalyzeGroup_ExcludedDistros(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "plugin.op",
		pathSpecs: []pathVariant{
			{path: "/_plugin/op", pathParams: []string{}, arrayParams: map[string]bool{}, excludedDistros: []string{"amazon-managed"}},
			{path: "/_plugin/op/{id}", pathParams: []string{"id"}, arrayParams: map[string]bool{}, excludedDistros: []string{"amazon-managed"}},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, []string{"amazon-managed"}, b.ExcludedDistros)
}

func TestAnalyzeGroup_DocsURL(t *testing.T) {
	t.Parallel()

	g := opGroup{
		name: "search",
		pathSpecs: []pathVariant{
			{path: "/_search", pathParams: []string{}, arrayParams: map[string]bool{}, docsURL: "https://opensearch.org/docs/latest/search/"},
		},
	}

	b, err := analyzeGroup(g)
	require.NoError(t, err)
	require.Equal(t, "https://opensearch.org/docs/latest/search/", b.DocsURL)
}

// TestDerivePositionalDeps_Bidirectional verifies that positional-dep
// derivation considers both directions: a field can require another
// field that is positionally LATER in the path, not just earlier.
//
// cluster.stats has variants:
//
//	/_cluster/stats
//	/_cluster/stats/nodes/{node_id}
//	/_cluster/stats/{metric}/nodes/{node_id}
//	/_cluster/stats/{metric}/{index_metric}/nodes/{node_id}
//
// Field-order from longest path: [Metric, IndexMetric, NodeID].
//
// Without the bidirectional check, the loop only finds:
//   - IndexMetric requires Metric (forward)
//
// and silently misses Metric requires NodeID -- producing a Build()
// that emits /_cluster/stats/{metric} for {Metric: [m]} alone, which
// is NOT a spec-valid path. With the fix, both deps are derived.
func TestDerivePositionalDeps_Bidirectional(t *testing.T) {
	t.Parallel()

	paths := []pathVariant{
		{path: "/_cluster/stats", pathParams: []string{}},
		{path: "/_cluster/stats/nodes/{node_id}", pathParams: []string{"node_id"}},
		{path: "/_cluster/stats/{metric}/nodes/{node_id}", pathParams: []string{"metric", "node_id"}},
		{path: "/_cluster/stats/{metric}/{index_metric}/nodes/{node_id}", pathParams: []string{"metric", "index_metric", "node_id"}},
	}
	fields := []builderField{
		{Name: "metric", Param: "metric", IsList: true},
		{Name: "index_metric", Param: "index_metric", IsList: true},
		{Name: "node_id", Param: "node_id", IsList: true},
	}

	deps := derivePositionalDeps(paths, fields)

	depMap := make(map[string]string, len(deps))
	for _, d := range deps {
		depMap[d.Dependent.Param] = d.Predecessor.Param
	}

	require.Contains(t, depMap, "metric", "Metric must require some predecessor (transitively NodeID)")
	require.Equal(t, "node_id", depMap["metric"],
		"Metric must require NodeID -- the spec has no /_cluster/stats/{metric} variant without nodes/{node_id}")
	require.Contains(t, depMap, "index_metric", "IndexMetric must require some predecessor")
}

// TestDerivePositionalDeps_NoSpuriousDepForUnion verifies that
// expandUnionPaths' synthetic single-segment variants prevent the
// derivation from emitting a spurious "NodeID requires Metric" guard
// for nodes.info, since the spec's /_nodes/{node_id_or_metric} path
// (expanded into /_nodes/{node_id} and /_nodes/{metric}) makes both
// fields independently reachable.
func TestDerivePositionalDeps_NoSpuriousDepForUnion(t *testing.T) {
	t.Parallel()

	// Already-expanded variants -- this is what derivePositionalDeps
	// sees after expandUnionPaths runs in analyzeGroup.
	paths := []pathVariant{
		{path: "/_nodes", pathParams: []string{}},
		{path: "/_nodes/{node_id}", pathParams: []string{"node_id"}},
		{path: "/_nodes/{metric}", pathParams: []string{"metric"}},
		{path: "/_nodes/{node_id}/{metric}", pathParams: []string{"node_id", "metric"}},
	}
	fields := []builderField{
		{Name: "node_id", Param: "node_id", IsList: true},
		{Name: "metric", Param: "metric", IsList: true},
	}

	deps := derivePositionalDeps(paths, fields)

	require.Empty(t, deps, "union expansion makes both fields independently reachable; no positional dep should be derived")
}
