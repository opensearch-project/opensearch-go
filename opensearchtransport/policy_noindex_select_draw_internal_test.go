// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDrawNoIndexPair covers near tiers of one and two nodes, which the pair is
// drawn from without random retries, and larger tiers that the request has
// mostly tried. tried lists the indices of the tier nodes the request tried in
// this pass; want lists every node the pair may hold, nil for none.
func TestDrawNoIndexPair(t *testing.T) {
	t.Parallel()

	const draws = 200

	tests := []struct {
		name  string
		tier  int
		tried []int
		// wantA and wantB are the nodes each side of the pair may be, nil
		// meaning the side must be empty.
		wantA []string
		wantB []string
		// bMayBeEmpty allows an empty second side: when the random draws all
		// land on tried nodes, the pair falls back to a single untried node.
		bMayBeEmpty bool
	}{
		{name: "empty tier"},
		{name: "one untried node", tier: 1, wantA: []string{"near-00"}},
		{name: "one tried node", tier: 1, tried: []int{0}},
		{name: "two untried nodes", tier: 2, wantA: []string{"near-00", "near-01"}, wantB: []string{"near-00", "near-01"}},
		{name: "two nodes, first tried", tier: 2, tried: []int{0}, wantA: []string{"near-01"}},
		{name: "two nodes, second tried", tier: 2, tried: []int{1}, wantA: []string{"near-00"}},
		{name: "two nodes, both tried", tier: 2, tried: []int{0, 1}},
		{name: "four nodes, three tried", tier: 4, tried: []int{0, 1, 2}, wantA: []string{"near-03"}},
		{
			name:        "four nodes, two tried",
			tier:        4,
			tried:       []int{0, 1},
			wantA:       []string{"near-02", "near-03"},
			wantB:       []string{"near-02", "near-03"},
			bMayBeEmpty: true,
		},
		{name: "four nodes, all tried", tier: 4, tried: []int{0, 1, 2, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tier := spreadConns(t, tt.tier, 0, 4)
			att := &routeAttempt{}
			for _, i := range tt.tried {
				att.record(tier[i])
			}

			for range draws {
				a, b := drawNoIndexPair(tier, att)
				requireDrawn(t, tt.wantA, a)
				if b == nil && tt.bMayBeEmpty {
					continue
				}
				requireDrawn(t, tt.wantB, b)
				if a != nil && b != nil {
					require.NotSame(t, a, b, "pair drew %q twice", a.ID)
				}
			}
		})
	}
}

// requireDrawn checks that c is one of the nodes in want, or nil when want is
// empty.
func requireDrawn(t *testing.T, want []string, c *Connection) {
	t.Helper()
	if len(want) == 0 {
		require.Nil(t, c)
		return
	}
	require.NotNil(t, c)
	require.Contains(t, want, c.ID)
}

// TestNoIndexScore covers the custom score function and how earlier tries of a
// node raise or rule out its score. want is a multiple of the node's idle
// default score with no shard data.
func TestNoIndexScore(t *testing.T) {
	t.Parallel()

	const custom = 7.0

	tests := []struct {
		name      string
		scoreFunc connScoreFunc
		// tries is how many earlier passes tried the node; inPass also records
		// a try in the current pass.
		tries  int
		inPass bool
		// want is the score as a multiple of the idle default score, or the
		// score itself when exact is set.
		want  float64
		exact bool
	}{
		{name: "default score", want: 1},
		{
			name: "custom score function gets costUnknown",
			scoreFunc: func(_ *Connection, shardCost, primaryPct float64, poolName string, poolInfoReady bool) float64 {
				if shardCost != costUnknown || primaryPct != 0 || poolName != poolWrite || !poolInfoReady {
					return math.NaN()
				}
				return custom
			},
			want:  custom,
			exact: true,
		},
		{name: "tried twice in earlier passes", tries: 2, want: 3},
		{name: "tried in this pass", tries: 1, inPass: true, want: math.Inf(1), exact: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := spreadConns(t, 1, 0, 8)[0]
			var att *routeAttempt
			if tt.tries > 0 || tt.inPass {
				att = &routeAttempt{}
				for range tt.tries {
					att.record(conn)
				}
				att.newPass()
				if tt.inPass {
					att.record(conn)
				}
			}

			want := tt.want
			if !tt.exact {
				want *= calcConnDefaultScore(conn, costUnknown, poolWrite, true)
			}
			got := noIndexScore(conn, poolWrite, true, tt.scoreFunc, att)
			if math.IsInf(want, 1) {
				require.True(t, math.IsInf(got, 1), "score %v, want +Inf", got)
				return
			}
			require.InDelta(t, want, got, 1e-9)
		})
	}
}

// TestSelectNoIndexWarmupAndFallback covers a warming node taking or declining
// its turn, and the two ways [selectNoIndex] hands the choice to the full
// ranking: when it can draw no node, and when every drawn node is warming up
// and declines its turn.
func TestSelectNoIndexWarmupAndFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		near  int
		far   int
		setup func(t *testing.T, conns []*Connection) *routeAttempt
		// want lists the nodes the selection may return.
		want []string
	}{
		{
			// The only node is warming up with no skips left in its round, so
			// it accepts the draw.
			name: "warming node accepts its turn",
			near: 1,
			setup: func(t *testing.T, conns []*Connection) *routeAttempt {
				t.Helper()
				startWarmingUp(t, conns[0], 0)
				return nil
			},
			want: []string{"near-00"},
		},
		{
			// Every node was tried in this pass, so nothing can be drawn. The
			// ranking starts a new pass over all of them and returns the
			// lowest score, an idle near node.
			name: "every node tried in this pass",
			near: 2,
			far:  2,
			setup: func(_ *testing.T, conns []*Connection) *routeAttempt {
				att := &routeAttempt{}
				for _, c := range conns {
					att.record(c)
				}
				return att
			},
			want: []string{"near-00", "near-01"},
		},
		{
			// The only node is warming up with skips left, so it declines the
			// draw, and the ranking returns it anyway.
			name: "only drawn node declines while warming up",
			near: 1,
			setup: func(t *testing.T, conns []*Connection) *routeAttempt {
				t.Helper()
				startWarmingUp(t, conns[0], 8)
				return nil
			},
			want: []string{"near-00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := spreadConns(t, tt.near, tt.far, 8)
			sortConnectionsByRTT(conns)
			att := tt.setup(t, conns)

			c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, nil, att)
			require.NotNil(t, c)
			require.Contains(t, tt.want, c.ID)
		})
	}
}

// startWarmingUp puts conn into warmup whose first round has the given number
// of skips, so that [Connection.tryWarmupSkip] declines that many turns before
// it accepts one.
func startWarmingUp(t *testing.T, conn *Connection, skips int) {
	t.Helper()
	conn.setLifecycleBit(lcActive | lcNeedsWarmup)
	conn.startWarmup(8, skips)
	require.True(t, conn.loadConnState().isWarmingUp())
}
