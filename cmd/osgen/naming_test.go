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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestTitleSegment(t *testing.T) {
	t.Parallel()

	// Cases are split into two groups, each sorted by input: acronym inputs
	// (exercising the acronyms map) first, then non-acronym inputs (baseline,
	// edge cases, idempotence).
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Acronyms, sorted by input.
		{name: "api acronym", input: "api", want: "API"},
		{name: "bm25 acronym", input: "bm25", want: "BM25"},
		{name: "cjk acronym", input: "cjk", want: "CJK"},
		{name: "cpu acronym", input: "cpu", want: "CPU"},
		{name: "csv acronym", input: "csv", want: "CSV"},
		{name: "dfs acronym", input: "dfs", want: "DFS"},
		{name: "gc acronym", input: "gc", want: "GC"},
		{name: "html acronym", input: "html", want: "HTML"},
		{name: "http acronym", input: "http", want: "HTTP"},
		{name: "icu acronym", input: "icu", want: "ICU"},
		{name: "id acronym", input: "id", want: "ID"},
		{name: "ids plural acronym keeps lowercase s", input: "ids", want: "IDs"},
		{name: "ip acronym", input: "ip", want: "IP"},
		{name: "ism acronym", input: "ism", want: "ISM"},
		{name: "json acronym", input: "json", want: "JSON"},
		{name: "jvm acronym", input: "jvm", want: "JVM"},
		{name: "knn acronym", input: "knn", want: "KNN"},
		{name: "ltr acronym", input: "ltr", want: "LTR"},
		{name: "ml acronym", input: "ml", want: "ML"},
		{name: "mmap two-cased not all-caps", input: "mmap", want: "MMap"},
		{name: "nio acronym", input: "nio", want: "NIO"},
		{name: "pits plural acronym keeps lowercase s", input: "pits", want: "PITs"},
		{name: "ppl acronym", input: "ppl", want: "PPL"},
		{name: "sm acronym", input: "sm", want: "SM"},
		{name: "ssl acronym", input: "ssl", want: "SSL"},
		{name: "tfidf acronym", input: "tfidf", want: "TFIDF"},
		{name: "tls acronym", input: "tls", want: "TLS"},
		{name: "ubi acronym", input: "ubi", want: "UBI"},
		{name: "url acronym", input: "url", want: "URL"},
		{name: "uuid acronym", input: "uuid", want: "UUID"},
		{name: "wlm acronym", input: "wlm", want: "WLM"},
		{name: "xy acronym", input: "xy", want: "XY"},

		// Non-acronyms (baseline, edge cases, idempotence), sorted by input.
		{name: "empty", input: "", want: ""},
		{name: "mixed case id", input: "ID", want: "ID"},
		{name: "already capitalized", input: "Stats", want: "Stats"},
		{name: "mixed case uuid", input: "UUID", want: "UUID"},
		{name: "simple word", input: "cluster", want: "Cluster"},
		{name: "whole-segment only, not substring", input: "smile", want: "Smile"},
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
// and are referenced by consumer files in opensearchapi/ and plugins/ via their
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

func TestPathFieldNameList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		isList bool
		want   string
	}{
		{name: "index list pluralizes", input: "index", isList: true, want: "Indices"},
		{name: "index scalar stays singular", input: "index", isList: false, want: "Index"},
		{name: "non-overridden list unchanged", input: "node_id", isList: true, want: "NodeID"},
		{name: "non-overridden scalar unchanged", input: "scroll_id", isList: false, want: "ScrollID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, pathFieldNameList(tt.input, tt.isList))
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

	// Cases are grouped with a blank line between groups: basic segment
	// splitting first, then aggregation-result branch codes, then hyphenated
	// titles.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Basic splitting on '_'/'.' and leading-underscore trimming.
		{name: "simple", input: "cluster_name", want: "ClusterName"},
		{name: "leading underscore", input: "_nodes", want: "Nodes"},
		{name: "uuid", input: "cluster_uuid", want: "ClusterUUID"},
		{name: "plain", input: "status", want: "Status"},
		{name: "dotted", input: "some.field", want: "SomeField"},

		// Aggregation-result branch titles: terse internal codes split into
		// the idiomatic PascalCase the decoded type uses. Sorted by input.
		{name: "lrareterms agg code", input: "lrareterms", want: "LRareTerms"},
		{name: "lterms agg code", input: "lterms", want: "LTerms"},
		{name: "siglterms agg code", input: "siglterms", want: "SigLTerms"},
		{name: "sterms agg code", input: "sterms", want: "STerms"},
		{name: "tdigest agg code", input: "tdigest_percentiles", want: "TDigestPercentiles"},
		{name: "ulterms agg code", input: "ulterms", want: "ULTerms"},
		{name: "umterms agg code", input: "umterms", want: "UMTerms"},

		// Hyphenated titles (search-pipeline processors) normalize to PascalCase.
		{name: "hyphenated title", input: "score-ranker-processor", want: "ScoreRankerProcessor"},
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
		{name: "core leaf", group: "search", want: "opensearchapi/search_gen.go"},
		{name: "core dotted", group: "indices.create", want: "opensearchapi/indices-create_gen.go"},
		{name: "_core stripped", group: "_core.search", want: "opensearchapi/search_gen.go"},
		{name: "plugin leaf", group: "knn.stats", want: "plugins/knn/stats_gen.go"},
		{name: "plugin underscore", group: "ism.add_policy", want: "plugins/ism/add_policy_gen.go"},
		{
			name:  "plugin multi word",
			group: "security.reload_http_certificates",
			want:  "plugins/security/reload_http_certificates_gen.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, dir := routeOperation(tt.group, ir.DefaultCoreSubpath, ir.DefaultPluginsSubpath)
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

	// Cases are grouped by the naming behavior they exercise, with a blank line
	// between groups: general de-stuttering and prefix handling first, then
	// embedded-acronym normalization, then idiomatic abbreviations.
	tests := []struct {
		name       string
		schemaKey  string
		isRespBody bool
		want       string
	}{
		// De-stuttering, prefix stripping, and group._common handling.
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
		{name: "ism plugin acronym", schemaKey: "ism._common___Policy", want: "ISMPolicy"},
		{name: "knn plugin acronym", schemaKey: "knn._common___Stats", want: "KNNStats"},

		// Embedded acronym in the local part must normalize and de-stutter:
		// "IsmTemplate" -> "ISMTemplate", not "ISMIsmTemplate".
		{name: "ism embedded acronym de-stutters", schemaKey: "ism._common___IsmTemplate", want: "ISMTemplate"},
		{name: "knn embedded acronym de-stutters", schemaKey: "knn._common___KnnMethod", want: "KNNMethod"},
		// Embedded acronyms normalize at PascalCase boundaries wherever they
		// appear in the local part, including plural forms (lowercase 's' kept)
		// and the two-cased MMap/NIO store types.
		{name: "cjk embedded acronym", schemaKey: "_common___AnalysisCjkAnalyzer", want: "AnalysisCJKAnalyzer"},
		{name: "html embedded acronym", schemaKey: "_common___AnalysisHtmlStripCharFilter", want: "AnalysisHTMLStripCharFilter"},
		{name: "ids plural embedded acronym", schemaKey: "_common___QueryDSLIdsQuery", want: "QueryDSLIDsQuery"},
		{name: "mmap embedded two-cased acronym", schemaKey: "_common___StoreHybridMmap", want: "StoreHybridMMap"},
		{name: "de-stutter empty result kept", schemaKey: "cluster.health___Health", want: "ClusterHealthHealth"},

		// Idiomatic abbreviations: M-prefix initialisms, compound nouns,
		// and the Response -> Resp shortening. Match at PascalCase
		// boundaries only -- "Responses" (lowercase 's') stays intact.
		// Ordered by the underlying abbreviation token, matching the sorted
		// idiomaticAbbreviations slice in naming.go.
		{name: "forcemerge compound", schemaKey: "indices.forcemerge___Stats", want: "IndicesForceMergeStats"},
		{name: "mget initialism", schemaKey: "_core.mget___Operation", want: "MGetOperation"},
		{name: "msearch initialism", schemaKey: "_core.msearch___RequestItem", want: "MSearchRequestItem"},
		{name: "msearch within name", schemaKey: "_common___MsearchMultiSearchItem", want: "MSearchMultiSearchItem"},
		{name: "mtermvectors initialism + compound", schemaKey: "_core.mtermvectors___Operation", want: "MTermVectorsOperation"},
		{name: "Response -> Resp at boundary", schemaKey: "_common___BulkResponseItem", want: "BulkRespItem"},
		{name: "Response standalone preserved", schemaKey: "_common___ErrorResponse", want: "ErrorResponse"},
		{
			name:      "Responses (plural) preserved",
			schemaKey: "_common___MsearchMultiSearchResultResponsesItem",
			want:      "MSearchMultiSearchResultResponsesItem",
		},
		{name: "termvectors compound", schemaKey: "_common___TermvectorsTerm", want: "TermVectorsTerm"},
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
