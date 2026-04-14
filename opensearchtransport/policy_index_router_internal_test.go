// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scoreTestConn creates a Connection with a known RTT and load for scoring tests.
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
	c.estLoad.clock = newTestClock()
	if load > 0 {
		c.estLoad.store(load)
	}
	return c
}

func TestCalcConnScoreShardCost(t *testing.T) {
	t.Parallel()

	t.Run("replica-only preferred for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Replicas: 3}

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(node), "", true)
		// With cwnd scoring: utilization = (0+1)/1 = 1.0, so score = rttBucket * 1.0 * shardCost.
		expected := float64(conn.rttRing.medianBucket()) * shardCostForReads[shardCostReplica]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("primary-only penalized for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 3}

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(node), "", true)
		expected := float64(conn.rttRing.medianBucket()) * shardCostForReads[shardCostPrimary]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("unknown gets heavy penalty", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(nil), "", true)
		expected := float64(conn.rttRing.medianBucket()) * shardCostForReads[shardCostUnknown]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("mixed node equals replica cost for reads", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 1, Replicas: 9}

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(node), "", true)
		// Mixed = min(replica, primary) = min(1.0, 2.0) = 1.0 for reads.
		expected := float64(conn.rttRing.medianBucket()) * shardCostForReads[shardCostReplica]
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

		replicaScore := calcConnDefaultScore(replicaConn, shardCostForReads.forNode(&shardNodeInfo{Replicas: 5}), "", true)
		mixedScore := calcConnDefaultScore(mixedConn, shardCostForReads.forNode(&shardNodeInfo{Primaries: 2, Replicas: 3}), "", true)
		primaryScore := calcConnDefaultScore(primaryConn, shardCostForReads.forNode(&shardNodeInfo{Primaries: 5}), "", true)
		unknownScore := calcConnDefaultScore(unknownConn, shardCostForReads.forNode(nil), "", true)

		require.InDelta(t, replicaScore, mixedScore, 0.01, "replica and mixed should score equal for reads")
		require.Less(t, mixedScore, primaryScore, "mixed should score lower than primary-only")
		require.Less(t, primaryScore, unknownScore, "primary should score lower than unknown")
	})

	t.Run("counter floor prevents zero score", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 0)
		node := &shardNodeInfo{Replicas: 1}

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(node), "", true)
		require.Greater(t, score, 0.0, "score should be positive even with zero load")
	})

	t.Run("zero total shards treated as unknown", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		// Info exists but has no shards -- edge case.
		node := &shardNodeInfo{}

		score := calcConnDefaultScore(conn, shardCostForReads.forNode(node), "", true)
		unknownScore := calcConnDefaultScore(conn, shardCostForReads.forNode(nil), "", true)
		require.InDelta(t, unknownScore, score, 0.01, "zero-total info should match unknown penalty")
	})
}

func TestCalcConnScoreShardCostWrites(t *testing.T) {
	t.Parallel()

	t.Run("primary-only preferred for writes", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Primaries: 3}

		score := calcConnDefaultScore(conn, shardCostForWrites.forNode(node), "", true)
		expected := float64(conn.rttRing.medianBucket()) * shardCostForWrites[shardCostPrimary]
		require.InDelta(t, expected, score, 0.01)
	})

	t.Run("replica-only penalized for writes", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node1", 200*time.Microsecond, 10.0)
		node := &shardNodeInfo{Replicas: 3}

		score := calcConnDefaultScore(conn, shardCostForWrites.forNode(node), "", true)
		expected := float64(conn.rttRing.medianBucket()) * shardCostForWrites[shardCostReplica]
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

		primaryScore := calcConnDefaultScore(primaryConn, shardCostForWrites.forNode(&shardNodeInfo{Primaries: 5}), "", true)
		mixedScore := calcConnDefaultScore(mixedConn, shardCostForWrites.forNode(&shardNodeInfo{Primaries: 2, Replicas: 3}), "", true)
		replicaScore := calcConnDefaultScore(replicaConn, shardCostForWrites.forNode(&shardNodeInfo{Replicas: 5}), "", true)
		unknownScore := calcConnDefaultScore(unknownConn, shardCostForWrites.forNode(nil), "", true)

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

func TestCalcConnScore_InFlightChangesScores(t *testing.T) {
	t.Parallel()

	// Two connections at the same RTT. One has in-flight requests.
	// Higher in-flight should produce a higher (worse) score because
	// utilization = (inFlight + 1) / cwnd increases.
	rtt := 200 * time.Microsecond
	node := &shardNodeInfo{Replicas: 1}

	connIdle := scoreTestConn(t, "idle", rtt, 0)
	connBusy := scoreTestConn(t, "busy", rtt, 0)
	connBusy.addInFlight("") // inFlight = 1

	idleScore := calcConnDefaultScore(connIdle, shardCostForReads.forNode(node), "", true)
	busyScore := calcConnDefaultScore(connBusy, shardCostForReads.forNode(node), "", true)

	bucket := float64(connIdle.rttRing.medianBucket())

	// Idle: utilization = (0+1)/1 = 1.0, score = bucket * 1.0 * 1.0
	require.InDelta(t, bucket*1.0*shardCostForReads[shardCostReplica], idleScore, 0.01)
	// Busy: utilization = (1+1)/1 = 2.0, score = bucket * 2.0 * 1.0
	require.InDelta(t, bucket*2.0*shardCostForReads[shardCostReplica], busyScore, 0.01)

	require.Less(t, idleScore, busyScore, "idle should score lower (better) than busy")
}
