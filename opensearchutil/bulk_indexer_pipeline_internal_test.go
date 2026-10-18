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
	"math"
	"math/rand"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixedBatches makes a batch exactly n of the items estimateItemBytes sizes at
// itemBytes, so a test knows how many documents a request carries.
func fixedBatches(itemBytes int64, n int) BulkIndexerConfig {
	return BulkIndexerConfig{
		FlushBytes:    int(itemBytes) * n,
		Sizing:        BulkSizing{Fixed: true},
		FlushInterval: time.Hour,
	}
}

// docBytes is the size estimateItemBytes gives the documents addDoc adds.
func docBytes(index, id string, seq int) int64 {
	return estimateItemBytes(BulkIndexerItem{Index: index, Action: actionIndex, DocumentID: id, Body: seqBody(seq)})
}

func TestBulkIndexerSendsEachShardToItsNode(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"alpha": DefaultShardMap(8), "beta": DefaultShardMap(5)}
	cluster := newFakeCluster(maps, "n0", "n1", "n2")
	server := newFakeServer(cluster)

	bi := newClusterIndexer(t, server, BulkIndexerConfig{FlushInterval: time.Hour})

	const docs = 300
	seq := 0
	for i := range docs {
		for _, index := range []string{"alpha", "beta"} {
			seq++
			addDoc(t, bi, index, "doc-"+fmt.Sprint(i), seq)
		}
	}
	require.NoError(t, bi.Close(t.Context()))

	requests := server.requests()

	total := 0
	nodes := map[string]bool{}
	shards := map[shardKey]int{}
	for _, r := range requests {
		nodes[r.node] = true
		total += len(r.actions)

		// A request carries one shard, and goes to the node hosting it.
		require.Len(t, r.shards, 1, "a request carries the documents of one shard")
		for key := range r.shards {
			shards[key]++
			require.Equal(t, cluster.mu.place(key.index, key.shard), r.node, "request for %v went to the wrong node", key)
		}
	}

	require.Equal(t, 2*docs, total, "every document is sent exactly once")
	require.Len(t, nodes, 3, "all three nodes get the documents of their shards")
	require.Len(t, shards, 8+5, "every shard is written")
	for key, n := range shards {
		require.Equal(t, 1, n, "%v: the shard's buffer is sent once, as a whole", key)
	}
	require.EqualValues(t, 2*docs, bi.Stats().NumGroupedItems)
	require.Zero(t, bi.Stats().NumUngroupedItems)
}

func TestBulkIndexerSinkFillsTheNearestNodeAndMovesOnWhenItIsFull(t *testing.T) {
	t.Parallel()

	const perBatch = 4
	itemBytes := estimateItemBytes(BulkIndexerItem{Action: actionIndex, Body: seqBody(1)})

	tests := []struct {
		name string
		// window is the write pool window of every node.
		window int
		// wantFirstWave is how many requests each node holds once the windows are
		// full, with six batches of documents offered.
		wantFirstWave map[string]int
	}{
		{
			name:          "a node with room keeps getting the batches",
			window:        8,
			wantFirstWave: map[string]int{"near": 6},
		},
		{
			name:          "a full node hands over to the next nearest",
			window:        1,
			wantFirstWave: map[string]int{"near": 1, "mid": 1, "far": 1},
		},
		{
			name:          "two requests per node before moving on",
			window:        2,
			wantFirstWave: map[string]int{"near": 2, "mid": 2, "far": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(nil, "near", "mid", "far")
			cluster.setWindow(tt.window, false)
			server := newFakeServer(cluster)
			server.gate = make(chan struct{})
			server.entered = make(chan string, 64)

			cfg := fixedBatches(itemBytes, perBatch)
			cfg.NumWorkers = 8
			bi := newClusterIndexer(t, server, cfg)

			// Six batches of documents with no ID: nothing ties them to a shard.
			for i := range 6 * perBatch {
				require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{Action: actionIndex, Body: seqBody(i)}))
			}

			// The requests the windows let out are held at the gate, so where they
			// went is settled.
			wantTotal := 0
			for _, n := range tt.wantFirstWave {
				wantTotal += n
			}
			firstWave := map[string]int{}
			for range wantTotal {
				firstWave[receive(t, server.entered)]++
			}
			require.Equal(t, tt.wantFirstWave, firstWave)

			close(server.gate)
			require.NoError(t, bi.Close(t.Context()))

			total := 0
			for _, r := range server.requests() {
				total += len(r.actions)
			}
			require.Equal(t, 6*perBatch, total, "every document is sent once the nodes take them")
		})
	}
}

func TestBulkIndexerWithoutADestinationLeavesRoutingToTheTransport(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	bi := newClusterIndexer(t, server, BulkIndexerConfig{ShardRouter: NoShardRouter, FlushInterval: time.Hour})

	for i := range 50 {
		addDoc(t, bi, "idx", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	requests := server.requests()
	require.Len(t, requests, 1)
	require.Empty(t, requests[0].node, "no node was chosen, so none was asked for")
	require.Len(t, requests[0].actions, 50)
	require.EqualValues(t, 50, bi.Stats().NumUngroupedItems)
}

// workload adds docs documents repeatedly, in random order, to two indexes, and
// returns what was added per document in order.
func (c *fakeCluster) workload(t *testing.T, bi BulkIndexer, seed int64, rounds, docs int, midway func()) map[string][]int {
	t.Helper()

	rng := rand.New(rand.NewSource(seed))
	added := map[string][]int{}

	total := rounds * docs
	for seq := 1; seq <= total; seq++ {
		if midway != nil && seq == total/2 {
			midway()
		}

		index := []string{"alpha", "beta"}[rng.Intn(2)]
		id := fmt.Sprintf("doc-%d", rng.Intn(docs))
		addDoc(t, bi, index, id, seq)

		added[index+"/"+id] = append(added[index+"/"+id], seq)
	}

	return added
}

func TestBulkIndexerWritesAShardsDocumentsInOrderAcrossRelocations(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			t.Parallel()

			maps := map[string]ShardMap{"alpha": DefaultShardMap(8), "beta": DefaultShardMap(3)}
			cluster := newFakeCluster(maps, "n0", "n1", "n2")
			cluster.setWindow(4, false)
			server := newFakeServer(cluster)
			server.yields = 20

			itemBytes := docBytes("alpha", "doc-00", 1000)
			cfg := fixedBatches(itemBytes, 7)
			cfg.NumWorkers = 8
			bi := newClusterIndexer(t, server, cfg)

			added := cluster.workload(t, bi, seed, 40, 30, func() {
				// Every shard moves to the next node while documents are still arriving.
				cluster.setPlacement(func(_ string, shard int) string { return fmt.Sprintf("n%d", (shard+1)%3) })
			})
			require.NoError(t, bi.Close(t.Context()))

			require.Zero(t, server.overlaps(), "two requests carrying one shard were in flight together")
			require.Greater(t, server.maxInflight(), 1, "the test must have exercised concurrent requests")

			got := map[string][]int{}
			for _, r := range server.requests() {
				for _, a := range r.actions {
					key := a.meta.Index + "/" + a.meta.DocumentID
					got[key] = append(got[key], a.seq)
				}
			}
			require.Equal(t, added, got, "a document's actions must reach the server in the order they were added")
		})
	}
}

// TestBulkIndexerHotShardDoesNotHoldBackTheOthers sends most documents to one
// shard. The hot shard has one request outstanding at a time, so the documents
// that arrive meanwhile gather into larger batches, and the other shards go on
// being written alongside it.
func TestBulkIndexerHotShardDoesNotHoldBackTheOthers(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"alpha": DefaultShardMap(8)}
	cluster := newFakeCluster(maps, "n0", "n1")
	cluster.setWindow(8, false)
	server := newFakeServer(cluster)
	server.yields = 50

	// Batches aim for 5 documents; a batch held back by the hot shard may grow to 50.
	itemBytes := docBytes("alpha", "hot-0", 1)
	cfg := BulkIndexerConfig{
		NumWorkers:    8,
		FlushBytes:    int(itemBytes) * 50,
		Sizing:        BulkSizing{MinBytes: int(itemBytes) * 5, InitialBytes: int(itemBytes) * 5, StepBytes: 1},
		FlushInterval: time.Hour,
	}
	bi := newClusterIndexer(t, server, cfg)

	// Find an id for the hot shard and ids for the rest.
	m := maps["alpha"]
	var hotID string
	var coldIDs []string
	for i := 0; len(coldIDs) < 40 || hotID == ""; i++ {
		id := fmt.Sprintf("probe-%d", i)
		switch {
		case m.Shard(id) == 0 && hotID == "":
			hotID = id
		case m.Shard(id) != 0 && len(coldIDs) < 40:
			coldIDs = append(coldIDs, id)
		}
	}

	seq := 0
	for round := range 60 {
		for range 10 {
			seq++
			addDoc(t, bi, "alpha", hotID, seq)
		}
		seq++
		addDoc(t, bi, "alpha", coldIDs[round%len(coldIDs)], seq)
	}
	require.NoError(t, bi.Close(t.Context()))

	require.Zero(t, server.overlaps(), "the hot shard must never have two requests outstanding")
	require.Greater(t, server.maxInflight(), 1, "other shards must have been written alongside the hot one")

	var hotBatches, hotDocs int
	for _, r := range server.requests() {
		for key := range r.shards {
			if key.shard == 0 {
				hotBatches++
				for _, a := range r.actions {
					if a.meta.DocumentID == hotID {
						hotDocs++
					}
				}
			}
		}
	}
	require.Equal(t, 600, hotDocs)
	require.Less(t, hotBatches, 600/5, "documents that arrive while the hot shard's request is out gather into larger batches")
}

func TestBulkIndexerHoldsToOneRequestWhileThePoolRejects(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"alpha": DefaultShardMap(8)}
	cluster := newFakeCluster(maps, "n0")
	cluster.setWindow(8, true) // The write pool is rejecting.
	server := newFakeServer(cluster)
	server.yields = 50

	itemBytes := docBytes("alpha", "doc-00", 1000)
	cfg := fixedBatches(itemBytes, 3)
	cfg.NumWorkers = 8
	bi := newClusterIndexer(t, server, cfg)

	for i := range 200 {
		addDoc(t, bi, "alpha", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, 1, server.maxInflight(), "a node whose pool is rejecting gets one request at a time")
}

func TestBulkIndexerWindowFollowsTheNodesPool(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"alpha": DefaultShardMap(8)}, "n0")
	cluster.setWindow(3, false)
	server := newFakeServer(cluster)
	server.yields = 200

	itemBytes := docBytes("alpha", "doc-00", 1000)
	cfg := fixedBatches(itemBytes, 2)
	cfg.NumWorkers = 16
	bi := newClusterIndexer(t, server, cfg)

	for i := range 400 {
		addDoc(t, bi, "alpha", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	require.LessOrEqual(t, server.maxInflight(), 3, "never more than the node's window")
	require.Greater(t, server.maxInflight(), 1, "and the window is used")
}

// TestBulkIndexerKeepsOrderWhenAShardMapArrives covers an index whose shard map
// the router does not know at first and learns midway: the items added before are
// in no shard's order, so the first one after waits for them.
func TestBulkIndexerKeepsOrderWhenAShardMapArrives(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"idx": DefaultShardMap(8)}
	cluster := newFakeCluster(maps, "n0", "n1")
	cluster.setUnknown("idx", true)
	server := newFakeServer(cluster)

	cfg := BulkIndexerConfig{NumWorkers: 4, FlushInterval: time.Hour} // Only the drain may send before Close.
	bi := newClusterIndexer(t, server, cfg)

	addDoc(t, bi, "idx", "doc", 1) // The shard map is unknown: the item is in no shard's order.
	require.Empty(t, server.requests(), "nothing sends by itself")

	cluster.setUnknown("idx", false)
	addDoc(t, bi, "idx", "doc", 2) // The first item with a shard drains seq 1 first.

	drained := server.requests()
	require.Len(t, drained, 1, "the switch to shard-aware buffering must drain what was buffered without a shard")
	require.Len(t, drained[0].actions, 1)
	require.Equal(t, 1, drained[0].actions[0].seq)

	addDoc(t, bi, "idx", "doc", 3)
	require.NoError(t, bi.Close(t.Context()))

	var seqs []int
	for _, r := range server.requests() {
		for _, a := range r.actions {
			seqs = append(seqs, a.seq)
		}
	}
	require.Equal(t, []int{1, 2, 3}, seqs)
	require.EqualValues(t, 1, bi.Stats().NumUngroupedItems)
	require.EqualValues(t, 2, bi.Stats().NumGroupedItems)
}

func TestBulkIndexerSwitchToShardsHonoursContext(t *testing.T) {
	t.Parallel()

	maps := map[string]ShardMap{"idx": DefaultShardMap(8)}
	cluster := newFakeCluster(maps, "n0")
	cluster.setUnknown("idx", true)
	server := newFakeServer(cluster)

	var onError atomic.Int64
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		NumWorkers: 2, FlushInterval: time.Hour,
		OnError: func(_ context.Context, _ error) { onError.Add(1) },
	})

	addDoc(t, bi, "idx", "doc", 1)
	cluster.setUnknown("idx", false)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, bi.Add(cancelled, BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "doc", Body: seqBody(2)}),
		context.Canceled)
	require.EqualValues(t, 1, bi.Stats().BulkAddFailCount)
	require.EqualValues(t, 1, bi.Stats().NumAdded, "the refused item is not counted as added")

	// The failed attempt left the index unswitched, so the next Add completes it.
	addDoc(t, bi, "idx", "doc", 3)
	require.Len(t, server.requests(), 1, "the retried switch drains the earlier item")
	require.NoError(t, bi.Close(t.Context()))
}

func TestBulkIndexerNodeIsAvailableToOnFlushStart(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(4)}, "n0", "n1")
	server := newFakeServer(cluster)

	var seen atomic.Value
	cfg := BulkIndexerConfig{
		FlushInterval: time.Hour,
		OnFlushStart: func(ctx context.Context) context.Context {
			seen.Store(FlushNode(ctx))
			return ctx
		},
	}
	bi := newClusterIndexer(t, server, cfg)

	addDoc(t, bi, "idx", "doc-1", 1)
	require.NoError(t, bi.Close(t.Context()))

	want := cluster.mu.place("idx", cluster.mu.maps["idx"].Shard("doc-1"))
	require.Equal(t, want, seen.Load())
}

func TestDefaultMemoryLimits(t *testing.T) { //nolint:paralleltest // Changes the process's memory limit.
	tests := []struct {
		name  string
		limit int64
		want  int64
	}{
		{name: "a quarter of the Go memory limit when there is one", limit: 32 << 30, want: 8 << 30},
		{name: "4 GiB at least, whatever the limit", limit: 1 << 30, want: minLimitedMemoryBytes},
		{name: "a quarter of the limit just above the floor", limit: 16<<30 + 4, want: 4<<30 + 1},
		{name: "a quarter of a very large limit", limit: 256 << 30, want: 64 << 30},
		{name: "the constant when there is none", limit: math.MaxInt64, want: DefaultMaxMemoryBytes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := debug.SetMemoryLimit(tt.limit)
			t.Cleanup(func() { debug.SetMemoryLimit(old) })

			require.Equal(t, tt.want, defaultMemoryLimit())
		})
	}
}

// TestDefaultMaxMemoryBytesCoversAGp3Volume: the default is what holding a gp3
// volume's 150 MB/s takes, rounded up to a power of two, and no more than that
// rounding.
func TestDefaultMaxMemoryBytesCoversAGp3Volume(t *testing.T) {
	t.Parallel()

	var need, def int64 = assumedMemoryBytesNeed, DefaultMaxMemoryBytes

	require.Equal(t, int64(301_500_000), need)
	require.GreaterOrEqual(t, def, need)
	require.Less(t, def, 2*need, "it is the next power of two, not a bigger guess")
	require.Zero(t, def&(def-1), "a power of two")

	// The wire alone, at 1 Gbps and 5 ms, is tiny beside it.
	const wire int64 = 125_000_000 * 5 / 1000
	require.Equal(t, int64(625_000), wire)
	require.Greater(t, need, 100*wire)
}

// TestBulkIndexerNumWorkersCapsRequests: NumWorkers is a cap and not a default
// concurrency; at 1 requests go one at a time even to a node that would take many.
func TestBulkIndexerNumWorkersCapsRequests(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		numWorkers int
		wantMax    int
	}{
		{name: "capped at one", numWorkers: 1, wantMax: 1},
		{name: "capped at two", numWorkers: 2, wantMax: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(map[string]ShardMap{"alpha": DefaultShardMap(8)}, "n0", "n1")
			cluster.setWindow(8, false)
			server := newFakeServer(cluster)
			server.yields = 100

			cfg := fixedBatches(docBytes("alpha", "doc-00", 1000), 2)
			cfg.NumWorkers = tt.numWorkers
			bi := newClusterIndexer(t, server, cfg)

			for i := range 300 {
				addDoc(t, bi, "alpha", fmt.Sprint("doc-", i), i)
			}
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, tt.wantMax, server.maxInflight())
		})
	}
}

// TestBulkIndexerRetriedRequestKeepsItsBody: the transport retries a request the
// cluster answers 503 to, and the retry carries the same documents, the whole body
// and not what was left of it after the first attempt.
func TestBulkIndexerRetriedRequestKeepsItsBody(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(2)}, "n0")
	server := newFakeServer(cluster)

	var calls atomic.Int64
	server.respond = func(_ sentRequest, _ string) (int, string) {
		if calls.Add(1) == 1 {
			return http.StatusServiceUnavailable, `{"error":"unavailable"}`
		}

		return http.StatusOK, `{"took":1,"errors":false,"items":[]}`
	}

	bi := newClusterIndexer(t, server, BulkIndexerConfig{FlushInterval: time.Hour})
	for i := range 20 {
		addDoc(t, bi, "idx", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	// The first shard's request was tried twice; both tries carried the same documents.
	seen := map[string][]string{}
	for _, r := range server.requests() {
		for key := range r.shards {
			seen[fmt.Sprint(key)] = append(seen[fmt.Sprint(key)], strings.Join(docsOf(r), ","))
		}
	}
	retried := 0
	for key, tries := range seen {
		for _, docs := range tries {
			require.NotEmpty(t, docs, "%s: a try carried no documents", key)
			require.Equal(t, tries[0], docs, "%s: every try carries the same documents", key)
		}
		if len(tries) > 1 {
			retried++
		}
	}
	require.Equal(t, 1, retried, "exactly one request was retried")
}

// docsOf returns the "index/id" keys of a request's actions in order.
func docsOf(r sentRequest) []string {
	docs := make([]string, len(r.actions))
	for i, a := range r.actions {
		docs[i] = fmt.Sprintf("%s/%s", a.meta.Index, a.meta.DocumentID)
	}

	return docs
}
