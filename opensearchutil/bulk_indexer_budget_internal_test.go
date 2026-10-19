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
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stalledServer is a fake server that holds every request at a gate, over a
// cluster with no nodes, so the indexer has one logical destination.
func stalledServer() *fakeServer {
	server := newFakeServer(newFakeCluster(nil))
	server.gate = make(chan struct{})
	server.entered = make(chan string, 256)

	return server
}

// fakeClock returns a clock that advances by tick on every reading.
func fakeClock(tick time.Duration) func() time.Time {
	var mu sync.Mutex
	now := time.Unix(0, 0)

	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(tick)

		return now
	}
}

// TestBulkIndexerStalledRequestsBlockAdd shows the in-flight limit at work: with
// every request stalled, Add stops accepting items once the limit is spent
// instead of queueing and buffering them without bound, and it resumes when the
// requests finish.
func TestBulkIndexerStalledRequestsBlockAdd(t *testing.T) {
	t.Parallel()

	const (
		totalItems = 60
		limitItems = 6
		tick       = 5 * time.Millisecond
	)

	itemBytes := docBytes("idx", "doc", 1000)
	server := stalledServer()

	// One item fills a batch, so every item is a request; the node's window of
	// two leaves the rest queued, holding bytes.
	cfg := fixedBatches(itemBytes, 1)
	cfg.NumWorkers = 8
	cfg.MaxMemoryBytes = int(itemBytes) * limitItems
	cfg.ShardRouter = NoShardRouter
	built := newClusterIndexer(t, server, cfg)
	indexer := built.(*bulkIndexer)

	indexer.budget.now = fakeClock(tick)
	blocked := make(chan struct{}, totalItems)
	relieve := indexer.budget.onBlock
	indexer.budget.onBlock = func() {
		relieve()
		blocked <- struct{}{}
	}

	var accepted atomic.Int64
	addsDone := make(chan struct{})
	go func() {
		defer close(addsDone)
		for i := range totalItems {
			if addErr := built.Add(t.Context(), BulkIndexerItem{
				Action: actionIndex, Index: "idx", DocumentID: "doc", Body: seqBody(1000 + i%10),
			}); addErr != nil {
				return
			}
			accepted.Add(1)
		}
	}()

	receive(t, server.entered) // A request is out and stalled.
	receive(t, blocked)        // Add has run out of room.

	stats := built.Stats()
	require.GreaterOrEqual(t, stats.NumBlockedAdds, uint64(1))
	require.Positive(t, stats.InFlightBytes, "a stalled request holds bytes")

	// The limit, not the loop, ended the accepting: far fewer than totalItems got
	// in, and what is held fits the limit.
	require.LessOrEqual(t, stats.BufferedBytes+stats.InFlightBytes, uint64(itemBytes)*limitItems) //nolint:gosec // A positive size.
	require.Less(t, accepted.Load(), int64(totalItems))
	// Memory stayed flat with it: a handful of chunks, not one per item accepted.
	require.LessOrEqual(t, indexer.chunks.allocated.Load(), uint64(limitItems))

	acceptedWhileStalled := accepted.Load()

	close(server.gate) // The cluster answers.
	receive(t, addsDone)

	require.Greater(t, accepted.Load(), acceptedWhileStalled, "Add must resume when requests finish")
	require.NoError(t, built.Close(t.Context()))

	stats = built.Stats()
	require.EqualValues(t, totalItems, stats.NumAdded)
	require.GreaterOrEqual(t, stats.NumBlockedAdds, uint64(1))
	require.Equal(t, tick*time.Duration(stats.NumBlockedAdds), stats.BlockedAddDuration, //nolint:gosec // A handful of waits.
		"the time Add spent waiting must be reported")
	require.Zero(t, stats.BufferedBytes+stats.InFlightBytes, "everything is given back after Close")

	total := 0
	for _, r := range server.requests() {
		total += len(r.actions)
	}
	require.Equal(t, totalItems, total)
}

// TestBulkIndexerReportsEachBlockedAdd: the waits reach OnAddBlocked one by one,
// and add up to what the stats report.
func TestBulkIndexerReportsEachBlockedAdd(t *testing.T) {
	t.Parallel()

	const (
		totalItems = 30
		tick       = 3 * time.Millisecond
	)

	itemBytes := docBytes("idx", "doc", 1000)
	server := stalledServer()

	var (
		mu     sync.Mutex
		waited []time.Duration
	)
	cfg := fixedBatches(itemBytes, 1)
	cfg.NumWorkers = 8
	cfg.ShardRouter = NoShardRouter
	cfg.MaxMemoryBytes = int(itemBytes) * 4
	cfg.OnAddBlocked = func(_ context.Context, d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		waited = append(waited, d)
	}
	bi := newClusterIndexer(t, server, cfg)
	indexer := bi.(*bulkIndexer)
	indexer.budget.now = fakeClock(tick)

	blocked := make(chan struct{}, totalItems)
	relieve := indexer.budget.onBlock
	indexer.budget.onBlock = func() {
		relieve()
		blocked <- struct{}{}
	}

	added := make(chan struct{})
	go func() {
		defer close(added)
		for i := range totalItems {
			if addErr := bi.Add(t.Context(), BulkIndexerItem{
				Action: actionIndex, Index: "idx", DocumentID: "doc", Body: seqBody(1000 + i%10),
			}); addErr != nil {
				return
			}
		}
	}()

	receive(t, server.entered)
	receive(t, blocked)

	close(server.gate)
	receive(t, added)
	require.NoError(t, bi.Close(t.Context()))

	stats := bi.Stats()
	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, waited)
	require.EqualValues(t, len(waited), stats.NumBlockedAdds, "one report per Add that waited")

	var total time.Duration
	for _, d := range waited {
		require.Equal(t, tick, d, "one reading at the start and one at the end, a tick apart")
		total += d
	}
	require.Equal(t, total, stats.BlockedAddDuration)
}

// TestBulkIndexerSpentLimitSealsIdleBatches holds the limit with open batches that
// are nowhere near their size, with nothing in flight, and requires Add to be let
// through by sending them, not by the next flush interval, an hour away here.
func TestBulkIndexerSpentLimitSealsIdleBatches(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0", "n1")
	server := newFakeServer(cluster)

	itemBytes := docBytes("idx", "doc-0000", 1000)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		NumWorkers:     2,
		FlushBytes:     1 << 20,
		Sizing:         BulkSizing{Fixed: true},
		FlushInterval:  time.Hour,
		MaxMemoryBytes: int(itemBytes) * 10,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const items = 200
	for i := range items {
		require.NoError(t, bi.Add(ctx, BulkIndexerItem{
			Action: actionIndex, Index: "idx", DocumentID: fmt.Sprintf("doc-%04d", i), Body: seqBody(1000 + i%10),
		}), "Add %d must be let through by sending the idle batches", i)
	}

	require.Positive(t, bi.Stats().NumBlockedAdds, "the workload must have spent the limit")
	require.NoError(t, bi.Close(t.Context()))

	total := 0
	for _, r := range server.requests() {
		total += len(r.actions)
	}
	require.Equal(t, items, total)
}

// TestBulkIndexerReusesChunksAcrossFlushes runs many requests and requires the
// slab to serve them from reuse, with no allocation per item.
func TestBulkIndexerReusesChunksAcrossFlushes(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	cfg := fixedBatches(docBytes("idx", "d0", 1), 1)
	cfg.NumWorkers = 1
	cfg.ShardRouter = NoShardRouter
	bi := newClusterIndexer(t, server, cfg)
	indexer := bi.(*bulkIndexer)

	const items = 200
	for i := range items {
		addDoc(t, bi, "idx", fmt.Sprintf("d%d", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	served, allocated := indexer.chunks.served.Load(), indexer.chunks.allocated.Load()
	require.GreaterOrEqual(t, served, uint64(items), "each request takes a chunk")
	require.Less(t, allocated, served/2, "chunks must be reused across requests")
}

// TestBulkIndexerBodyIsSentWithItsLength sends a batch that spans several chunks
// and checks the request is not left to chunked transfer encoding and a copy in
// the transport: it has a Content-Length, can be replayed, and arrives intact.
func TestBulkIndexerBodyIsSentWithItsLength(t *testing.T) {
	t.Parallel()

	type seen struct {
		contentLength int64
		hasGetBody    bool
		body          string
	}
	var (
		mu       sync.Mutex
		requests []seen
	)

	client := newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return defaultRoundTripFunc(req)
		}
		body, err := io.ReadAll(req.Body)
		if err == nil {
			mu.Lock()
			requests = append(requests, seen{req.ContentLength, req.GetBody != nil, string(body)})
			mu.Unlock()
		}
		return defaultRoundTripFunc(req)
	})

	bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client, NumWorkers: 1, ShardRouter: NoShardRouter})
	require.NoError(t, err)

	// Three bodies of 100 KB: about five 64 KiB chunks.
	var want strings.Builder
	for i := range 3 {
		payload := fmt.Sprintf(`{"v":"%s"}`, strings.Repeat(string(rune('a'+i)), 100_000))
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: actionIndex, Index: "idx", DocumentID: fmt.Sprintf("d%d", i), Body: strings.NewReader(payload),
		}))
		fmt.Fprintf(&want, "{\"index\":{\"_index\":\"idx\",\"_id\":\"d%d\"}}\n%s\n", i, payload)
	}
	require.NoError(t, bi.Close(t.Context()))

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, requests, 1)
	require.Equal(t, want.String(), requests[0].body)
	require.EqualValues(t, want.Len(), requests[0].contentLength, "the request must state its length")
	require.True(t, requests[0].hasGetBody, "the request must be replayable without the transport copying it")
}

func TestBulkIndexerOversizeItemIsLetThrough(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 1, MaxMemoryBytes: 100, ShardRouter: NoShardRouter})

	big := fmt.Sprintf(`{"v":"%s"}`, strings.Repeat("x", 5000))
	for i := range 3 {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: actionIndex, Index: "idx", DocumentID: fmt.Sprintf("d%d", i), Body: strings.NewReader(big),
		}))
	}
	require.NoError(t, bi.Close(t.Context()))

	total := 0
	for _, r := range server.requests() {
		total += len(r.actions)
	}
	require.Equal(t, 3, total, "an item bigger than the limit must still be sent")
}

func TestBulkIndexerDefaults(t *testing.T) {
	t.Parallel()

	bi := newClusterIndexer(t, newFakeServer(newFakeCluster(nil)), BulkIndexerConfig{ShardRouter: NoShardRouter})
	cfg := bi.(*bulkIndexer).config

	require.Equal(t, DefaultMaxMemoryBytes, cfg.MaxMemoryBytes)
	require.Equal(t, 5_000_000, cfg.FlushBytes)
	require.Equal(t, defaultFlushInterval, cfg.FlushInterval)
	require.Zero(t, cfg.NumWorkers, "no cap unless asked for")
	require.NoError(t, bi.Close(t.Context()))
}

// TestBulkIndexerPressureSealsOnlyWhatCanGoOut: Add waits for memory while the node
// has every request it allows out. A buffer sealed now would only queue behind them,
// freeing nothing until they are answered, and it would split the batch it was
// becoming into small requests. So nothing short of full is sealed until a request
// can go; when one is answered, the fullest buffer is sent.
func TestBulkIndexerPressureSealsOnlyWhatCanGoOut(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0")
	cluster.setWindow(1, false)
	server := newFakeServer(cluster)
	server.gate = make(chan struct{})

	const producers, perProducer = 4, 100
	const items = producers * perProducer
	// One per request at most, so the server never waits on the test to read it.
	server.entered = make(chan string, items)

	const perBatch = 8
	itemBytes := docBytes("idx", "doc-0000", 1000)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:     int(itemBytes) * perBatch,
		Sizing:         BulkSizing{Fixed: true},
		FlushInterval:  time.Hour,
		MaxMemoryBytes: int(itemBytes) * 40,
	})
	indexer := bi.(*bulkIndexer)

	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				// Not addDoc: a test's helper may not fail the test from another goroutine.
				_ = bi.Add(t.Context(), BulkIndexerItem{
					Action: actionIndex, Index: "idx", DocumentID: fmt.Sprintf("doc-%d-%04d", p, i), Body: seqBody(1000 + i%10),
				})
			}
		})
	}
	produced := make(chan struct{})
	go func() {
		wg.Wait()
		close(produced)
	}()

	// The first block sends a buffer, which the server holds: the node's one
	// request is out, and the producers that run out of memory after it wait.
	receive(t, server.entered)
	require.Eventually(t, func() bool { return indexer.budget.waiting() >= 2 },
		10*time.Second, time.Millisecond, "Adds must be waiting for memory with the node's window full")

	indexer.sched.Lock()
	for _, b := range indexer.sched.sealed {
		if !b.dispatched {
			require.GreaterOrEqual(t, b.bytes, itemBytes*perBatch, "only a full buffer queues behind the node's window")
		}
	}
	indexer.sched.Unlock()

	close(server.gate)
	receive(t, produced)
	require.NoError(t, bi.Close(t.Context()))

	total := 0
	for _, r := range server.requests() {
		total += len(r.actions)
	}
	require.Equal(t, items, total)
}
