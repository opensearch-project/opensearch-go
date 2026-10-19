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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// itemErrorResponse answers every action of r with status and an error.
func itemErrorResponse(status int) func(sentRequest, string) (int, string) {
	return func(r sentRequest, _ string) (int, string) {
		items := make([]string, len(r.actions))
		for i := range items {
			items[i] = fmt.Sprintf(`{"index":{"_id":"x","status":%d,"error":{"type":"mapper_parsing_exception","reason":"bad"}}}`, status)
		}

		return 200, fmt.Sprintf(`{"took":1,"errors":true,"items":[%s]}`, strings.Join(items, ","))
	}
}

// newTimedIndexer builds an indexer that sends everything in one request at Close,
// reads the time from clock, and whose server takes took to answer each request.
func newTimedIndexer(t *testing.T, server *fakeServer, cfg BulkIndexerConfig, took time.Duration) (*bulkIndexer, *manualClock) {
	t.Helper()

	clock := newManualClock()
	server.advance = func(sentRequest) { clock.advance(took) }
	cfg.NumWorkers = 1
	cfg.FlushInterval = time.Hour
	cfg.ShardRouter = NoShardRouter
	bi := newClusterIndexer(t, server, cfg).(*bulkIndexer)
	bi.now = clock.Now
	bi.epoch = clock.Now()

	return bi, clock
}

// TestBulkIndexerStatsCountsBytes: ItemBytes is the document bodies, MaxItemBytes
// the largest of them, and RequestBytes every request body as it was sent.
func TestBulkIndexerStatsCountsBytes(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 1, FlushInterval: time.Hour, ShardRouter: NoShardRouter})

	bodies := []string{`{"a":1}`, `{"bb":22}`, `{"c":3}`}
	for i, body := range bodies {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Index: "idx", Action: actionIndex, DocumentID: fmt.Sprint("d", i), Body: strings.NewReader(body),
		}))
	}
	require.NoError(t, bi.Close(t.Context()))

	sent := 0
	for _, r := range server.requests() {
		sent += r.bytes
	}

	stats := bi.Stats()
	require.EqualValues(t, len(bodies[0])+len(bodies[1])+len(bodies[2]), stats.ItemBytes)
	require.EqualValues(t, len(bodies[1]), stats.MaxItemBytes)
	require.EqualValues(t, sent, stats.RequestBytes)
	require.Greater(t, stats.RequestBytes, stats.ItemBytes, "a request carries action lines as well")
}

// TestBulkIndexerStatsMeasuresLatency: an item's latency runs from its Add to its
// outcome, across every attempt, and a request's duration is its time on the wire.
func TestBulkIndexerStatsMeasuresLatency(t *testing.T) {
	t.Parallel()

	const took = 50 * time.Millisecond

	tests := []struct {
		name    string
		respond func(sentRequest, string) (int, string)
		retry   RetryPolicy
		// requests is how many requests the two items take.
		requests uint64
		flushed  uint64
		failed   uint64
	}{
		{name: "accepted", requests: 1, flushed: 2},
		{name: "failed", respond: itemErrorResponse(400), requests: 1, failed: 2},
		{name: "retried", respond: retryThenAccept(), retry: retryOnce, requests: 2, flushed: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer(newFakeCluster(nil))
			server.respond = tt.respond
			bi, clock := newTimedIndexer(t, server, BulkIndexerConfig{RetryPolicy: tt.retry}, took)

			addDoc(t, bi, "idx", "a", 1)
			clock.advance(10 * time.Millisecond)
			addDoc(t, bi, "idx", "b", 2)
			require.NoError(t, bi.Close(t.Context()))

			stats := bi.Stats()
			require.Equal(t, tt.requests, stats.NumRequests)
			require.Equal(t, tt.flushed, stats.NumFlushed)
			require.Equal(t, tt.failed, stats.NumFailed)

			wire := took * time.Duration(tt.requests) //nolint:gosec // One or two requests.
			// a waited 10ms for b before the request went out; b did not wait.
			require.EqualValues(t, (wire+10*time.Millisecond)+wire, stats.ItemLatencyNanos)
			require.EqualValues(t, wire+10*time.Millisecond, stats.MaxItemLatencyNanos)
			require.EqualValues(t, wire, stats.RequestDurationNanos)
			require.EqualValues(t, took, stats.MaxRequestDurationNanos)
		})
	}
}

// retryThenAccept rejects every item of the first request with a 429 and accepts
// those of every request after it.
func retryThenAccept() func(sentRequest, string) (int, string) {
	first := true

	return func(r sentRequest, _ string) (int, string) {
		if first {
			first = false

			return 200, rejectingResponse(len(r.actions))
		}

		return 200, okResponse(len(r.actions))
	}
}

// TestBulkIndexerStatsCountsErrorsByLayerAndKind: each failed attempt is counted
// once under its layer and kind, whether or not it is retried.
func TestBulkIndexerStatsCountsErrorsByLayerAndKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		respond func(sentRequest, string) (int, string)
		retry   RetryPolicy
		layer   ErrorLayer
		kind    ErrorKind
		want    uint64
	}{
		{name: "a permanent item error", respond: itemErrorResponse(400), layer: LayerItem, kind: KindPermanent, want: 2},
		{name: "a rejected item, retried", respond: retryThenAccept(), retry: retryOnce, layer: LayerItem, kind: KindRejected, want: 2},
		{
			name:    "a request that failed as a whole",
			respond: func(sentRequest, string) (int, string) { return 400, `{"error":"bad"}` },
			layer:   LayerRequest, kind: KindPermanent, want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer(newFakeCluster(nil))
			server.respond = tt.respond
			bi, _ := newTimedIndexer(t, server, BulkIndexerConfig{RetryPolicy: tt.retry}, 0)

			addDoc(t, bi, "idx", "a", 1)
			addDoc(t, bi, "idx", "b", 2)
			_ = bi.Close(t.Context()) // A failed request is reported, not returned.

			stats := bi.Stats()
			require.Equal(t, tt.want, stats.Errors[tt.layer][tt.kind])
			stats.Errors[tt.layer][tt.kind] = 0
			require.Zero(t, stats.Errors, "nothing else is counted")
		})
	}

	t.Run("cancel", func(t *testing.T) {
		t.Parallel()

		server := newFakeServer(newFakeCluster(nil))
		bi, _ := newTimedIndexer(t, server, BulkIndexerConfig{}, 0)

		addDoc(t, bi, "idx", "a", 1)
		addDoc(t, bi, "idx", "b", 2)
		bi.Cancel()
		_ = bi.Close(t.Context())

		stats := bi.Stats()
		require.EqualValues(t, 2, stats.NumFailed)
		require.EqualValues(t, 2, stats.Errors[LayerIndexer][KindCanceled])
	})
}

// gaugeHarness is an index of two shards on one node that takes one request at a
// time, whose server holds each request until the test lets one go. A large
// document fills a buffer and a small one does not, so a test chooses which
// buffers are sealed and which stay open.
type gaugeHarness struct {
	t      *testing.T
	bi     BulkIndexer
	server *fakeServer
	// ids holds document IDs on each shard.
	ids [2][]string
	// end ends the indexer's construction context.
	end context.CancelFunc
}

func newGaugeHarness(t *testing.T) *gaugeHarness {
	t.Helper()

	m := DefaultShardMap(2)
	h := &gaugeHarness{t: t}
	for i := 0; len(h.ids[0]) < 4 || len(h.ids[1]) < 4; i++ {
		id := fmt.Sprint("d", i)
		h.ids[m.Shard(id)] = append(h.ids[m.Shard(id)], id)
	}

	cluster := newFakeCluster(map[string]ShardMap{"idx": m}, "n0")
	cluster.setWindow(1, false)
	h.server = newFakeServer(cluster)
	h.server.gate = make(chan struct{})
	h.server.entered = make(chan string, 8)
	cfg := fixedBatches(docBytes("idx", h.ids[0][0], 1000000), 1)
	cfg.Context, h.end = context.WithCancel(t.Context())
	h.bi = newClusterIndexer(t, h.server, cfg)

	return h
}

// add adds the n-th document of shard, a large one that fills its buffer or a
// small one that does not.
func (h *gaugeHarness) add(shard, n int, large bool) {
	h.t.Helper()

	seq := 1
	if large {
		seq = 1000000
	}
	addDoc(h.t, h.bi, "idx", h.ids[shard][n], seq)
}

// release lets the oldest held request answer.
func (h *gaugeHarness) release() { h.server.gate <- struct{}{} }

func (h *gaugeHarness) requireGauges(inflight, queued, blocked uint64, why string) {
	h.t.Helper()

	stats := h.bi.Stats()
	require.Equal(h.t, [3]uint64{inflight, queued, blocked},
		[3]uint64{stats.InFlightRequests, stats.QueuedBuffers, stats.BlockedShards}, "in flight, queued, blocked: %s", why)
}

// TestBulkIndexerStatsGauges: InFlightRequests, QueuedBuffers and BlockedShards
// follow each buffer as it is opened, sealed, sent, answered and failed.
func TestBulkIndexerStatsGauges(t *testing.T) {
	t.Parallel()

	t.Run("send and answer", func(t *testing.T) {
		t.Parallel()

		h := newGaugeHarness(t)

		h.add(0, 0, true)
		receive(t, h.server.entered)
		h.requireGauges(1, 0, 0, "shard 0's request is out")

		h.add(1, 0, true)
		h.requireGauges(1, 1, 0, "shard 1's buffer waits for the node, not for its shard")

		h.add(1, 1, false)
		h.requireGauges(1, 1, 0, "shard 1 has nothing out to wait behind")

		h.add(0, 1, false)
		h.requireGauges(1, 1, 1, "shard 0 has a buffer open behind its request")

		h.release()
		receive(t, h.server.entered)
		h.requireGauges(1, 0, 1, "shard 0 is answered; shard 1's request is out with a buffer open behind it")

		h.release()
		require.Eventually(t, func() bool { return h.bi.Stats().NumFlushed == 2 }, 10*time.Second, time.Millisecond)
		h.requireGauges(0, 0, 0, "both shards are answered and only open buffers are left")

		close(h.server.gate)
		require.NoError(t, h.bi.Close(t.Context()))
		h.requireGauges(0, 0, 0, "closed")
	})

	t.Run("cancel", func(t *testing.T) {
		t.Parallel()

		h := newGaugeHarness(t)

		h.add(0, 0, true)
		receive(t, h.server.entered)
		h.add(1, 0, true)
		h.add(0, 1, false)
		h.requireGauges(1, 1, 1, "before the cancel")

		// What was not sent is failed as it reaches the front, without a request.
		h.bi.Cancel()
		close(h.server.gate)
		require.Eventually(t, func() bool { return h.bi.Stats().NumFailed == 2 }, 10*time.Second, time.Millisecond)
		h.requireGauges(0, 0, 0, "every buffer is done")
		_ = h.bi.Close(t.Context())
	})

	t.Run("construction context ends", func(t *testing.T) {
		t.Parallel()

		h := newGaugeHarness(t)

		h.add(0, 0, true)
		receive(t, h.server.entered)
		h.add(1, 0, true)
		h.add(0, 1, true)
		h.requireGauges(1, 2, 1, "before the end")

		// The scheduler stops sending, and Close fails the buffers left queued.
		h.end()
		close(h.server.gate)
		_ = h.bi.Close(t.Context())
		h.requireGauges(0, 0, 0, "closed")
	})
}

// TestBulkIndexerStatsCountsPressureFlushes: an Add that runs out of memory sends
// the fullest buffer early, and that batch is counted as a pressure flush.
func TestBulkIndexerStatsCountsPressureFlushes(t *testing.T) {
	t.Parallel()

	itemBytes := docBytes("idx", "doc", 1000)
	server := newFakeServer(newFakeCluster(nil))
	cfg := fixedBatches(itemBytes, 100)
	cfg.NumWorkers = 1
	cfg.MaxMemoryBytes = int(itemBytes) * 4
	cfg.ShardRouter = NoShardRouter
	bi := newClusterIndexer(t, server, cfg)

	for i := range 20 {
		addDoc(t, bi, "idx", "doc", 1000+i%10)
	}
	require.NoError(t, bi.Close(t.Context()))

	stats := bi.Stats()
	require.Positive(t, stats.NumPressureFlushes)
	require.Zero(t, stats.NumByteFlushes, "no batch reached its size")
	require.Equal(t, stats.NumRequests, stats.NumPressureFlushes+stats.NumCloseFlushes)
}

// TestBulkIndexerStatsCountsItemsInRetryBackoff: the items a retry policy holds
// back are counted while they wait, whether the cluster rejected them one by one
// or with their whole request, and not once the wait ends.
func TestBulkIndexerStatsCountsItemsInRetryBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		respond func(sentRequest, string) (int, string)
	}{
		{name: "items rejected", respond: func(r sentRequest, _ string) (int, string) { return 200, rejectingResponse(len(r.actions)) }},
		{name: "request rejected", respond: func(sentRequest, string) (int, string) { return 429, `{"error":"busy"}` }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer(newFakeCluster(nil))
			server.respond = tt.respond
			waitLong := RetryPolicyFunc(func(int, *BulkError) (time.Duration, bool) { return time.Hour, true })
			bi, _ := newTimedIndexer(t, server, BulkIndexerConfig{RetryPolicy: waitLong}, 0)

			addDoc(t, bi, "idx", "a", 1)
			addDoc(t, bi, "idx", "b", 2)
			go func() { _ = bi.Flush(t.Context()) }()

			require.Eventually(t, func() bool { return bi.Stats().RetryBackoffItems == 2 },
				10*time.Second, time.Millisecond, "both items wait for their next attempt")

			bi.Cancel()
			require.Eventually(t, func() bool { return bi.Stats().NumFailed == 2 }, 10*time.Second, time.Millisecond)
			require.Zero(t, bi.Stats().RetryBackoffItems, "a canceled wait counts nothing")
			_ = bi.Close(t.Context())
		})
	}
}
