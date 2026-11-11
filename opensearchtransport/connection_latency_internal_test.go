// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConnectionLoadLatencyMicros(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rtt     time.Duration // health-check RTT; 0 = none
		samples map[string]time.Duration
		pool    string
		want    float64
	}{
		{name: "no rtt and no sample is the unknown sentinel", pool: poolSearch, want: float64(rttBucketUnknown)},
		{name: "no sample falls back to RTT median", rtt: 1024 * time.Microsecond, pool: poolSearch, want: 1024},
		{
			name: "sample wins over RTT", rtt: time.Millisecond,
			samples: map[string]time.Duration{poolSearch: 7 * time.Millisecond},
			pool:    poolSearch, want: 7000,
		},
		{
			name: "pools are isolated", rtt: 1024 * time.Microsecond,
			samples: map[string]time.Duration{poolWrite: 2 * time.Second},
			pool:    poolSearch, want: 1024,
		},
		{
			name:    "sample without any RTT",
			samples: map[string]time.Duration{poolSearch: 3 * time.Millisecond},
			pool:    poolSearch, want: 3000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conn := &Connection{rttRing: newRTTRing(4)}
			if tt.rtt > 0 {
				conn.rttRing.add(tt.rtt)
			}
			conn.pools.getOrCreate(poolSearch)
			conn.pools.getOrCreate(poolWrite)
			clk := newTestClock() // never advanced, so samples do not decay
			conn.pools.getForScoring(poolSearch).latency.clock = clk
			conn.pools.getForScoring(poolWrite).latency.clock = clk
			for pool, d := range tt.samples {
				conn.recordLatency(pool, d)
			}
			require.InEpsilon(t, tt.want, conn.loadLatencyMicros(tt.pool), 1e-9)
		})
	}
}

func TestConnectionLoadLatencyMicrosFlooredAtRTT(t *testing.T) {
	t.Parallel()
	clk := newTestClock()
	conn := &Connection{rttRing: newRTTRing(4)}
	conn.rttRing.add(512 * time.Microsecond)
	conn.pools.getOrCreate(poolSearch)
	conn.pools.getForScoring(poolSearch).latency.clock = clk
	conn.recordLatency(poolSearch, 5*time.Millisecond)
	clk.Advance(120 * time.Second)
	require.InDelta(t, 512.0, conn.loadLatencyMicros(poolSearch), 0.001)
}

func TestConnLoadScoreUsesLatency(t *testing.T) {
	t.Parallel()

	clk := newTestClock() // never advanced, so samples do not decay
	newConn := func(sample time.Duration) *Connection {
		conn := &Connection{rttRing: newRTTRing(4)}
		conn.rttRing.add(300 * time.Microsecond) // one bucket for both nodes
		conn.pools.getOrCreate(poolSearch)
		conn.pools.getForScoring(poolSearch).latency.clock = clk
		if sample > 0 {
			conn.recordLatency(poolSearch, sample)
		}
		return conn
	}
	fast := newConn(10 * time.Millisecond)
	slow := newConn(19 * time.Millisecond)
	require.Equal(t, fast.rttRing.medianBucket(), slow.rttRing.medianBucket())

	fastScore := calcConnDefaultScore(fast, 1, poolSearch, true, c3Inputs{})
	slowScore := calcConnDefaultScore(slow, 1, poolSearch, true, c3Inputs{})
	require.InEpsilon(t, 1.9, slowScore/fastScore, 1e-9)

	// A node with no request sample scores from its RTT bucket's lower bound.
	unsampled := newConn(0)
	require.InEpsilon(t, 256.0/10000.0, calcConnDefaultScore(unsampled, 1, poolSearch, true, c3Inputs{})/fastScore, 1e-9)
}
