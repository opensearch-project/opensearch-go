// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// shardTargetOf returns the buffer size the controller currently asks for the
// shard, and false when the indexer has not seen the shard yet.
//
//nolint:unparam // Every test here uses one index.
func (bi *bulkIndexer) shardTargetOf(index string, shard int) (int64, bool) {
	bi.sched.Lock()
	defer bi.sched.Unlock()

	ss, ok := bi.sched.shards[shardKey{index: index, shard: shard}]
	if !ok {
		return 0, false
	}

	return ss.sizer.bytes(), true
}

// nodeLimitOf returns how many bytes the indexer currently allows open for the
// node, and false when it has not seen the node yet.
//
//nolint:unparam // The found flag is for tests that may need it.
func (bi *bulkIndexer) nodeLimitOf(name string) (int64, bool) {
	bi.sched.Lock()
	defer bi.sched.Unlock()

	ns, ok := bi.sched.nodes[name]
	if !ok {
		return 0, false
	}

	return ns.limit.bytes(), true
}

// waitIdle blocks until every batch that has been sealed has completed, so the
// controller has seen the outcome of everything sent so far.
func waitIdle(t *testing.T, bi BulkIndexer) {
	t.Helper()

	sched := bi.(*bulkIndexer).sched
	require.Eventually(t, func() bool {
		sched.Lock()
		defer sched.Unlock()

		return sched.sealedPending == 0
	}, 10*time.Second, time.Millisecond)
}

// rejectingResponse is a 200 answer in which the node rejected every item.
func rejectingResponse(actions int) string {
	items := make([]string, actions)
	for i := range items {
		items[i] = `{"index":{"_id":"x","status":429,"error":{"type":"es_rejected_execution_exception","reason":"queue full"}}}`
	}

	return fmt.Sprintf(`{"took":1,"errors":true,"items":[%s]}`, strings.Join(items, ","))
}

// manualClock is a clock the fake server moves by the time a request took.
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock() *manualClock { return &manualClock{now: time.Unix(1_000_000, 0)} }

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// sizingHarness drives an indexer one buffer at a time, so each buffer is sized by
// everything the controllers learned from the ones before. It runs a cluster of
// nodes with shardsPerNode shards each, shard n*shardsPerNode+k on node n.
type sizingHarness struct {
	t      *testing.T
	bi     BulkIndexer
	server *fakeServer
	clock  *manualClock
	// itemBytes is the size of the documents added.
	itemBytes int64
	next      int

	nodes         []string
	shardsPerNode int
}

// newSizingHarness builds an indexer over the named nodes, whose shard buffers
// are at most ceilingDocs documents.
func newSizingHarness(t *testing.T, ceilingDocs, shardsPerNode int, nodes ...string) *sizingHarness {
	t.Helper()

	itemBytes := estimateItemBytes(BulkIndexerItem{Action: actionIndex, Index: "idx", DocumentID: "doc-0000", Body: seqBody(1000)})
	shards := len(nodes) * shardsPerNode
	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(shards)}, nodes...)
	cluster.setPlacement(func(_ string, shard int) string { return nodes[shard/shardsPerNode] })

	h := &sizingHarness{
		t: t, server: newFakeServer(cluster), clock: newManualClock(),
		itemBytes: itemBytes, nodes: nodes, shardsPerNode: shardsPerNode,
	}
	h.bi = newClusterIndexer(t, h.server, BulkIndexerConfig{
		NumWorkers:    1,
		FlushBytes:    int(itemBytes) * ceilingDocs,
		NodeBytes:     int(itemBytes) * ceilingDocs * 8,
		FlushInterval: time.Hour,
	})
	h.bi.(*bulkIndexer).now = h.clock.Now

	// One request at a time per node, so the controllers see each outcome before the
	// next buffer is sealed.
	cluster.setWindow(1, false)

	return h
}

// idsFor returns document IDs that land on the shard.
func (h *sizingHarness) idsFor(shard, n int) []string {
	h.t.Helper()

	cluster := h.server.cluster
	var ids []string
	for i := 0; len(ids) < n; i++ {
		id := fmt.Sprintf("doc-%04d", i)
		target, ok := cluster.LookupShard("idx", id)
		require.True(h.t, ok)
		if target.Shard == shard {
			ids = append(ids, id)
		}
	}

	return ids
}

// roundShard adds enough documents for the shard to fill the buffer the controller
// currently asks for, waits for the request to complete, and returns the size of
// that buffer in documents.
func (h *sizingHarness) roundShard(shard int) int {
	h.t.Helper()

	target, ok := h.bi.(*bulkIndexer).shardTargetOf("idx", shard)
	if !ok {
		// The first buffer of a shard is sized by the initial target, which
		// sizing.resolve fixes.
		target = int64(h.bi.(*bulkIndexer).sizing.initial)
	}
	docs := int((target + h.itemBytes - 1) / h.itemBytes)

	for _, id := range h.idsFor(shard, docs) {
		h.next++
		addDoc(h.t, h.bi, "idx", id, 1000+h.next%9000)
	}
	waitIdle(h.t, h.bi)

	requests := h.server.requests()
	last := requests[len(requests)-1]
	require.Contains(h.t, last.shards, shardKey{index: "idx", shard: shard})

	return len(last.actions)
}

// round is roundShard for the first shard of the named node.
func (h *sizingHarness) round(node string) int {
	h.t.Helper()

	for n, name := range h.nodes {
		if name == node {
			return h.roundShard(n * h.shardsPerNode)
		}
	}

	h.t.Fatalf("no node %q", node)

	return 0
}

// shardTarget returns the buffer size asked for the first shard of the node.
func (h *sizingHarness) shardTarget(node string) int64 {
	h.t.Helper()

	for n, name := range h.nodes {
		if name == node {
			target, _ := h.bi.(*bulkIndexer).shardTargetOf("idx", n*h.shardsPerNode)

			return target
		}
	}

	h.t.Fatalf("no node %q", node)

	return 0
}

func (h *sizingHarness) nodeLimit(node string) int64 {
	h.t.Helper()

	limit, _ := h.bi.(*bulkIndexer).nodeLimitOf(node)

	return limit
}

// TestBulkIndexerNodeLimitHalvesOnARejection: a 429 is the node's, so it cuts the
// limit on what is buffered for the node, and is held there for a few requests. It
// says nothing about any one shard, so the shard's buffer is not cut.
func TestBulkIndexerNodeLimitHalvesOnARejection(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0")
	var reject atomic.Bool
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if reject.Load() {
			return 200, rejectingResponse(len(r.actions))
		}

		return 200, `{"took":1,"errors":false,"items":[]}`
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	// A healthy node: the shard's buffers and the node's limit both grow.
	sizes := make([]int, 0, 12)
	for range 12 {
		sizes = append(sizes, h.round("n0"))
	}
	for i := 1; i < len(sizes); i++ {
		require.GreaterOrEqual(t, sizes[i], sizes[i-1], "a healthy node never sees its buffers shrink: %v", sizes)
	}
	require.Greater(t, sizes[len(sizes)-1], sizes[0])

	limitBefore, shardBefore := h.nodeLimit("n0"), h.shardTarget("n0")

	reject.Store(true)
	h.round("n0")
	reject.Store(false)

	limitAfter, shardAfter := h.nodeLimit("n0"), h.shardTarget("n0")
	require.InDelta(t, float64(limitBefore)/2, float64(limitAfter), float64(h.itemBytes)*2, "a rejection halves the node's limit")
	require.GreaterOrEqual(t, shardAfter, shardBefore, "and leaves the shard's buffer alone")

	// The node's limit is held for a few requests before it may grow again.
	for range sizingHoldFlushes {
		h.round("n0")
		require.LessOrEqual(t, h.nodeLimit("n0"), limitAfter, "no growth while the hold lasts")
	}
	h.round("n0")
	require.Greater(t, h.nodeLimit("n0"), limitAfter, "growth resumes after the hold")
}

// TestBulkIndexerBacksOffAsANodeSlowsDown is the bulk indexer giving way before
// anything is rejected: the node only gets slower, as when it is busy with other
// requests, and its shards' buffers shrink, then grow back when it recovers. The
// node's limit does not move: it is the ceiling above the buffers, and only a
// rejection cuts it.
func TestBulkIndexerBacksOffAsANodeSlowsDown(t *testing.T) {
	t.Parallel()

	const ceilingDocs = 400

	h := newSizingHarness(t, ceilingDocs, 1, "n0")

	var rate atomic.Uint64
	setRate := func(bytesPerSecond float64) { rate.Store(uint64(bytesPerSecond)) }
	setRate(100e6)
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
	}

	for range 80 {
		h.round("n0")
	}
	shardHealthy, limitHealthy := h.shardTarget("n0"), h.nodeLimit("n0")
	require.Equal(t, int64(h.bi.(*bulkIndexer).sizing.max), shardHealthy, "a healthy shard ends up at the ceiling")
	require.Equal(t, int64(h.bi.(*bulkIndexer).nodeSizing.max), limitHealthy, "and so does the node's limit")

	setRate(20e6) // Five times slower. Nothing is rejected.
	for range 6 {
		h.round("n0")
	}
	require.Less(t, h.shardTarget("n0"), shardHealthy/2, "buffers must shrink well before the node rejects anything")
	require.Equal(t, limitHealthy, h.nodeLimit("n0"), "a slow node is not a rejecting one, so its limit stands")

	setRate(100e6)
	for range 120 {
		h.round("n0")
	}
	require.Equal(t, shardHealthy, h.shardTarget("n0"), "and grow back when it recovers")
	require.Equal(t, limitHealthy, h.nodeLimit("n0"))
}

// TestBulkIndexerSlowShardShrinksAlone: a shard that is slow, as a hot one is,
// gets smaller buffers, and the other shards on the same node do not.
func TestBulkIndexerSlowShardShrinksAlone(t *testing.T) {
	t.Parallel()

	const (
		ceilingDocs = 400
		slowShard   = 0
		fastShard   = 1
	)

	h := newSizingHarness(t, ceilingDocs, 2, "n0")
	h.server.advance = func(r sentRequest) {
		rate := 100e6
		if _, slow := r.shards[shardKey{index: "idx", shard: slowShard}]; slow {
			rate = 15e6
		}
		h.clock.advance(time.Duration(float64(r.bytes) / rate * float64(time.Second)))
	}

	// Both shards are healthy first, so each has a best rate to compare with.
	h.server.mu.Lock()
	slowRate := h.server.advance
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}
	h.server.mu.Unlock()
	for range 40 {
		h.roundShard(slowShard)
		h.roundShard(fastShard)
	}
	h.server.advance = slowRate

	for range 12 {
		h.roundShard(slowShard)
		h.roundShard(fastShard)
	}

	bi := h.bi.(*bulkIndexer)
	slow, _ := bi.shardTargetOf("idx", slowShard)
	fast, _ := bi.shardTargetOf("idx", fastShard)

	require.Less(t, slow, fast/2, "the slow shard's buffers shrink")
	require.Equal(t, int64(bi.sizing.max), fast, "and the other shard's do not")
}

// TestBulkIndexerSizesEachNodeOnItsOwn: a node that rejects does not shrink the
// limit, or the shards' buffers, of a node that does not.
func TestBulkIndexerSizesEachNodeOnItsOwn(t *testing.T) {
	t.Parallel()

	const ceilingDocs = 200

	h := newSizingHarness(t, ceilingDocs, 1, "n0", "n1")
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if r.node == "n0" {
			return 200, rejectingResponse(len(r.actions))
		}

		return 200, `{"took":1,"errors":false,"items":[]}`
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	for range 10 {
		h.round("n0")
		h.round("n1")
	}

	bi := h.bi.(*bulkIndexer)
	require.Equal(t, int64(bi.nodeSizing.min), h.nodeLimit("n0"), "the node that keeps rejecting is allowed the least")
	require.Greater(t, h.nodeLimit("n1"), int64(bi.nodeSizing.initial), "the other node is not held back by it")
	require.Greater(t, h.shardTarget("n1"), int64(bi.sizing.initial))
}

// TestBulkIndexerDocumentRejectionsSayNothingAboutTheNode: items the cluster
// refuses for their own sake, a version conflict for instance, are not a reason to
// send a node smaller buffers.
func TestBulkIndexerDocumentRejectionsSayNothingAboutTheNode(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0")
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		items := make([]string, len(r.actions))
		for i := range items {
			items[i] = `{"index":{"_id":"x","status":409,"error":{"type":"version_conflict_engine_exception","reason":"conflict"}}}`
		}

		return 200, fmt.Sprintf(`{"took":1,"errors":true,"items":[%s]}`, strings.Join(items, ","))
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	sizes := make([]int, 0, 12)
	for range 12 {
		sizes = append(sizes, h.round("n0"))
	}

	for i := 1; i < len(sizes); i++ {
		require.GreaterOrEqual(t, sizes[i], sizes[i-1], "conflicts must not shrink the buffers: %v", sizes)
	}
	require.Greater(t, sizes[len(sizes)-1], sizes[0])
	require.Greater(t, h.nodeLimit("n0"), int64(h.bi.(*bulkIndexer).nodeSizing.initial))
}

// TestBulkIndexerBacksOffWhenARequestFails: a request that does not complete is
// a milder reason to back off than a rejection, but a reason.
func TestBulkIndexerBacksOffWhenARequestFails(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0")
	var fail atomic.Bool
	h.server.respond = func(_ sentRequest, _ string) (int, string) {
		if fail.Load() {
			return http.StatusBadGateway, `{"error":"bad gateway"}`
		}

		return 200, `{"took":1,"errors":false,"items":[]}`
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	for range 10 {
		h.round("n0")
	}
	shardBefore, limitBefore := h.shardTarget("n0"), h.nodeLimit("n0")

	fail.Store(true)
	h.round("n0")
	fail.Store(false)

	for _, c := range []struct {
		name          string
		before, after int64
	}{
		{"shard", shardBefore, h.shardTarget("n0")},
		{"node", limitBefore, h.nodeLimit("n0")},
	} {
		require.Less(t, c.after, c.before, c.name)
		require.Greater(t, c.after, c.before/2, "%s: milder than the halving a rejection gets", c.name)
	}
}
