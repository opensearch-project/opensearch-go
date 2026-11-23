// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
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
		c := selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, &cluster)
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

// TestNodeStatsPollFeedsCoordLoad checks that the node stats poll records the
// coordinating byte counter from indexing_pressure, so that two polls give the
// node a measured rate, and that a response without the section leaves the
// node unmeasured.
func TestNodeStatsPollFeedsCoordLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// pressure is the indexing_pressure the server reports on each poll;
		// nil omits the section.
		pressure     func(poll int64) *IndexingPressureStats
		wantMeasured bool
	}{
		{
			name: "indexing_pressure present",
			pressure: func(poll int64) *IndexingPressureStats {
				return &IndexingPressureStats{Memory: IndexingPressureMemory{
					Total: IndexingPressureTotal{CoordinatingInBytes: poll * 1 << 20},
				}}
			},
			wantMeasured: true,
		},
		{
			name:     "indexing_pressure absent",
			pressure: func(int64) *IndexingPressureStats { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each poll's response, encoded before the server runs so the
			// handler does not fail the test off the test goroutine.
			var responses [2][]byte
			for i := range responses {
				b, err := json.Marshal(NodeStatsResponse{Nodes: map[string]NodeStats{
					"testnode": {IndexingPressure: tt.pressure(int64(i + 1))},
				}})
				require.NoError(t, err)
				responses[i] = b
			}
			var polls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				i := min(polls.Add(1), int64(len(responses))) - 1
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(responses[i])
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			conn := &Connection{URL: serverURL}
			conn.setLifecycleBit(lcActive)
			pool := newStandbyPool([]*Connection{conn}, nil)
			client := newTestClientWithPool(server.Client().Transport, pool)
			t.Cleanup(client.cancelFunc)

			// The first poll is the baseline; the second measures a rate once
			// the clock has moved past it.
			client.fetchAndEvaluateNodeStats(conn, pool)
			require.False(t, conn.coordLoad.measured.Load(), "one poll measured a rate")
			start := time.Now()
			require.Eventually(t, func() bool { return time.Since(start) > 0 }, time.Second, time.Millisecond)
			client.fetchAndEvaluateNodeStats(conn, pool)

			require.Equal(t, tt.wantMeasured, conn.coordLoad.measured.Load())
			if tt.wantMeasured {
				require.Positive(t, conn.coordLoad.load())
			}
		})
	}
}

// TestRoutersWeighNoIndexByCoordLoad checks that both routers hand the
// transport's cluster coordinating load to the no-index selection, by way of
// [policyConfigurable.configurePolicySettings]: a node coordinating far more
// than its peers must lose every pair it is drawn in. Without the cluster
// load every node is weighted alike and the hot node takes about a fifth of
// the requests.
func TestRoutersWeighNoIndexByCoordLoad(t *testing.T) {
	t.Parallel()

	const (
		peers    = 4
		requests = 2000
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

			conns := make([]*Connection, 0, 1+peers)
			conns = append(conns, measuredConn(t, "hot", 100_000))
			for i := range peers {
				conns = append(conns, measuredConn(t, fmt.Sprintf("cold-%d", i), 1_000))
			}
			for _, c := range conns {
				c.pools.getOrCreate(poolWrite).cwnd.Store(8)
			}

			var cluster clusterCoordLoad
			cluster.update(conns)
			r := tt.build(t, conns)
			configurable, ok := r.(policyConfigurable)
			require.True(t, ok, "%T does not take policy settings", r)
			require.NoError(t, configurable.configurePolicySettings(policyConfig{clusterCoord: &cluster}))

			counts := make(map[string]int)
			for range requests {
				req, err := http.NewRequest(http.MethodPost, "/_bulk", nil)
				require.NoError(t, err)
				hop, err := r.Eval(t.Context(), req)
				require.NoError(t, err)
				require.NotNil(t, hop.Conn)
				counts[hop.Conn.ID]++
			}

			require.Zero(t, counts["hot"], "hot coordinator not avoided: %v", counts)
		})
	}
}

// TestPollNodeStatsUpdatesClusterCoordLoad checks that a poll cycle over a
// multi-node pool sets the cluster mean the no-index routers weigh nodes
// against, once two polls have measured the nodes' rates.
func TestPollNodeStatsUpdatesClusterCoordLoad(t *testing.T) {
	t.Parallel()

	var polls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Every node reports the same counter, growing by 1 MiB per poll
		// cycle of two nodes.
		cycle := (polls.Add(1) + 1) / 2
		b, err := json.Marshal(NodeStatsResponse{Nodes: map[string]NodeStats{
			"testnode": {IndexingPressure: &IndexingPressureStats{Memory: IndexingPressureMemory{
				Total: IndexingPressureTotal{CoordinatingInBytes: cycle << 20},
			}}},
		}})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	a := &Connection{URL: serverURL, Name: "a"}
	a.setLifecycleBit(lcActive)
	b := &Connection{URL: serverURL, Name: "b"}
	b.setLifecycleBit(lcActive)
	pool := &multiServerPool{}
	pool.mu.ready = []*Connection{a, b}
	pool.mu.activeCount = 2

	client := newTestClientWithPool(server.Client().Transport, pool)
	t.Cleanup(client.cancelFunc)

	client.pollNodeStats()
	require.Zero(t, client.clusterCoord.load(), "one cycle set a cluster mean")
	start := time.Now()
	require.Eventually(t, func() bool { return time.Since(start) > 0 }, time.Second, time.Millisecond)
	client.pollNodeStats()

	require.Positive(t, client.clusterCoord.load())
	require.InDelta(t, (a.coordLoad.load()+b.coordLoad.load())/2, client.clusterCoord.load(), 1e-6,
		"cluster mean is not the mean of the polled nodes' rates")
}
