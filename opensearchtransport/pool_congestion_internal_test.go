// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- poolRegistry tests ---

func TestPoolRegistryGetOrCreate(t *testing.T) {
	t.Parallel()

	t.Run("empty name returns default pool", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		pc := reg.getOrCreate("")
		require.Equal(t, &reg.defaultPool, pc)
	})

	t.Run("named pool is created with cwnd=1", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		pc := reg.getOrCreate("search")
		require.NotNil(t, pc)
		require.Equal(t, int32(1), pc.cwnd.Load())
	})

	t.Run("second call returns same pool", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		pc1 := reg.getOrCreate("write")
		pc2 := reg.getOrCreate("write")
		require.Equal(t, pc1, pc2)
	})
}

func TestPoolRegistryGetForScoring(t *testing.T) {
	t.Parallel()

	t.Run("empty name returns default", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		require.Equal(t, &reg.defaultPool, reg.getForScoring(""))
	})

	t.Run("unknown pool falls back to default", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		require.Equal(t, &reg.defaultPool, reg.getForScoring("nonexistent"))
	})

	t.Run("known pool returns actual pool", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		created := reg.getOrCreate("search")
		require.Equal(t, created, reg.getForScoring("search"))
	})
}

func TestPoolRegistrySetMaxCwnd(t *testing.T) {
	t.Parallel()

	var reg poolRegistry
	reg.setMaxCwnd("search", 13)

	pc := reg.get("search")
	require.NotNil(t, pc)
	pc.mu.Lock()
	require.Equal(t, int32(13), pc.mu.maxCwnd)
	pc.mu.Unlock()
}

func TestPoolRegistryRemove(t *testing.T) {
	t.Parallel()

	var reg poolRegistry
	reg.getOrCreate("search")
	require.NotNil(t, reg.get("search"))

	reg.remove("search")
	require.Nil(t, reg.get("search"))
}

// --- AIMD tests ---

func TestApplyPoolAIMD_SlowStart(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(1)
	pc.mu.Lock()
	pc.mu.maxCwnd = 16
	pc.mu.ssthresh = 8
	pc.mu.Unlock()

	// First poll: 100 completions, no rejections, no wait time.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 100})
	require.Equal(t, int32(2), pc.cwnd.Load(), "slow start should double cwnd")

	// Second poll: more completions.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 200})
	require.Equal(t, int32(4), pc.cwnd.Load(), "slow start should double again")

	// Third poll.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 300})
	require.Equal(t, int32(8), pc.cwnd.Load(), "slow start doubles to ssthresh")

	// Fourth poll: cwnd == ssthresh, switches to congestion avoidance.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 400})
	require.Equal(t, int32(9), pc.cwnd.Load(), "congestion avoidance: additive increase")
}

func TestApplyPoolAIMD_MultiplicativeDecrease_WaitTime(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(10)
	pc.mu.Lock()
	pc.mu.maxCwnd = 16
	pc.mu.ssthresh = 16
	pc.mu.Unlock()

	// Poll with high wait time (congested RESIZABLE pool).
	// wait_per_completed = 10ms = 10_000_000ns >> 1ms threshold.
	wait := int64(10_000_000) // 10ms total wait for 1 completed
	applyPoolAIMD(pc, ThreadPoolStats{
		Completed:            1,
		TotalWaitTimeInNanos: &wait,
	})
	require.Equal(t, int32(5), pc.cwnd.Load(), "congestion should halve cwnd")

	// Verify ssthresh was updated.
	pc.mu.Lock()
	require.Equal(t, int32(5), pc.mu.ssthresh)
	pc.mu.Unlock()
}

func TestApplyPoolAIMD_QueueSaturationFallback(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(8)
	pc.mu.Lock()
	pc.mu.maxCwnd = 8
	pc.mu.ssthresh = 8
	// hasWaitTime = false (non-RESIZABLE pool)
	pc.mu.Unlock()

	// Queue > 0 and Active >= maxCwnd -> congested.
	applyPoolAIMD(pc, ThreadPoolStats{
		Completed: 100,
		Queue:     5,
		Active:    8, // == maxCwnd
	})
	require.Equal(t, int32(4), pc.cwnd.Load(), "queue saturation should halve cwnd")
}

func TestApplyPoolAIMD_RejectedSetsOverloaded(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(10)

	// Rejected delta > 0.
	applyPoolAIMD(pc, ThreadPoolStats{
		Completed: 100,
		Rejected:  5,
	})
	require.True(t, pc.overloaded.Load(), "rejected should set overloaded")
	require.Equal(t, int32(5), pc.cwnd.Load(), "rejected should halve cwnd")
}

func TestApplyPoolAIMD_OverloadClearedOnlyByPoller(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(10)

	// First: rejection sets overloaded.
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 10})
	require.True(t, pc.overloaded.Load())

	// Second poll: no new rejections -> clears overloaded.
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 20})
	require.False(t, pc.overloaded.Load(), "overloaded should clear when delta(rejected)==0")
}

func TestApplyPoolAIMD_CwndNeverBelowOne(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(1)

	// Rejection with cwnd=1 should keep cwnd=1.
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 1})
	require.Equal(t, int32(1), pc.cwnd.Load(), "cwnd must never go below 1")
}

func TestApplyPoolAIMD_CwndCappedAtMaxCwnd(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(1)
	pc.mu.Lock()
	pc.mu.maxCwnd = 4
	pc.mu.ssthresh = 4
	pc.mu.Unlock()

	// Slow start: 1 -> 2 -> 4 (capped).
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 100})
	require.Equal(t, int32(2), pc.cwnd.Load())
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 200})
	require.Equal(t, int32(4), pc.cwnd.Load())
	// Already at cap -> additive increase also capped.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 300})
	require.Equal(t, int32(4), pc.cwnd.Load(), "cwnd should not exceed maxCwnd")
}

func TestApplyPoolAIMD_NoCompletionsDelta(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(5)

	// Same completed count as previous -> no change.
	applyPoolAIMD(pc, ThreadPoolStats{Completed: 0})
	require.Equal(t, int32(5), pc.cwnd.Load(), "no completions should leave cwnd unchanged")
}

func TestApplyPoolAIMD_RecoveryAfterCongestion(t *testing.T) {
	t.Parallel()

	pc := &poolCongestion{}
	pc.cwnd.Store(16)
	pc.mu.Lock()
	pc.mu.maxCwnd = 16
	pc.mu.ssthresh = 16
	pc.mu.Unlock()

	// Congestion event: rejection.
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 100})
	require.Equal(t, int32(8), pc.cwnd.Load())
	require.True(t, pc.overloaded.Load())

	// Recovery: no rejections, slow start resumes from new ssthresh=8.
	// cwnd=8, ssthresh=8 -> congestion avoidance (additive).
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 200})
	require.False(t, pc.overloaded.Load())
	require.Equal(t, int32(9), pc.cwnd.Load(), "recovery: additive increase")
}

// --- updatePoolCongestion tests ---

func TestUpdatePoolCongestion_AddsAndRemovesPools(t *testing.T) {
	t.Parallel()

	conn := &Connection{}

	// First poll: two pools.
	updatePoolCongestion(conn, map[string]ThreadPoolStats{
		"search": {Completed: 100},
		"write":  {Completed: 50},
	})
	require.NotNil(t, conn.pools.get("search"))
	require.NotNil(t, conn.pools.get("write"))

	// Second poll: "write" disappears.
	updatePoolCongestion(conn, map[string]ThreadPoolStats{
		"search": {Completed: 200},
	})
	require.NotNil(t, conn.pools.get("search"))
	require.Nil(t, conn.pools.get("write"), "removed pool should be deleted")
}

func TestUpdatePoolCongestion_NilMap(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	conn.pools.getOrCreate("search")

	// Should be a no-op.
	updatePoolCongestion(conn, nil)
	require.NotNil(t, conn.pools.get("search"), "nil map should not remove pools")
}

// --- Connection in-flight tests ---

func TestConnectionInFlight(t *testing.T) {
	t.Parallel()

	conn := &Connection{}

	require.Equal(t, int32(0), conn.loadInFlight("search"))

	n := conn.addInFlight("search")
	require.Equal(t, int32(1), n)
	require.Equal(t, int32(1), conn.loadInFlight("search"))

	n = conn.addInFlight("search")
	require.Equal(t, int32(2), n)

	n = conn.releaseInFlight("search")
	require.Equal(t, int32(1), n)

	n = conn.releaseInFlight("search")
	require.Equal(t, int32(0), n)
}

func TestConnectionInFlight_Concurrent(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			conn.addInFlight("search")
		}()
	}
	wg.Wait()
	require.Equal(t, int32(goroutines), conn.loadInFlight("search"))

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			conn.releaseInFlight("search")
		}()
	}
	wg.Wait()
	require.Equal(t, int32(0), conn.loadInFlight("search"))
}

func TestConnectionInFlight_PoolIsolation(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	// Create distinct pools so in-flight counters are isolated.
	conn.pools.getOrCreate("search")
	conn.pools.getOrCreate("write")
	conn.pools.getOrCreate("get")

	conn.addInFlight("search")
	conn.addInFlight("write")
	conn.addInFlight("write")

	require.Equal(t, int32(1), conn.loadInFlight("search"))
	require.Equal(t, int32(2), conn.loadInFlight("write"))
	require.Equal(t, int32(0), conn.loadInFlight("get"))
}

// --- loadCwnd quorum tests ---

func TestConnectionLoadCwnd_PreQuorum(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	conn.allocatedProcessors.Store(4)

	cwnd := conn.loadCwnd("search", false)
	require.Equal(t, int32(4*defaultSyntheticCwndMultiplier), cwnd,
		"pre-quorum should return 4 * allocatedProcessors")
}

func TestConnectionLoadCwnd_PreQuorum_NoProcessors(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	// allocatedProcessors = 0 (not yet discovered)

	cwnd := conn.loadCwnd("search", false)
	expected := int32(defaultSyntheticCwndMultiplier * defaultServerCoreCount)
	require.Equal(t, expected, cwnd,
		"pre-quorum with no processors should use defaultServerCoreCount")
}

func TestConnectionLoadCwnd_PostQuorum(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	conn.storeMaxCwnd("search", 13)

	// After setMaxCwnd, the pool exists with cwnd=1 (AIMD hasn't grown it yet).
	cwnd := conn.loadCwnd("search", true)
	require.Equal(t, int32(1), cwnd, "post-quorum should return actual cwnd")
}

func TestConnectionLoadCwnd_PostQuorum_UnknownPool(t *testing.T) {
	t.Parallel()

	conn := &Connection{}

	// Unknown pool falls back to default pool (cwnd=0 -> clamped to 1).
	cwnd := conn.loadCwnd("nonexistent", true)
	require.Equal(t, int32(1), cwnd, "unknown pool should return at least 1")
}

// --- isPoolOverloaded tests ---

func TestConnectionIsPoolOverloaded(t *testing.T) {
	t.Parallel()

	conn := &Connection{}
	require.False(t, conn.isPoolOverloaded("search"))

	// Simulate overload via AIMD rejection.
	pc := conn.pools.getOrCreate("search")
	pc.cwnd.Store(10)
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 10})
	require.True(t, conn.isPoolOverloaded("search"))

	// Clear via poller (no new rejections).
	applyPoolAIMD(pc, ThreadPoolStats{Rejected: 1, Completed: 20})
	require.False(t, conn.isPoolOverloaded("search"))
}

// --- storeThreadPoolSizes tests ---

func TestStoreThreadPoolSizes(t *testing.T) {
	t.Parallel()

	conn := &Connection{}

	storeThreadPoolSizes(conn, map[string]nodeInfoThreadPool{
		"search": {Type: "resizable", Size: 13},
		"write":  {Type: "fixed", Size: 0, Max: 8}, // scaling: use Max
		"get":    {Type: "fixed", Size: 4},
	})

	// Verify maxCwnd was set.
	searchPC := conn.pools.get("search")
	require.NotNil(t, searchPC)
	searchPC.mu.Lock()
	require.Equal(t, int32(13), searchPC.mu.maxCwnd)
	searchPC.mu.Unlock()

	writePC := conn.pools.get("write")
	require.NotNil(t, writePC)
	writePC.mu.Lock()
	require.Equal(t, int32(8), writePC.mu.maxCwnd)
	writePC.mu.Unlock()

	getPC := conn.pools.get("get")
	require.NotNil(t, getPC)
	getPC.mu.Lock()
	require.Equal(t, int32(4), getPC.mu.maxCwnd)
	getPC.mu.Unlock()
}

func TestSetMaxCwnd_ClampsCwndAboveCeiling(t *testing.T) {
	t.Parallel()

	// Simulate the race: stats poller grows cwnd to 32 (synthetic ceiling)
	// before hardware health check delivers real pool size of 4.
	var reg poolRegistry
	pc := reg.getOrCreate("write")
	pc.cwnd.Store(32) // grown by AIMD with maxCwnd=0 default
	pc.mu.Lock()
	pc.mu.ssthresh = 32 // also set to synthetic ceiling
	pc.mu.Unlock()

	// Hardware health check arrives: real pool size = 4.
	reg.setMaxCwnd("write", 4)

	// cwnd must be clamped to the real ceiling.
	require.Equal(t, int32(4), pc.cwnd.Load(), "cwnd should be clamped to maxCwnd")

	// ssthresh must be reset to the real ceiling.
	pc.mu.Lock()
	require.Equal(t, int32(4), pc.mu.ssthresh, "ssthresh should be reset to maxCwnd")
	require.Equal(t, int32(4), pc.mu.maxCwnd)
	pc.mu.Unlock()
}

func TestSetMaxCwnd_DoesNotClampWhenBelowCeiling(t *testing.T) {
	t.Parallel()

	var reg poolRegistry
	pc := reg.getOrCreate("search")
	pc.cwnd.Store(2) // still in slow start

	reg.setMaxCwnd("search", 13)

	// cwnd below ceiling should not be changed.
	require.Equal(t, int32(2), pc.cwnd.Load(), "cwnd below ceiling should be unchanged")
	pc.mu.Lock()
	require.Equal(t, int32(13), pc.mu.ssthresh, "ssthresh should be set to real ceiling")
	require.Equal(t, int32(13), pc.mu.maxCwnd)
	pc.mu.Unlock()
}

func TestStoreThreadPoolSizes_SkipsZeroSize(t *testing.T) {
	t.Parallel()

	conn := &Connection{}

	storeThreadPoolSizes(conn, map[string]nodeInfoThreadPool{
		"generic": {Type: "scaling", Size: 0, Max: 0}, // both zero
	})

	// Pool should not be created when size is 0.
	require.Nil(t, conn.pools.get("generic"))
}

// --- calcConnDefaultScore with pool overload ---

func TestCalcConnScore_OverloadedReturnsMaxFloat(t *testing.T) {
	t.Parallel()

	u := &url.URL{Scheme: "https", Host: "node:9200"}
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		rttRing:   newRTTRing(4),
	}
	for range 4 {
		conn.rttRing.add(1 * time.Millisecond)
	}

	// Set pool overloaded.
	pc := conn.pools.getOrCreate("search")
	pc.overloaded.Store(true)

	score := calcConnDefaultScore(conn, shardCostForReads.forNode(&shardNodeInfo{Replicas: 1}), "search", true)
	require.InDelta(t, math.MaxFloat64, score, 1, "overloaded pool should return MaxFloat64")
}

func TestCalcConnScore_EmptyPoolNameNoOverloadCheck(t *testing.T) {
	t.Parallel()

	u := &url.URL{Scheme: "https", Host: "node:9200"}
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		rttRing:   newRTTRing(4),
	}
	for range 4 {
		conn.rttRing.add(1 * time.Millisecond)
	}

	// Default pool overloaded --but poolName="" skips the overload check
	// because the condition is `poolName != "" && ...`.
	conn.pools.defaultPool.overloaded.Store(true)

	score := calcConnDefaultScore(conn, shardCostForReads.forNode(&shardNodeInfo{Replicas: 1}), "", true)
	require.NotEqual(t, math.MaxFloat64, score,
		"empty poolName should not trigger overload skip")
	require.Greater(t, score, 0.0)
}

// --- maxCwndOrDefault ---

func TestMaxCwndOrDefault(t *testing.T) {
	t.Parallel()

	t.Run("positive maxCwnd returned as-is", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, int32(13), maxCwndOrDefault(13))
	})

	t.Run("zero uses synthetic default", func(t *testing.T) {
		t.Parallel()
		expected := int32(defaultSyntheticCwndMultiplier * defaultServerCoreCount)
		require.Equal(t, expected, maxCwndOrDefault(0))
	})

	t.Run("negative uses synthetic default", func(t *testing.T) {
		t.Parallel()
		expected := int32(defaultSyntheticCwndMultiplier * defaultServerCoreCount)
		require.Equal(t, expected, maxCwndOrDefault(-1))
	})
}

// --- clusterSearchAIMD tests ---

// makeTestSample creates a nodeSearchSample with the given cumulative stats.
func makeTestSample(conn *Connection, completed int64, waitNanos int64, maxCwnd int32) nodeSearchSample {
	wn := waitNanos
	return nodeSearchSample{
		conn: conn,
		stats: ThreadPoolStats{
			Completed:            completed,
			TotalWaitTimeInNanos: &wn,
		},
		maxCwnd: maxCwnd,
	}
}

func TestClusterSearchAIMD(t *testing.T) {
	t.Parallel()

	newConn := func(name string) *Connection {
		u, _ := url.Parse("http://" + name + ":9200")
		return &Connection{URL: u, ID: name}
	}

	t.Run("first poll stores baseline, cwnd stays zero", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")

		ca.update([]nodeSearchSample{makeTestSample(c1, 100, 50000, 13)})
		require.Equal(t, int32(0), ca.cwnd.Load(),
			"first poll has no baseline for deltas, cwnd should stay 0")
	})

	t.Run("slow start doubles cwnd", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")

		// Poll 1: establish baseline.
		ca.update([]nodeSearchSample{makeTestSample(c1, 100, 50000, 13)})

		// Poll 2: completions increased, low wait time -> slow start.
		ca.update([]nodeSearchSample{makeTestSample(c1, 200, 60000, 13)})
		cwnd := ca.cwnd.Load()
		require.Positive(t, cwnd, "cwnd should have grown from slow start")

		// Poll 3: more completions -> continues slow start.
		prevCwnd := cwnd
		ca.update([]nodeSearchSample{makeTestSample(c1, 300, 70000, 13)})
		cwnd = ca.cwnd.Load()
		require.GreaterOrEqual(t, cwnd, prevCwnd, "cwnd should keep growing in slow start")
	})

	t.Run("congestion halves cwnd", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")

		// Poll 1: baseline.
		ca.update([]nodeSearchSample{makeTestSample(c1, 1000, 0, 13)})

		// Poll 2: low wait -> grow.
		ca.update([]nodeSearchSample{makeTestSample(c1, 2000, 100000, 13)})
		grownCwnd := ca.cwnd.Load()
		require.Positive(t, grownCwnd)

		// Poll 3: high wait time (2ms per completion = congested).
		// 1000 completions * 2_000_000 ns = 2_000_000_000 ns total wait.
		prevWait := int64(100000)
		highWait := prevWait + 1000*2_000_000
		ca.update([]nodeSearchSample{makeTestSample(c1, 3000, highWait, 13)})
		require.Less(t, ca.cwnd.Load(), grownCwnd,
			"congestion should halve cwnd")
	})

	t.Run("multi-node aggregation", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")
		c2 := newConn("n2")
		c3 := newConn("n3")

		// Poll 1: baselines for all 3 nodes.
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 1000, 50000, 13),
			makeTestSample(c2, 1000, 50000, 13),
			makeTestSample(c3, 1000, 50000, 13),
		})

		// Poll 2: all nodes healthy, low wait -> slow start.
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 2000, 60000, 13),
			makeTestSample(c2, 2000, 60000, 13),
			makeTestSample(c3, 2000, 60000, 13),
		})
		cwnd := ca.cwnd.Load()
		require.Positive(t, cwnd)

		// maxCwnd should be the largest single node's pool size (not the sum).
		ca.mu.Lock()
		maxCwnd := ca.mu.maxCwnd
		ca.mu.Unlock()
		require.Equal(t, int32(13), maxCwnd, "cluster maxCwnd = max of per-node maxCwnds")
	})

	t.Run("node churn: new node skips first delta", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")
		c2 := newConn("n2")

		// Poll 1: baseline for c1 only.
		ca.update([]nodeSearchSample{makeTestSample(c1, 1000, 50000, 13)})

		// Poll 2: c1 has completions, c2 is new (no baseline).
		// Only c1's delta should count.
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 2000, 60000, 13),
			makeTestSample(c2, 5000, 90000, 13),
		})
		cwnd := ca.cwnd.Load()
		require.Positive(t, cwnd, "c1 delta should drive AIMD")

		// Verify c2 is now tracked.
		ca.mu.Lock()
		_, hasC2 := ca.mu.nodes[c2]
		ca.mu.Unlock()
		require.True(t, hasC2, "c2 should be in the epoch map after first appearance")
	})

	t.Run("node churn: disappeared node is evicted", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")
		c2 := newConn("n2")

		// Poll 1: baselines for both.
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 1000, 50000, 13),
			makeTestSample(c2, 1000, 50000, 13),
		})

		// Poll 2: only c1 polled (c2 disappeared).
		ca.update([]nodeSearchSample{makeTestSample(c1, 2000, 60000, 13)})

		ca.mu.Lock()
		_, hasC2 := ca.mu.nodes[c2]
		nodeCount := len(ca.mu.nodes)
		ca.mu.Unlock()
		require.False(t, hasC2, "disappeared node should be evicted")
		require.Equal(t, 1, nodeCount)
	})

	t.Run("single node is same as per-node", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")

		// Baseline.
		ca.update([]nodeSearchSample{makeTestSample(c1, 100, 50000, 13)})

		// Low wait -> grow.
		ca.update([]nodeSearchSample{makeTestSample(c1, 200, 60000, 13)})
		require.Positive(t, ca.cwnd.Load())

		// maxCwnd = single node's maxCwnd.
		ca.mu.Lock()
		maxCwnd := ca.mu.maxCwnd
		ca.mu.Unlock()
		require.Equal(t, int32(13), maxCwnd)
	})

	t.Run("cwnd capped at cluster maxCwnd", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")

		// Baseline.
		ca.update([]nodeSearchSample{makeTestSample(c1, 100, 0, 5)})

		// Drive cwnd up with many low-wait polls.
		for i := int64(1); i <= 20; i++ {
			ca.update([]nodeSearchSample{makeTestSample(c1, 100+i*1000, i*1000, 5)})
		}

		// maxCwnd for 1 node with maxCwnd=5 -> cluster maxCwnd=5.
		cwnd := ca.cwnd.Load()
		require.LessOrEqual(t, cwnd, int32(5),
			"cluster cwnd should not exceed cluster maxCwnd (max of per-node maxCwnds)")
	})

	t.Run("node with zero delta completed is skipped", func(t *testing.T) {
		t.Parallel()
		var ca clusterSearchAIMD
		c1 := newConn("n1")
		c2 := newConn("n2")

		// Poll 1: baselines for both.
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 1000, 50000, 13),
			makeTestSample(c2, 1000, 50000, 13),
		})

		// Poll 2: c1 has new completions, c2 has same completed count (deltaCompleted <= 0).
		ca.update([]nodeSearchSample{
			makeTestSample(c1, 2000, 60000, 13),
			makeTestSample(c2, 1000, 50000, 13), // no change
		})

		// Should still grow from c1's contribution alone.
		require.Positive(t, ca.cwnd.Load(), "c1 delta should drive AIMD even when c2 has zero delta")
	})
}

// --- poolRegistry.get tests ---

func TestPoolRegistryGet(t *testing.T) {
	t.Parallel()

	t.Run("empty name returns default pool", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		pc := reg.get("")
		require.Equal(t, &reg.defaultPool, pc, "get('') should return default pool")
	})

	t.Run("unknown named pool returns nil", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		require.Nil(t, reg.get("nonexistent"))
	})

	t.Run("known named pool returns it", func(t *testing.T) {
		t.Parallel()
		var reg poolRegistry
		created := reg.getOrCreate("search")
		require.Equal(t, created, reg.get("search"))
	})
}

// --- debugLogger coverage for pool congestion ---

func TestSetMaxCwnd_DebugLogging(t *testing.T) {
	enableTestDebugLogger(t)

	var reg poolRegistry
	reg.setMaxCwnd("search", 13)

	// The logging path is now exercised. Verify the pool was still set correctly.
	pc := reg.get("search")
	require.NotNil(t, pc)
	pc.mu.Lock()
	require.Equal(t, int32(13), pc.mu.maxCwnd)
	pc.mu.Unlock()
}

func TestUpdatePoolCongestion_DebugLogging(t *testing.T) {
	enableTestDebugLogger(t)

	conn := &Connection{Name: "test-conn"}

	// First poll: establish baselines.
	updatePoolCongestion(conn, map[string]ThreadPoolStats{
		"search": {Completed: 100},
	})

	// Second poll: completions increased, cwnd will change (triggers debug log).
	updatePoolCongestion(conn, map[string]ThreadPoolStats{
		"search": {Completed: 200},
	})

	pc := conn.pools.get("search")
	require.NotNil(t, pc)
}

func TestClusterSearchAIMD_DebugLogging(t *testing.T) {
	enableTestDebugLogger(t)

	u, _ := url.Parse("http://n1:9200")
	c1 := &Connection{URL: u, ID: "n1"}

	var ca clusterSearchAIMD

	// Poll 1: baseline.
	ca.update([]nodeSearchSample{makeTestSample(c1, 100, 0, 13)})

	// Poll 2: slow start (debug log for cwnd change).
	ca.update([]nodeSearchSample{makeTestSample(c1, 200, 1000, 13)})
	require.Positive(t, ca.cwnd.Load())

	// Drive cwnd up to ssthresh so we hit congestion avoidance.
	for i := int64(3); i <= 20; i++ {
		ca.update([]nodeSearchSample{makeTestSample(c1, i*100, i*1000, 13)})
	}

	// Now trigger congestion (high wait time).
	prevWait := int64(20 * 1000)
	highWait := prevWait + 1000*2_000_000
	ca.update([]nodeSearchSample{makeTestSample(c1, 2100, highWait, 13)})
}

// testDebugLogger implements the DebuggingLogger interface for test coverage.
type testDebugLogger struct{}

func (l *testDebugLogger) Log(_ ...any) error            { return nil }
func (l *testDebugLogger) Logf(_ string, _ ...any) error { return nil }

// enableTestDebugLogger sets debugLogger to a no-op testDebugLogger exactly
// once for the lifetime of the test process. This avoids data races that
// arise when individual tests save/restore the package-level global while
// background goroutines from parallel tests are still reading it.
var initTestDebugLogger = sync.OnceFunc(func() {
	debugLogger = &testDebugLogger{}
})

func enableTestDebugLogger(t *testing.T) {
	t.Helper()
	initTestDebugLogger()
}
