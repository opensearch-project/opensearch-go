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

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestTitleSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple word", input: "cluster", want: "Cluster"},
		{name: "id acronym", input: "id", want: "ID"},
		{name: "uuid acronym", input: "uuid", want: "UUID"},
		{name: "http acronym", input: "http", want: "HTTP"},
		{name: "url acronym", input: "url", want: "URL"},
		{name: "ip acronym", input: "ip", want: "IP"},
		{name: "tls acronym", input: "tls", want: "TLS"},
		{name: "ssl acronym", input: "ssl", want: "SSL"},
		{name: "api acronym", input: "api", want: "API"},
		{name: "json acronym", input: "json", want: "JSON"},
		{name: "empty", input: "", want: ""},
		{name: "mixed case id", input: "ID", want: "ID"},
		{name: "mixed case uuid", input: "UUID", want: "UUID"},
		{name: "already capitalized", input: "Stats", want: "Stats"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, titleSegment(tt.input))
		})
	}
}

// TestPathBuilderName verifies the mapping from x-operation-group to path
// builder struct name. These struct names appear in internal/path/builders_gen.go
// and are referenced by consumer files in v5preview/opensearchapi/ and v5preview/opensearchapi/plugins/ via their
// GetRequest() methods. The naming must stay deterministic across regeneration.
func TestPathBuilderName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		group string
		want  string
	}{
		{name: "simple", group: "search", want: "SearchPath"},
		{name: "dotted", group: "cluster.stats", want: "ClusterStatsPath"},
		{name: "underscore", group: "nodes.hot_threads", want: "NodesHotThreadsPath"},
		{name: "http acronym", group: "security.reload_http_certificates", want: "SecurityReloadHTTPCertificatesPath"},
		{name: "id acronym", group: "dangling_indices.import_dangling_index", want: "DanglingIndicesImportDanglingIndexPath"},
		{name: "multi dot", group: "cat.thread_pool", want: "CatThreadPoolPath"},
		{name: "core prefix", group: "_core.search", want: "CoreSearchPath"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, pathBuilderName(tt.group))
		})
	}
}

func TestPathFieldName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "index", want: "Index"},
		{name: "id suffix", input: "node_id", want: "NodeID"},
		{name: "uuid suffix", input: "index_uuid", want: "IndexUUID"},
		{name: "multi word", input: "scroll_id", want: "ScrollID"},
		{name: "http in middle", input: "reload_http_certificates", want: "ReloadHTTPCertificates"},
		{name: "dotted", input: "chime.url", want: "ChimeURL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, pathFieldName(tt.input))
		})
	}
}

func TestUnexportedFieldName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "index", want: "index"},
		{name: "id suffix", input: "node_id", want: "nodeID"},
		{name: "uuid suffix", input: "index_uuid", want: "indexUUID"},
		{name: "starts with acronym", input: "id", want: "id"},
		{name: "url alone", input: "url", want: "url"},
		{name: "multi word", input: "scroll_id", want: "scrollID"},
		{name: "dotted url", input: "chime.url", want: "chimeURL"},
		{name: "underscore and dot", input: "cluster_name", want: "clusterName"},
		{name: "go keyword", input: "type", want: "typeVal"},
		{name: "predeclared len", input: "len", want: "lenVal"},
		{name: "predeclared new", input: "new", want: "newVal"},
		{name: "predeclared string", input: "string", want: "stringVal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, unexportedFieldName(tt.input))
		})
	}
}

func TestBaseGoName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "cluster_name", want: "ClusterName"},
		{name: "leading underscore", input: "_nodes", want: "Nodes"},
		{name: "uuid", input: "cluster_uuid", want: "ClusterUUID"},
		{name: "plain", input: "status", want: "Status"},
		{name: "dotted", input: "some.field", want: "SomeField"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, baseGoName(tt.input))
		})
	}
}

func TestPkgScopedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		group string
		want  string
	}{
		{name: "core leaf", group: "search", want: "Search"},
		{name: "core dotted", group: "cluster.stats", want: "ClusterStats"},
		{name: "core underscore", group: "indices.get_alias", want: "IndicesGetAlias"},
		{name: "_core prefix stripped", group: "_core.search", want: "Search"},
		{name: "plugin prefix stripped", group: "knn.stats", want: "Stats"},
		{name: "plugin multi", group: "knn.search_models", want: "SearchModels"},
		{name: "plugin deep", group: "security.reload_http_certificates", want: "ReloadHTTPCertificates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, pkgScopedName(tt.group))
		})
	}
}

func TestOperationFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		group string
		want  string // full relative path: dir/basename_gen.go
	}{
		{name: "core leaf", group: "search", want: "v5preview/opensearchapi/search_gen.go"},
		{name: "core dotted", group: "indices.create", want: "v5preview/opensearchapi/indices-create_gen.go"},
		{name: "_core stripped", group: "_core.search", want: "v5preview/opensearchapi/search_gen.go"},
		{name: "plugin leaf", group: "knn.stats", want: "v5preview/opensearchapi/plugins/knn/stats_gen.go"},
		{name: "plugin underscore", group: "ism.add_policy", want: "v5preview/opensearchapi/plugins/ism/add_policy_gen.go"},
		{
			name:  "plugin multi word",
			group: "security.reload_http_certificates",
			want:  "v5preview/opensearchapi/plugins/security/reload_http_certificates_gen.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, dir := routeOperation(tt.group, ir.DefaultCoreSubpath, ir.DefaultCoreSubpath+"/plugins")
			got := dir + "/" + operationFilename(tt.group) + genFileSuffix
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHTTPMethodConst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   string
	}{
		{name: "get", method: http.MethodGet, want: "http.MethodGet"},
		{name: "post", method: http.MethodPost, want: "http.MethodPost"},
		{name: "put", method: http.MethodPut, want: "http.MethodPut"},
		{name: "delete", method: http.MethodDelete, want: "http.MethodDelete"},
		{name: "head", method: http.MethodHead, want: "http.MethodHead"},
		{name: "patch", method: http.MethodPatch, want: "http.MethodPatch"},
		{name: "options", method: http.MethodOptions, want: "http.MethodOptions"},
		{name: "trace", method: http.MethodTrace, want: "http.MethodTrace"},
		{name: "connect", method: http.MethodConnect, want: "http.MethodConnect"},
		{name: "lowercase", method: "get", want: "http.MethodGet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, httpMethodConst(tt.method))
		})
	}
}

func TestHTTPMethodConst_PanicsOnUnknown(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() { httpMethodConst("UNKNOWN") })
}

func TestGoFieldName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "nodeID", goFieldName("node_id"))
	require.Equal(t, "clusterName", goFieldName("cluster_name"))
}

func TestSchemaTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schemaKey  string
		isRespBody bool
		want       string
	}{
		{name: "common type", schemaKey: "_common___ShardStatistics", want: "ShardStatistics"},
		{name: "common error cause", schemaKey: "_common___ErrorCause", want: "ErrorCause"},
		{name: "common acknowledged", schemaKey: "_common___AcknowledgedResponseBase", want: "AcknowledgedRespBase"},
		{name: "resp body", schemaKey: "cluster.health___HealthResponseBody", isRespBody: true, want: "ClusterHealthResp"},
		{name: "resp body search", schemaKey: "_core.search___ResponseBody", isRespBody: true, want: "SearchResp"},
		{name: "de-stutter cluster.health", schemaKey: "cluster.health___IndexHealthStats", want: "ClusterHealthIndexStats"},
		{name: "de-stutter cluster.health shard", schemaKey: "cluster.health___ShardHealthStats", want: "ClusterHealthShardStats"},
		{name: "no stutter", schemaKey: "cluster.health___AwarenessAttributeStats", want: "ClusterHealthAwarenessAttributeStats"},
		{name: "no stutter short name", schemaKey: "cluster.health___Level", want: "ClusterHealthLevel"},
		{name: "core prefix stripped", schemaKey: "_core.search___SearchHits", want: "SearchHits"},
		{name: "core search hit", schemaKey: "_core.search___SearchHit", want: "SearchHit"},
		{name: "de-stutter cat aliases", schemaKey: "cat.aliases___AliasesRecord", want: "CatAliasesRecord"},
		{name: "de-stutter cat health", schemaKey: "cat.health___HealthRecord", want: "CatHealthRecord"},
		{name: "nodes stats", schemaKey: "nodes.stats___ClusterNodes", want: "NodesStatsClusterNodes"},
		{name: "cluster stats indices", schemaKey: "cluster.stats___ClusterIndices", want: "ClusterStatsClusterIndices"},
		{name: "group._common", schemaKey: "nodes._common___NodesResponseBase", want: "NodesRespBase"},
		{name: "group._common cluster", schemaKey: "cluster._common___ComponentTemplate", want: "ClusterComponentTemplate"},
		{name: "acronyms", schemaKey: "security._common___SSLInfo", want: "SecuritySSLInfo"},
		{name: "sql plugin", schemaKey: "sql._common___SQLQuery", want: "SQLQuery"},
		{name: "de-stutter empty result kept", schemaKey: "cluster.health___Health", want: "ClusterHealthHealth"},

		// Idiomatic abbreviations: M-prefix initialisms, compound nouns,
		// and the Response -> Resp shortening. Match at PascalCase
		// boundaries only -- "Responses" (lowercase 's') stays intact.
		{name: "msearch initialism", schemaKey: "_core.msearch___RequestItem", want: "MSearchRequestItem"},
		{name: "msearch within name", schemaKey: "_common___MsearchMultiSearchItem", want: "MSearchMultiSearchItem"},
		{name: "mget initialism", schemaKey: "_core.mget___Operation", want: "MGetOperation"},
		{name: "mtermvectors initialism + compound", schemaKey: "_core.mtermvectors___Operation", want: "MTermVectorsOperation"},
		{name: "termvectors compound", schemaKey: "_common___TermvectorsTerm", want: "TermVectorsTerm"},
		{name: "forcemerge compound", schemaKey: "indices.forcemerge___Stats", want: "IndicesForceMergeStats"},
		{name: "Response -> Resp at boundary", schemaKey: "_common___BulkResponseItem", want: "BulkRespItem"},
		{name: "Response standalone preserved", schemaKey: "_common___ErrorResponse", want: "ErrorResponse"},
		{
			name:      "Responses (plural) preserved",
			schemaKey: "_common___MsearchMultiSearchResultResponsesItem",
			want:      "MSearchMultiSearchResultResponsesItem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, schemaTypeName(tt.schemaKey, tt.isRespBody))
		})
	}
}

func TestIsScalarAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ref     string
		wantTyp string
		wantOK  bool
	}{
		{name: "Name is string", ref: "_common___Name", wantTyp: "string", wantOK: true},
		{name: "uint is int", ref: "_common___uint", wantTyp: "int", wantOK: true},
		{name: "PercentageNumber is float64", ref: "_common___PercentageNumber", wantTyp: "float64", wantOK: true},
		{name: "Duration is string", ref: "_common___Duration", wantTyp: "string", wantOK: true},
		{name: "EpochTimeUnitMillis is int64", ref: "_common___EpochTimeUnitMillis", wantTyp: "int64", wantOK: true},
		{name: "ShardStatistics is not scalar", ref: "_common___ShardStatistics", wantTyp: "", wantOK: false},
		{name: "ErrorCause is not scalar", ref: "_common___ErrorCause", wantTyp: "", wantOK: false},
		{name: "non-common is not scalar", ref: "cluster.health___Level", wantTyp: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			goType, ok := isScalarAlias(tt.ref)
			require.Equal(t, tt.wantOK, ok)
			if ok {
				require.Equal(t, tt.wantTyp, goType)
			}
		})
	}
}

func TestPascalFromSegments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "cluster", want: "Cluster"},
		{name: "dotted", input: "cluster.health", want: "ClusterHealth"},
		{name: "underscore", input: "hot_threads", want: "HotThreads"},
		{name: "core prefix", input: "_core.search", want: "Search"},
		{name: "bare core", input: "_core", want: "Core"},
		{name: "acronym", input: "http", want: "HTTP"},
		{name: "multi segment", input: "reload_http_certificates", want: "ReloadHTTPCertificates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, pascalFromSegments(tt.input))
		})
	}
}
