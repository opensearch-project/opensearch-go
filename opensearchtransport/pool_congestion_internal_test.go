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
