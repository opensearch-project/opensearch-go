// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// testWindowConfig is a window of up to 1000 units, one request of 10, backing off by half.
func testWindowConfig() WindowConfig {
	return WindowConfig{Start: 1000, Ceiling: 1000, Floor: 20, Increment: 10, Backoff: 0.5}
}

// startAt starts cfg's window at size and, when fixed, holds it there by making
// size its floor and its ceiling.
func startAt(cfg *WindowConfig, size float64, fixed bool) {
	cfg.Start = size
	if fixed {
		cfg.Floor, cfg.Ceiling = size, size
	}
}

func TestWindowStart(t *testing.T) {
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
			cfg.Start = tt.start

			require.InDelta(t, tt.want, NewWindow(cfg).Size(), 1e-9)
		})
	}
}

func TestWindowAllows(t *testing.T) {
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
			cfg.Start = tt.size
			cfg.Floor = 1
			w := NewWindow(cfg)

			require.Equal(t, tt.wantAllowed, w.CanWrite(tt.outstanding, tt.request))
		})
	}
}

func TestWindowShrink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		size             float64
		outstanding      int64
		fixed            bool
		reductionsAtSend func(w *Window) uint64
		wantSize         float64
		wantShrink       bool
		wantPin          bool
	}{
		{
			name: "half of what was out, when that is less than the window", size: 1000, outstanding: 300,
			wantSize: 150, wantShrink: true, wantPin: true,
		},
		{
			name: "half of the window, when that is less than what was out", size: 80, outstanding: 300,
			wantSize: 40, wantShrink: true, wantPin: true,
		},
		{
			name: "never below the floor", size: 1000, outstanding: 10,
			wantSize: 20, wantShrink: true, wantPin: true,
		},
		{
			name: "a rejection of a request sent before the last cut is the same event", size: 1000, outstanding: 300,
			reductionsAtSend: func(w *Window) uint64 { return w.reductions - 1 },
			wantSize:         1000, wantShrink: false, wantPin: true,
		},
		{name: "a fixed window is not cut", size: 1000, outstanding: 300, fixed: true, wantSize: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			startAt(&cfg, tt.size, tt.fixed)
			w := NewWindow(cfg)
			w.reductions = 1 // Some reduction has happened, so there is a before.

			atSend := w.NumReductions()
			if tt.reductionsAtSend != nil {
				atSend = tt.reductionsAtSend(w)
			}
			shrunk := w.Shrink(tt.outstanding, atSend)

			require.Equal(t, tt.wantShrink, shrunk)
			require.InDelta(t, tt.wantSize, w.Size(), 1e-9)
			require.Equal(t, tt.wantPin, w.pinned > 0)
		})
	}
}

func TestWindowGrow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		size     float64
		pinned   int
		units    int64
		fixed    bool
		wantSize float64
		wantPin  int
	}{
		{name: "grows by one unit over the window for each unit", size: 100, units: 50, wantSize: 105},
		{name: "a bigger request grows it more", size: 100, units: 100, wantSize: 110},
		{name: "never past the ceiling", size: 999, units: 1000, wantSize: 1000},
		{name: "held after a rejection", size: 100, pinned: 3, units: 50, wantSize: 100, wantPin: 2},
		{name: "a fixed window does not move", size: 100, units: 50, fixed: true, wantSize: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			startAt(&cfg, tt.size, tt.fixed)
			w := NewWindow(cfg)
			w.pinned = tt.pinned

			w.Grow(tt.units)

			require.InDelta(t, tt.wantSize, w.Size(), 1e-9)
			require.Equal(t, tt.wantPin, w.pinned)
		})
	}
}

func TestWindowPin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pinned  int
		fixed   bool
		wantPin int
	}{
		{name: "holds growth as a rejection does", wantPin: PinCompletions},
		{name: "does not shorten a longer hold", pinned: PinCompletions + 2, wantPin: PinCompletions + 2},
		{name: "a fixed window is not held", fixed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			startAt(&cfg, 100, tt.fixed)
			w := NewWindow(cfg)
			w.pinned = tt.pinned

			w.Pin()

			require.Equal(t, tt.wantPin, w.pinned)
			require.InDelta(t, 100, w.Size(), 1e-9, "holding growth never cuts")
		})
	}
}

// TestWindowRecoversSlowly: after a reduction, a window's worth of completed units buys
// back about one unit, so recovery takes many requests and does not undo the reduction at once.
func TestWindowRecoversSlowly(t *testing.T) {
	t.Parallel()

	cfg := testWindowConfig()
	cfg.Start = 1000
	w := NewWindow(cfg)

	w.Shrink(400, w.NumReductions())
	shrunkSize := w.Size()
	require.InDelta(t, 200, shrunkSize, 1e-9)

	for range PinCompletions {
		w.Grow(50)
	}
	require.InDelta(t, shrunkSize, w.Size(), 1e-9, "held for a few completions")

	// 200 units is the window; four requests of 50 complete one window's worth.
	for range 4 {
		w.Grow(50)
	}
	require.InDelta(t, shrunkSize+cfg.Increment, w.Size(), 0.5, "a window's worth of traffic grows it by about one increment")
	require.Less(t, w.Size(), 1000.0)
}

func TestWindowSizeRatio(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  func(c *WindowConfig)
		want float64
	}{
		{name: "wide open", cfg: func(*WindowConfig) {}, want: 1},
		{name: "half", cfg: func(c *WindowConfig) { c.Start = 500 }, want: 0.5},
		{name: "no ceiling", cfg: func(c *WindowConfig) { c.Ceiling = 0; c.Floor = 0; c.Start = 0 }, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			tt.cfg(&cfg)

			require.InDelta(t, tt.want, NewWindow(cfg).SizeRatio(), 1e-9)
		})
	}
}
