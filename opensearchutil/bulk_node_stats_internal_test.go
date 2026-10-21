// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerGetNodeStatsFollowsEachNode: each node is listed in the order it
// was first used, with its own requests out, queue and held bytes, and every
// gauge is back to zero once the bulk client is closed.
func TestBulkIndexerGetNodeStatsFollowsEachNode(t *testing.T) {
	t.Parallel()

	m := DefaultShardMap(2)
	var ids [2][]string
	for i := 0; len(ids[0]) < 2 || len(ids[1]) < 2; i++ {
		id := "d" + string(rune('a'+i))
		ids[m.Shard(id)] = append(ids[m.Shard(id)], id)
	}

	cluster := newFakeCluster(map[string]ShardMap{"idx": m}, "n0", "n1")
	cluster.setPlacement(func(_ string, shard int) string { return []string{"n1", "n0"}[shard] })
	server := newFakeServer(cluster)
	server.gate = make(chan struct{})
	server.entered = make(chan string, 8)
	bi := newBulkClient(t, server, fixedBatches(docBytes("idx", ids[0][0], 1000000), 1))

	require.Empty(t, bi.GetTargetStats(nil), "no node is listed before one is used")

	// Shard 0 is on n1: one request out and the next buffer queued behind it.
	addDoc(t, bi, "idx", ids[0][0], 1000000)
	require.Equal(t, "n1", receive(t, server.entered))
	sent := bi.GetTargetStats(nil)
	require.Len(t, sent, 1)
	require.EqualValues(t, 1, sent[0].InFlightRequests, "the request is out")
	require.Zero(t, sent[0].QueuedBuffers, "and no longer queued")
	addDoc(t, bi, "idx", ids[0][1], 1000000)
	// Shard 1 is on n0: a small document, held in an open buffer.
	addDoc(t, bi, "idx", ids[1][0], 1)

	nodes := bi.GetTargetStats(nil)
	require.Len(t, nodes, 2)
	n1, n0 := nodes[0], nodes[1]
	require.Equal(t, "n1", n1.Name, "n1 was used first")
	require.Equal(t, "n0", n0.Name)

	require.EqualValues(t, 1, n1.InFlightRequests)
	require.Positive(t, n1.InFlightBytes)
	require.EqualValues(t, 1, n1.QueuedBuffers)
	require.Greater(t, n1.HeldBytes, n1.InFlightBytes, "the queued buffer is held too")

	require.Zero(t, n0.InFlightRequests)
	require.Zero(t, n0.InFlightBytes)
	require.Zero(t, n0.QueuedBuffers)
	require.Positive(t, n0.HeldBytes, "the open buffer is held")
	require.InDelta(t, 1.0, n0.Window, 1e-9, "a node not yet answered has its window wide open")
	require.Positive(t, n0.BufferSize, "and its buffers start where the indexer starts them")

	close(server.gate)
	require.NoError(t, bi.Close(t.Context()))

	for _, n := range bi.GetTargetStats(nodes) {
		require.Zero(t, n.InFlightRequests, n.Name)
		require.Zero(t, n.InFlightBytes, n.Name)
		require.Zero(t, n.QueuedBuffers, n.Name)
		require.Zero(t, n.HeldBytes, n.Name)
	}
}

// TestBulkIndexerGetNodeStatsReadsEachNodesController: a node that answers 429
// counts its rejections and has its window shrunk, and a node beside it that does
// not keeps its window open.
func TestBulkIndexerGetNodeStatsReadsEachNodesController(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0", "n1")
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if r.node == "n0" {
			return 200, rejectingResponse(len(r.actions))
		}

		return 200, okResponse(len(r.actions))
	}

	h.round("n0")
	h.round("n1")

	nodes := h.bi.GetTargetStats(nil)
	require.Len(t, nodes, 2)
	byName := map[string]BulkTargetStats{nodes[0].Name: nodes[0], nodes[1].Name: nodes[1]}

	require.EqualValues(t, 1, byName["n0"].NumRejections)
	require.Less(t, byName["n0"].Window, 1.0, "a 429 cuts the window")
	require.Zero(t, byName["n1"].NumRejections)
	require.InDelta(t, 1.0, byName["n1"].Window, 1e-9)
	for _, n := range nodes {
		require.Positive(t, n.BufferSize, n.Name)
	}

	var stats BulkIndexerStats
	require.NoError(t, h.bi.GetStats(&stats))
	require.Equal(t, stats.NumRejections, byName["n0"].NumRejections+byName["n1"].NumRejections,
		"the nodes' rejections add up to the indexer's")
	require.InDelta(t, stats.TargetWindow.Min, byName["n0"].Window, 1e-9, "the summary's lowest window is n0's")
}

// TestBulkIndexerGetNodeStatsWhenStopped: buffers left queued when the
// construction context ends are failed by Close, and leave no node with a queue.
func TestBulkIndexerGetNodeStatsWhenStopped(t *testing.T) {
	t.Parallel()

	h := newGaugeHarness(t)
	h.add(0, 0, true)
	receive(t, h.server.entered)
	h.add(1, 0, true)
	h.add(0, 1, true)
	require.EqualValues(t, 2, h.bi.GetTargetStats(nil)[0].QueuedBuffers)

	h.end()
	close(h.server.gate)
	_ = h.bi.Close(t.Context())

	nodes := h.bi.GetTargetStats(nil)
	require.Len(t, nodes, 1)
	require.Zero(t, nodes[0].QueuedBuffers)
	require.Zero(t, nodes[0].InFlightRequests)
	require.Zero(t, nodes[0].HeldBytes)
}

// TestBulkIndexerGetNodeStatsReadsSlowdown: a node that becomes slower than it
// was reads as slowed down, and alone it is the whole of the bulk client's summary.
func TestBulkIndexerGetNodeStatsReadsSlowdown(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0")
	var rate atomic.Uint64
	rate.Store(uint64(100e6))
	h.server.respond = func(r sentRequest, _ string) (int, string) { return 200, okResponse(len(r.actions)) }
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
	}
	for range 20 {
		h.round("n0")
	}
	rate.Store(uint64(20e6))
	for range 3 {
		h.round("n0")
	}

	nodes := h.bi.GetTargetStats(nil)
	require.Len(t, nodes, 1)
	require.Positive(t, nodes[0].Slowdown)

	var stats BulkIndexerStats
	require.NoError(t, h.bi.GetStats(&stats))
	require.InDelta(t, stats.Slowdown.Median, nodes[0].Slowdown, 1e-9)
	require.InDelta(t, stats.BufferSize.Median, nodes[0].BufferSize, 1e-9)
}

// TestBulkIndexerGetNodeStatsWithoutARouter: with no shard router the bulk client
// writes to one node that stands for the cluster, and lists it without a name.
func TestBulkIndexerGetNodeStatsWithoutARouter(t *testing.T) {
	t.Parallel()

	bi := newBulkClient(t, newFakeServer(newFakeCluster(nil)),
		BulkIndexerConfig{FlushInterval: time.Hour, ShardRouter: NoShardRouter})
	addDoc(t, bi, "idx", "a", 1)

	nodes := bi.GetTargetStats(nil)
	require.Len(t, nodes, 1)
	require.Empty(t, nodes[0].Name)
	require.Positive(t, nodes[0].HeldBytes)
	require.NoError(t, bi.Close(t.Context()))
}

// TestBulkIndexerGetNodeStatsTakesNoLockAndDoesNotAllocate: GetTargetStats returns
// while the scheduler's lock is held, and a caller that passes back the slice it
// got allocates nothing. It is not parallel, as AllocsPerRun cannot run in a
// parallel test.
//
//nolint:paralleltest // See above.
func TestBulkIndexerGetNodeStatsTakesNoLockAndDoesNotAllocate(t *testing.T) {
	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0", "n1", "n2")
	bi := newBulkClient(t, newFakeServer(cluster), BulkIndexerConfig{FlushInterval: time.Hour})
	for i := range 16 {
		addDoc(t, bi, "idx", "doc"+string(rune('a'+i)), i)
	}

	nodes := bi.GetTargetStats(nil)
	require.Len(t, nodes, 3)
	allocs := testing.AllocsPerRun(100, func() { nodes = bi.GetTargetStats(nodes) })
	require.Zero(t, allocs)

	sched := bi.(*bulkClient).sched
	sched.Lock()
	unlocked := false
	t.Cleanup(func() {
		if !unlocked {
			sched.Unlock()
		}
	})

	done := make(chan []BulkTargetStats, 1)
	go func() { done <- bi.GetTargetStats(nil) }()

	select {
	case got := <-done:
		require.Len(t, got, 3)
	case <-time.After(2 * time.Second):
		t.Fatal("GetTargetStats waited for the scheduler's lock")
	}
	sched.Unlock()
	unlocked = true

	require.NoError(t, bi.Close(t.Context()))
}
