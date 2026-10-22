// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testWindowConfig is a window of up to 1000 bytes, one buffer of 10, backing off by half.
func testWindowConfig() windowConfig {
	return windowConfig{start: 1000, ceiling: 1000, floor: 20, unit: 10, backoff: 0.5}
}

func TestByteWindowStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start float64
		want  float64
	}{
		{name: "where it is asked to start", start: 400, want: 400},
		{name: "never above the ceiling", start: 5000, want: 1000},
		{name: "never below the floor", start: 1, want: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start = tt.start

			require.InDelta(t, tt.want, newByteWindow(cfg).size, 1e-9)
		})
	}
}

func TestByteWindowAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		size        float64
		outstanding int64
		request     int64
		wantAllowed bool
	}{
		{name: "it fits", size: 100, outstanding: 40, request: 50, wantAllowed: true},
		{name: "it fits exactly", size: 100, outstanding: 50, request: 50, wantAllowed: true},
		{name: "it does not fit", size: 100, outstanding: 60, request: 50, wantAllowed: false},
		{name: "nothing is out, so one request goes whatever its size", size: 100, outstanding: 0, request: 5000, wantAllowed: true},
		{name: "something is out and the request is larger than the window", size: 100, outstanding: 1, request: 5000, wantAllowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start = tt.size
			cfg.floor = 1
			w := newByteWindow(cfg)

			require.Equal(t, tt.wantAllowed, w.allows(tt.outstanding, tt.request))
		})
	}
}

func TestByteWindowReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		size          float64
		outstanding   int64
		fixed         bool
		sentAtShrinks func(w *byteWindow) uint64
		wantSize      float64
		wantShrink    bool
		wantHold      bool
	}{
		{
			name: "half of what was out, when that is less than the window", size: 1000, outstanding: 300,
			wantSize: 150, wantShrink: true, wantHold: true,
		},
		{
			name: "half of the window, when that is less than what was out", size: 80, outstanding: 300,
			wantSize: 40, wantShrink: true, wantHold: true,
		},
		{
			name: "never below the floor", size: 1000, outstanding: 10,
			wantSize: 20, wantShrink: true, wantHold: true,
		},
		{
			name: "a rejection of a request sent before the last cut is the same event", size: 1000, outstanding: 300,
			sentAtShrinks: func(w *byteWindow) uint64 { return w.shrinks - 1 },
			wantSize:      1000, wantShrink: false, wantHold: true,
		},
		{name: "a fixed window is not cut", size: 1000, outstanding: 300, fixed: true, wantSize: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start, cfg.fixed = tt.size, tt.fixed
			w := newByteWindow(cfg)
			w.shrinks = 1 // Some reduction has happened, so there is a before.

			sentAt := w.shrinks
			if tt.sentAtShrinks != nil {
				sentAt = tt.sentAtShrinks(w)
			}
			shrunk := w.shrinkWindowSize(tt.outstanding, sentAt)

			require.Equal(t, tt.wantShrink, shrunk)
			require.InDelta(t, tt.wantSize, w.size, 1e-9)
			require.Equal(t, tt.wantHold, w.pinnedFlushes > 0)
		})
	}
}

func TestByteWindowComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		size     float64
		hold     int
		bytes    int64
		fixed    bool
		wantSize float64
		wantHold int
	}{
		{name: "grows by one unit over the window for each byte", size: 100, bytes: 50, wantSize: 105},
		{name: "a bigger request grows it more", size: 100, bytes: 100, wantSize: 110},
		{name: "never past the ceiling", size: 999, bytes: 1000, wantSize: 1000},
		{name: "held after a rejection", size: 100, hold: 3, bytes: 50, wantSize: 100, wantHold: 2},
		{name: "a fixed window does not move", size: 100, bytes: 50, fixed: true, wantSize: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start, cfg.fixed = tt.size, tt.fixed
			w := newByteWindow(cfg)
			w.pinnedFlushes = tt.hold

			w.growWindowSize(tt.bytes)

			require.InDelta(t, tt.wantSize, w.size, 1e-9)
			require.Equal(t, tt.wantHold, w.pinnedFlushes)
		})
	}
}

func TestByteWindowHoldGrowth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hold     int
		fixed    bool
		wantHold int
	}{
		{name: "holds growth as a rejection does", wantHold: pinFlushes},
		{name: "does not shorten a longer hold", hold: pinFlushes + 2, wantHold: pinFlushes + 2},
		{name: "a fixed window is not held", fixed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start, cfg.fixed = 100, tt.fixed
			w := newByteWindow(cfg)
			w.pinnedFlushes = tt.hold

			w.pinWindowSize()

			require.Equal(t, tt.wantHold, w.pinnedFlushes)
			require.InDelta(t, 100, w.size, 1e-9, "holding growth never cuts")
		})
	}
}

// TestByteWindowRecoversSlowly: after a reduction, a window's worth of completed bytes buys
// back about one unit, so recovery takes many requests and does not undo the reduction at once.
func TestByteWindowRecoversSlowly(t *testing.T) {
	t.Parallel()

	cfg := testWindowConfig()
	cfg.start = 1000
	w := newByteWindow(cfg)

	w.shrinkWindowSize(400, w.shrinks)
	shrunkSize := w.size
	require.InDelta(t, 200, shrunkSize, 1e-9)

	for range pinFlushes {
		w.growWindowSize(50)
	}
	require.InDelta(t, shrunkSize, w.size, 1e-9, "held for a few completions")

	// 200 bytes is the window; four requests of 50 complete one window's worth.
	for range 4 {
		w.growWindowSize(50)
	}
	require.InDelta(t, shrunkSize+cfg.unit, w.size, 0.5, "a window's worth of traffic buys about one unit back")
	require.Less(t, w.size, 1000.0)
}

func TestByteWindowFraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  func(c *windowConfig)
		want float64
	}{
		{name: "wide open", cfg: func(*windowConfig) {}, want: 1},
		{name: "half", cfg: func(c *windowConfig) { c.start = 500 }, want: 0.5},
		{name: "no ceiling", cfg: func(c *windowConfig) { c.ceiling = 0; c.floor = 0; c.start = 0 }, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			tt.cfg(&cfg)

			require.InDelta(t, tt.want, newByteWindow(cfg).fraction(), 1e-9)
		})
	}
}

func TestRateTracker(t *testing.T) {
	t.Parallel()

	const floor = 1 << 20

	fast := flushAtThroughput(floor, 100e6)
	slow := flushAtThroughput(floor, 50e6)

	var best rateTracker
	best.floor = floor
	best.learn(fast)
	require.Positive(t, best.base)

	tests := []struct {
		name  string
		learn []flushResult
		// learned is set when the flushes leave a best rate, which is then the fast one.
		learned bool
	}{
		{name: "a timed flush sets the best", learn: []flushResult{fast}, learned: true},
		{name: "the best is kept when a slower flush follows", learn: []flushResult{fast, slow}, learned: true},
		{name: "a rejected flush is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond, served: true, rejected: true}}},
		{name: "a failed flush is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond, served: true, failed: true}}},
		{name: "a flush that indexed nothing is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond}}},
		{name: "a flush too small to tell is not learned", learn: []flushResult{flushAtThroughput(floor/4, 1e12)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := rateTracker{floor: floor}
			for _, f := range tt.learn {
				r.learn(f)
			}

			if !tt.learned {
				require.Zero(t, r.base)

				return
			}
			require.InEpsilon(t, best.base, r.base, 0.01)
		})
	}
}
