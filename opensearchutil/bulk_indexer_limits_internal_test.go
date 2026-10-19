// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// idsOnShard returns n document IDs that land on the shard of the index.
func idsOnShard(t *testing.T, router ShardRouter, index string, shard, n int) []string {
	t.Helper()

	var ids []string
	for i := 0; len(ids) < n; i++ {
		id := fmt.Sprintf("doc-%05d", i)
		target, ok := router.LookupShard(index, id)
		require.True(t, ok)
		if target.Shard == shard {
			ids = append(ids, id)
		}
	}

	return ids
}

// TestBulkIndexerJobLimitCountsWhatItemsReallyCost: a buffered item holds its
// request body and also the structure that carries it to its callbacks. For a small
// document the second is several times the first, and a limit that counted only the
// body would let the process hold several times what it was told it could.
func TestBulkIndexerJobLimitCountsWhatItemsReallyCost(t *testing.T) {
	t.Parallel()

	const limitItems = 10

	one := BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: "doc-0000", Body: seqBody(1000)}
	body := estimateItemBytes(one)
	held := body + itemStructBytes
	require.Greater(t, itemStructBytes, body, "the document must be small enough for the struct to dominate")

	server := stalledServer()
	cfg := BulkIndexerConfig{
		FlushBytes:     1 << 20, // Nothing fills a buffer by size.
		Sizing:         BulkSizing{Fixed: true},
		FlushInterval:  time.Hour,
		MaxMemoryBytes: int(held) * limitItems,
		ShardRouter:    NoShardRouter,
	}
	bi := newClusterIndexer(t, server, cfg)

	blocked := make(chan struct{}, 4)
	indexer := bi.(*bulkIndexer)
	relieve := indexer.budget.onBlock
	indexer.budget.onBlock = func(key shardKey) {
		relieve(key)
		blocked <- struct{}{}
	}

	var accepted atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range limitItems + 5 {
			if err := bi.Add(t.Context(), BulkIndexerItem{
				Action: actionIndex, Index: "idx", DocumentID: fmt.Sprintf("doc-%04d", i), Body: seqBody(1000 + i),
			}); err != nil {
				return
			}
			accepted.Add(1)
		}
	}()

	receive(t, blocked)
	require.EqualValues(t, limitItems, accepted.Load(), "counting the body alone would have let in %d", int(held)*limitItems/int(body))
	require.LessOrEqual(t, bi.Stats().BufferedBytes+bi.Stats().InFlightBytes, uint64(held)*limitItems) //nolint:gosec // Positive.

	close(server.gate)
	receive(t, done)
	require.NoError(t, bi.Close(t.Context()))
}

// TestBulkIndexerJobLimitSendsTheFullestShardFirst: when the job's memory is spent
// the fullest shard buffer is the one sent, because it frees the most memory.
func TestBulkIndexerJobLimitSendsTheFullestShardFirst(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"idx": DefaultShardMap(4)}
	cluster := newFakeCluster(maps, "n0")
	cluster.setWindow(8, false)
	server := newFakeServer(cluster)
	server.entered = make(chan string, 16)

	one := BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: "doc-00000", Body: seqBody(1000)}
	held := estimateItemBytes(one) + itemStructBytes

	// Shard 2 gets the most documents, then 0, then 3, then 1.
	fill := map[int]int{2: 7, 0: 4, 3: 2, 1: 1}
	total := 0
	for _, n := range fill {
		total += n
	}

	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:     1 << 20,
		Sizing:         BulkSizing{Fixed: true},
		FlushInterval:  time.Hour,
		MaxMemoryBytes: int(held) * total,
	})

	seq := 0
	// Add each shard's documents, interleaved.
	next := map[int]int{}
	ids := map[int][]string{}
	for shard, n := range fill {
		ids[shard] = idsOnShard(t, cluster, "idx", shard, n)
	}
	for added := 0; added < total; {
		for _, shard := range []int{3, 0, 2, 1} {
			if next[shard] < fill[shard] {
				seq++
				addDoc(t, bi, "idx", ids[shard][next[shard]], 1000+seq)
				next[shard]++
				added++
			}
		}
	}
	require.Empty(t, server.requests(), "nothing is sent while there is room")

	// One more document finds the job at its limit.
	extra := idsOnShard(t, cluster, "idx", 1, fill[1]+1)[fill[1]]
	added := make(chan struct{})
	go func() {
		defer close(added)
		// Not addDoc: a test's helper may not fail the test from another goroutine.
		_ = bi.Add(t.Context(), BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: extra, Body: seqBody(9999)})
	}()

	receive(t, server.entered)

	first := server.requests()[0]
	require.Contains(t, first.shards, shardKey{index: "idx", shard: 2}, "the fullest shard is sent first")
	require.Len(t, first.actions, fill[2])

	receive(t, added)
	require.NoError(t, bi.Close(t.Context()))
}

// TestBulkIndexerByteWindowHoldsBackWhatDoesNotFit: a node is sent only as many bytes
// as its window allows to be outstanding at once. A buffer that does not fit waits,
// sealed, until the requests out have been answered, and then goes; one that is the only
// request out is sent whatever its size.
func TestBulkIndexerByteWindowHoldsBackWhatDoesNotFit(t *testing.T) {
	t.Parallel()

	one := BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: "doc-00000", Body: seqBody(1000)}
	size := estimateItemBytes(one)

	tests := []struct {
		name string
		// windowBuffers is the window, in buffers of three documents.
		windowBuffers float64
		// wantOut is how many of the two sealed buffers are sent at once.
		wantOut int
	}{
		{name: "room for both", windowBuffers: 2, wantOut: 2},
		{name: "room for one and a half, so only one", windowBuffers: 1.5, wantOut: 1},
		{name: "less than one buffer, so one still goes", windowBuffers: 0.5, wantOut: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(2)}, "n0")
			cluster.setWindow(8, false) // The transport allows eight requests; only the byte window holds this back.
			server := newFakeServer(cluster)
			server.gate = make(chan struct{})
			server.entered = make(chan string, 8)

			bi := newClusterIndexer(t, server, BulkIndexerConfig{
				FlushBytes:    int(size) * 3,
				NodeBytes:     int(float64(size) * 3 * tt.windowBuffers),
				FlushInterval: time.Hour,
				Sizing:        BulkSizing{InitialBytes: int(size) * 3},
			})
			indexer := bi.(*bulkIndexer)

			seq := 0
			for shard := range 2 {
				for _, id := range idsOnShard(t, cluster, "idx", shard, 3) {
					seq++
					addDoc(t, bi, "idx", id, 1000+seq)
				}
			}

			// Both buffers are sealed by size. Wait for the ones that may go.
			for range tt.wantOut {
				receive(t, server.entered)
			}
			require.Eventually(t, func() bool {
				indexer.sched.Lock()
				defer indexer.sched.Unlock()
				ns := indexer.sched.nodes["n0"]

				return ns.inflight == tt.wantOut && ns.queued == 2-tt.wantOut
			}, 10*time.Second, time.Millisecond, "requests out, and buffers held back")

			close(server.gate)
			require.NoError(t, bi.Close(t.Context()))
			require.Len(t, server.requests(), 2, "what was held back is sent once the window has room")
		})
	}
}

// TestBulkIndexerForgetsIdleShards: an indexer that moves on from an index does
// not keep a record of every shard it wrote to.
func TestBulkIndexerForgetsIdleShards(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"old": DefaultShardMap(6), "new": DefaultShardMap(2)}
	cluster := newFakeCluster(maps, "n0")
	server := newFakeServer(cluster)
	clock := newManualClock()

	bi := newClusterIndexer(t, server, BulkIndexerConfig{FlushInterval: time.Minute})
	indexer := bi.(*bulkIndexer)
	indexer.now = clock.Now

	for i := range 60 {
		addDoc(t, bi, "old", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Flush(t.Context()))

	count := func() int {
		indexer.sched.Lock()
		defer indexer.sched.Unlock()

		return len(indexer.sched.shards)
	}
	require.Equal(t, 6, count())

	indexer.sched.evictIdle()
	require.Equal(t, 6, count(), "shards that were just written are kept")

	clock.advance((idleShardIntervals + 1) * time.Minute)
	for i := range 20 {
		addDoc(t, bi, "new", fmt.Sprint("doc-", i), i)
	}
	indexer.sched.evictIdle()

	require.Equal(t, 2, count(), "the old index's shards are forgotten and the new one's, in use, are not")
	require.NoError(t, bi.Close(t.Context()))
}
