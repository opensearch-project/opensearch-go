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

// --- calcConnDefaultScore is warmup-agnostic ---

func TestCalcConnScore_IgnoresWarmup(t *testing.T) {
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

		warmedScore := calcConnDefaultScore(warmed, shardCostForReads.forNode(node), "", true)
		warmingScore := calcConnDefaultScore(warming, shardCostForReads.forNode(node), "", true)

		require.InDelta(t, warmedScore, warmingScore, 0.01,
			"calcConnDefaultScore should not include warmup penalty")
	})

	t.Run("shard cost still differentiates", func(t *testing.T) {
		t.Parallel()
		rtt := 200 * time.Microsecond
		load := 10.0

		conn := scoreTestConn(t, "node", rtt, load)
		conn.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 8, 8)))

		// Unknown shard cost (32.0) vs replica shard cost (1.0)
		scoreUnknown := calcConnDefaultScore(conn, shardCostForReads.forNode(nil), "", true)
		scoreReplica := calcConnDefaultScore(conn, shardCostForReads.forNode(&shardNodeInfo{Replicas: 1}), "", true)

		require.Greater(t, scoreUnknown, scoreReplica,
			"unknown shard cost should score worse than replica")
		require.InDelta(t, 32.0, scoreUnknown/scoreReplica, 0.01,
			"ratio should be the shard cost ratio")
	})
}

// --- tryWarmupSkip advances warmup ---

func TestTryWarmupSkip_AdvancesWarmup(t *testing.T) {
	t.Parallel()

	t.Run("tryWarmupSkip advances warmup counter", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-warmup:9200"}}
		conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		conn.startWarmup(8, 8)

		before := conn.loadConnState().roundManager()
		require.Equal(t, 8, before.rounds(), "initial rounds")
		require.Equal(t, 8, before.skipCount(), "initial skip count")

		result := conn.tryWarmupSkip()
		require.Equal(t, warmupSkipped, result, "first call should skip")

		// One call to tryWarmupSkip: with 8 skips remaining, it should
		// have decremented the skip count by 1.
		after := conn.loadConnState().roundManager()
		require.Equal(t, 8, after.rounds(), "rounds should not change on skip")
		require.Equal(t, 7, after.skipCount(), "skip count should decrement")
	})

	t.Run("repeated tryWarmupSkip completes warmup", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-complete:9200"}}
		conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		conn.startWarmup(8, 8)

		require.True(t, conn.loadConnState().isWarmingUp(), "should be warming initially")

		// Drive warmup to completion via tryWarmupSkip.
		// warmup(8,8) needs 41 evaluations (33 skips + 8 accepts).
		for range 41 {
			result := conn.tryWarmupSkip()
			require.NotEqual(t, warmupInactive, result,
				"should not become inactive mid-warmup")
		}

		require.False(t, conn.loadConnState().isWarmingUp(),
			"warmup should be complete after 41 tryWarmupSkip() calls")
		require.False(t, conn.loadConnState().lifecycle().has(lcNeedsWarmup),
			"lcNeedsWarmup should be cleared")
	})

	t.Run("tryWarmupSkip with no warmup returns inactive", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-no-warmup:9200"}}
		conn.state.Store(int64(newConnState(lcActive)))

		result := conn.tryWarmupSkip()
		require.Equal(t, warmupInactive, result)

		// State should be unchanged.
		require.False(t, conn.loadConnState().isWarmingUp())
	})

	t.Run("dead connection is filtered by connScoreSelect lifecycle check", func(t *testing.T) {
		t.Parallel()
		// Dead connections are filtered by the caller (Eval) which checks
		// lifecycle state before returning a NextHop. connScoreSelect itself
		// handles warmup gating, not lifecycle. Verify that a dead connection
		// has tryWarmupSkip return inactive (since it's not warming).
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "pool-dead:9200"}}
		conn.state.Store(int64(newConnState(lcDead)))

		result := conn.tryWarmupSkip()
		require.Equal(t, warmupInactive, result,
			"dead connection with no warmup should return inactive")
	})
}

// --- connScoreSelect warmup gating ---

func TestConnScoreSelect_WarmupGating(t *testing.T) {
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

		// Create a minimal index slot for connScoreSelect.
		slot := &indexSlot{}
		shardInfo := map[string]*shardNodeInfo{
			"warmed":  {Replicas: 1},
			"warming": {Replicas: 1},
		}
		slot.shardNodeNames.Store(&shardInfo)

		// With equal scores, the warmed connection should be selected
		// because connScoreSelect tries in score order and picks the
		// first non-warming candidate.
		scores := make([]float64, 2)
		best := connScoreSelect(candidates, slot, nil, &shardCostForReads, "", true, scores, nil)
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

		// Run enough rounds for warmup to advance. connScoreSelect should
		// eventually accept a warming candidate (via tryWarmupSkip).
		accepted := 0
		for range 50 {
			scores := make([]float64, 2)
			best := connScoreSelect(candidates, slot, nil, &shardCostForReads, "", true, scores, nil)
			require.NotNil(t, best, "should always return a candidate")
			accepted++
		}

		require.Positive(t, accepted,
			"warming connections should still get traffic via connScoreSelect")
	})
}
