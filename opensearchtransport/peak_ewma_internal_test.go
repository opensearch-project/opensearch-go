// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPeakEWMA(t *testing.T) {
	t.Parallel()

	type step struct {
		advance time.Duration
		sample  time.Duration // 0 = no sample, only advance
	}
	tests := []struct {
		name   string
		steps  []step
		wantOK bool
		want   float64 // microseconds
	}{
		{name: "never sampled", wantOK: false},
		{
			name:   "first sample is stored",
			steps:  []step{{sample: 10 * time.Millisecond}},
			wantOK: true, want: 10000,
		},
		{
			name:   "peak replaces outright",
			steps:  []step{{sample: 10 * time.Millisecond}, {sample: 50 * time.Millisecond}},
			wantOK: true, want: 50000,
		},
		{
			name: "lower sample blends with time weight",
			steps: []step{
				{sample: 100 * time.Millisecond},
				{advance: peakEWMADecay, sample: 10 * time.Millisecond},
			},
			// The stored value is decayed once: v*w + d*(1-w) with w = 1/e.
			wantOK: true, want: 100000/math.E + 10000*(1-1/math.E),
		},
		{
			name: "short gap decays the stored value once",
			steps: []step{
				{sample: 1000 * time.Microsecond},
				{advance: time.Second, sample: 500 * time.Microsecond},
			},
			// w = e^(-1s/10s); decaying twice would give 1000*w*w + 500*(1-w).
			wantOK: true, want: 1000*math.Exp(-0.1) + 500*(1-math.Exp(-0.1)),
		},
		{
			name:   "decays with no samples",
			steps:  []step{{sample: 100 * time.Millisecond}, {advance: peakEWMADecay}},
			wantOK: true, want: 100000 / math.E,
		},
		{
			name:   "sample above decayed value replaces it",
			steps:  []step{{sample: 100 * time.Millisecond}, {advance: peakEWMADecay, sample: 40 * time.Millisecond}},
			wantOK: true, want: 40000 * 1, // 100000/e = 36788 < 40000
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := newTestClock()
			var p peakEWMA
			p.clock = clk
			for _, s := range tt.steps {
				clk.Advance(s.advance)
				if s.sample > 0 {
					p.sample(s.sample)
				}
			}
			got, ok := p.load()
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.InEpsilon(t, tt.want, got, 1e-9)
			}
		})
	}
}

func TestPeakEWMAConcurrent(t *testing.T) {
	t.Parallel()
	var p peakEWMA
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 1000 {
				p.sample(time.Duration(i+1) * time.Millisecond)
				p.load()
			}
		})
	}
	wg.Wait()
	got, ok := p.load()
	require.True(t, ok)
	require.Greater(t, got, 0.0)
}
