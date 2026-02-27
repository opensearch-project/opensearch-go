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

// --- warmupPenalty ---

func TestWarmupPenalty(t *testing.T) {
	t.Parallel()

	t.Run("not warming returns 1.0", func(t *testing.T) {
		t.Parallel()
		cs := newConnState(lcActive)
		require.InDelta(t, 1.0, warmupPenalty(cs), 0)
	})

	t.Run("start of warmup returns max penalty", func(t *testing.T) {
		t.Parallel()
		// lcMgr and rdMgr both at full rounds -> fraction = 1.0
		cs := warmupState(lcActive|lcNeedsWarmup, 8, 8)
		penalty := warmupPenalty(cs)
		require.InDelta(t, warmupPenaltyMax, penalty, 0.01,
			"full remaining rounds should give max penalty")
	})

	t.Run("half-way through warmup", func(t *testing.T) {
		t.Parallel()
		// lcMgr = (8, 8), rdMgr = (4, *) -> fraction = 4/8 = 0.5
		lcMgr := packWarmupManager(8, 8)
		rdMgr := packWarmupManager(4, 2) // remaining 4 of 8 rounds
		cs := packConnState(lcActive|lcNeedsWarmup, lcMgr, rdMgr)

		penalty := warmupPenalty(cs)
		expected := 1.0 + (warmupPenaltyMax-1.0)*0.5
		require.InDelta(t, expected, penalty, 0.01,
			"50%% remaining should give half the penalty range")
	})

	t.Run("last round returns near 1.0", func(t *testing.T) {
		t.Parallel()
		// lcMgr = (8, 8), rdMgr = (1, 0) -> fraction = 1/8 = 0.125
		lcMgr := packWarmupManager(8, 8)
		rdMgr := packWarmupManager(1, 0)
		cs := packConnState(lcActive|lcNeedsWarmup, lcMgr, rdMgr)

		penalty := warmupPenalty(cs)
		expected := 1.0 + (warmupPenaltyMax-1.0)*0.125
		require.InDelta(t, expected, penalty, 0.01,
			"near end of warmup should give small penalty")
	})

	t.Run("zero total rounds returns 1.0", func(t *testing.T) {
		t.Parallel()
		// Edge case: lcMgr has 0 rounds (shouldn't happen in practice)
		lcMgr := packWarmupManager(0, 0)
		rdMgr := packWarmupManager(0, 0)
		cs := packConnState(lcActive|lcNeedsWarmup, lcMgr, rdMgr)

		require.InDelta(t, 1.0, warmupPenalty(cs), 0)
	})
}

// --- affinityScore is warmup-agnostic ---

func TestAffinityScore_IgnoresWarmup(t *testing.T) {
	t.Parallel()

	t.Run("warming and warmed connections score identically", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 10.0
		node := &shardNodeInfo{Replicas: 1}

		warmed := scoreTestConn(t, "warmed", rtt, load)
		warmed.state.Store(int64(newConnState(lcActive))) // no warmup

		warming := scoreTestConn(t, "warming", rtt, load)
		warming.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 8, 8)))

		warmedScore := affinityScore(warmed, node, &shardCostForReads)
		warmingScore := affinityScore(warming, node, &shardCostForReads)

		require.InDelta(t, warmedScore, warmingScore, 0.01,
			"affinityScore should not include warmup penalty")
	})

	t.Run("shard cost still differentiates", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 10.0

		conn := scoreTestConn(t, "node", rtt, load)
		conn.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 8, 8)))

		// Unknown shard cost (32.0) vs replica shard cost (1.0)
		scoreUnknown := affinityScore(conn, nil, &shardCostForReads)
		scoreReplica := affinityScore(conn, &shardNodeInfo{Replicas: 1}, &shardCostForReads)

		require.Greater(t, scoreUnknown, scoreReplica,
			"unknown shard cost should score worse than replica")
		require.InDelta(t, 32.0, scoreUnknown/scoreReplica, 0.01,
			"ratio should be the shard cost ratio")
	})
}

// --- affinityPool.Next advances warmup ---

func TestAffinityPoolNext_AdvancesWarmup(t *testing.T) {
	t.Parallel()

	t.Run("Next advances warmup counter", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-warmup:9200"}}
		conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		conn.startWarmup(8, 8)

		before := conn.loadConnState().roundManager()
		require.Equal(t, 8, before.rounds(), "initial rounds")
		require.Equal(t, 8, before.skipCount(), "initial skip count")

		pool := getAffinityPool(conn)
		got, err := pool.Next()
		require.NoError(t, err)
		require.Same(t, conn, got, "should return the same connection")

		// One call to tryWarmupSkip: with 8 skips remaining, it should
		// have decremented the skip count by 1.
		after := conn.loadConnState().roundManager()
		require.Equal(t, 8, after.rounds(), "rounds should not change on skip")
		require.Equal(t, 7, after.skipCount(), "skip count should decrement")
	})

	t.Run("repeated Next completes warmup", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-complete:9200"}}
		conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		conn.startWarmup(8, 8)

		require.True(t, conn.loadConnState().isWarmingUp(), "should be warming initially")

		// Drive warmup to completion via affinityPool.Next().
		// warmup(8,8) needs 41 evaluations (33 skips + 8 accepts).
		for range 41 {
			pool := getAffinityPool(conn)
			got, err := pool.Next()
			require.NoError(t, err)
			require.NotNil(t, got)
		}

		require.False(t, conn.loadConnState().isWarmingUp(),
			"warmup should be complete after 41 Next() calls")
		require.False(t, conn.loadConnState().lifecycle().has(lcNeedsWarmup),
			"lcNeedsWarmup should be cleared")
	})

	t.Run("Next with no warmup is a no-op", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-no-warmup:9200"}}
		conn.state.Store(int64(newConnState(lcActive)))

		pool := getAffinityPool(conn)
		got, err := pool.Next()
		require.NoError(t, err)
		require.Same(t, conn, got)

		// State should be unchanged.
		require.False(t, conn.loadConnState().isWarmingUp())
	})

	t.Run("Next returns error for dead connection", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-dead:9200"}}
		conn.state.Store(int64(newConnState(lcDead)))

		pool := getAffinityPool(conn)
		_, err := pool.Next()
		require.ErrorIs(t, err, ErrNoConnections)
	})
}

// --- affinitySelect warmup gating ---

func TestAffinitySelect_WarmupGating(t *testing.T) {
	t.Parallel()

	t.Run("warmed connection selected over warming when scores equal", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 10.0

		warmed := scoreTestConn(t, "warmed", rtt, load)
		warmed.state.Store(int64(newConnState(lcActive)))

		warming := scoreTestConn(t, "warming", rtt, load)
		warming.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 8, 8)))

		candidates := []*Connection{warming, warmed}

		// Create a minimal index slot for affinitySelect.
		slot := &indexSlot{}
		shardInfo := map[string]*shardNodeInfo{
			"warmed":  {Replicas: 1},
			"warming": {Replicas: 1},
		}
		slot.shardNodeNames.Store(&shardInfo)

		// With equal scores, the warmed connection should be selected
		// because affinitySelect tries in score order and picks the
		// first non-warming candidate.
		scores := make([]float64, 2)
		best := affinitySelect(candidates, slot, &shardCostForReads, scores)
		require.Equal(t, warmed, best,
			"warmed connection should be preferred over warming")
	})

	t.Run("warming connection gets traffic via skip/accept", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 10.0

		// Both connections are warming.
		connA := scoreTestConn(t, "nodeA", rtt, load)
		connA.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		connA.startWarmup(8, 8)

		connB := scoreTestConn(t, "nodeB", rtt, load)
		connB.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		connB.startWarmup(8, 8)

		candidates := []*Connection{connA, connB}
		slot := &indexSlot{}
		shardInfo := map[string]*shardNodeInfo{
			"nodeA": {Replicas: 1},
			"nodeB": {Replicas: 1},
		}
		slot.shardNodeNames.Store(&shardInfo)

		// Run enough rounds for warmup to advance. affinitySelect should
		// eventually accept a warming candidate (via tryWarmupSkip).
		accepted := 0
		for range 50 {
			scores := make([]float64, 2)
			best := affinitySelect(candidates, slot, &shardCostForReads, scores)
			require.NotNil(t, best, "should always return a candidate")
			accepted++
		}

		require.Greater(t, accepted, 0,
			"warming connections should still get traffic via affinitySelect")
	})
}
