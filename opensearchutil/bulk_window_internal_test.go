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
		name        string
		size        float64
		outstanding int64
		fixed       bool
		sentAtCuts  func(w *byteWindow) uint64
		wantSize    float64
		wantCut     bool
		wantHold    bool
	}{
		{
			name: "half of what was out, when that is less than the window", size: 1000, outstanding: 300,
			wantSize: 150, wantCut: true, wantHold: true,
		},
		{
			name: "half of the window, when that is less than what was out", size: 80, outstanding: 300,
			wantSize: 40, wantCut: true, wantHold: true,
		},
		{
			name: "never below the floor", size: 1000, outstanding: 10,
			wantSize: 20, wantCut: true, wantHold: true,
		},
		{
			name: "a rejection of a request sent before the last cut is the same event", size: 1000, outstanding: 300,
			sentAtCuts: func(w *byteWindow) uint64 { return w.cuts - 1 },
			wantSize:   1000, wantCut: false, wantHold: true,
		},
		{name: "a fixed window is not cut", size: 1000, outstanding: 300, fixed: true, wantSize: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start, cfg.fixed = tt.size, tt.fixed
			w := newByteWindow(cfg)
			w.cuts = 1 // Some cut has happened, so there is a before.

			sentAt := w.cuts
			if tt.sentAtCuts != nil {
				sentAt = tt.sentAtCuts(w)
			}
			cut := w.reject(tt.outstanding, sentAt)

			require.Equal(t, tt.wantCut, cut)
			require.InDelta(t, tt.wantSize, w.size, 1e-9)
			require.Equal(t, tt.wantHold, w.hold > 0)
		})
	}
}

func TestByteWindowComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		size       float64
		hold       int
		bytes      int64
		overloaded bool
		fixed      bool
		wantSize   float64
		wantHold   int
	}{
		{name: "grows by one unit over the window for each byte", size: 100, bytes: 50, wantSize: 105},
		{name: "a bigger request grows it more", size: 100, bytes: 100, wantSize: 110},
		{name: "never past the ceiling", size: 999, bytes: 1000, wantSize: 1000},
		{name: "held after a rejection", size: 100, hold: 3, bytes: 50, wantSize: 100, wantHold: 2},
		{name: "not while the node's write pool is overloaded", size: 100, bytes: 50, overloaded: true, wantSize: 100},
		{name: "a fixed window does not move", size: 100, bytes: 50, fixed: true, wantSize: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			cfg.start, cfg.fixed = tt.size, tt.fixed
			w := newByteWindow(cfg)
			w.hold = tt.hold

			w.complete(tt.bytes, tt.overloaded)

			require.InDelta(t, tt.wantSize, w.size, 1e-9)
			require.Equal(t, tt.wantHold, w.hold)
		})
	}
}

// TestByteWindowRecoversSlowly: after a cut, a window's worth of completed bytes buys
// back about one unit, so recovery takes many requests and does not undo the cut at once.
func TestByteWindowRecoversSlowly(t *testing.T) {
	t.Parallel()

	cfg := testWindowConfig()
	cfg.start = 1000
	w := newByteWindow(cfg)

	w.reject(400, w.cuts)
	cut := w.size
	require.InDelta(t, 200, cut, 1e-9)

	for range sizingHoldFlushes {
		w.complete(50, false)
	}
	require.InDelta(t, cut, w.size, 1e-9, "held for a few completions")

	// 200 bytes is the window; four requests of 50 complete one window's worth.
	for range 4 {
		w.complete(50, false)
	}
	require.InDelta(t, cut+cfg.unit, w.size, 0.5, "a window's worth of traffic buys about one unit back")
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

	fast := flushOf(floor, 100e6)
	slow := flushOf(floor, 50e6)

	tests := []struct {
		name string
		// learn is folded in first, in order.
		learn []flushSample
		// probe is measured against what was learned.
		probe        flushSample
		wantKnown    bool
		wantSlowdown float64
	}{
		{name: "nothing learned yet, so nothing to measure against", probe: fast},
		{name: "at its best", learn: []flushSample{fast}, probe: fast, wantKnown: true, wantSlowdown: 0},
		{name: "twice as long for the same bytes", learn: []flushSample{fast}, probe: slow, wantKnown: true, wantSlowdown: 1},
		{
			name: "the best is kept when a slower flush follows", learn: []flushSample{fast, slow},
			probe: slow, wantKnown: true, wantSlowdown: 1,
		},
		{
			name: "a rejected flush is not learned", learn: []flushSample{{bytes: floor, latency: time.Microsecond, rejected: true}},
			probe: fast,
		},
		{
			name: "a failed flush is not learned", learn: []flushSample{{bytes: floor, latency: time.Microsecond, failed: true}},
			probe: fast,
		},
		{name: "a flush too small to tell is not learned", learn: []flushSample{flushOf(floor/4, 1e12)}, probe: fast},
		{
			name: "a rejected flush is not measured", learn: []flushSample{fast},
			probe: flushSample{bytes: floor, latency: time.Second, rejected: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := rateTracker{floor: floor}
			for _, f := range tt.learn {
				r.learn(f)
			}

			got, ok := r.slowdown(tt.probe)
			require.Equal(t, tt.wantKnown, ok)
			require.InDelta(t, tt.wantSlowdown, got, 0.01)
		})
	}
}
