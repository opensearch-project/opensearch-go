// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRouteCandidate(t *testing.T) {
	t.Parallel()

	t.Run("with RTT data and shard info", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 512*time.Microsecond, 5.0)
		conn.Name = "data-node-1"
		slot := &indexSlot{}
		nodeInfo := map[string]*shardNodeInfo{"data-node-1": {Replicas: 3}}
		slot.shardNodeNames.Store(&nodeInfo)

		c := newRouteCandidate(conn, slot, nil, &shardCostForReads, "", true, nil)

		require.Equal(t, conn.URL.String(), c.URL)
		require.Equal(t, "node-1", c.ID)
		require.Equal(t, "data-node-1", c.Name)
		require.Positive(t, c.RTTBucket)
		require.Equal(t, int32(0), c.InFlight)
		require.Equal(t, int32(1), c.Cwnd) // default pool cwnd
		require.InDelta(t, shardCostForReads[shardCostReplica], c.ShardCostMultiplier, 0)
		require.Greater(t, c.Score, 0.0)
	})

	t.Run("without RTT data", func(t *testing.T) {
		t.Parallel()
		u := &url.URL{Scheme: "https", Host: "node-2:9200"}
		conn := &Connection{
			URL:       u,
			URLString: u.String(),
			ID:        "node-2",
			rttRing:   newRTTRing(4),
		}
		slot := &indexSlot{}

		c := newRouteCandidate(conn, slot, nil, &shardCostForReads, "", true, nil)

		require.Equal(t, int64(-1), c.RTTBucket)
		require.InDelta(t, shardCostForReads[shardCostUnknown], c.ShardCostMultiplier, 0)
	})

	t.Run("with primary-only shards", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-3", 1*time.Millisecond, 1.0)
		slot := &indexSlot{}
		nodeInfo := map[string]*shardNodeInfo{conn.Name: {Primaries: 5}}
		slot.shardNodeNames.Store(&nodeInfo)

		c := newRouteCandidate(conn, slot, nil, &shardCostForReads, "", true, nil)

		require.InDelta(t, shardCostForReads[shardCostPrimary], c.ShardCostMultiplier, 0)
	})
}

func TestBuildRouteEvent(t *testing.T) {
	t.Parallel()

	conn1 := scoreTestConn(t, "node-a", 200*time.Microsecond, 2.0)
	conn1.Name = "alpha"
	conn2 := scoreTestConn(t, "node-b", 400*time.Microsecond, 1.0)
	conn2.Name = "beta"

	slot := &indexSlot{}
	candidates := []*Connection{conn1, conn2}

	event := buildRouteEvent(
		"test-index", "test-index", 3, 5, candidates, conn1,
		slot, nil, &shardCostForReads, "", "", "", -1, false, true, nil, 0,
	)

	require.Equal(t, "test-index", event.IndexName)
	require.Equal(t, "test-index", event.Key)
	require.Equal(t, 3, event.FanOut)
	require.Equal(t, 5, event.TotalNodes)
	require.Equal(t, 2, event.CandidateCount)
	require.Len(t, event.Candidates, 2)
	require.Equal(t, "node-a", event.Selected.ID)
	require.False(t, event.Timestamp.IsZero())

	// Verify candidates are in order
	require.Equal(t, "node-a", event.Candidates[0].ID)
	require.Equal(t, "node-b", event.Candidates[1].ID)
}

func TestIndexRouterEmitsObserverEvent(t *testing.T) {
	t.Parallel()

	obs := newRecordingObserver()

	policy := NewIndexRouter(indexSlotCacheConfig{
		minFanOut:    2,
		maxFanOut:    8,
		decayFactor:  0.999,
		fanOutPerReq: 500,
	})

	var obsIface ConnectionObserver = obs
	err := policy.configurePolicySettings(policyConfig{observer: &obsIface})
	require.NoError(t, err)

	// Add connections via DiscoveryUpdate.
	conns := make([]*Connection, 3)
	for i := range conns {
		conns[i] = scoreTestConn(t, "node-"+string(rune('a'+i)), 200*time.Microsecond, 0)
	}
	err = policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	// Make a request targeting an index.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/my-index/_search", nil)
	hop, err := policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)

	events := obs.getRouteEvents()
	require.Len(t, events, 1)
	require.Equal(t, "my-index", events[0].IndexName)
	require.Equal(t, "my-index", events[0].Key)
	require.Positive(t, events[0].CandidateCount)
	require.NotEmpty(t, events[0].Selected.ID)
}

func TestDocRouterEmitsObserverEvent(t *testing.T) {
	t.Parallel()

	obs := newRecordingObserver()
	cache := newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:    2,
		maxFanOut:    8,
		decayFactor:  0.999,
		fanOutPerReq: 500,
	})

	policy := NewDocRouter(cache, 0.999)

	var obsIface ConnectionObserver = obs
	err := policy.configurePolicySettings(policyConfig{observer: &obsIface})
	require.NoError(t, err)

	// Add connections.
	conns := make([]*Connection, 3)
	for i := range conns {
		conns[i] = scoreTestConn(t, "node-"+string(rune('a'+i)), 200*time.Microsecond, 0)
	}
	err = policy.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	// Make a document-level request.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/my-index/_doc/123", nil)
	pool, err := policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, pool)

	events := obs.getRouteEvents()
	require.Len(t, events, 1)
	require.Equal(t, "my-index", events[0].IndexName)
	require.Equal(t, "my-index/123", events[0].Key)
}

func TestIndexSlotCacheSnapshot(t *testing.T) {
	t.Parallel()

	cache := newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:       2,
		maxFanOut:       16,
		decayFactor:     0.999,
		fanOutPerReq:    500,
		idleEvictionTTL: 90 * time.Minute,
	})

	// Create some index slots by requesting them.
	cache.getOrCreate("index-alpha")
	cache.getOrCreate("index-beta")
	cache.getOrCreate("index-gamma")

	snap := cache.snapshot()

	require.Len(t, snap.Indexes, 3)

	// Verify sorted by name.
	require.Equal(t, "index-alpha", snap.Indexes[0].Name)
	require.Equal(t, "index-beta", snap.Indexes[1].Name)
	require.Equal(t, "index-gamma", snap.Indexes[2].Name)

	// Each should have fan-out >= minFanOut.
	for _, idx := range snap.Indexes {
		require.GreaterOrEqual(t, idx.FanOut, 2)
		require.Greater(t, idx.RequestRate, 0.0)
		require.Nil(t, idx.IdleSince)
	}

	// Verify config.
	require.Equal(t, 2, snap.Config.MinFanOut)
	require.Equal(t, 16, snap.Config.MaxFanOut)
	require.InDelta(t, 0.999, snap.Config.DecayFactor, 0)
	require.InDelta(t, 500.0, snap.Config.FanOutPerReq, 0)
	require.Equal(t, (90 * time.Minute).String(), snap.Config.IdleEvictionTTL)
}

func TestCollectRouterSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, collectRouterSnapshot(nil))
	})

	t.Run("non-provider returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, collectRouterSnapshot("not a provider"))
	})

	t.Run("policy chain with no provider returns nil", func(t *testing.T) {
		t.Parallel()
		chain := &PolicyChain{policies: []Policy{&NullPolicy{}}}
		require.Nil(t, collectRouterSnapshot(chain))
	})

	t.Run("returns snapshot from IndexRouter", func(t *testing.T) {
		t.Parallel()
		policy := NewIndexRouter(indexSlotCacheConfig{
			minFanOut:    2,
			maxFanOut:    8,
			decayFactor:  0.999,
			fanOutPerReq: 500,
		})
		policy.cache.getOrCreate("test-index")

		snap := collectRouterSnapshot(policy)
		require.NotNil(t, snap)
		require.Len(t, snap.Indexes, 1)
		require.Equal(t, "test-index", snap.Indexes[0].Name)
	})

	t.Run("returns nil when no provider exists", func(t *testing.T) {
		t.Parallel()
		// A bare policy without cache doesn't implement routerSnapshotProvider.
		type barePolicy struct{ Policy }
		snap := collectRouterSnapshot(&barePolicy{})
		require.Nil(t, snap)
	})
}

func TestConnectionInspectionMethods(t *testing.T) {
	t.Parallel()

	t.Run("RTTMedian with data", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 512*time.Microsecond, 0)
		median := conn.RTTMedian()
		require.Greater(t, median, time.Duration(0))
	})

	t.Run("RTTMedian without data", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{
			URL:     &url.URL{Scheme: "https", Host: "node:9200"},
			rttRing: newRTTRing(4),
		}
		require.Equal(t, time.Duration(-1), conn.RTTMedian())
	})

	t.Run("RTTMedian with nil ring", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "node:9200"}}
		require.Equal(t, time.Duration(-1), conn.RTTMedian())
	})

	t.Run("RTTBucket with data", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 512*time.Microsecond, 0)
		bucket := conn.RTTBucket()
		require.Positive(t, bucket)
	})

	t.Run("RTTBucket without data", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{
			URL:     &url.URL{Scheme: "https", Host: "node:9200"},
			rttRing: newRTTRing(4),
		}
		require.Equal(t, int64(-1), conn.RTTBucket())
	})

	t.Run("EstLoad", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 200*time.Microsecond, 42.5)
		require.InDelta(t, 42.5, conn.EstLoad(), 0.01)
	})

	t.Run("EstLoad zero", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{URL: &url.URL{Scheme: "https", Host: "node:9200"}}
		require.InDelta(t, 0.0, conn.EstLoad(), 0)
	})
}

func TestBuildConnectionMetricScoringFields(t *testing.T) {
	t.Parallel()

	t.Run("includes RTT and load when available", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 512*time.Microsecond, 10.0)
		conn.Name = "test-node"
		conn.Roles = roleSet{"data": {}}

		cm := buildConnectionMetric(conn)

		require.NotNil(t, cm.RTTBucket)
		require.Positive(t, *cm.RTTBucket)
		require.NotNil(t, cm.RTTMedian)
		require.NotNil(t, cm.EstLoad)
		require.InDelta(t, 10.0, *cm.EstLoad, 0.01)
	})

	t.Run("omits RTT when no data", func(t *testing.T) {
		t.Parallel()
		conn := &Connection{
			URL:     &url.URL{Scheme: "https", Host: "node:9200"},
			rttRing: newRTTRing(4),
		}

		cm := buildConnectionMetric(conn)

		require.Nil(t, cm.RTTBucket)
		require.Nil(t, cm.RTTMedian)
	})

	t.Run("omits est load when zero", func(t *testing.T) {
		t.Parallel()
		conn := scoreTestConn(t, "node-1", 512*time.Microsecond, 0)

		cm := buildConnectionMetric(conn)

		require.Nil(t, cm.EstLoad)
	})
}
