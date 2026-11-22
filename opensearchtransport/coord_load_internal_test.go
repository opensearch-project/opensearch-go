// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoordLoadUpdate(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000, 0)

	type poll struct {
		bytes int64
		at    time.Time
		procs int32
	}

	tests := []struct {
		name         string
		polls        []poll
		wantRate     float64
		wantMeasured bool
	}{
		{
			name:  "first poll is a baseline",
			polls: []poll{{bytes: 1 << 30, at: t0, procs: 8}},
		},
		{
			name: "rate is bytes per second per core",
			polls: []poll{
				{bytes: 1_000, at: t0, procs: 4},
				{bytes: 81_000, at: t0.Add(10 * time.Second), procs: 4},
			},
			wantRate:     2_000, // 80_000 B / 10 s / 4 cores
			wantMeasured: true,
		},
		{
			name: "unknown core count uses the default",
			polls: []poll{
				{bytes: 0, at: t0},
				{bytes: int64(defaultServerCoreCount) * 1_000, at: t0.Add(time.Second)},
			},
			wantRate:     1_000,
			wantMeasured: true,
		},
		{
			name: "counter reset after a restart is a new baseline",
			polls: []poll{
				{bytes: 0, at: t0, procs: 1},
				{bytes: 10_000, at: t0.Add(time.Second), procs: 1},
				{bytes: 50, at: t0.Add(2 * time.Second), procs: 1},
			},
			wantRate:     10_000, // the restart poll leaves the last rate
			wantMeasured: true,
		},
		{
			name: "poll at the same instant changes nothing",
			polls: []poll{
				{bytes: 0, at: t0, procs: 1},
				{bytes: 10_000, at: t0, procs: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var l coordLoad
			for _, p := range tt.polls {
				l.update(p.bytes, p.at, p.procs)
			}
			require.InDelta(t, tt.wantRate, l.load(), 1e-9)
			require.Equal(t, tt.wantMeasured, l.measured.Load())
		})
	}
}

// measuredConn returns a connection whose coordinating rate is rate.
func measuredConn(t *testing.T, id string, rate float64) *Connection {
	t.Helper()
	c := makeTestConn(t, "http://"+id+":9200", id, 300*time.Microsecond)
	t0 := time.Unix(1_000, 0)
	c.coordLoad.update(0, t0, 1)
	c.coordLoad.update(int64(rate), t0.Add(time.Second), 1)
	return c
}

func TestCoordLoadFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rates      []float64 // measured nodes; the first is the node scored
		nilCluster bool      // whether the scorer has no cluster load at all
		cluster    bool      // whether the cluster mean is computed
		want       float64
	}{
		{name: "no cluster mean", rates: []float64{5_000, 1_000}, want: 1},
		{name: "nil cluster", rates: []float64{5_000}, nilCluster: true, want: 1},
		{name: "idle node", rates: []float64{0, 2_000}, cluster: true, want: 1},
		{name: "node at the mean", rates: []float64{1_000, 1_000}, cluster: true, want: 2},
		{name: "node at three times the mean", rates: []float64{3_000, 0, 0}, cluster: true, want: 4},
		{name: "every node idle", rates: []float64{0, 0}, cluster: true, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := make([]*Connection, len(tt.rates))
			for i, r := range tt.rates {
				conns[i] = measuredConn(t, fmt.Sprintf("n%d", i), r)
			}
			var cluster *clusterCoordLoad
			if !tt.nilCluster {
				cluster = &clusterCoordLoad{}
			}
			if tt.cluster {
				cluster.update(conns)
			}
			require.InDelta(t, tt.want, coordLoadFactor(conns[0], cluster), 1e-9)
		})
	}
}

func TestClusterCoordLoadSkipsUnmeasured(t *testing.T) {
	t.Parallel()

	unmeasured := makeTestConn(t, "http://new:9200", "new", 300*time.Microsecond)
	conns := []*Connection{measuredConn(t, "a", 1_000), measuredConn(t, "b", 3_000), unmeasured}

	var cluster clusterCoordLoad
	cluster.update(conns)
	require.InDelta(t, 2_000, cluster.load(), 1e-9)

	var empty clusterCoordLoad
	empty.update([]*Connection{unmeasured})
	require.Zero(t, empty.load())
}

// TestSelectNoIndexAvoidsHotCoordinator reproduces a node coordinating most of
// the cluster's writes for other clients: its tier peers must take its share.
// The two draws are distinct, so the node with the worst score in a tier loses
// every pair it is in.
func TestSelectNoIndexAvoidsHotCoordinator(t *testing.T) {
	t.Parallel()

	const (
		peers = 4
		draws = 8000
	)

	conns := make([]*Connection, 0, 1+peers)
	conns = append(conns, measuredConn(t, "hot", 100_000))
	for i := range peers {
		conns = append(conns, measuredConn(t, fmt.Sprintf("cold-%d", i), 1_000))
	}
	for _, c := range conns {
		c.pools.getOrCreate(poolWrite).cwnd.Store(8)
	}
	sortConnectionsByRTT(conns)

	var cluster clusterCoordLoad
	cluster.update(conns)

	counts := make(map[string]int)
	for range draws {
		c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, &cluster, nil)
		require.NotNil(t, c)
		counts[c.ID]++
	}

	// Uniform would give each node draws/5 = 1600. The hot node loses every
	// pair it is drawn in, so each cold node takes about draws/4.
	require.Zero(t, counts["hot"], "hot coordinator not avoided: %v", counts)
	for i := range peers {
		id := fmt.Sprintf("cold-%d", i)
		require.Greater(t, counts[id], draws/(peers+1), "cold node %s under-used: %v", id, counts)
	}
}
