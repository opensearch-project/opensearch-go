// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const c3TestServiceNs = int64(5 * time.Millisecond)

// noStats marks a test connection that never received adaptive_selection stats.
const noStats = int64(-1)

// c3TestConn returns a connection with a 500us RTT, the given search in-flight
// count and, when queue is not [noStats], adaptive_selection stats recorded long enough ago
// to be past the blackout, from a report on c3TestClients nodes.
func c3TestConn(t *testing.T, id string, inFlight int32, queue int64, recordedAgo time.Duration, serviceNs int64) *Connection {
	t.Helper()
	c := scoreTestConn(t, id, 500*time.Microsecond, 0)
	c.setLifecycleBit(lcActive)
	c.pools.getForScoring(poolSearch).inFlight.Store(inFlight)
	c.pools.getForScoring(poolSearch).cwnd.Store(100)
	if queue != noStats {
		recordUsable(&c.ars, time.Now().Add(-recordedAgo), id, id, c3TestClients,
			AdaptiveSelectionStats{AvgQueueSize: queue, AvgServiceTimeNs: serviceNs})
	}
	return c
}

// c3TestClients is the report size, the server's clientNum, behind c3TestConn's stats.
const c3TestClients = 3

func TestC3ScoreAvoidsQueuedNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		withStats    bool
		wantMaxShare float64 // share of picks that node "a" must stay under; 0 is unchecked
		wantMinShare float64 // share of picks that node "a" must exceed; 0 is unchecked
	}{
		{name: "fresh stats steer away from the queued node", withStats: true, wantMaxShare: 0.02},
		{name: "no stats spreads across all nodes", withStats: false, wantMinShare: 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ago := 11 * time.Second
			queue := func(q int64) int64 {
				if !tt.withStats {
					return noStats
				}
				return q
			}
			conns := []*Connection{
				c3TestConn(t, "a", 2, queue(20), ago, c3TestServiceNs),
				c3TestConn(t, "b", 2, queue(0), ago, c3TestServiceNs),
				c3TestConn(t, "c", 2, queue(0), ago, c3TestServiceNs),
			}
			rng := rand.New(rand.NewPCG(1, 2))
			scores := acquireFloats(len(conns))
			defer scores.Release()

			const n = 3000
			counts := map[string]int{}
			for range n {
				best := connScoreSelect(conns, nil, nil, &shardCostForReads, poolSearch, true, scores.Slice(), nil, nil, nil, 0.25, rng.Float64)
				counts[best.ID]++
			}

			// Utilization stays far from cwnd, so only the queue term can steer.
			require.Less(t, conns[0].loadInFlight(poolSearch), conns[0].loadCwnd(poolSearch, true)/2)
			share := float64(counts["a"]) / n
			if tt.wantMaxShare != 0 {
				require.Less(t, share, tt.wantMaxShare)
			}
			if tt.wantMinShare != 0 {
				require.Greater(t, share, tt.wantMinShare)
			}
		})
	}
}

func TestC3ScoreFallsBackToLinear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		pool             string
		ago              time.Duration // negative: never recorded
		serviceNs        int64
		peerWithoutStats bool
	}{
		{name: "blackout", pool: poolSearch, ago: time.Second, serviceNs: c3TestServiceNs},
		{name: "expired", pool: poolSearch, ago: 4 * time.Minute, serviceNs: c3TestServiceNs},
		{name: "never recorded", pool: poolSearch, ago: -1, serviceNs: c3TestServiceNs},
		{name: "one candidate without stats", pool: poolSearch, ago: 11 * time.Second, serviceNs: c3TestServiceNs, peerWithoutStats: true},
		{name: "write pool", pool: poolWrite, ago: 11 * time.Second, serviceNs: c3TestServiceNs},
		{name: "zero service time", pool: poolSearch, ago: 11 * time.Second, serviceNs: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queue := int64(20)
			if tt.ago < 0 {
				queue = noStats
			}
			conn := c3TestConn(t, "a", 2, queue, tt.ago, tt.serviceNs)
			conn.pools.getForScoring(tt.pool).inFlight.Store(2)
			peer := c3TestConn(t, "b", 2, noStats, 0, 0)
			if !tt.peerWithoutStats {
				peer = c3TestConn(t, "b", 2, queue, tt.ago, tt.serviceNs)
			}
			cands := []*Connection{conn, peer}

			c3 := newC3Inputs(cands, tt.pool)
			require.Equal(t, c3Inputs{}, c3)
			want := conn.loadLatencyMicros(tt.pool) * (2 + 1) / float64(conn.loadCwnd(tt.pool, true))
			require.InDelta(t, want, connLoadScore(conn, tt.pool, true, c3), 1e-9)
		})
	}
}

func TestC3LoadScoreFormula(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		inFlight int32
		queue    int64
		n        int // size of the report the stats came from
	}{
		{name: "idle", inFlight: 0, queue: 0, n: 3},
		{name: "in flight, no queue", inFlight: 2, queue: 0, n: 3},
		{name: "in flight and queue", inFlight: 2, queue: 4, n: 3},
		{name: "n is the report size, not the one candidate", inFlight: 2, queue: 4, n: 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := c3TestConn(t, "a", tt.inFlight, noStats, 0, 0)
			recordUsable(&conn.ars, time.Now().Add(-11*time.Second), "a", "a", tt.n,
				AdaptiveSelectionStats{AvgQueueSize: tt.queue, AvgServiceTimeNs: c3TestServiceNs})
			c3 := newC3Inputs([]*Connection{conn}, poolSearch)
			require.True(t, c3.active)

			r := conn.loadLatencyMicros(poolSearch)
			s := float64(c3TestServiceNs) / 1000
			qHat := 1 + float64(tt.inFlight)*float64(tt.n) + float64(tt.queue)
			want := r - s + qHat*qHat*qHat*s

			require.InDelta(t, want, connLoadScore(conn, poolSearch, true, c3), want*1e-9)
		})
	}
}
