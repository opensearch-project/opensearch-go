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
//
//nolint:unparam // Every test here uses one index.
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
	indexer.budget.onBlock = func() {
		relieve()
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

// nodeLimitFixture is an indexer whose one node may have six documents open, with
// the four shards of "idx" all on it, and a shard buffer that no test below fills
// by size.
//
//nolint:unparam // The held size is what some tests would compute from.
func nodeLimitFixture(t *testing.T) (BulkIndexer, *fakeServer, *fakeCluster, int64) {
	t.Helper()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(4)}, "n0")
	cluster.setWindow(8, false)
	server := newFakeServer(cluster)

	one := BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: "doc-00000", Body: seqBody(1000)}
	held := estimateItemBytes(one) + itemStructBytes

	// A shard buffer may be 3 documents' worth of request body, the least a node's
	// limit can come to.
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:    int(held) * 3,
		NodeBytes:     int(held) * 6,
		FlushInterval: time.Hour,
		// Shard buffers start at their ceiling, so only the node's limit seals them.
		Sizing: BulkSizing{InitialBytes: int(held) * 3},
	})

	return bi, server, cluster, held
}

// TestBulkIndexerShardsShareTheirNodesLimit: the shards being filled for a node
// divide what the node may have open among them, so together they stay inside it
// instead of each filling alone. A buffer is sent no later than when it holds its
// slice, which is the limit over the shards open at the time; as shards are sent
// the rest are allowed more.
func TestBulkIndexerShardsShareTheirNodesLimit(t *testing.T) {
	t.Parallel()

	bi, server, cluster, _ := nodeLimitFixture(t)
	indexer := bi.(*bulkIndexer)

	ids := map[int][]string{}
	for shard := 1; shard <= 3; shard++ {
		ids[shard] = idsOnShard(t, cluster, "idx", shard, 3)
	}

	seq := 0
	step := func(shard, doc int) {
		seq++
		addDoc(t, bi, "idx", ids[shard][doc], 1000+seq)

		indexer.sched.Lock()
		defer indexer.sched.Unlock()
		ns := indexer.sched.nodes["n0"]
		require.LessOrEqual(t, ns.openHeld.Load(), indexer.sched.nodeLimit(ns),
			"the open buffers together must stay inside the node's limit after shard %d doc %d", shard, doc)
	}

	// One document each opens three buffers. The node may have six open, so each
	// buffer's slice is a third of that: two documents.
	for shard := 1; shard <= 3; shard++ {
		step(shard, 0)
	}
	require.Empty(t, server.requests())

	step(1, 1) // Shard 1 holds its slice, two documents, and is sent.
	require.Eventually(t, func() bool { return len(server.requests()) == 1 }, 10*time.Second, time.Millisecond)
	require.Len(t, server.requests()[0].actions, 2)

	// Two shards are open now, so each may hold three.
	step(2, 1)
	step(3, 1)
	require.Len(t, server.requests(), 1, "two documents is under a slice of three")
	step(2, 2) // Shard 2 holds three.
	require.Eventually(t, func() bool { return len(server.requests()) == 2 }, 10*time.Second, time.Millisecond)

	byShard := map[int]int{}
	for _, r := range server.requests() {
		for key := range r.shards {
			byShard[key.shard] = len(r.actions)
		}
	}
	require.Equal(t, map[int]int{1: 2, 2: 3}, byShard, "alone, a shard's buffer would have held many more")

	require.NoError(t, bi.Close(t.Context()))
	require.Len(t, server.requests(), 3)
}

// TestBulkIndexerNodeLimitSendsThatNodesFullestBuffer is the backstop behind the
// slices: buffers started when fewer shards were open were sized for a larger
// slice, and together with the shards that have opened since they can outgrow the
// node's limit. The fullest is sent, the item that crossed the line was accepted,
// and the node is told it was crowded, so its limit comes down.
func TestBulkIndexerNodeLimitSendsThatNodesFullestBuffer(t *testing.T) {
	t.Parallel()

	bi, server, cluster, _ := nodeLimitFixture(t)
	indexer := bi.(*bulkIndexer)

	seq := 0
	add := func(shard, n int) {
		for _, id := range idsOnShard(t, cluster, "idx", shard, n) {
			seq++
			addDoc(t, bi, "idx", id, 1000+seq)
		}
	}

	add(0, 4) // Alone, so its slice is the whole limit: it may hold more than a third.
	add(1, 1)
	add(2, 1)
	require.Empty(t, server.requests(), "6 documents open is at the limit, not over it")

	before := func() int64 { n, _ := indexer.nodeLimitOf("n0"); return n }()

	add(3, 1) // 7 open: over.

	require.Eventually(t, func() bool { return len(server.requests()) >= 1 }, 10*time.Second, time.Millisecond)
	sent := server.requests()
	require.Len(t, sent, 1, "one buffer was enough to bring the node back under its limit")
	require.Contains(t, sent[0].shards, shardKey{index: "idx", shard: 0}, "the fullest of the node's buffers")
	require.Len(t, sent[0].actions, 4)

	waitIdle(t, bi)
	after, _ := indexer.nodeLimitOf("n0")
	require.Less(t, after, before, "a node that was given more than it allows is allowed less")
	require.Greater(t, after, before/2, "gently")

	require.NoError(t, bi.Close(t.Context()))
	require.Len(t, server.requests(), 4, "and the rest went when Close asked")
}

// TestNodeLimitFor pins the arithmetic of a node's limit.
func TestNodeLimitFor(t *testing.T) {
	t.Parallel()

	const (
		job   = 900
		shard = 50
	)

	tests := []struct {
		name string
		// fair is the node's even share of the nodes in use.
		fair, control, others int64
		want                  int64
	}{
		{name: "the node's controller, when nothing else limits it", fair: 300, control: 400, others: 0, want: 400},
		{name: "a node fixed on alone may have the whole job", fair: 900, control: job, others: 0, want: job},
		{name: "never more than the other nodes leave", fair: 300, control: job, others: 500, want: 400},
		{name: "others cannot squeeze it below its even share", fair: 300, control: job, others: 850, want: 300},
		{name: "its own signals can take it below its even share", fair: 300, control: 120, others: 0, want: 120},
		{name: "nothing takes it below one shard buffer", fair: 300, control: 10, others: 0, want: shard},
		{name: "a node short on everything still gets one shard buffer", fair: 20, control: job, others: 900, want: shard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, nodeLimitFor(job, shard, tt.fair, tt.control, tt.others))
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
