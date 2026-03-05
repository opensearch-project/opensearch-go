// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCatShardsEntryParsing(t *testing.T) {
	t.Parallel()

	raw := `[
		{"index":"my-index","shard":"0","prirep":"p","state":"STARTED","node":"node-1"},
		{"index":"my-index","shard":"0","prirep":"r","state":"STARTED","node":"node-2"},
		{"index":"my-index","shard":"1","prirep":"p","state":"INITIALIZING","node":"node-3"},
		{"index":"other-index","shard":"0","prirep":"p","state":"UNASSIGNED","node":""}
	]`

	var entries []catShardsEntry
	err := json.Unmarshal([]byte(raw), &entries)
	require.NoError(t, err)
	require.Len(t, entries, 4)

	// Verify first entry fields mapped correctly.
	require.Equal(t, "my-index", entries[0].Index)
	require.Equal(t, "0", entries[0].Shard)
	require.Equal(t, "p", entries[0].PriRep)
	require.Equal(t, "STARTED", entries[0].State)
	require.Equal(t, "node-1", entries[0].Node)

	// Verify replica entry.
	require.Equal(t, "r", entries[1].PriRep)
	require.Equal(t, "node-2", entries[1].Node)

	// Verify INITIALIZING entry.
	require.Equal(t, "INITIALIZING", entries[2].State)

	// Verify UNASSIGNED with empty node.
	require.Equal(t, "UNASSIGNED", entries[3].State)
	require.Empty(t, entries[3].Node)
}

// buildShardPlacement applies the same filtering logic as getShardPlacement's
// parsing loop: only STARTED shards with a non-empty node and non-empty index
// are included.
func buildShardPlacement(entries []catShardsEntry) map[string]*indexShardPlacement {
	result := make(map[string]*indexShardPlacement)
	for _, entry := range entries {
		if entry.Index == "" || entry.Node == "" {
			continue // Unassigned shard (no node)
		}
		if entry.State != shardStateStarted {
			continue // Skip unhealthy states
		}

		placement, ok := result[entry.Index]
		if !ok {
			placement = &indexShardPlacement{
				Nodes: make(map[string]*shardNodeInfo),
			}
			result[entry.Index] = placement
		}

		info, ok := placement.Nodes[entry.Node]
		if !ok {
			info = &shardNodeInfo{}
			placement.Nodes[entry.Node] = info
		}

		switch entry.PriRep {
		case shardTypePrimary:
			info.Primaries++
		case shardTypeReplica:
			info.Replicas++
		}
	}
	return result
}

func TestShardPlacementFiltering(t *testing.T) {
	t.Parallel()

	t.Run("only STARTED shards included", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "idx", Shard: "0", PriRep: "p", State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "1", PriRep: "p", State: shardStateInitializing, Node: "n2"},
			{Index: "idx", Shard: "2", PriRep: "p", State: shardStateRelocating, Node: "n3"},
			{Index: "idx", Shard: "3", PriRep: "p", State: shardStateUnassigned, Node: ""},
		}

		result := buildShardPlacement(entries)
		require.Len(t, result, 1)
		require.Len(t, result["idx"].Nodes, 1, "only STARTED shard on n1 should be included")
		require.Contains(t, result["idx"].Nodes, "n1")
	})

	t.Run("empty node filtered", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "idx", Shard: "0", PriRep: "p", State: shardStateStarted, Node: ""},
			{Index: "idx", Shard: "1", PriRep: "p", State: shardStateStarted, Node: "n1"},
		}

		result := buildShardPlacement(entries)
		require.Len(t, result["idx"].Nodes, 1)
		require.Contains(t, result["idx"].Nodes, "n1")
	})

	t.Run("primary and replica counting", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "idx", Shard: "0", PriRep: shardTypePrimary, State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "0", PriRep: shardTypeReplica, State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "1", PriRep: shardTypePrimary, State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "1", PriRep: shardTypeReplica, State: shardStateStarted, Node: "n2"},
		}

		result := buildShardPlacement(entries)
		require.Equal(t, 2, result["idx"].Nodes["n1"].Primaries)
		require.Equal(t, 1, result["idx"].Nodes["n1"].Replicas)
		require.Equal(t, 0, result["idx"].Nodes["n2"].Primaries)
		require.Equal(t, 1, result["idx"].Nodes["n2"].Replicas)
	})

	t.Run("multiple indexes separated correctly", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "alpha", Shard: "0", PriRep: "p", State: shardStateStarted, Node: "n1"},
			{Index: "beta", Shard: "0", PriRep: "p", State: shardStateStarted, Node: "n2"},
			{Index: "alpha", Shard: "1", PriRep: "r", State: shardStateStarted, Node: "n3"},
		}

		result := buildShardPlacement(entries)
		require.Len(t, result, 2, "should have two indexes")
		require.Contains(t, result, "alpha")
		require.Contains(t, result, "beta")
		require.Len(t, result["alpha"].Nodes, 2, "alpha should have n1 and n3")
		require.Len(t, result["beta"].Nodes, 1, "beta should have n2")
	})

	t.Run("same node multiple shards accumulate", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "idx", Shard: "0", PriRep: "p", State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "1", PriRep: "p", State: shardStateStarted, Node: "n1"},
			{Index: "idx", Shard: "2", PriRep: "p", State: shardStateStarted, Node: "n1"},
		}

		result := buildShardPlacement(entries)
		info := result["idx"].Nodes["n1"]
		require.Equal(t, 3, info.Primaries, "all three primaries should accumulate on n1")
		require.Equal(t, 0, info.Replicas)
	})

	t.Run("all shards unhealthy returns empty map", func(t *testing.T) {
		t.Parallel()

		entries := []catShardsEntry{
			{Index: "idx", Shard: "0", PriRep: "p", State: shardStateInitializing, Node: "n1"},
			{Index: "idx", Shard: "1", PriRep: "p", State: shardStateRelocating, Node: "n2"},
			{Index: "idx", Shard: "2", PriRep: "p", State: shardStateUnassigned, Node: ""},
		}

		result := buildShardPlacement(entries)
		require.Empty(t, result, "no healthy shards means empty result")
	})
}

func TestIndexShardPlacementNodeNameSet(t *testing.T) {
	t.Parallel()

	t.Run("nil placement returns nil", func(t *testing.T) {
		t.Parallel()

		var p *indexShardPlacement
		require.Nil(t, p.nodeNameSet())
	})

	t.Run("empty nodes returns nil", func(t *testing.T) {
		t.Parallel()

		p := &indexShardPlacement{
			Nodes: map[string]*shardNodeInfo{},
		}
		require.Nil(t, p.nodeNameSet())
	})

	t.Run("returns correct set of node names", func(t *testing.T) {
		t.Parallel()

		p := &indexShardPlacement{
			Nodes: map[string]*shardNodeInfo{
				"node-a": {Primaries: 2, Replicas: 1},
				"node-b": {Primaries: 1, Replicas: 0},
				"node-c": {Primaries: 0, Replicas: 3},
			},
		}

		set := p.nodeNameSet()
		require.Len(t, set, 3)
		require.Contains(t, set, "node-a")
		require.Contains(t, set, "node-b")
		require.Contains(t, set, "node-c")
	})

	t.Run("correct number of entries", func(t *testing.T) {
		t.Parallel()

		p := &indexShardPlacement{
			Nodes: map[string]*shardNodeInfo{
				"x": {Primaries: 1},
				"y": {Replicas: 1},
			},
		}

		set := p.nodeNameSet()
		require.Len(t, set, 2)
	})
}

func TestShardConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, "STARTED", shardStateStarted)
	require.Equal(t, "INITIALIZING", shardStateInitializing)
	require.Equal(t, "RELOCATING", shardStateRelocating)
	require.Equal(t, "UNASSIGNED", shardStateUnassigned)
	require.Equal(t, "p", shardTypePrimary)
	require.Equal(t, "r", shardTypeReplica)
}

func TestDefaultRouterConfig(t *testing.T) {
	t.Parallel()

	t.Run("default config values match constants", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		require.Equal(t, defaultMinFanOut, cfg.minFanOut)
		require.Equal(t, defaultMaxFanOut, cfg.maxFanOut)
		require.Equal(t, defaultIdleEvictionTTL, cfg.idleEvictionTTL)
		require.InDelta(t, defaultDecayFactor, cfg.decay, 1e-9)
		require.InDelta(t, defaultFanOutPerRequest, cfg.fanOutPerReq, 1e-9)
		require.Nil(t, cfg.overrides)
	})

	t.Run("WithMinFanOut sets value", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		WithMinFanOut(5)(&cfg)
		require.Equal(t, 5, cfg.minFanOut)
	})

	t.Run("WithMaxFanOut sets value", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		WithMaxFanOut(64)(&cfg)
		require.Equal(t, 64, cfg.maxFanOut)
	})

	t.Run("WithIndexFanOut sets overrides", func(t *testing.T) {
		t.Parallel()

		overrides := map[string]int{"my-index": 10, "other-index": 3}
		cfg := defaultRouterConfig()
		WithIndexFanOut(overrides)(&cfg)
		require.Equal(t, overrides, cfg.overrides)
	})

	t.Run("WithIdleEvictionTTL sets value", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		WithIdleEvictionTTL(30 * time.Minute)(&cfg)
		require.Equal(t, 30*time.Minute, cfg.idleEvictionTTL)
	})

	t.Run("WithDecayFactor sets value", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		WithDecayFactor(0.995)(&cfg)
		require.InDelta(t, 0.995, cfg.decay, 1e-9)
	})

	t.Run("WithFanOutPerRequest sets value", func(t *testing.T) {
		t.Parallel()

		cfg := defaultRouterConfig()
		WithFanOutPerRequest(1000)(&cfg)
		require.InDelta(t, 1000.0, cfg.fanOutPerReq, 1e-9)
	})

	t.Run("NewDefaultPolicy returns non-nil", func(t *testing.T) {
		t.Parallel()

		p := NewDefaultPolicy()
		require.NotNil(t, p)
	})

	t.Run("NewDefaultRouter returns non-nil", func(t *testing.T) {
		t.Parallel()

		r := NewDefaultRouter()
		require.NotNil(t, r)
	})

	t.Run("NewDefaultPolicy returns non-nil", func(t *testing.T) {
		t.Parallel()

		p := NewDefaultPolicy()
		require.NotNil(t, p)
	})

	t.Run("NewDefaultRouter returns non-nil", func(t *testing.T) {
		t.Parallel()

		r := NewDefaultRouter()
		require.NotNil(t, r)
	})

	t.Run("NewMuxRoutePolicy returns non-nil", func(t *testing.T) {
		t.Parallel()

		p := NewMuxRoutePolicy()
		require.NotNil(t, p)
	})

	t.Run("NewMuxRouter returns non-nil", func(t *testing.T) {
		t.Parallel()

		r := NewMuxRouter()
		require.NotNil(t, r)
	})

	t.Run("NewRoundRobinDefaultPolicy returns non-nil", func(t *testing.T) {
		t.Parallel()

		p := NewRoundRobinDefaultPolicy()
		require.NotNil(t, p)
	})

	t.Run("NewRoundRobinRouter returns non-nil", func(t *testing.T) {
		t.Parallel()

		r := NewRoundRobinRouter()
		require.NotNil(t, r)
	})
}
