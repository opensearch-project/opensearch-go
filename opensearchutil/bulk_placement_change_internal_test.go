// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// quiet is how long a test waits to see that something does not happen.
const quiet = 100 * time.Millisecond

// heldRequest is a cluster, a server whose bulk requests are held until release
// is called, and an indexer that sends every item as a request of its own. Add of
// the first item returns with its request in flight.
type heldRequest struct {
	cluster *fakeCluster
	server  *fakeServer
	bi      BulkIndexer
	release func()
}

func newHeldRequest(t *testing.T, m ShardMap) *heldRequest {
	t.Helper()

	cluster := newFakeCluster(map[string]ShardMap{"idx": m}, "n0", "n1")
	cluster.setShardMap("idx", m)
	server := newFakeServer(cluster)
	server.gate = make(chan struct{})
	server.entered = make(chan string, 16)

	h := &heldRequest{cluster: cluster, server: server}
	h.release = sync.OnceFunc(func() { close(server.gate) })
	t.Cleanup(h.release)

	h.bi = newClusterIndexer(t, server, fixedBatches(docBytes("idx", "x", 1), 1))

	return h
}

// addAsync adds item on its own goroutine and returns the channel its result arrives on.
func addAsync(bi BulkIndexer, item BulkIndexerItem) <-chan error {
	result := make(chan error, 1)
	go func() { result <- bi.Add(context.Background(), item) }()

	return result
}

// requireHeldBack fails when a request reaches the server before the first one is let go.
func (h *heldRequest) requireHeldBack(t *testing.T) {
	t.Helper()

	select {
	case node := <-h.server.entered:
		t.Fatalf("the second action overtook the first: a request reached %s while the first was in flight", node)
	case <-time.After(quiet):
	}
}

// secondAction is the next action on the document the first one wrote. It is the
// size of one batch, like the first, so that it is sent as soon as it is placed.
func secondAction(id string) BulkIndexerItem {
	return BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: id, Body: seqBody(2)}
}

// requireSequence checks the server received the actions with these sequence numbers, in order.
func requireSequence(t *testing.T, server *fakeServer, want ...int) {
	t.Helper()

	var got []int
	for _, r := range server.requests() {
		for _, a := range r.actions {
			got = append(got, a.seq)
		}
	}
	require.Equal(t, want, got)
}

// TestBulkIndexerKeepsOrderWhenTheShardMapChanges: a document's first action is in
// flight, and its second is sealed and queued behind it, when the router reports a
// new geometry for the index or stops answering for it. The document may now belong
// to another shard, or to the unordered buffers, neither of which waits for the old
// shard's requests, so the third action waits for both of them instead. Neither of
// the first two fails for the change.
func TestBulkIndexerKeepsOrderWhenTheShardMapChanges(t *testing.T) {
	t.Parallel()

	before, after := DefaultShardMap(8), DefaultShardMap(4)
	id := documentOnDifferentShards(t, before, after)

	tests := []struct {
		name   string
		change func(c *fakeCluster)
	}{
		{name: "the geometry changes", change: func(c *fakeCluster) { c.setShardMap("idx", after) }},
		{name: "the router stops answering", change: func(c *fakeCluster) { c.setUnknown("idx", true) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHeldRequest(t, before)
			addDoc(t, h.bi, "idx", id, 1)
			<-h.server.entered
			addDoc(t, h.bi, "idx", id, 2)

			tt.change(h.cluster)
			third := addAsync(h.bi, BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: id, Body: seqBody(3)})

			h.requireHeldBack(t)
			h.release()
			require.NoError(t, <-third)
			require.NoError(t, h.bi.Close(t.Context()))

			requireSequence(t, h.server, 1, 2, 3)
			require.Zero(t, h.bi.Stats().NumFailed, "a change in the shard map fails nothing")
		})
	}
}

// documentOnDifferentShards returns a document id that belongs to a different shard
// under each of the two shard maps.
func documentOnDifferentShards(t *testing.T, before, after ShardMap) string {
	t.Helper()

	for i := range 1000 {
		id := "doc-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
		if before.Shard(id) != after.Shard(id) {
			return id
		}
	}
	t.Fatal("no document id maps to a different shard")

	return ""
}

// TestBulkIndexerSendsBufferedItemsWhenTheShardMapChanges: a document's first action
// is still in a buffer for its shard when the next action on it finds the index's
// shard map changed or gone. The buffered action is sent, under the shard it was
// placed with, and answered before the next one goes out, so neither fails and the
// server sees them in the order they were added.
func TestBulkIndexerSendsBufferedItemsWhenTheShardMapChanges(t *testing.T) {
	t.Parallel()

	before, after := DefaultShardMap(8), DefaultShardMap(4)
	id := documentOnDifferentShards(t, before, after)

	tests := []struct {
		name   string
		change func(c *fakeCluster)
	}{
		{name: "the geometry changes", change: func(c *fakeCluster) { c.setShardMap("idx", after) }},
		{name: "the router stops answering", change: func(c *fakeCluster) { c.setUnknown("idx", true) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(map[string]ShardMap{"idx": before}, "n0")
			cluster.setShardMap("idx", before)
			server := newFakeServer(cluster)
			bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 2, FlushInterval: time.Hour})

			var succeeded, failed atomic.Int64
			item := func(seq int) BulkIndexerItem {
				return BulkIndexerItem{
					Index: "idx", Action: actionIndex, DocumentID: id, Body: seqBody(seq),
					OnSuccess: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { succeeded.Add(1) },
					OnError:   func(context.Context, BulkIndexerItem, *BulkError) { failed.Add(1) },
				}
			}

			require.NoError(t, bi.Add(t.Context(), item(1)))
			require.Empty(t, server.requests(), "the first action is buffered, not sent")

			tt.change(cluster)
			require.NoError(t, bi.Add(t.Context(), item(2)))
			require.Len(t, server.requests(), 1, "the buffered action is sent before the Add that found the change returns")

			require.NoError(t, bi.Close(t.Context()))

			requireSequence(t, server, 1, 2)
			require.Zero(t, failed.Load(), "a change in the shard map fails nothing")
			require.EqualValues(t, 2, succeeded.Load())
		})
	}
}

// documentOnOtherShard returns a document id whose shard under m is none of taken.
func documentOnOtherShard(t *testing.T, m ShardMap, taken ...int) string {
	t.Helper()

	for i := range 1000 {
		id := "other-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
		if !slices.Contains(taken, m.Shard(id)) {
			return id
		}
	}
	t.Fatal("no document id maps to a free shard")

	return ""
}

// TestBulkIndexerKeepsOrderWhenAnAddWaitingForMemorySpansAChange: a document's
// second action is placed under the old geometry and then waits for memory, while
// an Add for another document finds the geometry changed and waits out the index's
// buffers. When the memory comes back the second action must not join a buffer of
// the old shard that nothing waited for: the document's third action, placed under
// the new geometry, would not wait for it either, and could reach the server first.
// The memory may come back after the change is recorded, or while it still waits
// on another of the index's buffers.
func TestBulkIndexerKeepsOrderWhenAnAddWaitingForMemorySpansAChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// heldElsewhere keeps a buffer of another shard of the index out, so that the
		// change is still being waited out when the second action gets its memory.
		heldElsewhere bool
	}{
		{name: "the memory comes back once the change is recorded"},
		{name: "the memory comes back while the change is waited out", heldElsewhere: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before, after := DefaultShardMap(8), DefaultShardMap(4)
			x := documentOnDifferentShards(t, before, after)
			z := documentOnOtherShard(t, before, before.Shard(x))
			y := documentOnOtherShard(t, after, before.Shard(x), after.Shard(x), before.Shard(z))

			cluster := newFakeCluster(map[string]ShardMap{"idx": before}, "n0")
			cluster.setShardMap("idx", before)
			server := newFakeServer(cluster)

			firstHeld, elsewhereHeld := make(chan struct{}), make(chan struct{})
			thirdArrived := make(chan struct{})
			var thirdOnce sync.Once
			var secondAnswered, overtaken atomic.Bool
			server.advance = func(r sentRequest) {
				for _, a := range r.actions {
					switch {
					case a.meta.DocumentID == z:
						<-elsewhereHeld
					case a.meta.DocumentID != x:
					case a.seq == 1:
						<-firstHeld
					case a.seq == 2:
						// A third action that does not wait for this one gets the time to
						// reach the server before this one is answered.
						select {
						case <-thirdArrived:
						case <-time.After(time.Second):
						}
						secondAnswered.Store(true)
					case a.seq == 3:
						overtaken.Store(!secondAnswered.Load())
						thirdOnce.Do(func() { close(thirdArrived) })
					}
				}
			}

			itemBytes := docBytes("idx", x, 1)
			oneItem := itemHeldBytes(BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: x, Body: seqBody(1)}, itemBytes)
			cfg := fixedBatches(itemBytes, 1)
			// The second action on x waits while the first is out, and gets its memory
			// back when the first is answered, whether or not z's is.
			cfg.MaxMemoryBytes = int(oneItem)
			if tt.heldElsewhere {
				cfg.MaxMemoryBytes = int(2 * oneItem)
			}
			bi := newClusterIndexer(t, server, cfg).(*bulkIndexer)

			blocked := make(chan struct{}, 4)
			relieve := bi.budget.onBlock
			bi.budget.onBlock = func() {
				relieve()
				blocked <- struct{}{}
			}

			addDoc(t, bi, "idx", x, 1)
			if tt.heldElsewhere {
				addDoc(t, bi, "idx", z, 20)
			}
			second := addAsync(bi, BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: x, Body: seqBody(2)})
			receive(t, blocked)

			cluster.setShardMap("idx", after)
			other := addAsync(bi, BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: y, Body: seqBody(10)})
			require.Eventually(t, func() bool {
				if bi.placement.TryLock() {
					bi.placement.Unlock()

					return false
				}

				return true
			}, 10*time.Second, time.Millisecond, "the Add for the other document waits out the index's buffers")

			close(firstHeld)
			// An order-keeping second action waits for the change, which waits for z;
			// one that does not is queued while z is still out.
			secondErr, secondDone := error(nil), false
			select {
			case secondErr = <-second:
				secondDone = true
			case <-time.After(time.Second):
			}
			close(elsewhereHeld)
			if !secondDone {
				secondErr = <-second
			}
			require.NoError(t, secondErr)
			require.NoError(t, <-other)
			addDoc(t, bi, "idx", x, 3)
			require.NoError(t, bi.Close(t.Context()))

			var got []int
			for _, r := range server.requests() {
				for _, a := range r.actions {
					if a.meta.DocumentID == x {
						got = append(got, a.seq)
					}
				}
			}
			require.False(t, overtaken.Load(), "the third action on x reached the server before the second was answered")
			require.Equal(t, []int{1, 2, 3}, got, "the actions on x reach the server in the order they were added")
			require.Zero(t, bi.Stats().NumFailed)
		})
	}
}

// TestBulkIndexerIgnoresAChangeAnUnversionedRouterReports: a router that reports
// generation 0 cannot say the geometry changed, so the next Add does not wait out
// the index's buffers: the first item is still buffered when it returns.
func TestBulkIndexerIgnoresAChangeAnUnversionedRouterReports(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0")
	server := newFakeServer(cluster)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 2, FlushInterval: time.Hour})

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, DocumentID: "a", Body: seqBody(1),
	}))

	cluster.mu.Lock()
	cluster.mu.maps["idx"] = DefaultShardMap(4) // Still generation 0.
	cluster.mu.Unlock()
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, DocumentID: "b", Body: seqBody(2),
	}))
	require.Empty(t, server.requests(), "no change was detected, so nothing was sent to wait on")

	require.NoError(t, bi.Close(t.Context()))
	require.Zero(t, bi.Stats().NumFailed)
}

// TestBulkIndexerKeepsOrderWhenAShardRelocates: the node hosting a shard changes
// while a document's first action is in flight to the old one. The shard's next
// buffer goes to the new node, and only after the first request is answered.
func TestBulkIndexerKeepsOrderWhenAShardRelocates(t *testing.T) {
	t.Parallel()

	h := newHeldRequest(t, DefaultShardMap(8))
	h.cluster.setPlacement(func(string, int) string { return "n0" })
	addDoc(t, h.bi, "idx", "x", 1)
	require.Equal(t, "n0", <-h.server.entered)

	h.cluster.setPlacement(func(string, int) string { return "n1" })
	second := addAsync(h.bi, secondAction("x"))

	h.requireHeldBack(t)
	h.release()
	require.NoError(t, <-second)
	require.NoError(t, h.bi.Close(t.Context()))

	requireSequence(t, h.server, 1, 2)
	requests := h.server.requests()
	require.Equal(t, "n0", requests[0].node)
	require.Equal(t, "n1", requests[1].node, "the shard's next request goes to the node that hosts it now")
	require.Zero(t, h.server.overlaps())
}

// TestBulkIndexerKeepsOrderWhenTheShardsNodeDisappears: the router still places the
// document's shard but no longer knows a node for it. The shard keeps its order,
// and its next request goes to the node the indexer chooses.
func TestBulkIndexerKeepsOrderWhenTheShardsNodeDisappears(t *testing.T) {
	t.Parallel()

	h := newHeldRequest(t, DefaultShardMap(8))
	h.cluster.setPlacement(func(string, int) string { return "n0" })
	addDoc(t, h.bi, "idx", "x", 1)
	require.Equal(t, "n0", <-h.server.entered)

	h.cluster.setPlacement(func(string, int) string { return "" })
	second := addAsync(h.bi, secondAction("x"))

	h.requireHeldBack(t)
	h.release()
	require.NoError(t, <-second)
	require.NoError(t, h.bi.Close(t.Context()))

	requireSequence(t, h.server, 1, 2)
	require.Zero(t, h.server.overlaps())
}
