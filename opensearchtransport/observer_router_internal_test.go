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
	"sync"
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

	event := buildRouteEvent(routeEventParams{
		indexName:     "test-index",
		key:           "test-index",
		fanOut:        3,
		totalNodes:    5,
		candidates:    candidates,
		best:          conn1,
		slot:          slot,
		costs:         &shardCostForReads,
		targetShard:   -1,
		poolInfoReady: true,
	})

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

// captureCandidatesObserver records, for each OnRoute call, the length, the
// backing-array identity (address of the first element), and the candidate IDs
// of event.Candidates. When retain is true it calls RouteEvent.Retain inside
// OnRoute and stashes the event in retained, modeling an async consumer that
// takes ownership; the test drives the matching Release later.
type captureCandidatesObserver struct {
	BaseConnectionObserver
	retain   bool
	lens     []int
	firstPtr []*RouteCandidate
	ids      [][]string
	retained []RouteEvent
}

func (o *captureCandidatesObserver) OnRoute(e RouteEvent) {
	o.lens = append(o.lens, len(e.Candidates))
	if len(e.Candidates) > 0 {
		o.firstPtr = append(o.firstPtr, &e.Candidates[0])
	} else {
		o.firstPtr = append(o.firstPtr, nil)
	}
	ids := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		ids[i] = c.ID
	}
	o.ids = append(o.ids, ids)
	if o.retain {
		e.Retain()
		o.retained = append(o.retained, e)
	}
}

func TestDispatchRouteSyncNoBleed(t *testing.T) {
	t.Parallel()

	conn1 := scoreTestConn(t, "node-a", 200*time.Microsecond, 2.0)
	conn1.Name = "alpha"
	conn2 := scoreTestConn(t, "node-b", 400*time.Microsecond, 1.0)
	conn2.Name = "beta"
	conn3 := scoreTestConn(t, "node-c", 300*time.Microsecond, 1.5)
	conn3.Name = "gamma"

	slot := &indexSlot{}
	// A synchronous observer does nothing special; dispatchRoute auto-reclaims
	// the backing array after OnRoute returns, so the next dispatch may reuse it.
	obs := &captureCandidatesObserver{}

	dispatchRoute(obs, routeEventParams{
		indexName: "idx", key: "idx",
		candidates: []*Connection{conn1, conn2}, best: conn1,
		slot: slot, costs: &shardCostForReads, targetShard: -1, poolInfoReady: true,
	})
	dispatchRoute(obs, routeEventParams{
		indexName: "idx", key: "idx",
		candidates: []*Connection{conn3, conn1, conn2}, best: conn3,
		slot: slot, costs: &shardCostForReads, targetShard: -1, poolInfoReady: true,
	})

	// Auto-reclaim + reuse must not leak the first dispatch's candidates into the
	// second. Pointer-identity of the reused array is a sync.Pool implementation
	// detail asserted by the benchmark's allocs/op, not here.
	require.Equal(t, []int{2, 3}, obs.lens, "each dispatch delivers its own candidate count")
	require.Equal(t, []string{"node-a", "node-b"}, obs.ids[0], "first dispatch candidates correct")
	require.Equal(t, []string{"node-c", "node-a", "node-b"}, obs.ids[1], "second dispatch candidates correct, no bleed")
}

func TestDispatchRouteRetainedNotReused(t *testing.T) {
	t.Parallel()

	conn1 := scoreTestConn(t, "node-a", 200*time.Microsecond, 2.0)
	conn2 := scoreTestConn(t, "node-b", 400*time.Microsecond, 1.0)

	slot := &indexSlot{}
	// An async consumer retains each event and defers Release, so the transport
	// must not recycle a retained backing array under it.
	obs := &captureCandidatesObserver{retain: true}

	params := routeEventParams{
		indexName:     "idx",
		key:           "idx",
		candidates:    []*Connection{conn1, conn2},
		best:          conn1,
		slot:          slot,
		costs:         &shardCostForReads,
		targetShard:   -1,
		poolInfoReady: true,
	}
	dispatchRoute(obs, params)
	dispatchRoute(obs, params)

	// Both events are still retained (unreleased), so their backing arrays must
	// be distinct -- reusing the first under the second would corrupt candidates
	// the first consumer may still read on another goroutine.
	require.NotSame(t, obs.firstPtr[0], obs.firstPtr[1], "retained arrays must not be reused")

	// Releasing the retained references returns the arrays to the pool.
	require.NotPanics(t, func() {
		for _, e := range obs.retained {
			e.Release()
		}
	})
}

func TestRouteEventReleaseSafety(t *testing.T) {
	t.Parallel()

	conn1 := scoreTestConn(t, "node-a", 200*time.Microsecond, 2.0)
	slot := &indexSlot{}

	// Retain once inside OnRoute, then over-release: the matching Release plus an
	// extra one must not panic or return the buffer to the pool twice.
	var captured RouteEvent
	obs := &shardRoutingObserver{onRoute: func(e RouteEvent) {
		e.Retain()
		captured = e
	}}

	dispatchRoute(obs, routeEventParams{
		indexName:     "idx",
		key:           "idx",
		candidates:    []*Connection{conn1},
		best:          conn1,
		slot:          slot,
		costs:         &shardCostForReads,
		targetShard:   -1,
		poolInfoReady: true,
	})

	require.NotPanics(t, func() {
		captured.Release() // matches the Retain: refs 1 -> 0, reclaimed
		captured.Release() // over-release: refs -> -1, ignored (not a double Put)
	})

	// A zero-value / non-pooled event is safe to Retain and Release.
	require.NotPanics(t, func() {
		RouteEvent{}.Retain()
		RouteEvent{}.Release()
	})
}

// TestDispatchRouteConcurrentRetainRelease is the core async-safety check: many
// goroutines dispatch routes while their observers retain the event, hand it to
// a second goroutine, read Candidates there, and Release. Under -race this
// proves the refcount prevents a retained backing array from being reclaimed and
// reused by a concurrent dispatch while a reader still holds it.
func TestDispatchRouteConcurrentRetainRelease(t *testing.T) {
	t.Parallel()

	conn1 := scoreTestConn(t, "node-a", 200*time.Microsecond, 2.0)
	conn1.Name = "alpha"
	conn2 := scoreTestConn(t, "node-b", 400*time.Microsecond, 1.0)
	conn2.Name = "beta"
	slot := &indexSlot{}

	// The observer retains synchronously, then a reader goroutine consumes the
	// candidates and releases. A wrong count would let the array be recycled and
	// mutated under that read, tripping -race or the ID assertion.
	var wg sync.WaitGroup
	obs := &shardRoutingObserver{onRoute: func(e RouteEvent) {
		e.Retain()
		wg.Go(func() {
			defer e.Release()
			ids := make([]string, len(e.Candidates))
			for i, c := range e.Candidates {
				ids[i] = c.ID
			}
			require.Equal(t, []string{"node-a", "node-b"}, ids)
		})
	}}

	params := routeEventParams{
		indexName: "idx", key: "idx",
		candidates: []*Connection{conn1, conn2}, best: conn1,
		slot: slot, costs: &shardCostForReads, targetShard: -1, poolInfoReady: true,
	}

	var dispatchers sync.WaitGroup
	for range 64 {
		dispatchers.Go(func() {
			dispatchRoute(obs, params)
		})
	}
	dispatchers.Wait()
	wg.Wait()
}

func TestDispatchRouteDropsOversizedBacking(t *testing.T) {
	t.Parallel()

	n := routeCandidatePoolMaxCap + 1
	conns := make([]*Connection, n)
	for i := range conns {
		conns[i] = scoreTestConn(t, "node", 200*time.Microsecond, 1.0)
	}
	slot := &indexSlot{}
	obs := &captureCandidatesObserver{}

	// A fan-out beyond the pool cap must still deliver all candidates; on
	// Release the oversized backing array is dropped, not returned to the pool.
	require.NotPanics(t, func() {
		dispatchRoute(obs, routeEventParams{
			indexName:     "idx",
			key:           "idx",
			candidates:    conns,
			best:          conns[0],
			slot:          slot,
			costs:         &shardCostForReads,
			targetShard:   -1,
			poolInfoReady: true,
		})
	})

	require.Equal(t, []int{n}, obs.lens, "oversized dispatch still delivers every candidate")
}

func TestIndexRouterEmitsObserverEvent(t *testing.T) {
	t.Parallel()

	obs := newRecordingObserver()

	policy := newIndexRouter(indexSlotCacheConfig{
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

	policy := newDocRouter(cache, 0.999)

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

// BenchmarkDispatchRouteReleased proves the pooling goal: when the observer
// releases the event, repeated dispatches reuse the backing array and report
// near-zero allocations per operation. Contrast with a non-releasing observer,
// which allocates a fresh []RouteCandidate every dispatch.
func BenchmarkDispatchRouteReleased(b *testing.B) {
	conn1 := scoreTestConn(b, "node-a", 200*time.Microsecond, 2.0)
	conn2 := scoreTestConn(b, "node-b", 400*time.Microsecond, 1.0)
	slot := &indexSlot{}
	obs := &captureCandidatesObserver{}
	params := routeEventParams{
		indexName: "idx", key: "idx",
		candidates: []*Connection{conn1, conn2}, best: conn1,
		slot: slot, costs: &shardCostForReads, targetShard: -1, poolInfoReady: true,
	}

	b.ReportAllocs()
	for b.Loop() {
		// Reset capture slices so the observer's own appends don't dominate.
		obs.lens = obs.lens[:0]
		obs.firstPtr = obs.firstPtr[:0]
		obs.ids = obs.ids[:0]
		dispatchRoute(obs, params)
	}
}

// noopRouteObserver is a no-op ConnectionObserver so a benchmark measures
// dispatchRoute's own allocations (including the auto-reclaim) without the
// capture-slice overhead of captureCandidatesObserver. A synchronous observer
// does not call Release; dispatchRoute reclaims the buffer itself.
type noopRouteObserver struct{ BaseConnectionObserver }

func BenchmarkDispatchRoutePooledAllocs(b *testing.B) {
	conn1 := scoreTestConn(b, "node-a", 200*time.Microsecond, 2.0)
	conn2 := scoreTestConn(b, "node-b", 400*time.Microsecond, 1.0)
	slot := &indexSlot{}
	obs := noopRouteObserver{}
	params := routeEventParams{
		indexName: "idx", key: "idx",
		candidates: []*Connection{conn1, conn2}, best: conn1,
		slot: slot, costs: &shardCostForReads, targetShard: -1, poolInfoReady: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		dispatchRoute(obs, params)
	}
}
