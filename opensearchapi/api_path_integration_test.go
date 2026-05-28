// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestGetRequest_Paths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        requestBuilder
		wantMethod string
		wantPath   string
		wantErr    bool
	}{
		// Search
		{
			name: "search no index", req: opensearchapi.SearchReq{},
			wantMethod: http.MethodPost, wantPath: "/_search",
		},
		{
			name:       "search with index",
			req:        opensearchapi.SearchReq{Indices: []string{"logs"}},
			wantMethod: http.MethodPost, wantPath: "/logs/_search",
		},
		{
			name:       "search multi-index",
			req:        opensearchapi.SearchReq{Indices: []string{"logs", "metrics"}},
			wantMethod: http.MethodPost, wantPath: "/logs,metrics/_search",
		},

		// Index CRUD
		{
			name:       "index create",
			req:        opensearchapi.IndicesCreateReq{Index: "test-idx"},
			wantMethod: http.MethodPut, wantPath: "/test-idx",
		},
		{
			name: "index create empty", req: opensearchapi.IndicesCreateReq{},
			wantMethod: http.MethodPut, wantErr: true,
		},
		{
			name:       "index delete",
			req:        opensearchapi.IndicesDeleteReq{Indices: []string{"old-idx"}},
			wantMethod: http.MethodDelete, wantPath: "/old-idx",
		},
		{
			name:       "index exists",
			req:        opensearchapi.IndicesExistsReq{Indices: []string{"my-idx"}},
			wantMethod: http.MethodHead, wantPath: "/my-idx",
		},
		{
			name:       "index get",
			req:        opensearchapi.IndicesGetReq{Indices: []string{"my-idx"}},
			wantMethod: http.MethodGet, wantPath: "/my-idx",
		},

		// Index open/close
		{
			name:       "index open",
			req:        opensearchapi.IndicesOpenReq{Index: "my-idx"},
			wantMethod: http.MethodPost, wantPath: "/my-idx/_open",
		},
		{
			name:       "index close",
			req:        opensearchapi.IndicesCloseReq{Index: "my-idx"},
			wantMethod: http.MethodPost, wantPath: "/my-idx/_close",
		},

		// Alias
		{
			name: "alias get all", req: opensearchapi.AliasGetReq{},
			wantMethod: http.MethodGet, wantPath: "/_alias",
		},
		{
			name:       "alias get by name",
			req:        opensearchapi.AliasGetReq{Alias: []string{"my-alias"}},
			wantMethod: http.MethodGet, wantPath: "/_alias/my-alias",
		},
		{
			name:       "alias get by index",
			req:        opensearchapi.AliasGetReq{Indices: []string{"logs"}},
			wantMethod: http.MethodGet, wantPath: "/logs/_alias",
		},
		{
			name: "alias get both",
			req: opensearchapi.AliasGetReq{
				Indices: []string{"logs"}, Alias: []string{"a1"},
			},
			wantMethod: http.MethodGet, wantPath: "/logs/_alias/a1",
		},

		// Documents
		{
			name:       "doc get",
			req:        opensearchapi.DocumentGetReq{Index: "idx", DocumentID: "123"},
			wantMethod: http.MethodGet, wantPath: "/idx/_doc/123",
		},
		{
			name:       "doc delete",
			req:        opensearchapi.DocumentDeleteReq{Index: "idx", DocumentID: "456"},
			wantMethod: http.MethodDelete, wantPath: "/idx/_doc/456",
		},
		{
			name:       "doc exists",
			req:        opensearchapi.DocumentExistsReq{Index: "idx", DocumentID: "789"},
			wantMethod: http.MethodHead, wantPath: "/idx/_doc/789",
		},
		{
			name: "doc create",
			req: opensearchapi.DocumentCreateReq{
				Index: "idx", DocumentID: "abc", Body: strings.NewReader("{}"),
			},
			wantMethod: http.MethodPut, wantPath: "/idx/_create/abc",
		},

		// Bulk
		{
			name:       "bulk no index",
			req:        opensearchapi.BulkReq{Body: strings.NewReader("")},
			wantMethod: http.MethodPost, wantPath: "/_bulk",
		},
		{
			name: "bulk with index",
			req: opensearchapi.BulkReq{
				Index: "logs", Body: strings.NewReader(""),
			},
			wantMethod: http.MethodPost, wantPath: "/logs/_bulk",
		},

		// Cat
		{
			name: "cat indices all", req: opensearchapi.CatIndicesReq{},
			wantMethod: http.MethodGet, wantPath: "/_cat/indices",
		},
		{
			name:       "cat indices specific",
			req:        opensearchapi.CatIndicesReq{Indices: []string{"logs"}},
			wantMethod: http.MethodGet, wantPath: "/_cat/indices/logs",
		},
		{
			name: "cat health", req: opensearchapi.CatHealthReq{},
			wantMethod: http.MethodGet, wantPath: "/_cat/health",
		},
		{
			name: "cat nodes", req: opensearchapi.CatNodesReq{},
			wantMethod: http.MethodGet, wantPath: "/_cat/nodes",
		},
		{
			name: "cat aliases all", req: opensearchapi.CatAliasesReq{},
			wantMethod: http.MethodGet, wantPath: "/_cat/aliases",
		},
		{
			name:       "cat aliases specific",
			req:        opensearchapi.CatAliasesReq{Aliases: []string{"my-alias"}},
			wantMethod: http.MethodGet, wantPath: "/_cat/aliases/my-alias",
		},
		{
			name:       "cat shards",
			req:        opensearchapi.CatShardsReq{Indices: []string{"idx"}},
			wantMethod: http.MethodGet, wantPath: "/_cat/shards/idx",
		},

		// Cluster
		{
			name: "cluster health", req: opensearchapi.ClusterHealthReq{},
			wantMethod: http.MethodGet, wantPath: "/_cluster/health",
		},
		{
			name:       "cluster health index",
			req:        opensearchapi.ClusterHealthReq{Indices: []string{"logs"}},
			wantMethod: http.MethodGet, wantPath: "/_cluster/health/logs",
		},
		{
			name:       "cluster settings get",
			req:        opensearchapi.ClusterGetSettingsReq{},
			wantMethod: http.MethodGet, wantPath: "/_cluster/settings",
		},

		// Nodes
		{
			name: "nodes hot threads", req: opensearchapi.NodesHotThreadsReq{},
			wantMethod: http.MethodGet, wantPath: "/_nodes/hot_threads",
		},
		{
			name: "nodes hot threads node",
			req: opensearchapi.NodesHotThreadsReq{
				NodeID: []string{"node1"},
			},
			wantMethod: http.MethodGet, wantPath: "/_nodes/node1/hot_threads",
		},
		{
			name:       "nodes info no params",
			req:        opensearchapi.NodesInfoReq{},
			wantMethod: http.MethodGet, wantPath: "/_nodes",
		},
		{
			name:       "nodes info node",
			req:        opensearchapi.NodesInfoReq{NodeID: []string{"node1"}},
			wantMethod: http.MethodGet, wantPath: "/_nodes/node1",
		},
		{
			name:       "nodes info metric",
			req:        opensearchapi.NodesInfoReq{Metrics: []string{"jvm"}},
			wantMethod: http.MethodGet, wantPath: "/_nodes/jvm",
		},
		{
			name:       "nodes info node and metric",
			req:        opensearchapi.NodesInfoReq{NodeID: []string{"node1"}, Metrics: []string{"jvm"}},
			wantMethod: http.MethodGet, wantPath: "/_nodes/node1/jvm",
		},
		{
			name:       "nodes info multi-node multi-metric",
			req:        opensearchapi.NodesInfoReq{NodeID: []string{"node1", "node2"}, Metrics: []string{"jvm", "os"}},
			wantMethod: http.MethodGet, wantPath: "/_nodes/node1,node2/jvm,os",
		},

		// Count
		{
			name: "count all", req: opensearchapi.IndicesCountReq{},
			wantMethod: http.MethodPost, wantPath: "/_count",
		},
		{
			name:       "count index",
			req:        opensearchapi.IndicesCountReq{Indices: []string{"logs"}},
			wantMethod: http.MethodPost, wantPath: "/logs/_count",
		},

		// Mapping
		{
			name:       "mapping get",
			req:        opensearchapi.MappingGetReq{Indices: []string{"idx"}},
			wantMethod: http.MethodGet, wantPath: "/idx/_mapping",
		},
		{
			name: "mapping put",
			req: opensearchapi.MappingPutReq{
				Indices: []string{"idx"}, Body: strings.NewReader("{}"),
			},
			wantMethod: http.MethodPut, wantPath: "/idx/_mapping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			httpReq, err := tt.req.GetRequest(tt.wantMethod)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMethod, httpReq.Method)
			require.Equal(t, tt.wantPath, httpReq.URL.Path)
		})
	}
}

type requestBuilder interface {
	GetRequest(method string) (*http.Request, error)
}
