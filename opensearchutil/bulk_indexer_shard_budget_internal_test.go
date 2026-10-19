// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// idsOnShards returns n document ids for index whose shard, as the cluster routes
// them, satisfies want.
func idsOnShards(t *testing.T, cluster *fakeCluster, index string, n int, want func(shard int) bool) []string {
	t.Helper()

	ids := make([]string, 0, n)
	for i := 0; len(ids) < n; i++ {
		id := fmt.Sprintf("doc-%06d", i)
		target, ok := cluster.LookupShard(index, id)
		require.True(t, ok)
		if want(target.Shard) {
			ids = append(ids, id)
		}
	}

	return ids
}

// TestBulkIndexerStalledShardDoesNotStarveTheOthers stalls one shard, as a shard does
// whose documents are slow to index or whose primary is busy, and keeps adding to it.
// Every byte that shard takes stays held until its requests are answered, so with one
// pool for every shard it ends up holding all of it and Add stops for every shard. A
// shard may hold only its share of the pool while others want room: the stalled
// shard's producer waits, and the others go on.
func TestBulkIndexerStalledShardDoesNotStarveTheOthers(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(4)}, "n0")
	// A small window, as a small machine has: the stalled shard's queued buffers must
	// not take the room the other shards' requests need.
	cluster.setWindow(2, false)
	server := newFakeServer(cluster)

	stalled := shardKey{index: "idx", shard: 0}
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	server.advance = func(req sentRequest) {
		if _, ok := req.shards[stalled]; ok {
			<-release
		}
	}

	itemBytes := docBytes("idx", "doc-000000", 1000)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:     int(itemBytes) * 4,
		Sizing:         BulkSizing{Fixed: true},
		FlushInterval:  time.Hour,
		MaxMemoryBytes: int(itemBytes) * 40,
	})

	slowIDs := idsOnShards(t, cluster, "idx", 400, func(shard int) bool { return shard == stalled.shard })
	healthyIDs := idsOnShards(t, cluster, "idx", 400, func(shard int) bool { return shard != stalled.shard })

	// The stalled shard's producer adds until it is made to wait, and waits until the
	// test is done with the others.
	slowCtx, stopSlow := context.WithCancel(t.Context())
	slowDone := make(chan int)
	go func() {
		added := 0
		for _, id := range slowIDs {
			if bi.Add(slowCtx, BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: id, Body: seqBody(1000)}) != nil {
				break
			}
			added++
		}
		slowDone <- added
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for i, id := range healthyIDs {
		require.NoError(t, bi.Add(ctx, BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: id, Body: seqBody(1000)}),
			"Add %d for a healthy shard must not wait on a shard that is not being answered", i)
	}

	stopSlow()
	slowAdded := <-slowDone
	require.Less(t, slowAdded, len(slowIDs), "the stalled shard's producer must have been made to wait")

	releaseOnce.Do(func() { close(release) })
	require.NoError(t, bi.Close(t.Context()))

	delivered := 0
	for _, r := range server.requests() {
		delivered += len(r.actions)
	}
	require.Equal(t, len(healthyIDs)+slowAdded, delivered, "and once the shard is answered, nothing that was added is lost")
}
