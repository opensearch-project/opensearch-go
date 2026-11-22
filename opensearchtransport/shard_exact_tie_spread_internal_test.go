// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestShardExactReadsSpreadAcrossTiedCopies sends document reads, one at a
// time, to a shard whose primary and two replicas are equally near and idle.
// Copies that score the same must share the reads rather than the copy whose
// URL sorts first taking all of them.
func TestShardExactReadsSpreadAcrossTiedCopies(t *testing.T) {
	t.Parallel()

	const requests = 2000

	tests := []struct {
		name      string
		scoreFunc connScoreFunc
		// writeBusy puts in-flight writes on the primary, which the dynamic
		// read scorer turns into a higher primary read cost.
		writeBusy bool
		// want lists the copies that must share the reads; every other copy
		// must get none.
		want []string
	}{
		{
			name: "static scoring: the two replicas tie",
			want: []string{"a-replica", "b-replica"},
		},
		{
			name:      "dynamic scoring, primary busy with writes: the two replicas tie",
			scoreFunc: newReadScoreFunc(defaultReadCurveBase, defaultReadCurveAmplify, defaultReadCurveExponent),
			writeBusy: true,
			want:      []string{"a-replica", "b-replica"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names := []string{"a-replica", "b-replica", "c-primary"}
			conns := make([]*Connection, len(names))
			for i, n := range names {
				conns[i] = makeTestConn(t, "http://"+n+":9200", n, 300*time.Microsecond)
				conns[i].Name = n
				conns[i].pools.getOrCreate(poolWrite).cwnd.Store(4)
				conns[i].pools.getOrCreate(poolGet).cwnd.Store(8)
			}
			if tt.writeBusy {
				for range 4 {
					conns[2].addInFlight(poolWrite)
				}
			}

			shards := make(map[int]*shardNodes, 4)
			for s := range 4 {
				shards[s] = &shardNodes{Primary: "c-primary", Replicas: []string{"a-replica", "b-replica"}}
			}

			inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, enabled: true}
			cache := testIndexSlotCache()
			r := wrapWithRouter(inner, cache, &shardCostForReads, poolGet, tt.scoreFunc).(*poolRouter)
			sorted := append([]*Connection(nil), conns...)
			sortConnectionsByRTT(sorted)
			r.mu.sortedConns = sorted
			cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
				NumberOfPrimaryShards: 4,
				RoutingNumShards:      4,
				Shards:                shards,
			})

			counts := make(map[string]int, len(names))
			for range requests {
				req, err := http.NewRequest(http.MethodGet, "/idx/_doc/doc-1", nil)
				require.NoError(t, err)
				hop, err := r.Eval(t.Context(), req)
				require.NoError(t, err)
				require.NotNil(t, hop.Conn)
				counts[hop.Conn.Name]++
			}

			share := requests / len(tt.want)
			for _, name := range tt.want {
				require.Greater(t, counts[name], share/2, "copy %s starved: %v", name, counts)
			}
			total := 0
			for _, name := range tt.want {
				total += counts[name]
			}
			require.Equal(t, requests, total, "reads reached a copy that does not tie: %v", counts)
		})
	}
}
