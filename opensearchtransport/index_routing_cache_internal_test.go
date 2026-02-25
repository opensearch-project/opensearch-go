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

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})
		require.Equal(t, defaultMinFanOut, c.minFanOut)
		require.Equal(t, defaultMaxFanOut, c.maxFanOut)
		require.Equal(t, defaultIdleEvictionTTL, c.idleEvictionTTL)
		require.InDelta(t, defaultDecayFactor, c.decayFactor, 1e-9)
		require.InDelta(t, defaultFanOutPerRequest, c.fanOutPerReq, 1e-9)
	})

	t.Run("custom values preserved", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			minFanOut:       3,
			maxFanOut:       10,
			idleEvictionTTL: 5 * time.Minute,
			decayFactor:     0.99,
			fanOutPerReq:    100,
		})
		require.Equal(t, 3, c.minFanOut)
		require.Equal(t, 10, c.maxFanOut)
		require.Equal(t, 5*time.Minute, c.idleEvictionTTL)
		require.InDelta(t, 0.99, c.decayFactor, 1e-9)
		require.InDelta(t, 100.0, c.fanOutPerReq, 1e-9)
	})

	t.Run("invalid decay clamped to default", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{decayFactor: 1.5})
		require.InDelta(t, defaultDecayFactor, c.decayFactor, 1e-9)

		c = newIndexSlotCache(indexSlotCacheConfig{decayFactor: -0.5})
		require.InDelta(t, defaultDecayFactor, c.decayFactor, 1e-9)
	})
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

func TestIndexSlotCacheEffectiveFanOut(t *testing.T) {
	t.Parallel()

	t.Run("per-index override wins", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			overrides: map[string]int{"special": 5},
		})
		slot := &indexSlot{}
		require.Equal(t, 5, c.effectiveFanOut(slot, "special", 100))
	})

	t.Run("override clamped to active nodes", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			overrides: map[string]int{"special": 10},
		})
		slot := &indexSlot{}
		require.Equal(t, 3, c.effectiveFanOut(slot, "special", 3))
	})

	t.Run("shard floor raises fan-out", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			minFanOut: 1,
		})
		slot := &indexSlot{}
		slot.shardNodes.Store(4) // 4 shard-hosting nodes

		// Fan-out should be at least the shard node count.
		require.Equal(t, 4, c.effectiveFanOut(slot, "idx", 10))
	})

	t.Run("shard floor capped by maxFanOut", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			minFanOut: 1,
			maxFanOut: 8,
		})
		slot := &indexSlot{}
		slot.shardNodes.Store(100) // pathological: 100 shard-hosting nodes

		// shardFloor=100 but maxFanOut=8 caps it.
		require.Equal(t, 8, c.effectiveFanOut(slot, "idx", 200))
	})

	t.Run("default maxFanOut caps pathological shard count", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{}) // defaultMaxFanOut = 32
		slot := &indexSlot{}
		slot.shardNodes.Store(100)

		require.Equal(t, 32, c.effectiveFanOut(slot, "idx", 200))
	})

	t.Run("maxFanOut caps result", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			maxFanOut:    3,
			fanOutPerReq: 100,
		})
		slot := &indexSlot{}
		slot.requestDecay.store(500.0) // Would give rateFanOut = 6

		require.Equal(t, 3, c.effectiveFanOut(slot, "idx", 100))
	})

	t.Run("rate-driven fan-out from decay counter", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			fanOutPerReq: 100,
			minFanOut:    1,
		})
		slot := &indexSlot{}
		// Simulate high request rate.
		slot.requestDecay.store(500.0) // 500/100 + 1 = 6

		got := c.effectiveFanOut(slot, "idx", 20)
		require.Equal(t, 6, got)
	})
}

func TestClampFanOut(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, clampFanOut(0, 10))
	require.Equal(t, 1, clampFanOut(-5, 10))
	require.Equal(t, 5, clampFanOut(5, 10))
	require.Equal(t, 3, clampFanOut(5, 3))
	require.Equal(t, 5, clampFanOut(5, 0))
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

		require.Equal(t, int32(3), slot.shardNodes.Load())
		ids := slot.shardNodeNameSet()
		require.Len(t, ids, 3)
		require.Contains(t, ids, "node1")
	})

	t.Run("nil shard placement preserves existing data", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{})
		slot := c.getOrCreate("my-index")
		slot.shardNodes.Store(5)

		c.updateFromDiscovery(nil, 10, time.Now())
		require.Equal(t, int32(5), slot.shardNodes.Load())
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
		slot.shardNodes.Store(3)
		nodeIDs := map[string]*shardNodeInfo{"n1": {Primaries: 1}, "n2": {Replicas: 1}, "n3": {Primaries: 1, Replicas: 1}}
		slot.shardNodeNames.Store(&nodeIDs)

		// Discovery returns data without this index.
		shardPlacement := map[string]*indexShardPlacement{
			"other-index": {Nodes: map[string]*shardNodeInfo{
				"n1": {Primaries: 1},
			}},
		}
		c.updateFromDiscovery(shardPlacement, 10, time.Now())

		require.Equal(t, int32(0), slot.shardNodes.Load())
		require.Nil(t, slot.shardNodeNameSet())
	})
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

	t.Run("nil pointer returns nil", func(t *testing.T) {
		t.Parallel()
		slot := &indexSlot{}
		require.Nil(t, slot.shardNodeInfoFor("any-node"))
	})

	t.Run("unknown node returns nil", func(t *testing.T) {
		t.Parallel()
		slot := &indexSlot{}
		info := map[string]*shardNodeInfo{"node1": {Primaries: 1}}
		slot.shardNodeNames.Store(&info)
		require.Nil(t, slot.shardNodeInfoFor("unknown-node"))
	})

	t.Run("returns correct info", func(t *testing.T) {
		t.Parallel()
		slot := &indexSlot{}
		info := map[string]*shardNodeInfo{
			"primary": {Primaries: 3},
			"replica": {Replicas: 2},
			"mixed":   {Primaries: 1, Replicas: 4},
		}
		slot.shardNodeNames.Store(&info)

		got := slot.shardNodeInfoFor("primary")
		require.NotNil(t, got)
		require.Equal(t, 3, got.Primaries)
		require.Equal(t, 0, got.Replicas)

		got = slot.shardNodeInfoFor("replica")
		require.NotNil(t, got)
		require.Equal(t, 0, got.Primaries)
		require.Equal(t, 2, got.Replicas)

		got = slot.shardNodeInfoFor("mixed")
		require.NotNil(t, got)
		require.Equal(t, 1, got.Primaries)
		require.Equal(t, 4, got.Replicas)
	})
}

func TestIndexSlotCacheCompaction(t *testing.T) {
	t.Parallel()

	t.Run("compacts when live count drops to 50% of HWM", func(t *testing.T) {
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

		// Mark 6 entries as idle and expired so they get evicted.
		oldMapPtr := c.entries.Load()
		evicted := 0
		oldMapPtr.Range(func(key, value any) bool {
			if evicted >= 6 {
				return false
			}
			slot := value.(*indexSlot)
			slot.requestDecay.store(0.1)
			slot.idleSince.Store(time.Now().Add(-1 * time.Hour).UnixNano())
			evicted++
			return true
		})

		// Second discovery: evicts 6, leaving 4 live. 4 <= 10/2 -> compact.
		c.updateFromDiscovery(nil, 10, time.Now())

		// HWM should be reset to the new live count.
		require.Equal(t, int64(4), c.highWaterMark.Load())

		// The entries map should be a different pointer (compacted).
		require.NotSame(t, oldMapPtr, c.entries.Load(),
			"entries should be a new sync.Map after compaction")

		// Surviving entries should still be accessible.
		var liveCount int
		c.entries.Load().Range(func(_, _ any) bool {
			liveCount++
			return true
		})
		require.Equal(t, 4, liveCount, "should have exactly the surviving entries")
	})

	t.Run("no compaction when above threshold", func(t *testing.T) {
		t.Parallel()
		c := newIndexSlotCache(indexSlotCacheConfig{
			idleEvictionTTL: 1 * time.Millisecond,
		})

		// Create 10 entries.
		for i := range 10 {
			name := "index-" + string(rune('a'+i))
			slot := c.getOrCreate(name)
			slot.requestDecay.store(100.0) // active
		}

		// Establish HWM.
		c.updateFromDiscovery(nil, 10, time.Now())
		mapPtr := c.entries.Load()

		// Evict only 3 (leaving 7). 7 > 10/2 -> no compaction.
		evicted := 0
		mapPtr.Range(func(key, value any) bool {
			if evicted >= 3 {
				return false
			}
			slot := value.(*indexSlot)
			slot.requestDecay.store(0.1)
			slot.idleSince.Store(time.Now().Add(-1 * time.Hour).UnixNano())
			evicted++
			return true
		})

		c.updateFromDiscovery(nil, 10, time.Now())

		// Same map pointer -- no compaction occurred.
		require.Same(t, mapPtr, c.entries.Load(),
			"entries should be the same sync.Map when above threshold")
	})

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
