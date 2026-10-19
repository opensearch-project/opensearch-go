// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// withoutLoad clears the fields of the stats that describe the load on the cluster
// and not what the indexer counted, for a test that compares the counters alone.
func withoutLoad(s BulkIndexerStats) BulkIndexerStats {
	s.MaxMemoryBytes, s.NumRejections, s.NumNodes = 0, 0, 0
	s.NodeWindow, s.BufferSize, s.Slowdown = JobNodeStats{}, JobNodeStats{}, JobNodeStats{}

	return s
}

func TestBulkIndexerGetStatsRefusesNil(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 1, "n0")

	require.ErrorIs(t, h.bi.GetStats(nil), ErrNilStats)
}

func TestBulkIndexerGetStatsFillsTheCallersStruct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		dirty BulkIndexerStats
		close bool
	}{
		{name: "an empty struct", dirty: BulkIndexerStats{}},
		{
			name: "a used struct is replaced entirely, not added to",
			dirty: BulkIndexerStats{
				NumAdded: 9999, NumRejections: 7, MaxMemoryBytes: 1, NodeWindow: JobNodeStats{Min: 0.25},
				NumRetries: [numFailureKinds]uint64{1, 2, 3, 4},
			},
		},
		{name: "a closed indexer still answers", dirty: BulkIndexerStats{NumAdded: 5}, close: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 200, 1, "n0")
			for range 5 {
				h.round("n0")
			}
			waitIdle(t, h.bi)
			if tt.close {
				require.NoError(t, h.bi.Close(t.Context()))
			}

			dst := tt.dirty
			require.NoError(t, h.bi.GetStats(&dst))

			require.Equal(t, h.bi.Stats(), dst, "GetStats and Stats agree once the indexer is idle")
			require.Positive(t, dst.NumAdded)
			require.NotZero(t, dst.MaxMemoryBytes)
		})
	}
}

// TestBulkIndexerGetStatsLoad checks the two values a consumer steers by: a node that
// rejects is counted and pulls the worst node limit down, and a node that only slows
// down does neither.
func TestBulkIndexerGetStatsLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// rate is how fast the node serves, in bytes per second; reject answers 429.
		rate       float64
		reject     bool
		fixed      bool
		wantReject bool
		wantCut    bool
		wantSlow   bool
	}{
		{name: "a healthy node", rate: 100e6},
		{
			name: "a node five times slower than it was is slow, and is not a rejecting one",
			rate: 20e6, wantSlow: true,
		},
		{
			name: "a node five times slower than it was is slow under fixed sizing",
			rate: 20e6, fixed: true, wantSlow: true,
		},
		{name: "a node that rejects", rate: 100e6, reject: true, wantReject: true, wantCut: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 200, 1, "n0")
			h.bi.(*bulkIndexer).sizing.fixed = tt.fixed

			var reject atomic.Bool
			var rate atomic.Uint64
			rate.Store(uint64(100e6))
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				if reject.Load() {
					return 200, rejectingResponse(len(r.actions))
				}

				return 200, okResponse(len(r.actions))
			}
			h.server.advance = func(r sentRequest) {
				h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
			}

			var stats BulkIndexerStats
			require.NoError(t, h.bi.GetStats(&stats))
			require.InDelta(t, 1.0, stats.NodeWindow.Min, 0, "before any node is used the window is wide open")
			require.Zero(t, stats.NumRejections)
			require.Zero(t, stats.NumNodes)

			for range 20 {
				h.round("n0")
			}

			rate.Store(uint64(tt.rate))
			reject.Store(tt.reject)
			for range 3 {
				h.round("n0")
			}
			waitIdle(t, h.bi)

			require.NoError(t, h.bi.GetStats(&stats))
			require.Equal(t, tt.wantReject, stats.NumRejections > 0, "rejections counted: %d", stats.NumRejections)
			require.Equal(t, tt.wantCut, stats.NodeWindow.Min < 1, "node window %+v", stats.NodeWindow)
			require.EqualValues(t, 1, stats.NumNodes)
			require.Equal(t, tt.wantSlow, stats.Slowdown.Max > 0.5, "slowdown %+v", stats.Slowdown)
			if !tt.wantReject {
				// With one node every point of the spread is that node.
				require.InDelta(t, stats.Slowdown.Min, stats.Slowdown.Max, 1e-9)
			}

			if !tt.wantCut {
				return
			}

			// The limit climbs back as the node takes writes again, and the count stays.
			counted := stats.NumRejections
			reject.Store(false)
			for range 60 {
				h.round("n0")
			}
			waitIdle(t, h.bi)

			require.NoError(t, h.bi.GetStats(&stats))
			require.InDelta(t, 1.0, stats.NodeWindow.Min, 1e-9, "relief: the limit is back to wide open")
			require.Equal(t, counted, stats.NumRejections, "the count only grows")
		})
	}
}

// TestBulkIndexerGetStatsSlowdownCountsTheFixedCostOfARequest: a node that charges a
// fixed cost per request serves small requests at fewer bytes per second than large
// ones without being any slower. Its Slowdown must read 0 once the sizer has learned
// that cost, which is the notion of slowdown the sizer acts on, and must not stay at
// the gap between the first large request and the small ones that follow.
func TestBulkIndexerGetStatsSlowdownCountsTheFixedCostOfARequest(t *testing.T) {
	t.Parallel()

	const perRequest = 3 * time.Millisecond

	h := newSizingHarness(t, 200, 1, "n0")
	bi := h.bi.(*bulkIndexer)
	// Hold every buffer at the smallest size the sizer asks for, which is where it
	// learns the fixed cost.
	bi.sizing.min = bi.sizing.initial
	bi.sizing.step = 1

	h.server.advance = func(r sentRequest) {
		h.clock.advance(perRequest + time.Duration(float64(r.bytes)/100e6*float64(time.Second)))
	}

	// One large request first: it sets the best rate the small requests are held
	// against, and is too large to teach the fixed cost.
	big := strings.Repeat("x", int(bi.sizing.initial)*3)
	require.NoError(t, h.bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, DocumentID: h.idsFor(0, 1)[0],
		Body: strings.NewReader(`{"p":"` + big + `"}`),
	}))
	waitIdle(t, h.bi)

	for range 150 {
		h.round("n0")
	}
	waitIdle(t, h.bi)

	var stats BulkIndexerStats
	require.NoError(t, h.bi.GetStats(&stats))
	require.Less(t, stats.Slowdown.Max, 0.1, "slowdown %+v", stats.Slowdown)
}

// TestBulkIndexerGetStatsWhileAdding reads the statistics from another goroutine
// while items are being added and sent; the race detector checks that this is safe.
func TestBulkIndexerGetStatsWhileAdding(t *testing.T) {
	t.Parallel()

	h := newSizingHarness(t, 200, 2, "n0", "n1")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		var stats BulkIndexerStats
		for {
			select {
			case <-stop:
				return
			default:
				_ = h.bi.GetStats(&stats)
			}
		}
	})

	for range 30 {
		h.round("n0")
		h.round("n1")
	}
	close(stop)
	wg.Wait()
}

// TestBulkIndexerGetStatsTakesNoLock: GetStats is documented to load every value from
// an atomic, so a caller that polls it to steer its input does not contend with Add
// for the byte budget's lock. It returns while that lock is held, and still sees what
// is buffered.
func TestBulkIndexerGetStatsTakesNoLock(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0")
	bi := newClusterIndexer(t, newFakeServer(cluster), BulkIndexerConfig{FlushInterval: time.Hour})
	addDoc(t, bi, "idx", "doc", 1) // Buffered, not sent.

	budget := bi.(*bulkIndexer).budget
	budget.mu.Lock()
	unlocked := false
	t.Cleanup(func() {
		if !unlocked {
			budget.mu.Unlock()
		}
	})

	var stats BulkIndexerStats
	done := make(chan error, 1)
	go func() { done <- bi.GetStats(&stats) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("GetStats waited for the byte budget's lock")
	}
	budget.mu.Unlock()
	unlocked = true

	require.Positive(t, stats.BufferedBytes, "the buffered item is counted")
	require.EqualValues(t, budget.inUse(), stats.BufferedBytes+stats.InFlightBytes)
	require.NoError(t, bi.Close(t.Context()))
}

func TestBulkIndexerStatsBudgetFill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stats BulkIndexerStats
		want  float64
	}{
		{name: "an unknown limit", stats: BulkIndexerStats{BufferedBytes: 10}, want: 0},
		{name: "empty", stats: BulkIndexerStats{MaxMemoryBytes: 100}, want: 0},
		{name: "buffered and in flight add up", stats: BulkIndexerStats{MaxMemoryBytes: 100, BufferedBytes: 20, InFlightBytes: 30}, want: 0.5},
		{name: "full", stats: BulkIndexerStats{MaxMemoryBytes: 100, BufferedBytes: 60, InFlightBytes: 40}, want: 1},
		{name: "never above one", stats: BulkIndexerStats{MaxMemoryBytes: 100, BufferedBytes: 150}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.InDelta(t, tt.want, tt.stats.BudgetFill(), 1e-9)
		})
	}
}

// TestBulkIndexerGetStatsSpread has one node of five misbehave and checks that the
// spread tells it from the cluster: the outlier is at the end of the reading it
// disturbs, and the median, which is what a consumer steers by, stays with the
// other four.
func TestBulkIndexerGetStatsSpread(t *testing.T) {
	t.Parallel()

	nodes := []string{"n0", "n1", "n2", "n3", "n4"}

	tests := []struct {
		name string
		// reject makes n0 answer 429; slow makes it serve at a fifth of the rate.
		reject, slow bool
		check        func(t *testing.T, stats BulkIndexerStats)
	}{
		{
			name:   "one node rejecting",
			reject: true,
			check: func(t *testing.T, stats BulkIndexerStats) {
				t.Helper()

				require.Less(t, stats.NodeWindow.Min, 1.0, "the outlier is the minimum")
				require.InDelta(t, 1.0, stats.NodeWindow.Median, 1e-9, "the median is the other four")
				require.InDelta(t, 1.0, stats.NodeWindow.Max, 1e-9)
				require.Greater(t, stats.NodeWindow.Mean, stats.NodeWindow.Min)
				require.LessOrEqual(t, stats.NodeWindow.Mean, stats.NodeWindow.Median)
				require.Positive(t, stats.NumRejections)
			},
		},
		{
			name: "one node slow",
			slow: true,
			check: func(t *testing.T, stats BulkIndexerStats) {
				t.Helper()

				require.Greater(t, stats.Slowdown.Max, 0.5, "the outlier is the maximum")
				require.Less(t, stats.Slowdown.Median, 0.1, "the median is the other four")
				// A node slowing for all its shards is load, which only a rejection answers.
				require.InDelta(t, stats.BufferSize.Median, stats.BufferSize.Min, 1e-9, "its buffers keep their size")
				require.InDelta(t, 1.0, stats.NodeWindow.Min, 1e-9, "a slow node is not a rejecting one")
				require.Zero(t, stats.NumRejections)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 200, 1, nodes...)

			var bad atomic.Bool
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				if tt.reject && bad.Load() && r.node == "n0" {
					return 200, rejectingResponse(len(r.actions))
				}

				return 200, okResponse(len(r.actions))
			}
			h.server.advance = func(r sentRequest) {
				rate := 100e6
				if tt.slow && bad.Load() && r.node == "n0" {
					rate = 20e6
				}
				h.clock.advance(time.Duration(float64(r.bytes) / rate * float64(time.Second)))
			}

			for range 20 {
				for _, node := range nodes {
					h.round(node)
				}
			}
			bad.Store(true)
			for range 4 {
				for _, node := range nodes {
					h.round(node)
				}
			}
			waitIdle(t, h.bi)

			var stats BulkIndexerStats
			require.NoError(t, h.bi.GetStats(&stats))
			require.EqualValues(t, len(nodes), stats.NumNodes)
			tt.check(t, stats)
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  []float64
		weights []float64
		want    JobNodeStats
	}{
		{name: "no nodes", want: JobNodeStats{}},
		{name: "one node", values: []float64{0.4}, weights: []float64{10}, want: JobNodeStats{Min: 0.4, Median: 0.4, Mean: 0.4, Max: 0.4}},
		{
			name: "an odd count has a middle node", values: []float64{1, 0.2, 0.6}, weights: []float64{1, 1, 1},
			want: JobNodeStats{Min: 0.2, Median: 0.6, Mean: 0.6, Max: 1},
		},
		{
			name: "an even count takes the middle two", values: []float64{1, 0.2, 0.6, 0.4}, weights: []float64{1, 1, 1, 1},
			want: JobNodeStats{Min: 0.2, Median: 0.5, Mean: 0.55, Max: 1},
		},
		{
			name:   "one bad node among ten moves the minimum and not the median",
			values: []float64{0.05, 1, 1, 1, 1, 1, 1, 1, 1, 1}, weights: []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			want: JobNodeStats{Min: 0.05, Median: 1, Mean: 0.905, Max: 1},
		},
		{
			name:   "a node with little work waiting counts for little in the mean",
			values: []float64{0.1, 1}, weights: []float64{1, 99},
			want: JobNodeStats{Min: 0.1, Median: 0.55, Mean: 0.991, Max: 1},
		},
		{
			name:   "no weights counts each node the same",
			values: []float64{0, 1}, weights: []float64{0, 0},
			want: JobNodeStats{Min: 0, Median: 0.5, Mean: 0.5, Max: 1},
		},
		{
			name:   "an unusable weight counts as none",
			values: []float64{0, 1, 1}, weights: []float64{-5, math.Inf(1), 2},
			want: JobNodeStats{Min: 0, Median: 1, Mean: 1, Max: 1},
		},
		{
			name:   "missing weights count each node the same",
			values: []float64{0.2, 0.4},
			want:   JobNodeStats{Min: 0.2, Median: 0.3, Mean: 0.3, Max: 0.4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := append([]float64(nil), tt.values...)
			got := summarize(tt.values, tt.weights, nil)

			require.InDelta(t, tt.want.Min, got.Min, 1e-9, "min")
			require.InDelta(t, tt.want.Median, got.Median, 1e-9, "median")
			require.InDelta(t, tt.want.Mean, got.Mean, 1e-9, "mean")
			require.InDelta(t, tt.want.Max, got.Max, 1e-9, "max")
			require.Equal(t, before, append([]float64(nil), tt.values...), "the caller's values are left as they were")
		})
	}
}
