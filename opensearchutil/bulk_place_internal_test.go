// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerPlaceUsesTheServersRoutingOrder checks the value an item is placed
// by: the routing on the action line, else the indexer's default routing, else the
// document ID, as the server does.
func TestBulkIndexerPlaceUsesTheServersRoutingOrder(t *testing.T) {
	t.Parallel()

	const shards = 16

	empty := ""
	itemRouting := "tenant-a"

	tests := []struct {
		name        string
		item        BulkIndexerItem
		defaultRout string
		wantRouting string
		unplaced    bool
	}{
		{
			name:        "the item's routing wins over the default and the id",
			item:        BulkIndexerItem{Index: "alpha", DocumentID: "doc-1", Routing: &itemRouting},
			defaultRout: "tenant-default",
			wantRouting: itemRouting,
		},
		{
			name:        "the default routing applies to an item with none",
			item:        BulkIndexerItem{Index: "alpha", DocumentID: "doc-1"},
			defaultRout: "tenant-default",
			wantRouting: "tenant-default",
		},
		{
			name:        "an empty routing on the item is no routing",
			item:        BulkIndexerItem{Index: "alpha", DocumentID: "doc-1", Routing: &empty},
			defaultRout: "tenant-default",
			wantRouting: "tenant-default",
		},
		{
			name:        "with neither, the document id decides",
			item:        BulkIndexerItem{Index: "alpha", DocumentID: "doc-1"},
			wantRouting: "doc-1",
		},
		{
			name: "an item with no routing value at all belongs to no shard",
			item: BulkIndexerItem{Index: "alpha"}, unplaced: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			maps := map[string]ShardMap{"alpha": DefaultShardMap(shards)}
			cluster := newFakeCluster(maps, "n0", "n1")
			server := newFakeServer(cluster)
			bi := newClusterIndexer(t, server, BulkIndexerConfig{
				Routing: tt.defaultRout, FlushInterval: time.Hour,
			}).(*bulkIndexer)

			key, node, err := bi.place(t.Context(), tt.item)
			require.NoError(t, err)

			if tt.unplaced {
				require.Nil(t, key)
				require.Zero(t, node)

				return
			}

			want, ok := cluster.LookupShard("alpha", tt.wantRouting)
			require.True(t, ok)
			require.NotNil(t, key)
			require.Equal(t, shardKey{index: "alpha", shard: want.Shard}, *key)
			require.Equal(t, want.Node, node)
		})
	}
}

// TestBaseBulkIndexerObserverIgnoresEveryEvent: the embeddable base is a no-op, so
// an observer that overrides one method can leave the rest to it.
func TestBaseBulkIndexerObserverIgnoresEveryEvent(t *testing.T) {
	t.Parallel()

	var obs BulkIndexerObserver = BaseBulkIndexerObserver{}

	require.NotPanics(t, func() {
		obs.OnRequestEnd(t.Context(), BulkRequestEvent{})
		obs.OnRequestFailure(t.Context(), BulkFailureEvent{})
		obs.OnItemFailure(t.Context(), BulkFailureEvent{})
		obs.OnItemDone(t.Context(), BulkItemEvent{})
	})
}
