// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

func TestOperationClassifier(t *testing.T) {
	t.Parallel()
	c := opensearchtransport.NewOperationClassifier()

	tests := []struct {
		name   string
		method string
		path   string
		want   opensearchtransport.OperationID
	}{
		// Bulk
		{"bulk POST", http.MethodPost, "/_bulk", opensearchtransport.OpBulk},
		{"bulk PUT", http.MethodPut, "/_bulk", opensearchtransport.OpBulk},
		{"bulk with index", http.MethodPost, "/my-index/_bulk", opensearchtransport.OpBulk},
		{"bulk stream", http.MethodPost, "/_bulk/stream", opensearchtransport.OpBulkStream},
		{"reindex", http.MethodPost, "/_reindex", opensearchtransport.OpReindex},

		// Search
		{"search GET", http.MethodGet, "/_search", opensearchtransport.OpSearch},
		{"search POST", http.MethodPost, "/_search", opensearchtransport.OpSearch},
		{"search with index", http.MethodPost, "/events/_search", opensearchtransport.OpSearch},
		{"msearch", http.MethodPost, "/_msearch", opensearchtransport.OpMSearch},
		{"count", http.MethodGet, "/_count", opensearchtransport.OpCount},
		{"count with index", http.MethodPost, "/events/_count", opensearchtransport.OpCount},
		{"delete_by_query", http.MethodPost, "/events/_delete_by_query", opensearchtransport.OpDeleteByQuery},
		{"update_by_query", http.MethodPost, "/events/_update_by_query", opensearchtransport.OpUpdateByQuery},
		{"validate", http.MethodGet, "/_validate/query", opensearchtransport.OpValidate},
		{"rank_eval", http.MethodPost, "/_rank_eval", opensearchtransport.OpRankEval},
		{"search shards", http.MethodGet, "/_search_shards", opensearchtransport.OpSearchShards},
		{"field caps", http.MethodGet, "/_field_caps", opensearchtransport.OpFieldCaps},

		// Templates
		{"search template", http.MethodPost, "/_search/template", opensearchtransport.OpSearchTemplate},
		{"search template with index", http.MethodPost, "/events/_search/template", opensearchtransport.OpSearchTemplate},
		{"msearch template", http.MethodPost, "/_msearch/template", opensearchtransport.OpMSearchTmpl},

		// Scroll
		{"scroll get", http.MethodGet, "/_search/scroll", opensearchtransport.OpScrollGet},
		{"scroll post", http.MethodPost, "/_search/scroll", opensearchtransport.OpScrollGet},
		{"scroll delete", http.MethodDelete, "/_search/scroll", opensearchtransport.OpScrollDelete},

		// PIT
		{"pit create", http.MethodPost, "/events/_search/point_in_time", opensearchtransport.OpPITCreate},
		{"pit delete", http.MethodDelete, "/_search/point_in_time", opensearchtransport.OpPITDelete},
		{"pit list", http.MethodGet, "/_search/point_in_time/_all", opensearchtransport.OpPITList},

		// Document ops
		{"doc get", http.MethodGet, "/events/_doc/123", opensearchtransport.OpDocGet},
		{"doc exists", http.MethodHead, "/events/_doc/123", opensearchtransport.OpDocExists},
		{"doc index PUT", http.MethodPut, "/events/_doc/123", opensearchtransport.OpDocIndex},
		{"doc index POST", http.MethodPost, "/events/_doc", opensearchtransport.OpDocIndex},
		{"doc create", http.MethodPut, "/events/_create/123", opensearchtransport.OpDocCreate},
		{"doc update", http.MethodPost, "/events/_update/123", opensearchtransport.OpDocUpdate},
		{"doc delete", http.MethodDelete, "/events/_doc/123", opensearchtransport.OpDocDelete},
		{"source get", http.MethodGet, "/events/_source/123", opensearchtransport.OpDocSourceGet},
		{"source exists", http.MethodHead, "/events/_source/123", opensearchtransport.OpDocSourceExist},
		{"mget", http.MethodPost, "/_mget", opensearchtransport.OpMGet},
		{"termvectors", http.MethodGet, "/events/_termvectors", opensearchtransport.OpTermVectors},
		{"mtermvectors", http.MethodPost, "/_mtermvectors", opensearchtransport.OpMTermVectors},
		{"explain", http.MethodPost, "/events/_explain/123", opensearchtransport.OpExplain},

		// Ingest
		{"ingest get", http.MethodGet, "/_ingest/pipeline/my-pipe", opensearchtransport.OpIngestGet},
		{"ingest get all", http.MethodGet, "/_ingest/pipeline", opensearchtransport.OpIngestGet},
		{"ingest create", http.MethodPut, "/_ingest/pipeline/my-pipe", opensearchtransport.OpIngestCreate},
		{"ingest delete", http.MethodDelete, "/_ingest/pipeline/my-pipe", opensearchtransport.OpIngestDelete},
		{"ingest simulate", http.MethodPost, "/_ingest/pipeline/my-pipe/_simulate", opensearchtransport.OpIngestSimulate},

		// Maintenance
		{"refresh", http.MethodPost, "/_refresh", opensearchtransport.OpRefresh},
		{"refresh index", http.MethodPost, "/events/_refresh", opensearchtransport.OpRefresh},
		{"flush", http.MethodPost, "/_flush", opensearchtransport.OpFlush},
		{"flush synced", http.MethodPost, "/_flush/synced", opensearchtransport.OpFlush},
		{"forcemerge", http.MethodPost, "/_forcemerge", opensearchtransport.OpForceMerge},
		{"segments", http.MethodGet, "/_segments", opensearchtransport.OpSegments},
		{"cache clear", http.MethodPost, "/_cache/clear", opensearchtransport.OpCacheClear},
		{"recovery", http.MethodGet, "/events/_recovery", opensearchtransport.OpRecovery},
		{"shard stores", http.MethodGet, "/events/_shard_stores", opensearchtransport.OpShardStores},
		{"stats", http.MethodGet, "/_stats", opensearchtransport.OpStats},
		{"stats with metric", http.MethodGet, "/_stats/indexing", opensearchtransport.OpStats},
		{"stats with index", http.MethodGet, "/events/_stats", opensearchtransport.OpStats},

		// Rethrottle
		{"reindex rethrottle", http.MethodPost, "/_reindex/task1/_rethrottle", opensearchtransport.OpReindexRethrottle},
		{"ubq rethrottle", http.MethodPost, "/_update_by_query/task1/_rethrottle", opensearchtransport.OpUBQRethrottle},
		{"dbq rethrottle", http.MethodPost, "/_delete_by_query/task1/_rethrottle", opensearchtransport.OpDBQRethrottle},

		// Cluster info
		{"root GET", http.MethodGet, "/", opensearchtransport.OpClusterInfo},
		{"root HEAD", http.MethodHead, "/", opensearchtransport.OpClusterInfo},

		// Unknown
		{"unrecognized path", http.MethodGet, "/_unknown/endpoint", opensearchtransport.OpOther},
		{"unrecognized method", "DESTROY", "/_search", opensearchtransport.OpOther},
		{"empty path", http.MethodGet, "", opensearchtransport.OpOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.Classify(tt.method, tt.path)
			require.Equal(t, tt.want, got, "Classify(%q, %q) = %s, want %s",
				tt.method, tt.path, got, tt.want)
		})
	}
}

func TestOperationClassifier_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	c := opensearchtransport.NewOperationClassifier()

	const goroutines = 50
	done := make(chan struct{})
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 500 {
				c.Classify(http.MethodPost, "/events/_search")
				c.Classify(http.MethodPost, "/_bulk")
				c.Classify(http.MethodGet, "/events/_doc/123")
			}
		}()
	}
	for range goroutines {
		<-done
	}
}

func TestOperationID_Masking(t *testing.T) {
	t.Parallel()

	t.Run("IsWrite", func(t *testing.T) {
		t.Parallel()
		require.False(t, opensearchtransport.OpSearch.IsWrite())
		require.False(t, opensearchtransport.OpDocGet.IsWrite())
		require.True(t, opensearchtransport.OpBulk.IsWrite())
		require.True(t, opensearchtransport.OpDocIndex.IsWrite())
		require.True(t, opensearchtransport.OpDocDelete.IsWrite())
	})

	t.Run("IsRead", func(t *testing.T) {
		t.Parallel()
		require.True(t, opensearchtransport.OpSearch.IsRead())
		require.True(t, opensearchtransport.OpDocGet.IsRead())
		require.False(t, opensearchtransport.OpBulk.IsRead())
		require.False(t, opensearchtransport.OpDocIndex.IsRead())
	})

	t.Run("Category", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, opensearchtransport.CatSearch, opensearchtransport.OpSearch.Category())
		require.Equal(t, opensearchtransport.CatSearch, opensearchtransport.OpMSearch.Category())
		require.Equal(t, opensearchtransport.CatSearch, opensearchtransport.OpCount.Category())
		require.Equal(t, opensearchtransport.CatBulk, opensearchtransport.OpBulk.Category())
		require.Equal(t, opensearchtransport.CatDocRead, opensearchtransport.OpDocGet.Category())
		require.Equal(t, opensearchtransport.CatDocWrite, opensearchtransport.OpDocIndex.Category())
	})

	t.Run("IsSearchFamily", func(t *testing.T) {
		t.Parallel()
		isSearchFamily := func(op opensearchtransport.OperationID) bool {
			return op.Category() == opensearchtransport.CatSearch
		}
		require.True(t, isSearchFamily(opensearchtransport.OpSearch))
		require.True(t, isSearchFamily(opensearchtransport.OpMSearch))
		require.True(t, isSearchFamily(opensearchtransport.OpCount))
		require.False(t, isSearchFamily(opensearchtransport.OpBulk))
		require.False(t, isSearchFamily(opensearchtransport.OpDocGet))
	})
}

func TestOperationID_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   opensearchtransport.OperationID
		want string
	}{
		{opensearchtransport.OpSearch, "search"},
		{opensearchtransport.OpMSearch, "msearch"},
		{opensearchtransport.OpCount, "count"},
		{opensearchtransport.OpBulk, "bulk"},
		{opensearchtransport.OpBulkStream, "bulk_stream"},
		{opensearchtransport.OpReindex, "reindex"},
		{opensearchtransport.OpDocGet, "doc_get"},
		{opensearchtransport.OpDocIndex, "doc_index"},
		{opensearchtransport.OpDocDelete, "doc_delete"},
		{opensearchtransport.OpDocCreate, "doc_create"},
		{opensearchtransport.OpDocUpdate, "doc_update"},
		{opensearchtransport.OpScrollGet, "scroll_get"},
		{opensearchtransport.OpScrollDelete, "scroll_delete"},
		{opensearchtransport.OpRefresh, "refresh"},
		{opensearchtransport.OpFlush, "flush"},
		{opensearchtransport.OpForceMerge, "forcemerge"},
		{opensearchtransport.OpStats, "stats"},
		{opensearchtransport.OpClusterInfo, "cluster_info"},
		{opensearchtransport.OpPing, "ping"},
		{opensearchtransport.OpOther, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.op.String())
		})
	}
}
