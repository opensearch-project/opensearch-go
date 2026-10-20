// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewIndexSlotCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cfg        indexSlotCacheConfig
		wantMin    int
		wantMax    int
		wantTTL    time.Duration
		wantDecay  float64
		wantPerReq float64
	}{
		{
			name:    "defaults",
			wantMin: defaultMinFanOut, wantMax: defaultMaxFanOut, wantTTL: defaultIdleEvictionTTL,
			wantDecay: defaultDecayFactor, wantPerReq: defaultFanOutPerRequest,
		},
		{
			name: "custom values preserved",
			cfg: indexSlotCacheConfig{
				minFanOut: 3, maxFanOut: 10, idleEvictionTTL: 5 * time.Minute, decayFactor: 0.99, fanOutPerReq: 100,
			},
			wantMin: 3, wantMax: 10, wantTTL: 5 * time.Minute, wantDecay: 0.99, wantPerReq: 100,
		},
		{
			name:    "a decay above 1 is clamped to the default",
			cfg:     indexSlotCacheConfig{decayFactor: 1.5},
			wantMin: defaultMinFanOut, wantMax: defaultMaxFanOut, wantTTL: defaultIdleEvictionTTL,
			wantDecay: defaultDecayFactor, wantPerReq: defaultFanOutPerRequest,
		},
		{
			name:    "a negative decay is clamped to the default",
			cfg:     indexSlotCacheConfig{decayFactor: -0.5},
			wantMin: defaultMinFanOut, wantMax: defaultMaxFanOut, wantTTL: defaultIdleEvictionTTL,
			wantDecay: defaultDecayFactor, wantPerReq: defaultFanOutPerRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newIndexSlotCache(tt.cfg)
			require.Equal(t, tt.wantMin, c.minFanOut)
			require.Equal(t, tt.wantMax, c.maxFanOut)
			require.Equal(t, tt.wantTTL, c.idleEvictionTTL)
			require.InDelta(t, tt.wantDecay, c.decayFactor, 1e-9)
			require.InDelta(t, tt.wantPerReq, c.fanOutPerReq, 1e-9)
		})
	}
}

func TestIndexSlotCacheGetOrCreate(t *testing.T) {
	t.Parallel()

	t.Run("creates new slot", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})

		slot := c.getOrCreate("test-index")
		require.NotNil(t, slot)
		require.Equal(t, int32(defaultMinFanOut), slot.fanOut.Load())
		require.Greater(t, slot.requestDecay.load(), 0.0, "should have been incremented")
	})

	t.Run("returns existing slot", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})

		slot1 := c.getOrCreate("test-index")
		slot2 := c.getOrCreate("test-index")
		require.Same(t, slot1, slot2, "should return the same slot object")

		// Decay counter should have been incremented twice (once per getOrCreate).
		require.Greater(t, slot1.requestDecay.load(), 1.5, "should have 2 increments")
	})

	t.Run("clears idle state on access", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})

		slot := c.getOrCreate("test-index")
		// Simulate idle state.
		slot.idleSince.Store(time.Now().UnixNano())

		// Access should clear idle.
		c.getOrCreate("test-index")
		require.Equal(t, int64(0), slot.idleSince.Load(), "should clear idle on access")
	})

	t.Run("concurrent creation safety", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})

		const goroutines = 20
		slots := make([]*indexSlot, goroutines)
		var wg sync.WaitGroup
		wg.Add(goroutines)

		for i := range goroutines {
			go func(idx int) {
				defer wg.Done()
				slots[idx] = c.getOrCreate("contended-index")
			}(i)
		}
		wg.Wait()

		// All goroutines should get the same slot.
		for i := 1; i < goroutines; i++ {
			require.Same(t, slots[0], slots[i], "all goroutines should share the same slot")
		}
	})
}

// TestIndexSlotCacheRegister: register gives an index a slot without counting a
// request against it, and callers racing to register or use the same index all
// end up with the one slot.
func TestIndexSlotCacheRegister(t *testing.T) {
	t.Parallel()

	t.Run("creates a slot that has seen no request", func(t *testing.T) {
		t.Parallel()

		c := newIndexSlotCache(indexSlotCacheConfig{minFanOut: 3})

		slot := c.register("idx")
		require.NotNil(t, slot)
		require.EqualValues(t, 3, slot.fanOut.Load())
		require.InDelta(t, 0.0, slot.requestDecay.load(), 1e-9)
		require.Same(t, slot, c.slotFor("idx"), "the slot is the cache's slot for the index")
	})

	t.Run("an existing slot keeps its request count", func(t *testing.T) {
		t.Parallel()

		c := newIndexSlotCache(indexSlotCacheConfig{})
		slot := c.getOrCreate("idx")
		before := slot.requestDecay.load()

		require.Same(t, slot, c.register("idx"))
		require.InDelta(t, before, slot.requestDecay.load(), 1e-9, "register is not a request")
	})

	t.Run("indexes have slots of their own", func(t *testing.T) {
		t.Parallel()

		c := newIndexSlotCache(indexSlotCacheConfig{})
		require.NotSame(t, c.register("a"), c.register("b"))
	})

	t.Run("concurrent register and use share one slot", func(t *testing.T) {
		t.Parallel()

		const (
			registrars = 16
			users      = 16
		)
		c := newIndexSlotCache(indexSlotCacheConfig{})

		start := make(chan struct{})
		slots := make([]*indexSlot, registrars+users)

		var wg sync.WaitGroup
		for i := range registrars + users {
			wg.Go(func() {
				<-start
				if i < registrars {
					slots[i] = c.register("contended-index")
				} else {
					slots[i] = c.getOrCreate("contended-index")
				}
			})
		}
		close(start)
		wg.Wait()

		for _, slot := range slots {
			require.Same(t, slots[0], slot, "every caller shares the same slot")
		}

		// Each getOrCreate counted one request on the shared slot, whichever of
		// them created it, and no register counted any.
		var want float64
		for range users {
			want = want*c.decayFactor + 1
		}
		require.InDelta(t, want, slots[0].requestDecay.load(), 1e-6)
	})
}

func TestIndexSlotCacheEffectiveFanOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  indexSlotCacheConfig
		// shardNodeCount and requestDecay are stored on the slot when non-zero.
		shardNodeCount int32
		requestDecay   float64
		index          string
		active         int
		want           int
	}{
		{
			name: "per-index override wins", cfg: indexSlotCacheConfig{overrides: map[string]int{"special": 5}},
			index: "special", active: 100, want: 5,
		},
		{
			name: "override clamped to active nodes", cfg: indexSlotCacheConfig{overrides: map[string]int{"special": 10}},
			index: "special", active: 3, want: 3,
		},
		{name: "shard floor raises fan-out", cfg: indexSlotCacheConfig{minFanOut: 1}, shardNodeCount: 4, index: "idx", active: 10, want: 4},
		{
			name: "shard floor capped by maxFanOut", cfg: indexSlotCacheConfig{minFanOut: 1, maxFanOut: 8},
			shardNodeCount: 100, index: "idx", active: 200, want: 8,
		},
		{name: "default maxFanOut caps pathological shard count", shardNodeCount: 100, index: "idx", active: 200, want: defaultMaxFanOut},
		{
			// A decay of 500 at 100 per request gives a rate fan-out of 6.
			name: "maxFanOut caps result", cfg: indexSlotCacheConfig{maxFanOut: 3, fanOutPerReq: 100},
			requestDecay: 500, index: "idx", active: 100, want: 3,
		},
		{
			// 500/100 + 1 = 6.
			name: "rate-driven fan-out from decay counter", cfg: indexSlotCacheConfig{fanOutPerReq: 100, minFanOut: 1},
			requestDecay: 500, index: "idx", active: 20, want: 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newIndexSlotCache(tt.cfg)
			slot := &indexSlot{}
			if tt.shardNodeCount != 0 {
				slot.shardNodeCount.Store(tt.shardNodeCount)
			}
			if tt.requestDecay != 0 {
				slot.requestDecay.store(tt.requestDecay)
			}

			require.Equal(t, tt.want, c.effectiveFanOut(slot, tt.index, tt.active))
		})
	}
}

func TestClampFanOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		n, maxFanOut int
		want         int
	}{
		{name: "zero is raised to one", n: 0, maxFanOut: 10, want: 1},
		{name: "a negative is raised to one", n: -5, maxFanOut: 10, want: 1},
		{name: "within the maximum", n: 5, maxFanOut: 10, want: 5},
		{name: "capped at the maximum", n: 5, maxFanOut: 3, want: 3},
		{name: "no maximum", n: 5, maxFanOut: 0, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, clampFanOut(tt.n, tt.maxFanOut))
		})
	}
}

func TestIndexSlotCacheUpdateFromDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("updates shard placement", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})
		slot := c.getOrCreate("my-index")

		shardPlacement := map[string]*indexShardPlacement{
			"my-index": {Nodes: map[string]*shardNodeInfo{
				"node1": {Primaries: 1},
				"node2": {Replicas: 1},
				"node3": {Primaries: 1, Replicas: 1},
			}},
		}

		c.updateFromDiscovery(shardPlacement, 10, time.Now())

		require.Equal(t, int32(3), slot.shardNodeCount.Load())
		ids := slot.shardNodeNameSet()
		require.Len(t, ids, 3)
		require.Contains(t, ids, "node1")
	})

	t.Run("nil shard placement preserves existing data", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})
		slot := c.getOrCreate("my-index")
		slot.shardNodeCount.Store(5)

		c.updateFromDiscovery(nil, 10, time.Now())
		require.Equal(t, int32(5), slot.shardNodeCount.Load())
	})

	t.Run("evicts idle entries", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			idleEvictionTTL: 1 * time.Millisecond,
		})

		slot := c.getOrCreate("idle-index")
		// Set counter near zero and mark idle in the past.
		slot.requestDecay.store(0.1)
		slot.idleSince.Store(time.Now().Add(-1 * time.Hour).UnixNano())

		c.updateFromDiscovery(nil, 10, time.Now())

		// The entry should have been evicted.
		_, ok := c.entries.Load().Load("idle-index")
		require.False(t, ok, "idle entry should be evicted")
	})

	t.Run("active entries not evicted", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			idleEvictionTTL: 1 * time.Millisecond,
		})

		slot := c.getOrCreate("active-index")
		slot.requestDecay.store(100.0) // Clearly active

		c.updateFromDiscovery(nil, 10, time.Now())

		_, ok := c.entries.Load().Load("active-index")
		require.True(t, ok, "active entry should not be evicted")
	})

	t.Run("deleted index clears shard data", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})
		slot := c.getOrCreate("deleted-index")
		slot.shardNodeCount.Store(3)
		nodeIDs := map[string]*shardNodeInfo{"n1": {Primaries: 1}, "n2": {Replicas: 1}, "n3": {Primaries: 1, Replicas: 1}}
		slot.shardNodeNames.Store(&nodeIDs)

		// Discovery returns data without this index.
		shardPlacement := map[string]*indexShardPlacement{
			"other-index": {Nodes: map[string]*shardNodeInfo{
				"n1": {Primaries: 1},
			}},
		}
		c.updateFromDiscovery(shardPlacement, 10, time.Now())

		require.Equal(t, int32(0), slot.shardNodeCount.Load())
		require.Nil(t, slot.shardNodeNameSet())
	})
}

// TestIndexSlotCacheShardMapMerge covers the merge semantics of updateFromDiscovery
// for the per-shard placement map: a new partial /_cat/shards response (e.g. during
// a relocation window where source/destination rows are skipped) must not clobber
// shards from a previously-complete cached map, while a genuine shrink in primary
// count must drop unreachable old entries.
func TestIndexSlotCacheShardMapMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prior     *indexShardMap // nil means no prior map
		incoming  *indexShardPlacement
		wantCount int
		wantPrims int
		wantShard map[int]*shardNodes // expected entries; nil means "must not be present"
	}{
		{
			name:  "no prior map: store as-is",
			prior: nil,
			incoming: &indexShardPlacement{
				NumberOfPrimaryShards: 2,
				RoutingNumShards:      256,
				ShardToNodes: map[int]*shardNodes{
					0: {Primary: "node0"},
					1: {Primary: "node1"},
				},
			},
			wantCount: 2,
			wantPrims: 2,
			wantShard: map[int]*shardNodes{
				0: {Primary: "node0"},
				1: {Primary: "node1"},
			},
		},
		{
			name: "complete new placement overwrites prior",
			prior: &indexShardMap{
				NumberOfPrimaryShards: 2,
				RoutingNumShards:      256,
				Shards: map[int]*shardNodes{
					0: {Primary: "old0"},
					1: {Primary: "old1"},
				},
			},
			incoming: &indexShardPlacement{
				NumberOfPrimaryShards: 2,
				RoutingNumShards:      256,
				ShardToNodes: map[int]*shardNodes{
					0: {Primary: "new0"},
					1: {Primary: "new1"},
				},
			},
			wantCount: 2,
			wantPrims: 2,
			wantShard: map[int]*shardNodes{
				0: {Primary: "new0"},
				1: {Primary: "new1"},
			},
		},
		{
			name: "relocation window: missing shard preserved from prior",
			prior: &indexShardMap{
				NumberOfPrimaryShards: 3,
				RoutingNumShards:      384,
				Shards: map[int]*shardNodes{
					0: {Primary: "node0", Replicas: []string{"node1"}},
					1: {Primary: "node1", Replicas: []string{"node2"}},
					2: {Primary: "node2", Replicas: []string{"node0"}},
				},
			},
			incoming: &indexShardPlacement{
				NumberOfPrimaryShards: 3,
				RoutingNumShards:      384,
				ShardToNodes: map[int]*shardNodes{
					// Shard 1 absent: source is RELOCATING, destination is INITIALIZING,
					// both filtered out by the /_cat/shards parser.
					0: {Primary: "node0", Replicas: []string{"node3"}}, // replica moved
					2: {Primary: "node2", Replicas: []string{"node0"}},
				},
			},
			wantCount: 3,
			wantPrims: 3,
			wantShard: map[int]*shardNodes{
				0: {Primary: "node0", Replicas: []string{"node3"}}, // new wins
				1: {Primary: "node1", Replicas: []string{"node2"}}, // carried forward
				2: {Primary: "node2", Replicas: []string{"node0"}}, // new wins
			},
		},
		{
			name: "shrunk index drops shards beyond new primary count",
			prior: &indexShardMap{
				NumberOfPrimaryShards: 4,
				RoutingNumShards:      512,
				Shards: map[int]*shardNodes{
					0: {Primary: "node0"},
					1: {Primary: "node1"},
					2: {Primary: "node2"},
					3: {Primary: "node3"},
				},
			},
			incoming: &indexShardPlacement{
				NumberOfPrimaryShards: 2,
				RoutingNumShards:      512,
				ShardToNodes: map[int]*shardNodes{
					0: {Primary: "node0"},
					1: {Primary: "node1"},
				},
			},
			wantCount: 2,
			wantPrims: 2,
			wantShard: map[int]*shardNodes{
				0: {Primary: "node0"},
				1: {Primary: "node1"},
				2: nil,
				3: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newIndexSlotCache(indexSlotCacheConfig{})
			slot := c.getOrCreate("idx")
			if tt.prior != nil {
				slot.shardMap.Store(tt.prior)
			}

			c.updateFromDiscovery(map[string]*indexShardPlacement{"idx": tt.incoming}, 10, time.Now())

			sm := slot.shardMap.Load()
			require.NotNil(t, sm)
			require.Equal(t, tt.wantPrims, sm.NumberOfPrimaryShards)
			require.Len(t, sm.Shards, tt.wantCount)

			for shard, want := range tt.wantShard {
				if want == nil {
					require.NotContains(t, sm.Shards, shard, "shard %d should be dropped", shard)
					continue
				}
				got, ok := sm.Shards[shard]
				require.True(t, ok, "shard %d missing from merged map", shard)
				require.Equal(t, want.Primary, got.Primary, "shard %d primary", shard)
				require.Equal(t, want.Replicas, got.Replicas, "shard %d replicas", shard)
			}
		})
	}
}

func TestIndexSlotShardNodeNameSet(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns nil", func(t *testing.T) {
		t.Parallel()
		slot := &indexSlot{}
		require.Nil(t, slot.shardNodeNameSet())
	})

	t.Run("returns stored set", func(t *testing.T) {
		t.Parallel()
		slot := &indexSlot{}
		ids := map[string]*shardNodeInfo{"a": {Replicas: 1}, "b": {Primaries: 1}}
		slot.shardNodeNames.Store(&ids)

		got := slot.shardNodeNameSet()
		require.Len(t, got, 2)
		require.Contains(t, got, "a")
		require.Contains(t, got, "b")
	})
}

func TestIndexSlotShardNodeInfoFor(t *testing.T) {
	t.Parallel()

	stored := map[string]*shardNodeInfo{
		"primary": {Primaries: 3},
		"replica": {Replicas: 2},
		"mixed":   {Primaries: 1, Replicas: 4},
	}

	tests := []struct {
		name string
		// stored is the slot's node info; nil leaves the slot without any.
		stored map[string]*shardNodeInfo
		node   string
		want   *shardNodeInfo
	}{
		{name: "nil pointer returns nil", node: "any-node"},
		{name: "unknown node returns nil", stored: stored, node: "unknown-node"},
		{name: "a node holding primaries", stored: stored, node: "primary", want: &shardNodeInfo{Primaries: 3}},
		{name: "a node holding replicas", stored: stored, node: "replica", want: &shardNodeInfo{Replicas: 2}},
		{name: "a node holding both", stored: stored, node: "mixed", want: &shardNodeInfo{Primaries: 1, Replicas: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			slot := &indexSlot{}
			if tt.stored != nil {
				slot.shardNodeNames.Store(&tt.stored)
			}

			got := slot.shardNodeInfoFor(tt.node)
			if tt.want == nil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want.Primaries, got.Primaries)
			require.Equal(t, tt.want.Replicas, got.Replicas)
		})
	}
}

func TestIndexSlotCacheCompaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// evict is how many of the 10 entries go idle before the second discovery.
		evict         int
		wantCompacted bool
	}{
		// 4 left <= 10/2: compact.
		{name: "compacts when live count drops to 50% of HWM", evict: 6, wantCompacted: true},
		// 7 left > 10/2: no compaction.
		{name: "no compaction when above threshold", evict: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newIndexSlotCache(indexSlotCacheConfig{
				idleEvictionTTL: 1 * time.Millisecond,
			})

			// Create 10 entries to establish a high-water mark.
			for i := range 10 {
				name := "index-" + string(rune('a'+i))
				slot := c.getOrCreate(name)
				slot.requestDecay.store(100.0) // active
			}

			// First discovery: establishes HWM = 10.
			c.updateFromDiscovery(nil, 10, time.Now())
			require.Equal(t, int64(10), c.highWaterMark.Load())

			// Mark evict entries as idle and expired so they get evicted.
			oldMapPtr := c.entries.Load()
			evicted := 0
			oldMapPtr.Range(func(key, value any) bool {
				if evicted >= tt.evict {
					return false
				}
				slot := value.(*indexSlot)
				slot.requestDecay.store(0.1)
				slot.idleSince.Store(time.Now().Add(-1 * time.Hour).UnixNano())
				evicted++
				return true
			})

			c.updateFromDiscovery(nil, 10, time.Now())

			if !tt.wantCompacted {
				require.Same(t, oldMapPtr, c.entries.Load(),
					"entries should be the same sync.Map when above threshold")
				return
			}

			live := int64(10 - tt.evict)
			// HWM should be reset to the new live count.
			require.Equal(t, live, c.highWaterMark.Load())

			// The entries map should be a different pointer (compacted).
			require.NotSame(t, oldMapPtr, c.entries.Load(),
				"entries should be a new sync.Map after compaction")

			// Surviving entries should still be accessible.
			var liveCount int64
			c.entries.Load().Range(func(_, _ any) bool {
				liveCount++
				return true
			})
			require.Equal(t, live, liveCount, "should have exactly the surviving entries")
		})
	}

	t.Run("getOrCreate works after compaction", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			idleEvictionTTL: 1 * time.Millisecond,
		})

		// Create 10 entries, establish HWM.
		for i := range 10 {
			name := "index-" + string(rune('a'+i))
			slot := c.getOrCreate(name)
			slot.requestDecay.store(100.0)
		}
		c.updateFromDiscovery(nil, 10, time.Now())

		// Evict all 10 (leaving 0). Triggers compaction.
		c.entries.Load().Range(func(_, value any) bool {
			slot := value.(*indexSlot)
			slot.requestDecay.store(0.1)
			slot.idleSince.Store(time.Now().Add(-1 * time.Hour).UnixNano())
			return true
		})
		c.updateFromDiscovery(nil, 10, time.Now())

		// getOrCreate should work on the fresh map.
		slot := c.getOrCreate("new-index")
		require.NotNil(t, slot)
		require.Greater(t, slot.requestDecay.load(), 0.0)
	})
}
