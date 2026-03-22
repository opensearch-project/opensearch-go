// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// scoringEquilibriumSim runs the discrete-time simulation of the scoring
// feedback loop and returns per-node win counts during the measurement
// phase.
//
// The simulation models:
//  1. Apply EWMA decay to all counters (dt seconds elapsed)
//  2. Score each node: rttBucket * max(decayCounter, 1.0) * wp
//  3. Pick the lowest-scoring node as the winner
//  4. Compute the winner's cost via costFn, which receives the winner's
//     rttBucket and returns the final cost (after baseline subtraction,
//     processor division, and bucket normalization)
//  5. Add the cost to the winner's decay counter
func scoringEquilibriumSim(nodes []simNode, costFn func(rttBucket float64) float64) {
	const (
		dt      = 0.1   // seconds between requests (10 req/s)
		warmup  = 5000  // iterations to reach steady state (~500s simulated)
		measure = 20000 // measurement window (~2000s simulated)
	)

	lambda := loadDecayLambda // ln(2) / 5.0
	decayFactor := math.Exp(-lambda * dt)

	for iter := range warmup + measure {
		// Apply EWMA decay to all counters.
		for i := range nodes {
			nodes[i].decay *= decayFactor
		}

		// Score each node and pick the winner (lowest score).
		bestIdx := 0
		bestScore := math.MaxFloat64
		for i := range nodes {
			counter := max(nodes[i].decay, counterFloor)
			score := nodes[i].rttBucket * counter * nodes[i].wp
			if score < bestScore {
				bestScore = score
				bestIdx = i
			}
		}

		// Compute cost for winner (costFn handles all normalization).
		nodes[bestIdx].decay += costFn(nodes[bestIdx].rttBucket)

		// Count wins during measurement phase only.
		if iter >= warmup {
			nodes[bestIdx].wins++
		}
	}
}

// simNode is one node in the equilibrium simulation.
type simNode struct {
	name      string
	rttBucket float64
	wp        float64
	decay     float64
	wins      int
}

// TestScoringEquilibrium verifies that the connection scoring feedback loop
// converges to the expected traffic distribution across nodes with different
// RTT buckets and shard cost multipliers.
//
// The production cost model is:
//
//	serverTime = requestDuration - RTTMedian
//	cost = serverTime / processors / rttBucket
//
// The /rttBucket normalization cancels the rttBucket multiplier in the scoring
// formula (score = rttBucket * counter * wp), yielding:
//
//	score = rttBucket * (rate * serverTime / (proc * bucket * lambda)) * wp
//	      = rate * serverTime / (proc * lambda) * wp
//
// At equilibrium: rate_i * wp_i = rate_j * wp_j, i.e., rate proportional
// to 1/wp regardless of RTT tier placement.
func TestScoringEquilibrium(t *testing.T) {
	t.Parallel()

	const (
		measure = 20000 // must match scoringEquilibriumSim
		tol     = 0.05  // +/- 5 percentage points
	)

	tests := []struct {
		name  string
		nodes []simNode
	}{
		{
			name: "same_bucket",
			nodes: []simNode{
				{name: "primary", rttBucket: 9, wp: 2.0},
				{name: "replica", rttBucket: 9, wp: 1.0},
			},
		},
		{
			name: "two_buckets_cross_AZ",
			nodes: []simNode{
				{name: "local-primary", rttBucket: 9, wp: 2.0},
				{name: "remote-replica", rttBucket: 12, wp: 1.0},
			},
		},
		{
			name: "three_buckets",
			nodes: []simNode{
				{name: "az1", rttBucket: 8, wp: 1.0},
				{name: "az2", rttBucket: 13, wp: 1.0},
				{name: "az3", rttBucket: 15, wp: 1.0},
			},
		},
		{
			name: "four_buckets",
			nodes: []simNode{
				{name: "az1", rttBucket: 8, wp: 1.0},
				{name: "az2", rttBucket: 12, wp: 1.0},
				{name: "az3", rttBucket: 14, wp: 1.0},
				{name: "az4", rttBucket: 18, wp: 1.0},
			},
		},
	}

	// Uniform server time model: same server processing time for all tiers.
	// Cost is divided by rttBucket to cancel the bucket multiplier in scoring.
	uniformServerTime := 500.0 // microseconds
	bucketNormCost := func(rttBucket float64) float64 {
		return uniformServerTime / rttBucket
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes := make([]simNode, len(tt.nodes))
			copy(nodes, tt.nodes)

			scoringEquilibriumSim(nodes, bucketNormCost)

			verifyEquilibriumDistribution(t, nodes, measure, tol)
		})
	}
}

// TestScoringEquilibriumRealisticCost verifies convergence when each node's
// per-request cost models the real recordCPUTime calculation:
//
//	serverTime = requestDuration - RTTMedian (where RTTMedian = 2^bucket us)
//	cost = serverTime / processors / rttBucket
//
// Cross-AZ scenario: a 5ms server-processing request measured at the client:
//
//	bucket 9 (RTTMedian=512us): D=5512us, serverTime=5000us, cost=5000/(8*9)=69.4
//	bucket 12 (RTTMedian=4096us): D=9096us, serverTime=5000us, cost=5000/(8*12)=52.1
//
// The baseline subtraction correctly removes wire time, leaving the same
// serverTime for all tiers. The /bucket normalization then cancels the
// bucket multiplier in scoring.
func TestScoringEquilibriumRealisticCost(t *testing.T) {
	t.Parallel()

	const (
		measure    = 20000 // must match scoringEquilibriumSim
		tol        = 0.05  // +/- 5 percentage points
		serverTime = 5000  // 5ms server-side processing in microseconds
		processors = 8
	)

	tests := []struct {
		name  string
		nodes []simNode
	}{
		{
			// Cross-AZ: different buckets, same server processing time.
			// RTTMedian subtraction isolates serverTime, /bucket cancels scoring.
			name: "cross_AZ_different_buckets",
			nodes: []simNode{
				{name: "local-primary", rttBucket: 9, wp: 2.0},
				{name: "remote-replica", rttBucket: 12, wp: 1.0},
			},
		},
		{
			name: "three_tiers_equal_wp",
			nodes: []simNode{
				{name: "az1", rttBucket: 8, wp: 1.0},
				{name: "az2", rttBucket: 13, wp: 1.0},
				{name: "az3", rttBucket: 15, wp: 1.0},
			},
		},
		{
			name: "wide_spread_equal_wp",
			nodes: []simNode{
				{name: "local", rttBucket: 8, wp: 1.0},
				{name: "remote", rttBucket: 18, wp: 1.0},
			},
		},
	}

	// Realistic cost model: baseline subtraction + bucket normalization.
	// serverTime is constant across tiers (wire time already removed).
	realisticCost := func(rttBucket float64) float64 {
		return float64(serverTime) / float64(processors) / rttBucket
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes := make([]simNode, len(tt.nodes))
			copy(nodes, tt.nodes)

			scoringEquilibriumSim(nodes, realisticCost)

			verifyEquilibriumDistribution(t, nodes, measure, tol)
		})
	}
}

// TestScoringEquilibriumOldInflation demonstrates that the old inflation
// formula (maxBucket/thisBucket) with baseline subtraction fails to converge
// when nodes have different RTT buckets.
//
// The old formula operated in log-space (bucket index ratios) while the
// baseline subtraction operated in linear-space (2^bucket microseconds).
// This mismatch prevented convergence with power-of-two bucketing.
//
// With the old formula:
//
//	node1 (bucket 9): cost = (5000-512)/8 * 12/9 = 561 * 1.33 = 748
//	node2 (bucket 12): cost = (5000-4096)/8 * 1.0 = 113
//
// The 6.6x cost ratio overwhelms the 1.33x inflation.
func TestScoringEquilibriumOldInflation(t *testing.T) {
	t.Parallel()

	const (
		measure    = 20000 // must match scoringEquilibriumSim
		requestDur = 5000  // 5ms request duration in microseconds
		processors = 8
	)

	nodes := []simNode{
		{name: "node1", rttBucket: 9, wp: 2.0},
		{name: "node2", rttBucket: 12, wp: 1.0},
	}

	// Find maxBucket for old inflation formula.
	var maxBucket float64
	for _, n := range nodes {
		if n.rttBucket > maxBucket {
			maxBucket = n.rttBucket
		}
	}

	// Old model: baseline subtraction + maxBucket/thisBucket inflation.
	oldInflationCost := func(rttBucket float64) float64 {
		rttMedianMicros := math.Pow(2, rttBucket) // 2^bucket microseconds
		serverTime := float64(requestDur) - rttMedianMicros
		if serverTime <= 0 {
			return 0
		}
		base := serverTime / float64(processors)
		inflation := maxBucket / rttBucket
		return base * inflation
	}

	scoringEquilibriumSim(nodes, oldInflationCost)

	// Expected: 33.3% node1, 66.7% node2 (proportional to 1/wp).
	// Actual with old inflation: heavily skewed toward node2.
	node1Frac := float64(nodes[0].wins) / float64(measure)
	node2Frac := float64(nodes[1].wins) / float64(measure)

	t.Logf("old inflation model: node1=%.1f%% node2=%.1f%% (expected: 33.3%% 66.7%%)",
		node1Frac*100, node2Frac*100)

	// The distribution error should be large (> 10pp).
	node1Error := math.Abs(node1Frac - 1.0/3.0)
	require.Greater(t, node1Error, 0.10,
		"old inflation should cause > 10pp distribution error, got %.1fpp", node1Error*100)
}

// verifyEquilibriumDistribution checks that the simulated traffic distribution
// matches the expected 1/wp proportional distribution within tolerance.
func verifyEquilibriumDistribution(t *testing.T, nodes []simNode, measure int, tol float64) {
	t.Helper()

	// Compute expected distribution: rate proportional to 1/wp.
	var totalInvWP float64
	for _, n := range nodes {
		totalInvWP += 1.0 / n.wp
	}
	expected := make([]float64, len(nodes))
	for i, n := range nodes {
		expected[i] = (1.0 / n.wp) / totalInvWP
	}

	// Log distribution.
	parts := make([]string, 0, len(nodes))
	for i, n := range nodes {
		actual := float64(n.wins) / float64(measure)
		parts = append(parts, fmt.Sprintf("%s: expected=%.1f%% actual=%.1f%%",
			n.name, expected[i]*100, actual*100))
	}
	t.Log(strings.Join(parts, "  |  "))

	// Verify each node is within tolerance.
	for i, n := range nodes {
		actual := float64(n.wins) / float64(measure)
		require.InDelta(t, expected[i], actual, tol,
			"%s (bucket=%.0f wp=%.1f): expected %.1f%%, got %.1f%%",
			n.name, n.rttBucket, n.wp, expected[i]*100, actual*100)
	}
}
