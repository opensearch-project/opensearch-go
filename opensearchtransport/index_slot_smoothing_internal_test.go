// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIndexSlot_UpdateSmoothedMaxBucket_FirstObservation(t *testing.T) {
	slot := &indexSlot{clock: newTestClock()}

	result := slot.updateSmoothedMaxBucket(8.0)
	require.InDelta(t, 8.0, result, 0.01, "first observation should snap to observed value")
	require.InDelta(t, 8.0, slot.loadSmoothedMaxBucket(), 0.01)
}

func TestIndexSlot_UpdateSmoothedMaxBucket_FirstObservation_FloorAtOne(t *testing.T) {
	slot := &indexSlot{clock: newTestClock()}

	result := slot.updateSmoothedMaxBucket(0.5)
	require.InDelta(t, 1.0, result, 0.01, "first observation should be floored at 1.0")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_MI_FastGrowth(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 1 (local-only fan-out).
	slot.updateSmoothedMaxBucket(1.0)

	// Advance 2 seconds (one MI half-life), then observe bucket 8.
	clk.Advance(2 * time.Second)
	result := slot.updateSmoothedMaxBucket(8.0)

	// Gap was 7 (from 1 to 8). After 2s (one MI half-life), ~50% of gap closed.
	// Expected: 8 - 7*0.5 = 4.5
	require.InDelta(t, 4.5, result, 0.5, "MI should close ~50%% of gap in one half-life")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_MI_ConvergesQuickly(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 1.
	slot.updateSmoothedMaxBucket(1.0)

	// After 6 seconds (3 MI half-lives), ~87.5% of gap should close.
	clk.Advance(6 * time.Second)
	result := slot.updateSmoothedMaxBucket(8.0)

	// Expected: 8 - 7*0.125 = 7.125
	require.InDelta(t, 7.125, result, 0.01, "MI should close ~87.5%% of gap in 3 half-lives")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_AD_SlowDecrease(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 8 (remote tier active).
	slot.updateSmoothedMaxBucket(8.0)

	// After 50 seconds at AD rate of 0.03/sec, should decrease by 1.5.
	clk.Advance(50 * time.Second)
	result := slot.updateSmoothedMaxBucket(1.0)

	// Expected: max(8 - 0.03*50, 1.0) = max(6.5, 1.0) = 6.5
	require.InDelta(t, 6.5, result, 0.01, "AD should decrease linearly at 0.03 buckets/sec")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_AD_ClampsToObserved(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 8.
	slot.updateSmoothedMaxBucket(8.0)

	// After 200 seconds at AD rate 0.03/sec: 8 - 6.0 = 2.0, clamped to observed (4.0).
	clk.Advance(200 * time.Second)
	result := slot.updateSmoothedMaxBucket(4.0)

	require.InDelta(t, 4.0, result, 0.01, "AD should clamp to observed value, not overshoot")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_AD_FloorAtOne(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	slot.updateSmoothedMaxBucket(4.0)

	// Long idle period: decrease to floor.
	clk.Advance(200 * time.Second)
	result := slot.updateSmoothedMaxBucket(0.5)

	require.InDelta(t, 1.0, result, 0.01, "AD should not go below floor of 1.0")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_MIAD_Asymmetry(t *testing.T) {
	// Verify that growth is faster than decrease: MIAD, not AIMD.
	clkUp := newTestClock()
	clkDown := newTestClock()
	slotUp := &indexSlot{clock: clkUp}
	slotDown := &indexSlot{clock: clkDown}

	// Growth scenario: 1 -> 8, measured after 2 seconds.
	slotUp.updateSmoothedMaxBucket(1.0)
	clkUp.Advance(2 * time.Second)
	up := slotUp.updateSmoothedMaxBucket(8.0)

	// Decrease scenario: 8 -> 1, measured after 2 seconds.
	slotDown.updateSmoothedMaxBucket(8.0)
	clkDown.Advance(2 * time.Second)
	down := slotDown.updateSmoothedMaxBucket(1.0)

	// Growth should be closer to 8 than decrease is to 1.
	growthFraction := (up - 1.0) / 7.0   // fraction of gap closed upward
	shrinkFraction := (8.0 - down) / 7.0 // fraction of gap closed downward

	require.Greater(t, growthFraction, shrinkFraction,
		"MI growth (%.1f%%) should be faster than AD decrease (%.1f%%)",
		growthFraction*100, shrinkFraction*100)
}

// tierSpanTestConn creates a connection with a known RTT bucket for
// tier-span tests. The rttRing is populated with enough samples for
// the median to stabilize.
func tierSpanTestConn(t *testing.T, host string, rtt time.Duration) *Connection {
	t.Helper()
	conn := &Connection{URLString: host, rttRing: newRTTRing(4)}
	for range 4 {
		conn.rttRing.add(rtt)
	}
	conn.allocatedProcessors.Store(4)
	// Freeze the clock so load() returns exactly what store()/add() wrote.
	conn.estLoad.clock = newTestClock()
	return conn
}

func TestRecordCPUTime_BucketNormalization(t *testing.T) {
	// Verify that cost is divided by rttBucket: connections in higher buckets
	// accrue proportionally less cost per request, which cancels the higher
	// rttBucket multiplier in the scoring formula.
	local := tierSpanTestConn(t, "https://local:9200", 200*time.Microsecond)    // bucket 8
	remote := tierSpanTestConn(t, "https://remote:9200", 2048*time.Microsecond) // bucket 11

	localBucket := float64(local.rttRing.medianBucket())
	remoteBucket := float64(remote.rttRing.medianBucket())

	// Both handle the same 10ms request.
	local.recordCPUTime(10 * time.Millisecond)
	remote.recordCPUTime(10 * time.Millisecond)

	localCounter := local.estLoad.load()
	remoteCounter := remote.estLoad.load()

	// The counters should differ by approximately the inverse bucket ratio.
	// The tolerance is 0.5 (not tighter) because each connection subtracts
	// a different RTTMedian baseline before computing cost:
	//   serverTime = requestDuration - RTTMedian(bucket)
	// Since RTTMedian differs (256us for bucket 8 vs 2048us for bucket 11),
	// the actual ratio diverges from the pure remoteBucket/localBucket ideal.
	expectedRatio := remoteBucket / localBucket
	ratio := localCounter / remoteCounter
	require.InDelta(t, expectedRatio, ratio, 0.5,
		"local (bucket=%.0f) should accrue ~%.1fx more cost than remote (bucket=%.0f)",
		localBucket, expectedRatio, remoteBucket)
}

func TestRecordCPUTime_SameBucketEqualCost(t *testing.T) {
	// Two connections at the same bucket should accrue the same cost.
	conn1 := tierSpanTestConn(t, "https://node1:9200", 200*time.Microsecond) // bucket 8
	conn2 := tierSpanTestConn(t, "https://node2:9200", 200*time.Microsecond) // bucket 8

	conn1.recordCPUTime(10 * time.Millisecond)
	conn2.recordCPUTime(10 * time.Millisecond)

	require.InDelta(t, conn1.estLoad.load(), conn2.estLoad.load(), 0.01,
		"same-bucket connections should accrue equal cost")
}

func TestBucketNormalization_EqualScoresAtEquilibrium(t *testing.T) {
	// Verify the mathematical property: at equilibrium, the scoring formula
	// produces equal scores across tiers when cost is normalized by 1/bucket.
	//
	// score = rttBucket * counter * shardCost
	// counter ~ rate * serverTime / (proc * bucket * lambda)
	// score = rttBucket * rate * serverTime / (proc * bucket * lambda) * sc
	//       = rate * serverTime / (proc * lambda) * sc
	// The bucket terms cancel, so rate = constant at equal scores.

	type tier struct {
		bucket int
		rate   float64 // requests per unit time at equilibrium
	}

	serverTime := 2250.0
	shardCost := shardCostForReads[shardCostReplica]

	tiers := []tier{
		{bucket: 8},  // same-AZ
		{bucket: 11}, // cross-AZ near
		{bucket: 14}, // cross-AZ far
	}

	// At equilibrium, scores equalize. Solve for rate.
	// score = bucket * (rate * serverTime / (proc * bucket * lambda)) * sc
	//       = rate * serverTime / (proc * lambda) * sc
	// rate = K * proc * lambda / (serverTime * sc) -- independent of bucket
	lambda := math.Ln2 / 5.0
	K := 1000000.0 // arbitrary target score
	proc := 8.0

	for i := range tiers {
		tiers[i].rate = K * proc * lambda / (serverTime * shardCost)
	}

	// All rates should be identical (bucket does not appear in the formula).
	for i := 1; i < len(tiers); i++ {
		require.InDelta(t, tiers[0].rate, tiers[i].rate, 0.001,
			"tier %d (bucket=%d) should have same rate as tier 0 (bucket=%d)",
			i, tiers[i].bucket, tiers[0].bucket)
	}

	// Verify scores are equal at these rates.
	scores := make([]float64, len(tiers))
	for i, ti := range tiers {
		costPerReq := serverTime / (proc * float64(ti.bucket))
		steadyCounter := costPerReq * ti.rate / lambda
		scores[i] = float64(ti.bucket) * steadyCounter * shardCost
	}

	for i := 1; i < len(scores); i++ {
		require.InDelta(t, scores[0], scores[i], 0.01,
			"score for tier %d should equal tier 0", i)
	}
}

func TestPolicyChain_SmoothedMaxBucketForIndex(t *testing.T) {
	cache := newIndexSlotCache(indexSlotCacheConfig{})

	// Create a slot and set its smoothed max bucket.
	slot := cache.getOrCreate("orders")
	slot.clock = newTestClock()
	slot.updateSmoothedMaxBucket(8.0)

	policy := &IndexRouter{cache: cache}
	chain := &PolicyChain{policies: []Policy{policy}}

	result := chain.smoothedMaxBucketForIndex("orders")
	require.InDelta(t, 8.0, result, 0.01, "should return slot's smoothed max bucket")

	result = chain.smoothedMaxBucketForIndex("nonexistent")
	require.InDelta(t, 0.0, result, 0.01, "should return 0 for unknown index")

	result = chain.smoothedMaxBucketForIndex("")
	require.InDelta(t, 0.0, result, 0.01, "should return 0 for empty index name")
}

func TestFindRouterCache_WalksTree(t *testing.T) {
	cache := newIndexSlotCache(indexSlotCacheConfig{})
	inner := &IndexRouter{cache: cache}
	wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

	chain := &PolicyChain{policies: []Policy{wrapper}}

	// findRouterCache should find the cache through the wrapper.
	found := findRouterCache(chain)
	require.NotNil(t, found, "should find cache through policy tree")
	require.Equal(t, cache, found, "should return the same cache instance")
}

// --- MIAD edge-case tests ---

func TestIndexSlot_UpdateSmoothedMaxBucket_MI_FullConvergence(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 1.
	slot.updateSmoothedMaxBucket(1.0)

	// Repeatedly observe bucket 8 every 2s (one MI half-life per step).
	// After 10 iterations (20s = 10 MI half-lives), the smoothed value
	// should be within 0.01 of the target.
	for range 10 {
		clk.Advance(2 * time.Second)
		slot.updateSmoothedMaxBucket(8.0)
	}

	got := slot.loadSmoothedMaxBucket()
	require.InDelta(t, 8.0, got, 0.01, "MI should converge to observed after 10 half-lives")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_AD_FullDrain(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 8.
	slot.updateSmoothedMaxBucket(8.0)

	// AD rate = 0.03 buckets/sec. Every 10s step decreases by 0.3.
	// 8.0 -> 7.7 -> 7.4 -> ... -> 1.1 -> 1.0 (clamped) -> 1.0 (stable)
	prev := 8.0
	for step := range 30 {
		clk.Advance(10 * time.Second)
		got := slot.updateSmoothedMaxBucket(1.0)

		expected := math.Max(prev-miadADRate*10, 1.0)
		require.InDelta(t, expected, got, 0.01,
			"step %d: AD should decrease by 0.3 per 10s step", step)
		prev = got

		if got <= 1.0+0.01 {
			// Once clamped, verify it stays at 1.0 for remaining steps.
			for range 3 {
				clk.Advance(10 * time.Second)
				got = slot.updateSmoothedMaxBucket(1.0)
				require.InDelta(t, 1.0, got, 0.01, "should stay clamped at floor")
			}
			return
		}
	}
	t.Fatal("AD did not reach floor within 30 steps")
}

func TestIndexSlot_UpdateSmoothedMaxBucket_AlternatingSpikes(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 1.
	slot.updateSmoothedMaxBucket(1.0)

	// Alternate between observing 8 (MI, fast grow) and 1 (AD, slow shrink)
	// every 2 seconds. The MIAD asymmetry should ratchet the value upward.
	for range 10 {
		clk.Advance(2 * time.Second)
		slot.updateSmoothedMaxBucket(8.0) // MI: fast convergence toward 8

		clk.Advance(2 * time.Second)
		slot.updateSmoothedMaxBucket(1.0) // AD: slow linear decrease
	}

	got := slot.loadSmoothedMaxBucket()
	require.Greater(t, got, 4.5,
		"MIAD asymmetry should ratchet value above midpoint under alternating spikes (got %.2f)", got)
}

func TestIndexSlot_UpdateSmoothedMaxBucket_TinyDt(t *testing.T) {
	clk := newTestClock()
	slot := &indexSlot{clock: clk}

	// Initialize at bucket 1.
	slot.updateSmoothedMaxBucket(1.0)

	// 100 updates with 1ms gaps, always observing 8.
	// Each MI step closes a tiny fraction of the gap.
	// Total elapsed: 100ms. Compound formula:
	//   new = 8 - (8-1) * exp(-ln(2)/2 * 0.1) = 8 - 7 * exp(-0.03466) ~= 8 - 6.761 ~= 1.239
	for range 100 {
		clk.Advance(1 * time.Millisecond)
		slot.updateSmoothedMaxBucket(8.0)
	}

	miLambda := math.Ln2 / miadMIHalfLife
	expected := 8.0 - 7.0*math.Exp(-miLambda*0.1)
	got := slot.loadSmoothedMaxBucket()
	require.InDelta(t, expected, got, 0.01,
		"100 tiny MI steps (100ms total) should compound to match formula")
}
