// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scoreTestConn creates a Connection with a known RTT and affinity load for scoring tests.
func scoreTestConn(t *testing.T, id string, rtt time.Duration, load float64) *Connection {
	t.Helper()
	u := &url.URL{Scheme: "https", Host: id + ":9200"}
	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        id,
		rttRing:   newRTTRing(4),
	}
	// Fill RTT ring so medianBucket returns the bucketed value.
	for range 4 {
		c.rttRing.add(rtt)
	}
	// Freeze the clock so load() returns exactly what store() wrote.
	c.affinityCounter.clock = newTestClock()
	if load > 0 {
		c.affinityCounter.store(load)
	}
	return c
}

func TestAffinityScoreShardCost(t *testing.T) {
	t.Parallel()

	t.Run("replica-only preferred for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Replicas: 3}

		score := affinityScore(conn, node, &shardCostForReads)
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForReads[shardCostReplica]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("primary-only penalized for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 3}

		score := affinityScore(conn, node, &shardCostForReads)
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForReads[shardCostPrimary]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("unknown gets heavy penalty", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)

		score := affinityScore(conn, nil, &shardCostForReads)
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForReads[shardCostUnknown]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("mixed node equals replica cost for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 1, Replicas: 9}

		score := affinityScore(conn, node, &shardCostForReads)
		// Mixed = min(replica, primary) = min(1.0, 2.0) = 1.0 for reads.
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForReads[shardCostReplica]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("ordering for reads: replica = mixed < primary < unknown", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 5.0

		replicaConn := scoreTestConn(t, "r", rtt, load)
		mixedConn := scoreTestConn(t, "m", rtt, load)
		primaryConn := scoreTestConn(t, "p", rtt, load)
		unknownConn := scoreTestConn(t, "u", rtt, load)

		replicaScore := affinityScore(replicaConn, &shardNodeInfo{Replicas: 5}, &shardCostForReads)
		mixedScore := affinityScore(mixedConn, &shardNodeInfo{Primaries: 2, Replicas: 3}, &shardCostForReads)
		primaryScore := affinityScore(primaryConn, &shardNodeInfo{Primaries: 5}, &shardCostForReads)
		unknownScore := affinityScore(unknownConn, nil, &shardCostForReads)

		require.InDelta(t, replicaScore, mixedScore, 0.01, "replica and mixed should score equal for reads")
		require.Less(t, mixedScore, primaryScore, "mixed should score lower than primary-only")
		require.Less(t, primaryScore, unknownScore, "primary should score lower than unknown")
	})

	t.Run("counter floor prevents zero score", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 0)
		node := &shardNodeInfo{Replicas: 1}

		score := affinityScore(conn, node, &shardCostForReads)
		require.Greater(t, score, 0.0, "score should be positive even with zero load")
	})

	t.Run("zero total shards treated as unknown", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		// Info exists but has no shards -- edge case.
		node := &shardNodeInfo{}

		score := affinityScore(conn, node, &shardCostForReads)
		unknownScore := affinityScore(conn, nil, &shardCostForReads)
		require.InDelta(t, unknownScore, score, 0.01, "zero-total info should match unknown penalty")
	})
}

func TestAffinityScoreShardCostWrites(t *testing.T) {
	t.Parallel()

	t.Run("primary-only preferred for writes", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 3}

		score := affinityScore(conn, node, &shardCostForWrites)
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForWrites[shardCostPrimary]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("replica-only penalized for writes", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Replicas: 3}

		score := affinityScore(conn, node, &shardCostForWrites)
		expected := float64(conn.rttRing.medianBucket()) * 10.0 * shardCostForWrites[shardCostReplica]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("ordering for writes: primary = mixed < replica < unknown", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 5.0

		primaryConn := scoreTestConn(t, "p", rtt, load)
		mixedConn := scoreTestConn(t, "m", rtt, load)
		replicaConn := scoreTestConn(t, "r", rtt, load)
		unknownConn := scoreTestConn(t, "u", rtt, load)

		primaryScore := affinityScore(primaryConn, &shardNodeInfo{Primaries: 5}, &shardCostForWrites)
		mixedScore := affinityScore(mixedConn, &shardNodeInfo{Primaries: 2, Replicas: 3}, &shardCostForWrites)
		replicaScore := affinityScore(replicaConn, &shardNodeInfo{Replicas: 5}, &shardCostForWrites)
		unknownScore := affinityScore(unknownConn, nil, &shardCostForWrites)

		require.InDelta(t, primaryScore, mixedScore, 0.01, "primary and mixed should score equal for writes")
		require.Less(t, mixedScore, replicaScore, "mixed should score lower than replica-only")
		require.Less(t, replicaScore, unknownScore, "replica should score lower than unknown")
	})
}

func TestShardCostMixedNode(t *testing.T) {
	t.Parallel()

	t.Run("reads: mixed = min(replica, primary) = replica cost", func(t *testing.T) {
		t.Parallel()
		node := &shardNodeInfo{Primaries: 3, Replicas: 7}
		cost := shardCostForReads.forNode(node)
		require.InDelta(t, shardCostForReads[shardCostReplica], cost, 0,
			"mixed node should get replica cost (1.0) for reads")
	})

	t.Run("writes: mixed = min(replica, primary) = primary cost", func(t *testing.T) {
		t.Parallel()
		node := &shardNodeInfo{Primaries: 3, Replicas: 7}
		cost := shardCostForWrites.forNode(node)
		require.InDelta(t, shardCostForWrites[shardCostPrimary], cost, 0,
			"mixed node should get primary cost (1.0) for writes")
	})

	t.Run("both tables: mixed cost is 1.0", func(t *testing.T) {
		t.Parallel()
		node := &shardNodeInfo{Primaries: 1, Replicas: 1}
		require.InDelta(t, 1.0, shardCostForReads.forNode(node), 0)
		require.InDelta(t, 1.0, shardCostForWrites.forNode(node), 0)
	})
}

func TestAffinityScore_DecayChangesScores(t *testing.T) {
	t.Parallel()

	// Two connections at the same RTT but different loads. A shared clock
	// lets us verify that time-based decay narrows the score gap.
	clk := newTestClock()
	rtt := 200 * time.Microsecond

	hot := scoreTestConnWithClock(t, "hot", rtt, 500.0, clk)
	cold := scoreTestConnWithClock(t, "cold", rtt, 10.0, clk)
	node := &shardNodeInfo{Replicas: 1}
	bucket := float64(hot.rttRing.medianBucket())

	// At t=0 (frozen clock): scores are proportional to load.
	hotScore0 := affinityScore(hot, node, &shardCostForReads)
	coldScore0 := affinityScore(cold, node, &shardCostForReads)

	require.InDelta(t, bucket*500.0*shardCostForReads[shardCostReplica], hotScore0, 0.01)
	require.InDelta(t, bucket*10.0*shardCostForReads[shardCostReplica], coldScore0, 0.01)
	require.Greater(t, hotScore0, coldScore0, "hot should score higher (worse) than cold")

	// After 10s (2 half-lives): hot decays to ~125, cold to ~2.5.
	clk.Advance(10 * time.Second)

	hotScore10 := affinityScore(hot, node, &shardCostForReads)
	coldScore10 := affinityScore(cold, node, &shardCostForReads)

	expectedHotLoad := 500.0 * math.Exp(-affinityDecayLambda*10)
	expectedColdLoad := 10.0 * math.Exp(-affinityDecayLambda*10)

	require.InDelta(t, bucket*expectedHotLoad*shardCostForReads[shardCostReplica], hotScore10, 0.01)
	require.InDelta(t, bucket*expectedColdLoad*shardCostForReads[shardCostReplica], coldScore10, 0.01)

	// The gap should narrow: the ratio of scores should be the same (load
	// ratio is unchanged by uniform decay), but absolute difference shrinks.
	require.Less(t, hotScore10-coldScore10, hotScore0-coldScore0,
		"absolute score gap should narrow after decay")
}

// scoreTestConnWithClock creates a Connection with a specific shared clock,
// allowing multiple connections to share the same time reference for
// Advance()-based tests.
func scoreTestConnWithClock(t *testing.T, id string, rtt time.Duration, load float64, clk *testClock) *Connection {
	t.Helper()
	u := &url.URL{Scheme: "https", Host: id + ":9200"}
	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        id,
		rttRing:   newRTTRing(4),
	}
	for range 4 {
		c.rttRing.add(rtt)
	}
	c.affinityCounter.clock = clk
	if load > 0 {
		c.affinityCounter.store(load)
	}
	return c
}
