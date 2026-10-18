// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// withoutLoad clears the fields of the stats that describe the load on the cluster
// and not what the indexer counted, for a test that compares the counters alone.
func withoutLoad(s BulkIndexerStats) BulkIndexerStats {
	s.MaxMemoryBytes, s.NumRejections, s.WorstNodeLimit = 0, 0, 0

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
				NumAdded: 9999, NumRejections: 7, MaxMemoryBytes: 1, WorstNodeLimit: 0.25,
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
		wantReject bool
		wantCut    bool
	}{
		{name: "a healthy node", rate: 100e6},
		{name: "a node five times slower than it was is not a rejecting one", rate: 20e6},
		{name: "a node that rejects", rate: 100e6, reject: true, wantReject: true, wantCut: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, 200, 1, "n0")

			var reject atomic.Bool
			var rate atomic.Uint64
			rate.Store(uint64(100e6))
			h.server.respond = func(r sentRequest, _ string) (int, string) {
				if reject.Load() {
					return 200, rejectingResponse(len(r.actions))
				}

				return 200, `{"took":1,"errors":false,"items":[]}`
			}
			h.server.advance = func(r sentRequest) {
				h.clock.advance(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
			}

			var stats BulkIndexerStats
			require.NoError(t, h.bi.GetStats(&stats))
			require.InDelta(t, 1.0, stats.WorstNodeLimit, 0, "before any node is used the limit is wide open")
			require.Zero(t, stats.NumRejections)

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
			require.Equal(t, tt.wantCut, stats.WorstNodeLimit < 1, "worst node limit %v", stats.WorstNodeLimit)

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
			require.InDelta(t, 1.0, stats.WorstNodeLimit, 1e-9, "relief: the limit is back to wide open")
			require.Equal(t, counted, stats.NumRejections, "the count only grows")
		})
	}
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
