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

// windowOf returns how many bytes the indexer currently allows outstanding to the
// node, and false when it has not seen the node yet.
func (bi *bulkIndexer) windowOf(name string) (int64, bool) {
	bi.sched.Lock()
	defer bi.sched.Unlock()

	ns, ok := bi.sched.nodes[name]
	if !ok {
		return 0, false
	}

	return int64(ns.windowBytes.size), true
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

// okResponse is a 200 answer in which the node indexed every item.
func okResponse(actions int) string {
	items := make([]string, actions)
	for i := range items {
		items[i] = `{"index":{"_id":"x","status":201,"result":"created"}}`
	}

	return fmt.Sprintf(`{"took":1,"errors":false,"items":[%s]}`, strings.Join(items, ","))
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

	return newSizingHarnessFrom(t, ceilingDocs, 0, shardsPerNode, nodes...)
}

// newSizingHarnessFrom is newSizingHarness with shards starting at initialDocs
// documents, or where the indexer starts them when it is 0. A test of whether a
// flush is read starts them low, where the reading shows as growth.
func newSizingHarnessFrom(t *testing.T, ceilingDocs, initialDocs, shardsPerNode int, nodes ...string) *sizingHarness {
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
		Sizing:        BulkSizing{InitialBytes: int(itemBytes) * initialDocs},
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
		// The first buffer of a shard on a node that is not overloaded starts at
		// the size sizing.start gives: the ceiling, unless InitialBytes is set.
		target = int64(h.bi.(*bulkIndexer).sizing.start())
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

// window is how many bytes the indexer allows outstanding to the node.
func (h *sizingHarness) window(node string) int64 {
	h.t.Helper()

	size, _ := h.bi.(*bulkIndexer).windowOf(node)

	return size
}

// TestBulkIndexerByteWindowIsCutByARejection: a 429 is the node's, so it cuts the
// bytes the indexer allows outstanding to the node, from what was outstanding when it
// arrived and not from the window, which started far above that. The cut is held for a
// few requests. It says nothing about any one shard, so the shard's buffer is not cut.
func TestBulkIndexerByteWindowIsCutByARejection(t *testing.T) {
	t.Parallel()

	h := newSizingHarnessFrom(t, 200, 50, 1, "n0")
	bi := h.bi.(*bulkIndexer)
	var reject atomic.Bool
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if reject.Load() {
			return 200, rejectingResponse(len(r.actions))
		}

		return 200, okResponse(len(r.actions))
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	// A healthy node: the shard's buffers grow, and the window stays where it was.
	sizes := make([]int, 0, 12)
	for range 12 {
		sizes = append(sizes, h.round("n0"))
	}
	for i := 1; i < len(sizes); i++ {
		require.GreaterOrEqual(t, sizes[i], sizes[i-1], "a healthy node never sees its buffers shrink: %v", sizes)
	}
	require.Greater(t, sizes[len(sizes)-1], sizes[0])

	windowBefore, shardBefore := h.window("n0"), h.shardTarget("n0")
	require.GreaterOrEqual(t, windowBefore, int64(bi.window.start), "a healthy node does not shrink the window")

	reject.Store(true)
	h.round("n0")
	reject.Store(false)

	windowAfter, shardAfter := h.window("n0"), h.shardTarget("n0")
	require.Equal(t, int64(bi.window.floor), windowAfter,
		"the window falls to half of the one request that was out, which is under the floor of one full buffer, "+
			"where half of the window would be %d", windowBefore/2)
	require.GreaterOrEqual(t, shardAfter, shardBefore, "and the shard's buffer is left alone")

	// The window is held for a few requests before it may grow again.
	for range sizingHoldFlushes {
		h.round("n0")
		require.LessOrEqual(t, h.window("n0"), windowAfter, "no growth while the hold lasts")
	}
	h.round("n0")
	require.Greater(t, h.window("n0"), windowAfter, "growth resumes after the hold")
}

// TestBulkIndexerHoldsWhileTheWholeNodeSlows: the node only gets slower, as when it
// is busy with other requests, and nothing is rejected. That is the node's load,
// which the indexer answers when the node says it is full: until a rejection cuts
// the node's window, the node is sent as much as it takes, and its shard keeps its
// size. A shard gives way only when it is slower than the rest of its node; see
// TestBulkIndexerSlowShardShrinksAlone.
func TestBulkIndexerHoldsWhileTheWholeNodeSlows(t *testing.T) {
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
	shardHealthy, windowHealthy := h.shardTarget("n0"), h.window("n0")
	require.Equal(t, int64(h.bi.(*bulkIndexer).sizing.max), shardHealthy, "a healthy shard ends up at the ceiling")

	setRate(20e6) // Five times slower. Nothing is rejected.
	for range 40 {
		h.round("n0")
	}
	require.Equal(t, shardHealthy, h.shardTarget("n0"), "a node that slows without rejecting keeps being sent full buffers")
	require.GreaterOrEqual(t, h.window("n0"), windowHealthy, "and a slow node is not a rejecting one, so its window is not cut")
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
// window, or the shards' buffers, of a node that does not.
func TestBulkIndexerSizesEachNodeOnItsOwn(t *testing.T) {
	t.Parallel()

	const ceilingDocs = 200

	h := newSizingHarness(t, ceilingDocs, 1, "n0", "n1")
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if r.node == "n0" {
			return 200, rejectingResponse(len(r.actions))
		}

		return 200, okResponse(len(r.actions))
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	for range 10 {
		h.round("n0")
		h.round("n1")
	}

	bi := h.bi.(*bulkIndexer)
	require.Equal(t, int64(bi.window.floor), h.window("n0"), "the node that keeps rejecting is allowed the least")
	require.GreaterOrEqual(t, h.window("n1"), int64(bi.window.start), "the other node is not held back by it")
	require.Greater(t, h.shardTarget("n1"), int64(bi.sizing.initial))
}

// TestBulkIndexerDocumentRejectionsSayNothingAboutTheNode: items the cluster refuses for
// their own sake, a version conflict for instance, are not a reason to send a node
// smaller buffers. Where some items were indexed the request is read as any other and
// the buffers grow; where none were, nothing was indexed, so the duration says nothing
// about the node and the buffers stay where they are.
func TestBulkIndexerDocumentRejectionsSayNothingAboutTheNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// conflicting says whether item i of a request is refused with a 409.
		conflicting func(i int) bool
		wantGrowth  bool
	}{
		{name: "some items conflict and the rest are indexed", conflicting: func(i int) bool { return i%2 == 0 }, wantGrowth: true},
		{name: "every item conflicts, so nothing is indexed", conflicting: func(int) bool { return true }, wantGrowth: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarnessFrom(t, 200, 50, 1, "n0")
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				items := make([]string, len(r.actions))
				for i := range items {
					if tt.conflicting(i) {
						items[i] = `{"index":{"_id":"x","status":409,"error":{"type":"version_conflict_engine_exception","reason":"conflict"}}}`
					} else {
						items[i] = `{"index":{"_id":"x","status":201,"result":"created"}}`
					}
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
			if tt.wantGrowth {
				require.Greater(t, sizes[len(sizes)-1], sizes[0])
			} else {
				require.Equal(t, sizes[0], sizes[len(sizes)-1], "nothing was indexed, so the buffers are not moved: %v", sizes)
			}
			require.GreaterOrEqual(t, h.window("n0"), int64(h.bi.(*bulkIndexer).window.start))
		})
	}
}

// TestBulkIndexerRequestErrorsDoNotMoveTheControllers: an error that is not a 429 and not
// about one shard, a 5xx, a 4xx, a refusal to answer, is read by neither controller: the
// shard's buffer is not cut or grown, no rate is learned from it, and the node's window
// is not cut, since only a 429 does that.
func TestBulkIndexerRequestErrorsDoNotMoveTheControllers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "a bad gateway", status: http.StatusBadGateway, body: `{"error":"bad gateway"}`},
		{name: "a service unavailable", status: http.StatusServiceUnavailable, body: `{"error":"no master"}`},
		{name: "an internal error", status: http.StatusInternalServerError, body: `{"error":"boom"}`},
		{name: "a bad request", status: http.StatusBadRequest, body: `{"error":{"type":"illegal_argument_exception"},"status":400}`},
		{name: "a forbidden request", status: http.StatusForbidden, body: `{"error":{"type":"security_exception"},"status":403}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 200, 1, "n0")
			var fail atomic.Bool
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				if fail.Load() {
					return tt.status, tt.body
				}

				return http.StatusOK, okResponse(len(r.actions))
			}
			h.server.advance = func(r sentRequest) {
				h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
			}

			for range 10 {
				h.round("n0")
			}
			shardBefore, windowBefore := h.shardTarget("n0"), h.window("n0")

			fail.Store(true)
			h.round("n0")
			fail.Store(false)

			require.Equal(t, shardBefore, h.shardTarget("n0"), "the shard's buffer is neither cut nor grown")
			require.GreaterOrEqual(t, h.window("n0"), windowBefore, "and the window is not cut")
		})
	}
}

// TestBulkIndexerLearnsTheLargestRequestANodeAccepts: a 413 says a request was too large,
// which is a fact about the cluster's settings and not about its load. No later request
// is larger than half of what was refused, so the indexer stops sending what cannot be
// accepted instead of growing its buffers back to the same size.
func TestBulkIndexerLearnsTheLargestRequestANodeAccepts(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 400, 1, "n0")
	bi := h.bi.(*bulkIndexer)

	const accepts = 6_000
	var refused atomic.Int64
	h.server.respond = func(r sentRequest, _ string) (int, string) {
		if r.bytes > accepts {
			refused.Add(1)

			return http.StatusRequestEntityTooLarge, `{"error":"Request Entity Too Large"}`
		}

		return http.StatusOK, okResponse(len(r.actions))
	}
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / 100e6 * float64(time.Second)))
	}

	for range 12 {
		h.round("n0")
	}
	waitIdle(t, h.bi)

	require.Positive(t, refused.Load(), "the first buffers were over what the node accepts")

	bi.sched.Lock()
	limit := bi.sched.maxRequest
	bi.sched.Unlock()
	require.NotZero(t, limit, "a limit was learned")
	require.LessOrEqual(t, limit, int64(accepts))

	before := refused.Load()
	for range 12 {
		h.round("n0")
	}
	waitIdle(t, h.bi)
	require.Equal(t, before, refused.Load(), "nothing larger than the node accepts is sent once the limit is learned")
}

// TestBulkIndexerNewShardStartsFromTheNodesBaseline: a shard that starts on a node
// that has slowed down is measured against the best the node has shown, so it is
// cut at once, and does not take the node's slow rate for the best it can do.
func TestBulkIndexerNewShardStartsFromTheNodesBaseline(t *testing.T) {
	t.Parallel()

	const (
		ceilingDocs = 400
		oldShard    = 0
		newShard    = 1
	)

	h := newSizingHarness(t, ceilingDocs, 2, "n0")
	var rate atomic.Uint64
	rate.Store(uint64(100e6))
	h.server.advance = func(r sentRequest) {
		h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
	}

	for range 40 {
		h.roundShard(oldShard)
	}

	rate.Store(uint64(20e6)) // The node slows down before the second shard has sent anything.
	h.roundShard(newShard)

	bi := h.bi.(*bulkIndexer)
	got, ok := bi.shardTargetOf("idx", newShard)
	require.True(t, ok)
	require.Less(t, got, int64(bi.sizing.start()), "its first flush shows a node slower than it was")
}

// TestBulkIndexerRejectedRequestsDoNotTeachTheShardController answers every request
// with a rejection, far faster than the node serves real work, as a node does, and
// checks that the node's window is cut and the shard's buffer is left where it was,
// where a shard controller that took the quick answer for a fast flush would grow it.
func TestBulkIndexerRejectedRequestsDoNotTeachTheShardController(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// respond answers a request that is being rejected.
		respond func(r sentRequest) (int, string)
	}{
		{
			name: "every item rejected in an answered request",
			respond: func(r sentRequest) (int, string) {
				return http.StatusOK, rejectingResponse(len(r.actions))
			},
		},
		{
			name: "the whole request refused with a 429",
			respond: func(sentRequest) (int, string) {
				return http.StatusTooManyRequests, `{"error":{"type":"es_rejected_execution_exception","reason":"queue full"},"status":429}`
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 400, 1, "n0")

			var reject atomic.Bool
			var rate atomic.Uint64
			rate.Store(uint64(100e6))
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				if reject.Load() {
					return tt.respond(r)
				}

				return http.StatusOK, okResponse(len(r.actions))
			}
			h.server.advance = func(r sentRequest) {
				h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
			}

			for range 10 {
				h.round("n0")
			}
			shardBefore, windowBefore := h.shardTarget("n0"), h.window("n0")

			reject.Store(true)
			rate.Store(uint64(1e9)) // The rejection comes back ten times faster than the work takes.
			for range 5 {
				h.round("n0")
			}
			waitIdle(t, h.bi)

			require.Equal(t, shardBefore, h.shardTarget("n0"), "the shard's buffer is not grown by rejections")
			require.Less(t, h.window("n0"), windowBefore/4, "while the node's window is cut")
		})
	}
}

// TestBulkIndexerStartsAShardAtFullSizeOnAnIdleNode: a node that shows no sign of load
// is sent full buffers from the first, and a shard backs off from there only if it
// turns out slower than the rest of its node. Climbing from a fraction would spend a
// short job's first requests below what the node could take.
func TestBulkIndexerStartsAShardAtFullSizeOnAnIdleNode(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 400, 1, "n0")
	bi := h.bi.(*bulkIndexer)

	addDoc(t, h.bi, "idx", h.idsFor(0, 1)[0], 1)
	target, ok := bi.shardTargetOf("idx", 0)
	require.True(t, ok)
	require.Equal(t, int64(bi.sizing.max), target, "a shard on an idle node starts at FlushBytes")
}

// TestBulkIndexerRampsAShardThatStartsOnAnOverloadedNode: a shard that starts on
// an overloaded node, one whose write pool is rejecting, is not sent full buffers.
// It starts at InitialBytes and climbs to full as the node keeps up.
func TestBulkIndexerRampsAShardThatStartsOnAnOverloadedNode(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 400, 1, "n0")
	bi := h.bi.(*bulkIndexer)
	h.server.cluster.setWindow(1, true)

	addDoc(t, h.bi, "idx", h.idsFor(0, 1)[0], 1)
	target, ok := bi.shardTargetOf("idx", 0)
	require.True(t, ok)
	require.Equal(t, int64(bi.sizing.initial), target, "a shard on an overloaded node starts at InitialBytes")
	require.Less(t, target, int64(bi.sizing.max))
}

// TestBulkIndexerStartsEveryShardAtAnInitialBytesThatIsSet: a caller who sets
// InitialBytes has said where every shard starts, on an overloaded node or not.
func TestBulkIndexerStartsEveryShardAtAnInitialBytesThatIsSet(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(1)}, "n0")
	server := newFakeServer(cluster)
	itemBytes := docBytes("idx", "doc-0000", 1000)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:    int(itemBytes) * 400,
		Sizing:        BulkSizing{InitialBytes: int(itemBytes) * 100},
		FlushInterval: time.Hour,
	}).(*bulkIndexer)

	addDoc(t, bi, "idx", "doc-0000", 1)
	target, ok := bi.shardTargetOf("idx", 0)
	require.True(t, ok)
	require.Equal(t, itemBytes*100, target)
}
