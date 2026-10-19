// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
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

// TestBulkIndexerKeepsOrderWhenTheShardMapIsLost: a document's first action is in
// flight when the router stops answering for its index. The next action on the
// document would join the unordered buffers, which do not wait for a shard's
// request, so it waits for the shard's requests instead.
func TestBulkIndexerKeepsOrderWhenTheShardMapIsLost(t *testing.T) {
	t.Parallel()

	h := newHeldRequest(t, DefaultShardMap(8))
	addDoc(t, h.bi, "idx", "x", 1)
	<-h.server.entered

	h.cluster.setUnknown("idx", true)
	second := addAsync(h.bi, secondAction("x"))

	h.requireHeldBack(t)
	h.release()
	require.NoError(t, <-second)
	require.NoError(t, h.bi.Close(t.Context()))

	requireSequence(t, h.server, 1, 2)
}

// TestBulkIndexerKeepsOrderWhenTheShardGeometryChanges: the index is recreated with
// fewer shards while a document's first action is in flight. The document may now
// belong to another shard, whose buffer does not wait for the first action's, so the
// next action waits for the old shards' requests.
func TestBulkIndexerKeepsOrderWhenTheShardGeometryChanges(t *testing.T) {
	t.Parallel()

	before, after := DefaultShardMap(8), DefaultShardMap(4)
	id := documentOnDifferentShards(t, before, after)

	h := newHeldRequest(t, before)
	addDoc(t, h.bi, "idx", id, 1)
	<-h.server.entered

	h.cluster.setShardMap("idx", after)
	second := addAsync(h.bi, secondAction(id))

	h.requireHeldBack(t)
	h.release()
	require.NoError(t, <-second)
	require.NoError(t, h.bi.Close(t.Context()))

	requireSequence(t, h.server, 1, 2)
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

// TestBulkIndexerIgnoresAChangeAnUnversionedRouterReports: a router that reports
// generation 0 cannot say the geometry changed, so nothing is aborted.
func TestBulkIndexerIgnoresAChangeAnUnversionedRouterReports(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0")
	server := newFakeServer(cluster)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 2, FlushInterval: time.Hour})

	var failed atomic.Int64
	onFailure := func(context.Context, BulkIndexerItem, *BulkError) { failed.Add(1) }
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, DocumentID: "a", Body: seqBody(1), OnError: onFailure,
	}))

	cluster.mu.Lock()
	cluster.mu.maps["idx"] = DefaultShardMap(4) // Still generation 0.
	cluster.mu.Unlock()
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, DocumentID: "b", Body: seqBody(2), OnError: onFailure,
	}))

	require.NoError(t, bi.Close(t.Context()))
	require.Zero(t, failed.Load())
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
