// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestConnScoreSelectBreaksTiesUniformly checks that connScoreSelect picks
// among candidates tied at the lowest score with equal chance, rather than
// always returning the first of them, and never returns a worse candidate.
func TestConnScoreSelectBreaksTiesUniformly(t *testing.T) {
	t.Parallel()

	const draws = 6000

	tests := []struct {
		name string
		// rtts is each candidate's RTT; equal RTTs and equal idle pools tie.
		rtts []time.Duration
		// tied are the indexes of the candidates tied at the lowest score.
		tied []int
	}{
		{
			name: "all tied",
			rtts: []time.Duration{300 * time.Microsecond, 300 * time.Microsecond, 300 * time.Microsecond},
			tied: []int{0, 1, 2},
		},
		{
			name: "tied candidates apart, a worse one between them",
			rtts: []time.Duration{300 * time.Microsecond, 5 * time.Millisecond, 300 * time.Microsecond},
			tied: []int{0, 2},
		},
		{
			name: "a single best candidate",
			rtts: []time.Duration{5 * time.Millisecond, 300 * time.Microsecond, 5 * time.Millisecond},
			tied: []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := make([]*Connection, len(tt.rtts))
			for i, rtt := range tt.rtts {
				name := fmt.Sprintf("n%d", i)
				u := &url.URL{Scheme: "https", Host: name + ":9200"}
				c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
				c.setLifecycleBit(lcActive)
				for range 4 {
					c.rttRing.add(rtt)
				}
				conns[i] = c
			}

			counts := make(map[int]int, len(conns))
			for range draws {
				scores := acquireFloats(len(conns))
				best := connScoreSelect(conns, nil, nil, &shardCostForReads, "", true, scores.Slice(), nil, nil, 0, nil)
				scores.Release()
				require.NotNil(t, best)
				for i, c := range conns {
					if c == best {
						counts[i]++
					}
				}
			}

			share := draws / len(tt.tied)
			total := 0
			for _, i := range tt.tied {
				require.Greater(t, counts[i], share*3/4, "tied candidate %d under-chosen: %v", i, counts)
				require.Less(t, counts[i], share*5/4, "tied candidate %d over-chosen: %v", i, counts)
				total += counts[i]
			}
			require.Equal(t, draws, total, "a worse candidate was chosen: %v", counts)
		})
	}
}

// TestRoutingListTieFavorsCoverage checks that, for candidates from a shard
// set tied at the lowest score, each wins in proportion to the target shards
// it hosts (totalShards - extraCost), and that a candidate scoring strictly
// better still wins every time.
func TestRoutingListTieFavorsCoverage(t *testing.T) {
	t.Parallel()

	const draws = 9000

	tests := []struct {
		name        string
		rtts        []time.Duration
		extra       []float64
		totalShards int
		// wantShare is each candidate's expected share of the picks.
		wantShare []float64
	}{
		{
			// A bucket nearer with one of two shards scores the same as a node
			// hosting both: weights 1 and 2.
			name:        "nearer node with half the shards",
			rtts:        []time.Duration{200 * time.Microsecond, time.Millisecond},
			extra:       []float64{1, 0},
			totalShards: 2,
			wantShare:   []float64{1.0 / 3, 2.0 / 3},
		},
		{
			// node0 (2 ms, bucket 10) hosts 3 of 4; node1 (200 us, bucket 8)
			// hosts 1 of 4. Both score 11, so the weights are 3 and 1.
			name:        "three shards against one, two buckets apart",
			rtts:        []time.Duration{2 * time.Millisecond, 200 * time.Microsecond},
			extra:       []float64{1, 3},
			totalShards: 4,
			wantShare:   []float64{3.0 / 4, 1.0 / 4},
		},
		{
			// node0 is strictly better (same RTT, fewer missing shards), so the
			// coverage weight never applies.
			name:        "strictly better score always wins",
			rtts:        []time.Duration{300 * time.Microsecond, 300 * time.Microsecond},
			extra:       []float64{0, 1},
			totalShards: 2,
			wantShare:   []float64{1, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := make([]*Connection, len(tt.rtts))
			for i, rtt := range tt.rtts {
				name := fmt.Sprintf("n%d", i)
				u := &url.URL{Scheme: "https", Host: name + ":9200"}
				c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
				c.setLifecycleBit(lcActive)
				for range 4 {
					c.rttRing.add(rtt)
				}
				conns[i] = c
			}

			counts := make([]int, len(conns))
			for range draws {
				scores := acquireFloats(len(conns))
				// No shard placement on the slot: every node costs costUnknown,
				// so the score is RTT bucket plus extraCost.
				best := connScoreSelect(conns, &indexSlot{clock: realClock{}}, nil, &shardCostForReads, "", false,
					scores.Slice(), nil, tt.extra, tt.totalShards, nil)
				scores.Release()
				for i, c := range conns {
					if c == best {
						counts[i]++
					}
				}
			}

			for i, want := range tt.wantShare {
				got := float64(counts[i]) / draws
				require.InDelta(t, want, got, 0.04, "candidate %d share: counts %v", i, counts)
			}
		})
	}
}
