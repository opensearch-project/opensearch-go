// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- E2E test helpers -------------------------------------------------------

// e2eTestConn creates a Connection suitable for the full
// Eval -> affinityScore -> recordCPUTime feedback loop.
//
// The connection is initialized with:
//   - URL, URLString, ID, Name (needed by shardNodeInfoFor)
//   - rttRing filled with the given RTT
//   - allocatedProcessors (needed by recordCPUTime normalization)
//   - affinityCounter.clock set to the shared testClock
//   - lcActive state so affinityPool.Next() accepts it
func e2eTestConn(t *testing.T, name string, rtt time.Duration, processors int, clk *testClock) *Connection {
	t.Helper()
	u := &url.URL{Scheme: "https", Host: name + ":9200"}
	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        name,
		Name:      name,
		Roles:     make(roleSet),
		rttRing:   newRTTRing(4),
	}
	for range 4 {
		c.rttRing.add(rtt)
	}
	c.allocatedProcessors.Store(int32(processors)) //nolint:gosec // test value
	c.affinityCounter.clock = clk
	c.state.Store(int64(newConnState(lcActive)))
	return c
}

// e2eResult collects per-node selection counts during the measurement phase.
type e2eResult struct {
	wins map[string]int // node name -> selection count
}

// runAffinityE2E exercises the full production code path in a loop:
//
//  1. clk.Advance(interval) -- simulates time between requests
//  2. policy.Eval(ctx, req) -- real Eval, real rendezvousTopK, real affinityScore
//  3. pool.Next() -- get winning connection
//  4. winner.recordCPUTime(RTTMedian + serverTime) -- real baseline subtraction
//  5. Count wins during measurement phase
//
// serverTime is the constant server-side processing time. Each winner's
// requestDuration is computed as conn.RTTMedian() + serverTime, matching
// what the client would observe: wire time + on-server time. recordCPUTime
// then subtracts RTTMedian to recover serverTime, so the cost estimate is
// correct regardless of the winner's RTT tier.
func runAffinityE2E(
	t *testing.T,
	policy *IndexAffinityPolicy,
	index string,
	serverTime time.Duration,
	clk *testClock,
	interval time.Duration,
	warmup, measure int,
) e2eResult {
	t.Helper()

	ctx := context.Background()
	req := &http.Request{
		URL: &url.URL{Path: "/" + index + "/_search"},
	}

	result := e2eResult{wins: make(map[string]int)}

	for iter := range warmup + measure {
		clk.Advance(interval)

		pool, err := policy.Eval(ctx, req)
		require.NoError(t, err, "Eval iteration %d", iter)
		require.NotNil(t, pool, "Eval returned nil pool at iteration %d", iter)

		conn, err := pool.Next()
		require.NoError(t, err, "pool.Next iteration %d", iter)
		require.NotNil(t, conn, "pool.Next returned nil conn at iteration %d", iter)

		// Simulate the client-observed duration: wire time + server processing.
		requestDuration := conn.RTTMedian() + serverTime
		conn.recordCPUTime(requestDuration)

		if iter >= warmup {
			result.wins[conn.Name]++
		}
	}

	return result
}

// verifyE2EDistribution checks that the observed traffic distribution matches
// expected fractions within tolerance. expectedFrac maps node name to the
// expected fraction of total traffic (values should sum to ~1.0).
func verifyE2EDistribution(t *testing.T, result e2eResult, measure int, expectedFrac map[string]float64, tol float64) {
	t.Helper()

	parts := make([]string, 0, len(expectedFrac))
	for name, expected := range expectedFrac {
		actual := float64(result.wins[name]) / float64(measure)
		parts = append(parts, fmt.Sprintf("%s: expected=%.1f%% actual=%.1f%%",
			name, expected*100, actual*100))
	}
	t.Log(strings.Join(parts, "  |  "))

	for name, expected := range expectedFrac {
		actual := float64(result.wins[name]) / float64(measure)
		require.InDelta(t, expected, actual, tol,
			"%s: expected %.1f%%, got %.1f%%", name, expected*100, actual*100)
	}
}

// newE2EPolicy creates an IndexAffinityPolicy configured for E2E testing
// with the given connections registered via DiscoveryUpdate.
func newE2EPolicy(t *testing.T, conns []*Connection) *IndexAffinityPolicy {
	t.Helper()
	policy := NewIndexAffinityPolicy(indexSlotCacheConfig{
		minFanOut:    1,
		maxFanOut:    32,
		decayFactor:  defaultDecayFactor,
		fanOutPerReq: defaultFanOutPerRequest,
	})
	err := policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)
	return policy
}

// forceFanOut sets the shard-node floor for an index slot, ensuring
// effectiveFanOut returns at least k. effectiveFanOut recomputes K on
// every Eval from max(minFanOut, shardFloor, rateFanOut); it does NOT
// read slot.fanOut. Setting shardNodes provides the floor that guarantees
// all k nodes appear in the candidate set.
func forceFanOut(policy *IndexAffinityPolicy, index string, k int) {
	slot := policy.cache.getOrCreate(index)
	slot.shardNodes.Store(int32(k)) //nolint:gosec // test value
}

// setShardPlacement installs per-node shard placement data on an index slot.
// This sets shardNodeNames (used by rendezvousTopK for the shard/non-shard
// partition and by affinityScore for shard cost lookup) but does NOT set
// shardNodes (the shard floor for effectiveFanOut). Use forceFanOut to
// control the effective fan-out independently.
func setShardPlacement(policy *IndexAffinityPolicy, index string, placement map[string]*shardNodeInfo) {
	slot := policy.cache.getOrCreate(index)
	slot.shardNodeNames.Store(&placement)
}

// setSlotClock injects a testClock into the index slot for deterministic
// MIAD smoothedMaxBucket updates.
func setSlotClock(policy *IndexAffinityPolicy, index string, clk *testClock) {
	slot := policy.cache.getOrCreate(index)
	slot.clock = clk
}

// --- E2E test scenarios -----------------------------------------------------

// TestAffinityE2E_SameTier_EqualWP validates the basic feedback loop: 3 nodes
// at the same RTT bucket with equal shard cost should receive equal traffic.
func TestAffinityE2E_SameTier_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index      = "same-tier-orders"
		warmup     = 3000
		measure    = 6000
		tol        = 0.05
		interval   = 100 * time.Millisecond
		serverTime = 5 * time.Millisecond
	)

	clk := newTestClock()
	rtt := 1 * time.Millisecond // bucket 9
	processors := 8

	conns := []*Connection{
		e2eTestConn(t, "node-a", rtt, processors, clk),
		e2eTestConn(t, "node-b", rtt, processors, clk),
		e2eTestConn(t, "node-c", rtt, processors, clk),
	}

	policy := newE2EPolicy(t, conns)
	setSlotClock(policy, index, clk)
	forceFanOut(policy, index, len(conns))

	// All replica shards — equal shard cost.
	placement := map[string]*shardNodeInfo{
		"node-a": {Replicas: 1},
		"node-b": {Replicas: 1},
		"node-c": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runAffinityE2E(t, policy, index, serverTime, clk, interval, warmup, measure)

	verifyE2EDistribution(t, result, measure, map[string]float64{
		"node-a": 1.0 / 3.0,
		"node-b": 1.0 / 3.0,
		"node-c": 1.0 / 3.0,
	}, tol)
}

// TestAffinityE2E_CrossAZ_EqualWP validates that bucket normalization in
// recordCPUTime cancels the rttBucket multiplier in affinityScore, producing
// equal distribution across 2 RTT tiers despite a 16x RTT gap.
func TestAffinityE2E_CrossAZ_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index      = "cross-az-orders"
		warmup     = 3000
		measure    = 6000
		tol        = 0.05
		interval   = 100 * time.Millisecond
		serverTime = 5 * time.Millisecond
	)

	clk := newTestClock()
	processors := 8

	localRTT := 256 * time.Microsecond   // bucket 8
	remoteRTT := 4096 * time.Microsecond // bucket 12

	conns := []*Connection{
		e2eTestConn(t, "local-1", localRTT, processors, clk),
		e2eTestConn(t, "local-2", localRTT, processors, clk),
		e2eTestConn(t, "remote-1", remoteRTT, processors, clk),
	}

	policy := newE2EPolicy(t, conns)
	setSlotClock(policy, index, clk)
	forceFanOut(policy, index, len(conns))

	placement := map[string]*shardNodeInfo{
		"local-1":  {Replicas: 1},
		"local-2":  {Replicas: 1},
		"remote-1": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runAffinityE2E(t, policy, index, serverTime, clk, interval, warmup, measure)

	verifyE2EDistribution(t, result, measure, map[string]float64{
		"local-1":  1.0 / 3.0,
		"local-2":  1.0 / 3.0,
		"remote-1": 1.0 / 3.0,
	}, tol)
}

// TestAffinityE2E_ThreeTiers_EqualWP validates cost normalization across
// three RTT tiers spanning a 128x range (256us to 32768us).
func TestAffinityE2E_ThreeTiers_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index      = "three-tier-orders"
		warmup     = 5000
		measure    = 10000
		tol        = 0.05
		interval   = 100 * time.Millisecond
		serverTime = 10 * time.Millisecond
	)

	clk := newTestClock()
	processors := 8

	az1RTT := 256 * time.Microsecond   // bucket 8
	az2RTT := 4096 * time.Microsecond  // bucket 12
	az3RTT := 32768 * time.Microsecond // bucket 15 (32ms)

	conns := []*Connection{
		e2eTestConn(t, "az1", az1RTT, processors, clk),
		e2eTestConn(t, "az2", az2RTT, processors, clk),
		e2eTestConn(t, "az3", az3RTT, processors, clk),
	}

	policy := newE2EPolicy(t, conns)
	setSlotClock(policy, index, clk)
	forceFanOut(policy, index, len(conns))

	placement := map[string]*shardNodeInfo{
		"az1": {Replicas: 1},
		"az2": {Replicas: 1},
		"az3": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runAffinityE2E(t, policy, index, serverTime, clk, interval, warmup, measure)

	verifyE2EDistribution(t, result, measure, map[string]float64{
		"az1": 1.0 / 3.0,
		"az2": 1.0 / 3.0,
		"az3": 1.0 / 3.0,
	}, tol)
}

// TestAffinityE2E_CrossAZ_MixedWP validates that the shard cost multiplier
// correctly steers traffic: a primary node (wp=2.0) should get ~33% and a
// replica node (wp=1.0) should get ~67%, proportional to 1/wp.
func TestAffinityE2E_CrossAZ_MixedWP(t *testing.T) {
	t.Parallel()

	const (
		index      = "mixed-wp-orders"
		warmup     = 5000
		measure    = 10000
		interval   = 100 * time.Millisecond
		serverTime = 5 * time.Millisecond
	)

	clk := newTestClock()
	processors := 8

	localRTT := 1 * time.Millisecond     // bucket 9
	remoteRTT := 4096 * time.Microsecond // bucket 12

	conns := []*Connection{
		e2eTestConn(t, "local-primary", localRTT, processors, clk),
		e2eTestConn(t, "remote-replica", remoteRTT, processors, clk),
	}

	policy := newE2EPolicy(t, conns)
	setSlotClock(policy, index, clk)
	forceFanOut(policy, index, len(conns))

	placement := map[string]*shardNodeInfo{
		"local-primary":  {Primaries: 1},
		"remote-replica": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runAffinityE2E(t, policy, index, serverTime, clk, interval, warmup, measure)

	// Expected: rate proportional to 1/wp.
	// primary wp=2.0 -> 1/2.0 = 0.5
	// replica wp=1.0 -> 1/1.0 = 1.0
	// total = 1.5 -> primary=33.3%, replica=66.7%
	verifyE2EDistribution(t, result, measure, map[string]float64{
		"local-primary":  1.0 / 3.0,
		"remote-replica": 2.0 / 3.0,
	}, 0.06)
}

// TestAffinityE2E_HeterogeneousProcessors validates that /processors
// normalization in recordCPUTime routes traffic proportionally to core count.
// Node B (8 cores) should get ~80% of traffic vs Node A (2 cores, ~20%).
func TestAffinityE2E_HeterogeneousProcessors(t *testing.T) {
	t.Parallel()

	const (
		index      = "hetero-proc-orders"
		warmup     = 5000
		measure    = 10000
		tol        = 0.05
		interval   = 50 * time.Millisecond
		serverTime = 5 * time.Millisecond
	)

	clk := newTestClock()
	rtt := 1 * time.Millisecond // same RTT bucket for both

	conns := []*Connection{
		e2eTestConn(t, "small", rtt, 2, clk), // 2 cores
		e2eTestConn(t, "large", rtt, 8, clk), // 8 cores
	}

	policy := newE2EPolicy(t, conns)
	setSlotClock(policy, index, clk)
	forceFanOut(policy, index, len(conns))

	placement := map[string]*shardNodeInfo{
		"small": {Replicas: 1},
		"large": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runAffinityE2E(t, policy, index, serverTime, clk, interval, warmup, measure)

	// Expected: rate proportional to processor count (inverse cost).
	// small: cost = serverTime / 2 -> high cost -> fewer requests
	// large: cost = serverTime / 8 -> low cost -> more requests
	// Ratio: large/small = 8/2 = 4, so large gets 4/(4+1)=80%, small gets 20%.
	verifyE2EDistribution(t, result, measure, map[string]float64{
		"small": 2.0 / 10.0,
		"large": 8.0 / 10.0,
	}, tol)
}

// TestAffinityE2E_FanOutExpansion validates that fan-out growth and tier-span
// equalization work together. Initially K is small so only local nodes see
// traffic. As request volume grows, K expands to include remote nodes, and
// equalization ensures traffic distributes across all tiers.
func TestAffinityE2E_FanOutExpansion(t *testing.T) {
	t.Parallel()

	const (
		index      = "fanout-orders"
		interval   = 10 * time.Millisecond
		serverTime = 5 * time.Millisecond
	)

	clk := newTestClock()
	processors := 8

	localRTT := 256 * time.Microsecond   // bucket 8
	remoteRTT := 4096 * time.Microsecond // bucket 12

	conns := []*Connection{
		e2eTestConn(t, "local-1", localRTT, processors, clk),
		e2eTestConn(t, "local-2", localRTT, processors, clk),
		e2eTestConn(t, "local-3", localRTT, processors, clk),
		e2eTestConn(t, "remote-1", remoteRTT, processors, clk),
		e2eTestConn(t, "remote-2", remoteRTT, processors, clk),
		e2eTestConn(t, "remote-3", remoteRTT, processors, clk),
	}

	// Use a low fanOutPerReq so K grows with moderate request volume.
	// With decay=0.999 and rate=1 per getOrCreate call, requestDecay
	// approaches steady state ~1000. K = counter/50 + 1, clamped to
	// activeNodeCount. So K grows: 1 -> 2 -> ... -> 6 over ~250 requests.
	policy := NewIndexAffinityPolicy(indexSlotCacheConfig{
		minFanOut:    1,
		maxFanOut:    32,
		decayFactor:  defaultDecayFactor,
		fanOutPerReq: 50, // K grows by 1 per ~50 concurrent-equivalent requests
	})
	err := policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)
	setSlotClock(policy, index, clk)

	// Set shard node names for the shard/non-shard partition in rendezvousTopK,
	// but do NOT set shardNodes (the shard floor) so rateFanOut drives K growth.
	placement := map[string]*shardNodeInfo{
		"local-1":  {Replicas: 1},
		"local-2":  {Replicas: 1},
		"local-3":  {Replicas: 1},
		"remote-1": {Replicas: 1},
		"remote-2": {Replicas: 1},
		"remote-3": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	ctx := context.Background()
	req := &http.Request{
		URL: &url.URL{Path: "/" + index + "/_search"},
	}

	// Phase 1: Run with natural fan-out (starts at K=1).
	// Count how many unique nodes receive traffic initially.
	phase1Wins := make(map[string]int)
	for range 200 {
		clk.Advance(interval)
		pool, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, pool)
		conn, nextErr := pool.Next()
		require.NoError(t, nextErr)
		requestDuration := conn.RTTMedian() + serverTime
		conn.recordCPUTime(requestDuration)
		phase1Wins[conn.Name]++
	}

	// With K starting small and shard nodes filling by nearest tier first,
	// local nodes should dominate early traffic.
	localTraffic := phase1Wins["local-1"] + phase1Wins["local-2"] + phase1Wins["local-3"]
	remoteTraffic := phase1Wins["remote-1"] + phase1Wins["remote-2"] + phase1Wins["remote-3"]
	t.Logf("phase1: local=%d remote=%d (K should be small initially)", localTraffic, remoteTraffic)
	require.Greater(t, localTraffic, remoteTraffic,
		"with low K, local nodes should receive more traffic than remote")

	// Phase 2: Continue sending requests so requestDecay counter grows.
	// After enough iterations, K should expand to include remote nodes.
	for range 2000 {
		clk.Advance(interval)
		pool, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, pool)
		conn, nextErr := pool.Next()
		require.NoError(t, nextErr)
		requestDuration := conn.RTTMedian() + serverTime
		conn.recordCPUTime(requestDuration)
	}

	// Verify K has grown by calling effectiveFanOut (the actual computation
	// used by Eval). slot.fanOut is only written by updateFromDiscovery,
	// not by Eval, so reading it directly would be misleading.
	slot := policy.cache.slotFor(index)
	require.NotNil(t, slot)
	effectiveK := policy.cache.effectiveFanOut(slot, index, len(conns))
	t.Logf("phase2: effectiveFanOut=%d (requestDecay=%.1f)", effectiveK, slot.requestDecay.load())
	require.GreaterOrEqual(t, effectiveK, 4,
		"fan-out should grow to include remote tier after sustained traffic")

	// Phase 3: Force K = len(conns) via shard floor and measure distribution.
	forceFanOut(policy, index, len(conns))

	phase3Wins := make(map[string]int)
	const phase3Warmup = 5000
	const phase3Measure = 10000
	for iter := range phase3Warmup + phase3Measure {
		clk.Advance(interval)
		pool, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, pool)
		conn, nextErr := pool.Next()
		require.NoError(t, nextErr)
		requestDuration := conn.RTTMedian() + serverTime
		conn.recordCPUTime(requestDuration)
		if iter >= phase3Warmup {
			phase3Wins[conn.Name]++
		}
	}

	// With K = all nodes, equal wp, equal processors: equal distribution.
	result := e2eResult{wins: phase3Wins}
	expected := make(map[string]float64, len(conns))
	frac := 1.0 / float64(len(conns))
	for _, c := range conns {
		expected[c.Name] = frac
	}
	verifyE2EDistribution(t, result, phase3Measure, expected, 0.05)
}

// TestAffinityE2E_RecordCPUTimeBaseline validates that recordCPUTime correctly
// subtracts the RTT median baseline, so the scored cost reflects server
// processing time, not wire time.
func TestAffinityE2E_RecordCPUTimeBaseline(t *testing.T) {
	t.Parallel()

	clk := newTestClock()
	rtt := 1 * time.Millisecond // bucket 9, RTTMedian = 512us (2^9)
	processors := 4

	conn := e2eTestConn(t, "node", rtt, processors, clk)

	// Request takes 5ms total. RTTMedian for bucket 9 = 512us.
	// serverTime = 5ms - 512us = 4488us
	// cpuNanos = 4488us / 4 processors = 1122us
	// cost = 1122 / bucket(9) = 124.67 microseconds
	requestDur := 5 * time.Millisecond
	clk.Advance(100 * time.Millisecond)
	conn.recordCPUTime(requestDur)

	load := conn.affinityCounter.load()
	require.Greater(t, load, 0.0, "affinity counter should be positive after recordCPUTime")

	// Compute expected cost manually.
	baseline := conn.RTTMedian()
	serverTimeNanos := int64(requestDur - baseline)
	cpuNanos := serverTimeNanos / int64(processors)
	cpuMicros := float64(cpuNanos / 1000)
	bucket := float64(conn.rttRing.medianBucket())
	expectedCost := cpuMicros / bucket

	require.InDelta(t, expectedCost, load, 1.0,
		"affinity counter should reflect (requestDur-baseline)/processors/bucket in micros")

	// A second recordCPUTime should add more load (time-weighted).
	clk.Advance(100 * time.Millisecond)
	conn.recordCPUTime(requestDur)

	load2 := conn.affinityCounter.load()
	require.Greater(t, load2, 0.0, "load should remain positive")

	// With 100ms gap and 5s half-life, first load decays by factor
	// exp(-lambda * 0.1) ~ 0.9862, plus new cost.
	decayFactor := math.Exp(-affinityDecayLambda * 0.1)
	expectedLoad2 := load*decayFactor + expectedCost
	require.InDelta(t, expectedLoad2, load2, 1.0,
		"second recordCPUTime should add decayed prior + new cost")
}
