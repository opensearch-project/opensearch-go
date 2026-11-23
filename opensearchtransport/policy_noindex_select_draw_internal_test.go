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

// TestDrawNoIndexPair covers near tiers of zero, one, two and four nodes: the
// pair holds up to two distinct nodes of the tier, and with more than two
// nodes every node is drawn into some pair.
func TestDrawNoIndexPair(t *testing.T) {
	t.Parallel()

	const draws = 400

	tests := []struct {
		name string
		tier int
		// wantB reports whether the pair has a second node.
		wantB bool
	}{
		{name: "empty tier"},
		{name: "one node", tier: 1},
		{name: "two nodes", tier: 2, wantB: true},
		{name: "four nodes", tier: 4, wantB: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tier := spreadConns(t, tt.tier, 0, 4)
			drawn := make(map[string]int)
			for range draws {
				a, b := drawNoIndexPair(tier)
				if tt.tier == 0 {
					require.Nil(t, a)
					require.Nil(t, b)
					continue
				}
				require.NotNil(t, a)
				drawn[a.ID]++
				if !tt.wantB {
					require.Nil(t, b)
					continue
				}
				require.NotNil(t, b)
				require.NotSame(t, a, b, "pair drew %q twice", a.ID)
				drawn[b.ID]++
			}
			for _, c := range tier {
				require.Positive(t, drawn[c.ID], "node %q never drawn: %v", c.ID, drawn)
			}
		})
	}
}

// TestNoIndexScore checks that the score is the default score with no shard
// data, or the custom score function's, given costUnknown.
func TestNoIndexScore(t *testing.T) {
	t.Parallel()

	const custom = 7.0

	tests := []struct {
		name      string
		scoreFunc connScoreFunc
		// want is the score, or 0 for the default score with no shard data.
		want float64
	}{
		{name: "default score"},
		{
			name: "custom score function gets costUnknown",
			scoreFunc: func(_ *Connection, shardCost, primaryPct float64, poolName string, poolInfoReady bool) float64 {
				if shardCost != costUnknown || primaryPct != 0 || poolName != poolWrite || !poolInfoReady {
					return math.NaN()
				}
				return custom
			},
			want: custom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := spreadConns(t, 1, 0, 8)[0]
			want := tt.want
			if want == 0 {
				want = calcConnDefaultScore(conn, costUnknown, poolWrite, true)
			}
			require.InDelta(t, want, noIndexScore(conn, poolWrite, true, tt.scoreFunc), 1e-9)
		})
	}
}

// TestSelectNoIndexWarmup covers a warming node taking or declining its turn:
// one that declines hands the choice to the full ranking, which still returns
// it when it is the only node.
func TestSelectNoIndexWarmup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		skips int
	}{
		{name: "warming node accepts its turn", skips: 0},
		{name: "only drawn node declines while warming up", skips: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := spreadConns(t, 1, 0, 8)
			startWarmingUp(t, conns[0], tt.skips)

			c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, nil)
			require.NotNil(t, c)
			require.Equal(t, "near-00", c.ID)
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
