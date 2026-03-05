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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- E2E test helpers -------------------------------------------------------

// e2eTestConn creates a Connection suitable for the full
// Eval -> calcConnScore -> recordCPUTime feedback loop.
//
// The connection is initialized with:
//   - URL, URLString, ID, Name (needed by shardNodeInfoFor)
//   - rttRing filled with the given RTT
//   - allocatedProcessors (needed by recordCPUTime normalization)
//   - estLoad.clock set to the shared testClock
//   - lcActive state so connScoreSelect accepts it
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
	c.estLoad.clock = clk
	c.state.Store(int64(newConnState(lcActive)))
	return c
}

// e2eResult collects per-node selection counts during the measurement phase.
type e2eResult struct {
	wins map[string]int // node name -> selection count
}

// runRouterE2E exercises the full production code path in a loop:
//
//  1. clk.Advance(interval) -- simulates time between requests
//  2. policy.Eval(ctx, req) -- real Eval, real rendezvousTopK, real calcConnScore -> NextHop
//  3. hop.Conn -- get winning connection
//  4. Count wins during measurement phase
//
// With cwnd-based scoring, the selection formula is rtt * (inFlight+1)/cwnd * shardCost.
// In sequential (non-concurrent) tests, inFlight is always 0 and cwnd is the same
// default for all nodes, so the effective score is rttBucket * (1/cwnd) * shardCost --
// purely RTT and shardCost driven.
func runRouterE2E(
	t *testing.T,
	policy *IndexRouter,
	index string,
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

		hop, err := policy.Eval(ctx, req)
		require.NoError(t, err, "Eval iteration %d", iter)
		require.NotNil(t, hop.Conn, "Eval returned nil conn at iteration %d", iter)

		conn := hop.Conn

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

// newE2EPolicy creates an IndexRouter configured for E2E testing
// with the given connections registered via DiscoveryUpdate.
func newE2EPolicy(t *testing.T, conns []*Connection) *IndexRouter {
	t.Helper()
	pir := &atomic.Bool{}
	pir.Store(true) // E2E tests use post-quorum pool data
	policy := NewIndexRouter(indexSlotCacheConfig{
		minFanOut:    1,
		maxFanOut:    32,
		decayFactor:  defaultDecayFactor,
		fanOutPerReq: defaultFanOutPerRequest,
	})
	err := policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)
	policy.config.poolInfoReady = pir
	return policy
}

// forceFanOut sets the shard-node floor for an index slot, ensuring
// effectiveFanOut returns at least k. effectiveFanOut recomputes K on
// every Eval from max(minFanOut, shardFloor, rateFanOut); it does NOT
// read slot.fanOut. Setting shardNodeCount provides the floor that guarantees
// all k nodes appear in the candidate set.
func forceFanOut(policy *IndexRouter, index string, k int) {
	slot := policy.cache.getOrCreate(index)
	slot.shardNodeCount.Store(int32(k)) //nolint:gosec // test value
}

// setShardPlacement installs per-node shard placement data on an index slot.
// This sets shardNodeNames (used by rendezvousTopK for the shard/non-shard
// partition and by calcConnScore for shard cost lookup) but does NOT set
// shardNodeCount (the shard floor for effectiveFanOut). Use forceFanOut to
// control the effective fan-out independently.
func setShardPlacement(policy *IndexRouter, index string, placement map[string]*shardNodeInfo) {
	slot := policy.cache.getOrCreate(index)
	slot.shardNodeNames.Store(&placement)
}

// setSlotClock injects a testClock into the index slot for deterministic
// MIAD smoothedMaxBucket updates.
func setSlotClock(policy *IndexRouter, index string, clk *testClock) {
	slot := policy.cache.getOrCreate(index)
	slot.clock = clk
}

// --- E2E test scenarios -----------------------------------------------------

// TestRouterE2E_SameTier_EqualWP validates the basic feedback loop: 3 nodes
// at the same RTT bucket with equal shard cost should receive equal traffic.
func TestRouterE2E_SameTier_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index    = "same-tier-orders"
		warmup   = 3000
		measure  = 6000
		tol      = 0.05
		interval = 100 * time.Millisecond
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

	// All replica shards --equal shard cost.
	placement := map[string]*shardNodeInfo{
		"node-a": {Replicas: 1},
		"node-b": {Replicas: 1},
		"node-c": {Replicas: 1},
	}
	setShardPlacement(policy, index, placement)

	result := runRouterE2E(t, policy, index, clk, interval, warmup, measure)

	verifyE2EDistribution(t, result, measure, map[string]float64{
		"node-a": 1.0 / 3.0,
		"node-b": 1.0 / 3.0,
		"node-c": 1.0 / 3.0,
	}, tol)
}

// TestRouterE2E_CrossAZ_EqualWP validates that with cwnd-based scoring,
// lower-RTT nodes always win. Two local nodes (bucket 8) beat one remote
// node (bucket 12) because rttBucket * (1/cwnd) * shardCost is lower for
// the local tier. Jitter rotation distributes traffic evenly across the
// two local nodes (~50% each), while the remote gets ~0%.
func TestRouterE2E_CrossAZ_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index    = "cross-az-orders"
		warmup   = 3000
		measure  = 6000
		interval = 100 * time.Millisecond
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

	result := runRouterE2E(t, policy, index, clk, interval, warmup, measure)

	// With cwnd scoring, both local nodes always beat the remote node because
	// rttBucket is lower. The exact local-1 vs local-2 split depends on
	// rendezvous hashing and jitter rotation, so we only assert:
	//   - remote gets ~0% traffic
	//   - both locals together get ~100% traffic
	local1Frac := float64(result.wins["local-1"]) / float64(measure)
	local2Frac := float64(result.wins["local-2"]) / float64(measure)
	remoteFrac := float64(result.wins["remote-1"]) / float64(measure)

	t.Logf("local-1=%.1f%% local-2=%.1f%% remote-1=%.1f%%",
		local1Frac*100, local2Frac*100, remoteFrac*100)

	require.InDelta(t, 0.0, remoteFrac, 0.05,
		"remote node should receive ~0%% traffic")
	require.InDelta(t, 1.0, local1Frac+local2Frac, 0.05,
		"local nodes together should receive ~100%% traffic")
	require.Greater(t, local1Frac, 0.0,
		"local-1 should receive some traffic")
	require.Greater(t, local2Frac, 0.0,
		"local-2 should receive some traffic")
}

// TestRouterE2E_ThreeTiers_EqualWP validates that with cwnd-based scoring,
// the lowest RTT tier always wins. With three nodes at buckets 8, 12, and 15,
// az1 (bucket 8) always has the lowest score and receives 100% of traffic.
func TestRouterE2E_ThreeTiers_EqualWP(t *testing.T) {
	t.Parallel()

	const (
		index    = "three-tier-orders"
		warmup   = 5000
		measure  = 10000
		tol      = 0.05
		interval = 100 * time.Millisecond
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

	result := runRouterE2E(t, policy, index, clk, interval, warmup, measure)

	verifyE2EDistribution(t, result, measure, map[string]float64{
		"az1": 1.0,
		"az2": 0.0,
		"az3": 0.0,
	}, tol)
}

// TestRouterE2E_CrossAZ_MixedWP validates that shard cost can override
// the RTT advantage. Local-primary (bucket 9, shardCost 2.0) scores
// 9*(1/cwnd)*2.0 = 18/cwnd, while remote-replica (bucket 12, shardCost 1.0)
// scores 12*(1/cwnd)*1.0 = 12/cwnd. The remote wins because 12 < 18.
func TestRouterE2E_CrossAZ_MixedWP(t *testing.T) {
	t.Parallel()

	const (
		index    = "mixed-wp-orders"
		warmup   = 5000
		measure  = 10000
		interval = 100 * time.Millisecond
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

	result := runRouterE2E(t, policy, index, clk, interval, warmup, measure)

	// Expected: remote-replica wins because its cwnd score is lower.
	// local-primary:  score = 9 * (1/cwnd) * 2.0 = 18/cwnd
	// remote-replica: score = 12 * (1/cwnd) * 1.0 = 12/cwnd
	// Remote always wins (12 < 18), so remote gets ~100%.
	verifyE2EDistribution(t, result, measure, map[string]float64{
		"local-primary":  0.0,
		"remote-replica": 1.0,
	}, 0.05)
}

// TestRouterE2E_HeterogeneousProcessors validates that processor count
// does not affect routing under cwnd-based scoring. With the same RTT bucket
// and same shard cost, both nodes have equal scores regardless of processor
// count, so jitter rotation distributes traffic evenly (~50/50).
func TestRouterE2E_HeterogeneousProcessors(t *testing.T) {
	t.Parallel()

	const (
		index    = "hetero-proc-orders"
		warmup   = 5000
		measure  = 10000
		tol      = 0.08
		interval = 50 * time.Millisecond
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

	result := runRouterE2E(t, policy, index, clk, interval, warmup, measure)

	// Expected: equal distribution --processor count is not part of cwnd scoring.
	// Same RTT bucket + same shardCost + same cwnd = same score for both nodes.
	// Jitter rotation distributes traffic evenly.
	verifyE2EDistribution(t, result, measure, map[string]float64{
		"small": 0.50,
		"large": 0.50,
	}, tol)
}

// TestRouterE2E_FanOutExpansion validates that fan-out growth works with
// request volume. Initially K is small so only local nodes see traffic. As
// request volume grows, K expands to include remote nodes. With cwnd-based
// scoring (no decay counter feedback), local nodes always win when candidates
// include both tiers because lower RTT produces lower scores.
func TestRouterE2E_FanOutExpansion(t *testing.T) {
	t.Parallel()

	const (
		index    = "fanout-orders"
		interval = 10 * time.Millisecond
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
	policy := NewIndexRouter(indexSlotCacheConfig{
		minFanOut:    1,
		maxFanOut:    32,
		decayFactor:  defaultDecayFactor,
		fanOutPerReq: 50, // K grows by 1 per ~50 concurrent-equivalent requests
	})
	err := policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)
	setSlotClock(policy, index, clk)

	// Set shard node names for the shard/non-shard partition in rendezvousTopK,
	// but do NOT set shardNodeCount (the shard floor) so rateFanOut drives K growth.
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
		hop, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn)
		phase1Wins[hop.Conn.Name]++
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
		hop, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn)
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
	// With cwnd-based scoring and no concurrent in-flight, local nodes
	// (lower RTT bucket) always score better than remote nodes. Verify that
	// local nodes receive the majority of traffic.
	forceFanOut(policy, index, len(conns))

	phase3Wins := make(map[string]int)
	const phase3Measure = 5000
	for range phase3Measure {
		clk.Advance(interval)
		hop, evalErr := policy.Eval(ctx, req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn)
		phase3Wins[hop.Conn.Name]++
	}

	localPhase3 := phase3Wins["local-1"] + phase3Wins["local-2"] + phase3Wins["local-3"]
	t.Logf("phase3: wins=%v local=%d remote=%d",
		phase3Wins, localPhase3, phase3Measure-localPhase3)
	require.Greater(t, localPhase3, phase3Measure*2/3,
		"local nodes (lower RTT bucket) should receive majority of traffic with cwnd scoring")
}

// TestRouterE2E_RecordCPUTimeBaseline validates that recordCPUTime correctly
// subtracts the RTT median baseline, so the scored cost reflects server
// processing time, not wire time.
func TestRouterE2E_RecordCPUTimeBaseline(t *testing.T) {
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

	load := conn.estLoad.load()
	require.Greater(t, load, 0.0, "estimated load should be positive after recordCPUTime")

	// Compute expected cost manually.
	baseline := conn.RTTMedian()
	serverTimeNanos := int64(requestDur - baseline)
	cpuNanos := serverTimeNanos / int64(processors)
	cpuMicros := float64(cpuNanos / 1000)
	bucket := float64(conn.rttRing.medianBucket())
	expectedCost := cpuMicros / bucket

	require.InDelta(t, expectedCost, load, 1.0,
		"estimated load should reflect (requestDur-baseline)/processors/bucket in micros")

	// A second recordCPUTime should add more load (time-weighted).
	clk.Advance(100 * time.Millisecond)
	conn.recordCPUTime(requestDur)

	load2 := conn.estLoad.load()
	require.Greater(t, load2, 0.0, "load should remain positive")

	// With 100ms gap and 5s half-life, first load decays by factor
	// exp(-lambda * 0.1) ~ 0.9862, plus new cost.
	decayFactor := math.Exp(-loadDecayLambda * 0.1)
	expectedLoad2 := load*decayFactor + expectedCost
	require.InDelta(t, expectedLoad2, load2, 1.0,
		"second recordCPUTime should add decayed prior + new cost")
}
