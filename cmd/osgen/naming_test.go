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
		{name: "starts with acronym", input: "id", want: "iD"},
		{name: "multi word", input: "scroll_id", want: "scrollID"},
		{name: "dotted url", input: "chime.url", want: "chimeURL"},
		{name: "underscore and dot", input: "cluster_name", want: "clusterName"},
		{name: "go keyword", input: "type", want: "typeVal"},
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
		{name: "core leaf", group: "search", want: "opensearchapi/search_gen.go"},
		{name: "core dotted", group: "indices.create", want: "opensearchapi/indices-create_gen.go"},
		{name: "_core stripped", group: "_core.search", want: "opensearchapi/search_gen.go"},
		{name: "plugin leaf", group: "knn.stats", want: "plugins/knn/stats_gen.go"},
		{name: "plugin underscore", group: "ism.add_policy", want: "plugins/ism/add_policy_gen.go"},
		{name: "plugin multi word", group: "security.reload_http_certificates", want: "plugins/security/reload_http_certificates_gen.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, dir := routeOperation(tt.group, "opensearchapi", "plugins")
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
		{name: "get", method: "GET", want: "http.MethodGet"},
		{name: "post", method: "POST", want: "http.MethodPost"},
		{name: "put", method: "PUT", want: "http.MethodPut"},
		{name: "delete", method: "DELETE", want: "http.MethodDelete"},
		{name: "head", method: "HEAD", want: "http.MethodHead"},
		{name: "patch", method: "PATCH", want: "http.MethodPatch"},
		{name: "options", method: "OPTIONS", want: "http.MethodOptions"},
		{name: "trace", method: "TRACE", want: "http.MethodTrace"},
		{name: "connect", method: "CONNECT", want: "http.MethodConnect"},
		{name: "lowercase", method: "get", want: "http.MethodGet"},
		{name: "unknown defaults to get", method: "UNKNOWN", want: "http.MethodGet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, httpMethodConst(tt.method))
		})
	}
}

func TestGoFieldName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "nodeID", goFieldName("node_id"))
	require.Equal(t, "clusterName", goFieldName("cluster_name"))
}
