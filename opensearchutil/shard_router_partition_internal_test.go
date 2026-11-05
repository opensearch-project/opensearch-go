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

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// partitionedRouting is the routing value of the test documents.
const partitionedRouting = "user-7"

// partitioned is a 640-shard index (routing factor 1) with routing_partition_size 4.
var partitioned = ShardMap{RoutingNumShards: 640, PrimaryShards: 640, RoutingPartitionSize: 4}

// partitionedDoc returns an id whose partition offset is not zero for routing, and the
// shard the server puts the document on.
func partitionedDoc(t *testing.T) (string, int) {
	t.Helper()
	const routing = partitionedRouting

	for _, id := range []string{"doc-1", "doc-2", "doc-3", "doc-4", "doc-5", "doc-6"} {
		shard, _ := partitioned.ShardForDocument(routing, id)
		if shard != partitioned.Shard(routing) {
			return id, shard
		}
	}
	require.FailNow(t, "no test id with a non-zero partition offset")

	return "", 0
}

// keyOnlyRouter is a ShardRouter that does not implement DocShardRouter.
type keyOnlyRouter struct{ shard int }

func (r keyOnlyRouter) LookupShard(_, _ string, dst *[]*ShardTarget) error {
	*dst = (*dst)[:0]
	*dst = append(*dst, &ShardTarget{Shard: r.shard, Primary: true})

	return nil
}

func (keyOnlyRouter) WriteNodes() []Node { return nil }

func TestStaticShardRouterLookupDocShard(t *testing.T) {
	t.Parallel()

	const routing = partitionedRouting
	id, wantShard := partitionedDoc(t)
	idOnlyShard, _ := partitioned.ShardForDocument("", id)
	router := StaticShardRouter{"part": partitioned, "plain": DefaultShardMap(5)}

	tests := []struct {
		name        string
		index       string
		routing, id string
		wantShard   int
		wantErr     error
	}{
		{name: "partitioned routing and id", index: "part", routing: routing, id: id, wantShard: wantShard},
		{name: "partitioned id only", index: "part", id: id, wantShard: idOnlyShard},
		{name: "partitioned key-only lookup", index: "part", routing: routing, wantErr: ErrDocumentIDRequired},
		{name: "unpartitioned ignores the id", index: "plain", routing: routing, id: id, wantShard: router["plain"].Shard(routing)},
		{name: "absent index", index: "absent", routing: routing, id: id, wantErr: ErrShardMapUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dst := []*ShardTarget{{Shard: 99}}
			err := router.LookupDocShard(tt.index, tt.routing, tt.id, &dst)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorIs(t, err, ErrShardMapUnknown)
				require.Empty(t, dst)

				return
			}
			require.NoError(t, err)
			require.Len(t, dst, 1)
			require.Equal(t, tt.wantShard, dst[0].Shard)
		})
	}

	// The key-only lookup cannot name the shard of a partitioned index either.
	dst := []*ShardTarget{}
	err := router.LookupShard("part", routing, &dst)
	require.ErrorIs(t, err, ErrDocumentIDRequired)
	require.ErrorIs(t, err, ErrShardMapUnknown)
	require.Empty(t, dst)
}

func TestChainShardRouterLookupDocShard(t *testing.T) {
	t.Parallel()

	const routing = partitionedRouting
	id, wantShard := partitionedDoc(t)
	static := StaticShardRouter{"idx": partitioned}

	tests := []struct {
		name        string
		routers     []ShardRouter
		routing, id string
		wantShard   int
		wantErr     error
	}{
		{
			name:    "a router that fails is skipped",
			routers: []ShardRouter{errRouter{ErrShardMapUnknown}, static},
			routing: routing, id: id, wantShard: wantShard,
		},
		{
			name:    "a key-only router is asked for routing",
			routers: []ShardRouter{keyOnlyRouter{shard: 7}},
			routing: routing, id: id, wantShard: 7,
		},
		{
			name:    "every router fails",
			routers: []ShardRouter{errRouter{ErrShardMapUnknown}, errRouter{ErrShardMapUnknown}},
			routing: routing, wantErr: ErrShardMapUnknown,
		},
		{
			name:    "a missing ID is reported even when an earlier router fails first",
			routers: []ShardRouter{errRouter{ErrShardMapUnknown}, static},
			routing: routing, wantErr: ErrDocumentIDRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chain, ok := ChainShardRouter(tt.routers...).(DocShardRouter)
			require.True(t, ok)

			dst := []*ShardTarget{{Shard: 99}}
			err := chain.LookupDocShard("idx", tt.routing, tt.id, &dst)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, dst)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantShard, dst[0].Shard)
		})
	}
}

// docTransportRouter is a transport that also offers LookupDocShard, and records what it was asked.
type docTransportRouter struct {
	fakeTransportRouter
	gotRouting, gotID string
}

func (f *docTransportRouter) LookupDocShard(_, routing, id string, dst *[]*opensearchtransport.ShardTarget) error {
	f.gotRouting, f.gotID = routing, id

	return f.LookupShard("", "", dst)
}

func TestClientShardRouterLookupDocShard(t *testing.T) {
	t.Parallel()

	target := opensearchtransport.ShardTarget{Shard: 3, Primary: true}
	tr := &docTransportRouter{fakeTransportRouter: fakeTransportRouter{found: true, target: target}}
	var dst []*ShardTarget
	require.NoError(t, clientShardRouter{tr: tr}.LookupDocShard("idx", "r", "d", &dst))
	require.Equal(t, "r", tr.gotRouting)
	require.Equal(t, "d", tr.gotID)
	require.Equal(t, 3, dst[0].Shard)
}

// TestBulkIndexerPlaceHonoursRoutingPartitionSize: a document of a partitioned index is
// placed on the shard the server picks from its routing value and its ID together.
func TestBulkIndexerPlaceHonoursRoutingPartitionSize(t *testing.T) {
	t.Parallel()

	const routing = partitionedRouting
	id, wantShard := partitionedDoc(t)
	idOnlyShard, _ := partitioned.ShardForDocument("", id)
	itemRouting := routing

	tests := []struct {
		name      string
		item      BulkIndexerItem
		wantShard int
		unplaced  bool
	}{
		{name: "routing and id", item: BulkIndexerItem{Index: "alpha", DocumentID: id, Routing: &itemRouting}, wantShard: wantShard},
		{name: "id only", item: BulkIndexerItem{Index: "alpha", DocumentID: id}, wantShard: idOnlyShard},
		{name: "a generated id leaves the item unplaced", item: BulkIndexerItem{Index: "alpha", Routing: &itemRouting}, unplaced: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer(newFakeCluster(nil, "n0"))
			bi := newBulkClient(t, server, BulkIndexerConfig{
				FlushInterval: time.Hour,
				ShardRouter:   StaticShardRouter{"alpha": partitioned},
			}).(*bulkClient)

			key, _, _, err := bi.place(t.Context(), tt.item)
			require.NoError(t, err)
			if tt.unplaced {
				require.Nil(t, key)

				return
			}
			require.NotNil(t, key)
			require.Equal(t, shardKey{index: "alpha", shard: tt.wantShard}, *key)
		})
	}
}

// TestBulkIndexerPlaceKeepsThePlacementOfAnItemWithoutAnID: an item of a partitioned index
// that has a routing value but no ID is unplaced on its own, without marking the index
// unplaced or dropping the placement of the documents that have an ID.
func TestBulkIndexerPlaceKeepsThePlacementOfAnItemWithoutAnID(t *testing.T) {
	t.Parallel()

	const routing = partitionedRouting
	id, _ := partitionedDoc(t)
	itemRouting := routing

	server := newFakeServer(newFakeCluster(nil, "n0"))
	bi := newBulkClient(t, server, BulkIndexerConfig{
		FlushInterval: time.Hour,
		ShardRouter:   StaticShardRouter{"alpha": partitioned},
	}).(*bulkClient)

	key, _, _, err := bi.place(t.Context(), BulkIndexerItem{Index: "alpha", DocumentID: id, Routing: &itemRouting})
	require.NoError(t, err)
	require.NotNil(t, key)
	_, known := bi.shardMapGeneration("alpha")
	require.True(t, known)

	key, node, _, err := bi.place(t.Context(), BulkIndexerItem{Index: "alpha", Routing: &itemRouting})
	require.NoError(t, err)
	require.Nil(t, key)
	require.Zero(t, node)

	_, known = bi.shardMapGeneration("alpha")
	require.True(t, known, "the placement of the index must survive")
	_, marked := bi.placement.unplaced.Load("alpha")
	require.False(t, marked, "the index must not be marked unplaced")
}

// TestChainShardRouterLookupShardStopsAtAPartitionedIndex: a key cannot name the shard of a
// partitioned index, and no later router may answer it with a wrong one.
func TestChainShardRouterLookupShardStopsAtAPartitionedIndex(t *testing.T) {
	t.Parallel()

	chain := ChainShardRouter(
		StaticShardRouter{"idx": partitioned},
		StaticShardRouter{"idx": DefaultShardMap(640)},
	)

	dst := []*ShardTarget{{Shard: 99}}
	require.ErrorIs(t, chain.LookupShard("idx", partitionedRouting, &dst), ErrDocumentIDRequired)
	require.Empty(t, dst)
}
