// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMultiRoutingSpreadsAcrossTiedNodes sends searches with a comma-separated
// ?routing= list, one at a time. Nodes that host the same number of the
// targeted shards, at the same distance and equally idle, must share them
// rather than the node whose URL sorts first taking all of them. A node that
// hosts fewer of the targeted shards must still lose.
func TestMultiRoutingSpreadsAcrossTiedNodes(t *testing.T) {
	t.Parallel()

	const requests = 2000

	tests := []struct {
		name      string
		scoreFunc connScoreFunc
		// shards is the placement of each of the index's 4 shards.
		shards map[int]*shardNodes
		want   []string // nodes that must share the searches
	}{
		{
			name: "every node holds every shard",
			shards: map[int]*shardNodes{
				0: {Primary: "a", Replicas: []string{"b", "c"}},
				1: {Primary: "b", Replicas: []string{"a", "c"}},
				2: {Primary: "c", Replicas: []string{"a", "b"}},
				3: {Primary: "a", Replicas: []string{"b", "c"}},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name:      "dynamic read scoring, every node holds every shard",
			scoreFunc: newReadScoreFunc(defaultReadCurveBase, defaultReadCurveAmplify, defaultReadCurveExponent),
			shards: map[int]*shardNodes{
				0: {Primary: "a", Replicas: []string{"b", "c"}},
				1: {Primary: "b", Replicas: []string{"a", "c"}},
				2: {Primary: "c", Replicas: []string{"a", "b"}},
				3: {Primary: "a", Replicas: []string{"b", "c"}},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "a node holding fewer target shards is not chosen",
			shards: map[int]*shardNodes{
				0: {Primary: "b", Replicas: []string{"c"}},
				1: {Primary: "c", Replicas: []string{"b"}},
				2: {Primary: "b", Replicas: []string{"c", "a"}},
				3: {Primary: "c", Replicas: []string{"b"}},
			},
			want: []string{"b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names := []string{"a", "b", "c"}
			conns := make([]*Connection, len(names))
			for i, n := range names {
				conns[i] = makeTestConn(t, "http://"+n+":9200", n, 300*time.Microsecond)
				conns[i].Name = n
				conns[i].pools.getOrCreate(poolSearch).cwnd.Store(8)
			}

			inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, enabled: true}
			cache := testIndexSlotCache()
			r := wrapWithRouter(inner, cache, &shardCostForReads, poolSearch, tt.scoreFunc).(*poolRouter)
			sorted := append([]*Connection(nil), conns...)
			sortConnectionsByRTT(sorted)
			r.mu.sortedConns = sorted
			cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
				NumberOfPrimaryShards: 4,
				RoutingNumShards:      4,
				Shards:                tt.shards,
			})

			// Routing values that, together, reach every one of the 4 shards.
			path := routingListReachingAllShards(t, 4)

			counts := make(map[string]int, len(names))
			for range requests {
				req, err := http.NewRequest(http.MethodGet, path, nil)
				require.NoError(t, err)
				hop, err := r.Eval(t.Context(), req)
				require.NoError(t, err)
				require.NotNil(t, hop.Conn)
				counts[hop.Conn.Name]++
			}

			share := requests / len(tt.want)
			total := 0
			for _, name := range tt.want {
				require.Greater(t, counts[name], share/2, "node %q starved: %v", name, counts)
				total += counts[name]
			}
			require.Equal(t, requests, total, "searches reached a node that hosts fewer target shards: %v", counts)
		})
	}
}

// routingListReachingAllShards returns a search path whose ?routing= list
// targets every shard of an index with numShards shards and numShards routing
// shards.
func routingListReachingAllShards(t *testing.T, numShards int) string {
	t.Helper()

	seen := make(map[int]struct{}, numShards)
	var values []string
	for i := 0; len(seen) < numShards; i++ {
		require.Less(t, i, 10_000, "no routing values found for every shard")
		v := fmt.Sprintf("k%d", i)
		s := shardForRouting(v, numShards, numShards)
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		values = append(values, v)
	}

	path := "/idx/_search?routing="
	for i, v := range values {
		if i > 0 {
			path += ","
		}
		path += v
	}
	return path
}
