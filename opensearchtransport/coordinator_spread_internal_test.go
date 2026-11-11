// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// With equal base scores, the locality multipliers set the candidates' relative
// scores. The first pick is a weighted random draw (weight 1/score) among the
// candidates scoring within best*(1+spread); later picks stay in ascending order.
func TestConnScoreSelectSpread(t *testing.T) {
	t.Parallel()

	type try struct {
		node   int
		tries  int
		inPass bool
	}
	tests := []struct {
		name       string
		locality   []float64 // nil: equal scores
		spread     float64
		draw       float64 // value randFloat returns
		wantRandom bool    // randFloat must be consulted
		tried      []try
		warming    int // index of a candidate that is warming up; 0 for none
		want       int
	}{
		{name: "spread 0 is argmin", locality: []float64{1.1, 1, 1.2}, spread: 0, draw: 0.99, want: 1},
		{name: "lone candidate in the band is argmin", locality: []float64{1, 2, 3}, spread: 0.25, draw: 0.99, want: 0},
		{name: "zero best score is argmin", locality: []float64{0, 1, 1}, spread: 0.25, draw: 0.99, want: 0},
		{name: "draw 0 takes the best", locality: []float64{1, 1.2, 1.3}, spread: 0.25, draw: 0, wantRandom: true, want: 0},
		{
			name: "draw below the best's share takes the best", locality: []float64{1, 1.2, 1.3},
			spread: 0.25, draw: 0.5, wantRandom: true, want: 0,
		},
		{
			name: "draw above the best's share takes the next in the band", locality: []float64{1, 1.2, 1.3},
			spread: 0.25, draw: 0.6, wantRandom: true, want: 1,
		},
		{
			name: "draw near 1 stays inside the band", locality: []float64{1, 1.2, 1.3},
			spread: 0.25, draw: 0.999999, wantRandom: true, want: 1,
		},
		{
			name: "a candidate on the band edge is in the band", locality: []float64{1, 1.25},
			spread: 0.25, draw: 0.999999, wantRandom: true, want: 1,
		},
		{name: "all-equal scores spread across every candidate", spread: 0.25, draw: 0.4, wantRandom: true, want: 1},
		{name: "all-equal scores, last slice of the draw", spread: 0.25, draw: 0.99, wantRandom: true, want: 2},
		{
			name: "a candidate tried in this pass is not drawn", spread: 0.25, draw: 0, wantRandom: true, want: 1,
			tried: []try{{node: 0, tries: 1, inPass: true}},
		},
		{
			name: "an earlier try lifts a candidate out of the band", locality: []float64{1, 1.1, 1.2},
			spread: 0.25, draw: 0, wantRandom: true, want: 1,
			tried: []try{{node: 0, tries: 1}},
		},
		{name: "a warming candidate that skips falls back to ascending order", spread: 0.25, draw: 0.4, wantRandom: true, warming: 1, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := []*Connection{
				scoreTestConn(t, "a", 200*time.Microsecond, 0),
				scoreTestConn(t, "b", 200*time.Microsecond, 0),
				scoreTestConn(t, "c", 200*time.Microsecond, 0),
			}
			if tt.locality != nil {
				conns = conns[:len(tt.locality)]
			}
			for _, c := range conns {
				c.setLifecycleBit(lcActive)
			}
			if tt.warming != 0 {
				conns[tt.warming].setLifecycleBit(lcNeedsWarmup)
				conns[tt.warming].startWarmup(8, 8)
			}

			var att *routeAttempt
			if tt.tried != nil {
				att = &routeAttempt{}
				for _, tr := range tt.tried {
					att.tried = append(att.tried, triedConn{conn: conns[tr.node], tries: tr.tries, inPass: tr.inPass})
				}
			}

			var draws int
			randFloat := func() float64 {
				draws++
				return tt.draw
			}

			scores := acquireFloats(len(conns))
			defer scores.Release()
			best := connScoreSelect(conns, nil, nil, &shardCostForReads, "", true, scores.Slice(), nil, tt.locality, att, tt.spread, randFloat)

			require.Same(t, conns[tt.want], best)
			if tt.wantRandom {
				require.Equal(t, 1, draws)
			} else {
				require.Equal(t, 0, draws)
			}
		})
	}
}

// Over many seeded draws the first pick lands on each candidate in the band in
// proportion to 1/score, and never on one outside it.
func TestConnScoreSelectSpreadWeights(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		scoreTestConn(t, "a", 200*time.Microsecond, 0),
		scoreTestConn(t, "b", 200*time.Microsecond, 0),
		scoreTestConn(t, "c", 200*time.Microsecond, 0),
	}
	for _, c := range conns {
		c.setLifecycleBit(lcActive)
	}
	// Scores 1, 1.25, 2 with spread 0.25: only the first two are in the band, with
	// weights 1 and 0.8.
	locality := []float64{1, 1.25, 2}
	rng := rand.New(rand.NewPCG(1, 2))

	const n = 20000
	counts := make(map[string]int, len(conns))
	scores := acquireFloats(len(conns))
	defer scores.Release()
	for range n {
		best := connScoreSelect(conns, nil, nil, &shardCostForReads, "", true, scores.Slice(), nil, locality, nil, 0.25, rng.Float64)
		counts[best.ID]++
	}

	require.Zero(t, counts["c"])
	require.InDelta(t, 1.0/1.8, float64(counts["a"])/n, 0.02)
	require.InDelta(t, 0.8/1.8, float64(counts["b"])/n, 0.02)
}

// Concurrent clients that see the same near-tie must not all pick the same
// coordinator. Each router below is an independent client with its own cache,
// connections and seeded random source; they all route the same request on a
// partitioned index whose target shards sit on a few nodes.
func TestCoordinatorSpreadAcrossClients(t *testing.T) {
	t.Parallel()

	const (
		clients = 8
		picks   = 250
		primary = 8
		index   = "tenants"
	)

	// The four shards one routing key targets on the partitioned index (one
	// routing shard per primary, so consecutive offsets land on distinct shards).
	var routing string
	var targets []int
	for i := range 1000 {
		key := "tenant-" + strconv.Itoa(i)
		shards := make([]int, 0, partitionSize)
		for off := range partitionSize {
			shards = append(shards, shardhash.ForRoutingOffset(key, off, primary, primary))
		}
		if len(slices.Compact(slices.Sorted(slices.Values(shards)))) == partitionSize {
			routing, targets = key, shards

			break
		}
	}
	require.NotEmpty(t, routing, "no routing key targets %d distinct shards", partitionSize)

	// hosts lists, per node, the indexes into targets of the shards it hosts.
	type hosting map[string][]int

	tests := []struct {
		name   string
		hosts  hosting
		spec   string
		loaded string // node with one request in flight on every client
		c3     bool   // every node has fresh adaptive_selection stats, so search scoring uses C3
		queued string // with c3, node whose stats report a deep queue
		check  func(t *testing.T, share map[string]float64)
	}{
		{
			// nodeA and nodeB host the same three of four shards; nodeC hosts one.
			name:  "spread 0 sends every client to one node",
			hosts: hosting{"nodeA": {0, 1, 2}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			spec:  "spread=0",
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.InDelta(t, 1.0, max(share["nodeA"], share["nodeB"], share["nodeC"]), 0)
			},
		},
		{
			name:  "default spread splits the tied nodes",
			hosts: hosting{"nodeA": {0, 1, 2}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			spec:  "",
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.Less(t, share["nodeA"], 0.65)
				require.Less(t, share["nodeB"], 0.65)
				require.Greater(t, share["nodeA"], 0.25)
				require.Greater(t, share["nodeB"], 0.25)
			},
		},
		{
			name:  "default spread splits the tied nodes under C3",
			hosts: hosting{"nodeA": {0, 1, 2}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			c3:    true,
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.Less(t, share["nodeA"], 0.65)
				require.Less(t, share["nodeB"], 0.65)
				require.Greater(t, share["nodeA"], 0.25)
				require.Greater(t, share["nodeB"], 0.25)
			},
		},
		{
			// Only the C3 term sees the server-side queue, so this fails if
			// selection fell back to the linear term.
			name:   "a deep server queue moves a tied node's picks under C3",
			hosts:  hosting{"nodeA": {0, 1, 2}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			c3:     true,
			queued: "nodeA",
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.InDelta(t, 0.0, share["nodeA"], 0)
			},
		},
		{
			// nodeA hosts every shard but carries twice nodeB's utilization, more than
			// the default locality bonus (at most 1.5x) can offset.
			name:   "a loaded node hosting the most shards loses its picks",
			hosts:  hosting{"nodeA": {0, 1, 2, 3}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			loaded: "nodeA",
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.InDelta(t, 0.0, share["nodeA"], 0)
			},
		},
		{
			name:   "a strong locality keeps the loaded node",
			hosts:  hosting{"nodeA": {0, 1, 2, 3}, "nodeB": {0, 1, 2}, "nodeC": {3}},
			spec:   "locality=10",
			loaded: "nodeA",
			check: func(t *testing.T, share map[string]float64) {
				t.Helper()
				require.InDelta(t, 1.0, share["nodeA"], 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			costCfg, err := parseShardCostConfig(tt.spec)
			require.NoError(t, err)

			shards := map[int]*shardNodes{}
			for _, node := range []string{"nodeA", "nodeB", "nodeC"} {
				for _, idx := range tt.hosts[node] {
					sn := shards[targets[idx]]
					if sn == nil {
						sn = &shardNodes{Primary: node}
						shards[targets[idx]] = sn
					} else {
						sn.Replicas = append(sn.Replicas, node)
					}
				}
			}

			req := &http.Request{URL: &url.URL{Path: "/" + index + "/_search", RawQuery: "routing=" + routing}}
			counts := map[string]int{}
			for client := range clients {
				r := newIndexRouter(indexSlotCacheConfig{})
				r.cache.locality, r.cache.spread = costCfg.locality, costCfg.spread
				r.cache.randFloat = rand.New(rand.NewPCG(uint64(client)+1, 7)).Float64
				if tt.c3 {
					// A mux route names the pool; the standalone router has none,
					// and C3 applies only to the search and get pools.
					r.poolName = poolSearch
				}

				conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB"), newTestConn(t, "nodeC")}
				if tt.c3 {
					// A measured RTT, so latency is not the unknown sentinel that
					// would dwarf the queue term.
					for i, name := range []string{"nodeA", "nodeB", "nodeC"} {
						conns[i] = newTestConnRTT(t, name, 500*time.Microsecond)
					}
				}
				for _, c := range conns {
					if c.Name == tt.loaded {
						c.addInFlight("")
					}
					if tt.c3 {
						st := AdaptiveSelectionStats{AvgServiceTimeNs: int64(time.Millisecond)}
						if c.Name == tt.queued {
							st.AvgQueueSize = 50
						}
						recordUsable(&c.ars, time.Now().Add(-time.Minute), c.ID, c.ID, len(conns), st)
					}
				}
				if tt.c3 {
					require.True(t, newC3Inputs(conns, poolSearch).active, "C3 inactive")
				}
				require.NoError(t, r.DiscoveryUpdate(conns, nil, nil))
				r.cache.getOrCreate(index).shardMap.Store(&indexShardMap{
					NumberOfPrimaryShards: primary,
					RoutingNumShards:      primary,
					RoutingPartitionSize:  partitionSize,
					Shards:                shards,
				})

				for range picks {
					hop, err := r.Eval(t.Context(), req)
					require.NoError(t, err)
					require.NotNil(t, hop.Conn)
					counts[hop.Conn.Name]++
				}
			}

			share := map[string]float64{}
			for node, n := range counts {
				share[node] = float64(n) / float64(clients*picks)
			}
			tt.check(t, share)
		})
	}
}
