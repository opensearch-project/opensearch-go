// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// noIndexEvaluator is the router surface the spread test drives: a policy whose
// no-index branch selects from its RTT-sorted connection list.
type noIndexEvaluator interface {
	Eval(ctx context.Context, req *http.Request) (NextHop, error)
}

// newNoIndexPoolRouter builds a poolRouter over conns, as the default router
// wraps the ingest pool for POST /_bulk.
func newNoIndexPoolRouter(t *testing.T, conns []*Connection) noIndexEvaluator {
	t.Helper()
	inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, enabled: true}
	r := wrapWithRouter(inner, testIndexSlotCache(), &shardCostForWrites, poolWrite, nil).(*poolRouter)
	sorted := append([]*Connection(nil), conns...)
	sortConnectionsByRTT(sorted)
	r.mu.sortedConns = sorted
	return r
}

// newNoIndexIndexRouter builds a standalone IndexRouter over conns.
func newNoIndexIndexRouter(t *testing.T, conns []*Connection) noIndexEvaluator {
	t.Helper()
	r := newIndexRouter(indexSlotCacheConfig{minFanOut: 1, maxFanOut: 32, fanOutPerReq: defaultFanOutPerRequest})
	r.shardCosts = &shardCostForWrites
	r.poolName = poolWrite
	require.NoError(t, r.DiscoveryUpdate(conns, nil, nil))
	return r
}

// spreadConns returns near nodes sharing one RTT tier followed by far nodes in
// a slower tier, each with an equal write-pool cwnd.
func spreadConns(t *testing.T, near, far, cwnd int) []*Connection {
	t.Helper()
	conns := make([]*Connection, 0, near+far)
	for i := range near {
		conns = append(conns, makeTestConn(t, fmt.Sprintf("http://near-%02d:9200", i), fmt.Sprintf("near-%02d", i), 300*time.Microsecond))
	}
	for i := range far {
		conns = append(conns, makeTestConn(t, fmt.Sprintf("http://far-%02d:9200", i), fmt.Sprintf("far-%02d", i), 5*time.Millisecond))
	}
	for _, c := range conns {
		c.pools.getOrCreate(poolWrite).cwnd.Store(int32(cwnd)) //nolint:gosec // test cwnd is small
	}
	return conns
}

// TestNoIndexRequestsSpread sends no-index bulks one at a time, so every node is
// idle at each selection. They must spread across the nearest tier rather than
// all reaching the node whose URL sorts first, and must stay in that tier while
// it has room.
func TestNoIndexRequestsSpread(t *testing.T) {
	t.Parallel()

	const (
		near     = 8
		far      = 8
		requests = 4000
	)

	tests := []struct {
		name  string
		build func(t *testing.T, conns []*Connection) noIndexEvaluator
	}{
		{name: "poolRouter", build: newNoIndexPoolRouter},
		{name: "IndexRouter", build: newNoIndexIndexRouter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := spreadConns(t, near, far, 8)
			r := tt.build(t, conns)

			counts := make(map[string]int, len(conns))
			for range requests {
				req, err := http.NewRequest(http.MethodPost, "/_bulk", nil)
				require.NoError(t, err)
				hop, err := r.Eval(context.Background(), req)
				require.NoError(t, err)
				require.NotNil(t, hop.Conn)
				counts[hop.Conn.ID]++
			}

			// Uniform over the near tier is requests/near = 500 each. Every
			// near node must take a fair share, and none may dominate.
			for i := range near {
				id := fmt.Sprintf("near-%02d", i)
				require.Greater(t, counts[id], requests/near/2, "near node %s starved: %v", id, counts)
				require.Less(t, counts[id], requests/near*2, "near node %s dominates: %v", id, counts)
			}
			for i := range far {
				id := fmt.Sprintf("far-%02d", i)
				require.Zero(t, counts[id], "far node %s used while near tier has room: %v", id, counts)
			}
		})
	}
}

// TestSelectNoIndexCrossover checks that no-index requests stay in the nearest
// RTT tier while it scores better, and cross into a farther tier by score as
// the near tier gets busy, as guides/transport-routing.md "AZ-Aware Crossover"
// describes. near nodes are bucket 8, far nodes bucket 12, and every write
// pool has cwnd 8, so an idle near node scores 8*1/8 = 1.0 and an idle far
// node 12*1/8 = 1.5.
func TestSelectNoIndexCrossover(t *testing.T) {
	t.Parallel()

	const (
		near  = 4
		far   = 4
		cwnd  = 8
		draws = 2000
	)

	tests := []struct {
		name string
		// setup prepares conns (near-00..03 then far-00..03) and returns the
		// request's route attempt, nil for a first try.
		setup func(t *testing.T, conns []*Connection) *routeAttempt
		// wantFar is the share of picks that may go to the far tier: [min, max].
		wantFar [2]float64
		// mustUse lists nodes that must be chosen at least once.
		mustUse []string
		// never lists nodes that must not be chosen.
		never []string
	}{
		{
			name:    "idle: every pick stays near",
			setup:   func(*testing.T, []*Connection) *routeAttempt { return nil },
			wantFar: [2]float64{0, 0},
			mustUse: []string{"near-00", "near-01", "near-02", "near-03"},
		},
		{
			// One request in flight on each near node: near scores 8*2/8 = 2.0
			// against an idle far node's 1.5, so far wins.
			name: "busy near tier crosses over by score",
			setup: func(_ *testing.T, conns []*Connection) *routeAttempt {
				for _, c := range conns[:near] {
					c.addInFlight(poolWrite)
				}
				return nil
			},
			wantFar: [2]float64{1, 1},
			mustUse: []string{"far-00", "far-01", "far-02", "far-03"},
		},
		{
			// Half the near tier is busy: a pair holding an idle near node
			// still wins locally (1.0 < 1.5); a pair of two busy ones loses to
			// far (2.0 > 1.5). The pair is two of four, both busy with
			// probability (2/4)*(1/3) = 1/6, so about a sixth crosses over.
			name: "partly busy near tier crosses over in part",
			setup: func(_ *testing.T, conns []*Connection) *routeAttempt {
				conns[0].addInFlight(poolWrite)
				conns[1].addInFlight(poolWrite)
				return nil
			},
			wantFar: [2]float64{0.10, 0.23},
			mustUse: []string{"near-02", "near-03", "far-00"},
		},
		{
			name: "retry skips nodes tried in this pass",
			setup: func(_ *testing.T, conns []*Connection) *routeAttempt {
				att := &routeAttempt{}
				att.record(conns[0])
				att.record(conns[1])
				return att
			},
			wantFar: [2]float64{0, 0},
			mustUse: []string{"near-02", "near-03"},
			never:   []string{"near-00", "near-01"},
		},
		{
			// Only near-03 is untried, so the pair is that node alone, and the
			// far challenger loses to it while it is idle.
			name: "one untried near node still beats an idle far node",
			setup: func(_ *testing.T, conns []*Connection) *routeAttempt {
				att := &routeAttempt{}
				for _, c := range conns[:near-1] {
					att.record(c)
				}
				return att
			},
			wantFar: [2]float64{0, 0},
			mustUse: []string{"near-03"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := spreadConns(t, near, far, cwnd)
			sortConnectionsByRTT(conns)
			att := tt.setup(t, conns)

			used := make(map[string]int)
			farPicks := 0
			for range draws {
				c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, nil, att)
				require.NotNil(t, c)
				used[c.ID]++
				if c.ID[:3] == "far" {
					farPicks++
				}
			}
			share := float64(farPicks) / draws
			require.GreaterOrEqual(t, share, tt.wantFar[0], "far share %.3f: %v", share, used)
			require.LessOrEqual(t, share, tt.wantFar[1], "far share %.3f: %v", share, used)
			for _, id := range tt.mustUse {
				require.Positive(t, used[id], "never chose %s: %v", id, used)
			}
			for _, id := range tt.never {
				require.Zero(t, used[id], "chose %s: %v", id, used)
			}
		})
	}
}

// TestSelectNoIndexSmallSets covers candidate lists too small to draw two
// distinct nodes from.
func TestSelectNoIndexSmallSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		near int
		far  int
		want string
	}{
		{name: "empty"},
		{name: "single node", near: 1, want: "near-00"},
		{name: "single near node beside a far tier", near: 1, far: 3, want: "near-00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := spreadConns(t, tt.near, tt.far, 4)
			sortConnectionsByRTT(conns)
			c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, nil, nil)
			if tt.want == "" {
				require.Nil(t, c)
				return
			}
			require.NotNil(t, c)
			require.Equal(t, tt.want, c.ID)
		})
	}
}
