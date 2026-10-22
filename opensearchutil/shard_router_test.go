// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

func TestDefaultRoutingNumShards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		numShards int
		want      int
	}{
		{name: "zero", numShards: 0, want: 0},
		{name: "negative", numShards: -3, want: 0},
		{name: "1 shard", numShards: 1, want: 1024},
		{name: "2 shards", numShards: 2, want: 1024},
		{name: "3 shards", numShards: 3, want: 768},
		{name: "5 shards", numShards: 5, want: 640},
		{name: "8 shards", numShards: 8, want: 1024},
		{name: "32 shards", numShards: 32, want: 1024},
		{name: "100 shards", numShards: 100, want: 800},
		{name: "512 shards splits once", numShards: 512, want: 1024},
		{name: "1000 shards splits once", numShards: 1000, want: 2000},
		{name: "1024 shards splits once", numShards: 1024, want: 2048},
		{name: "2000 shards splits once", numShards: 2000, want: 4000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, opensearchutil.DefaultRoutingNumShards(tt.numShards))
		})
	}
}

// TestDefaultRoutingNumShardsInvariants checks the properties the server relies
// on, independent of the table above: the result is a power-of-two multiple of
// the shard count, so every shard owns a whole number of hash slots, and the
// index can be split at least once.
func TestDefaultRoutingNumShardsInvariants(t *testing.T) {
	t.Parallel()

	for numShards := 1; numShards <= 2048; numShards++ {
		routing := opensearchutil.DefaultRoutingNumShards(numShards)

		require.Zero(t, routing%numShards, "numShards=%d routing=%d", numShards, routing)
		factor := routing / numShards
		require.Zero(t, factor&(factor-1), "routing factor %d is not a power of two (numShards=%d)", factor, numShards)
		require.GreaterOrEqual(t, factor, 2, "numShards=%d must be splittable at least once", numShards)
	}
}

func TestStaticShardRouter(t *testing.T) {
	t.Parallel()

	router := opensearchutil.StaticShardRouter{
		"logs":                 {RoutingNumShards: 1024, PrimaryShards: 32},
		"metrics":              opensearchutil.DefaultShardMap(5),
		"zero-shards":          {RoutingNumShards: 640},
		"not-a-multiple":       {RoutingNumShards: 100, PrimaryShards: 7},
		"routing-below-shards": {RoutingNumShards: 2, PrimaryShards: 5},
	}

	tests := []struct {
		name      string
		index     string
		wantMap   opensearchutil.ShardMap
		wantFound bool
	}{
		{name: "explicit shard map", index: "logs", wantMap: opensearchutil.ShardMap{RoutingNumShards: 1024, PrimaryShards: 32}, wantFound: true},
		{name: "derived shard map", index: "metrics", wantMap: opensearchutil.DefaultShardMap(5), wantFound: true},
		{name: "unknown index", index: "absent"},
		{name: "no primary shards", index: "zero-shards"},
		{name: "routing not a multiple of shards", index: "not-a-multiple"},
		{name: "routing smaller than shards", index: "routing-below-shards"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, ok := lookupOne(router, tt.index, "doc-1")
			require.Equal(t, tt.wantFound, ok)
			if !tt.wantFound {
				return
			}

			require.Equal(t, shardhash.ForRouting("doc-1", tt.wantMap.RoutingNumShards, tt.wantMap.PrimaryShards), target.Shard)
			require.Empty(t, target.Node.Name, "a static router places nothing on a node")
		})
	}

	require.Empty(t, router.WriteNodes())
}

func TestStaticShardRouterNil(t *testing.T) {
	t.Parallel()

	var router opensearchutil.StaticShardRouter

	_, ok := lookupOne(router, "any", "id")
	require.False(t, ok)
}

func TestNoShardRouter(t *testing.T) {
	t.Parallel()

	_, ok := lookupOne(opensearchutil.NoShardRouter, "idx", "id")
	require.False(t, ok)
	require.Empty(t, opensearchutil.NoShardRouter.WriteNodes())
}

// TestStaticShardRouterAgainstShardhash proves a derived shard map is directly
// usable for every derived shard count: the shard is in range, and a routing
// value always maps to the same shard.
func TestStaticShardRouterAgainstShardhash(t *testing.T) {
	t.Parallel()

	for _, numShards := range []int{1, 2, 3, 5, 8, 16, 32, 100} {
		router := opensearchutil.StaticShardRouter{"idx": opensearchutil.DefaultShardMap(numShards)}

		for _, id := range []string{"a", "doc-1", "user42", "tenant:acme", "café"} {
			target, ok := lookupOne(router, "idx", id)
			require.True(t, ok)
			require.GreaterOrEqual(t, target.Shard, 0)
			require.Less(t, target.Shard, numShards, "numShards=%d id=%q", numShards, id)
		}
	}
}

// recordingRouter is a ShardRouter that records how often it is asked.
type recordingRouter struct {
	shardMap opensearchutil.ShardMap
	known    bool
	nodes    []opensearchutil.Node
	lookups  int
}

func (r *recordingRouter) LookupShard(_, key string, dst *[]*opensearchutil.ShardTarget) error {
	r.lookups++
	*dst = (*dst)[:0]
	if !r.known {
		return opensearchutil.ErrShardMapUnknown
	}

	*dst = append(*dst, &opensearchutil.ShardTarget{
		Shard:   r.shardMap.Shard(key),
		Node:    opensearchutil.Node{Name: "cluster"},
		Primary: true,
	})

	return nil
}

func (r *recordingRouter) WriteNodes() []opensearchutil.Node { return r.nodes }

func TestChainShardRouter(t *testing.T) {
	t.Parallel()

	// A split of a 2-shard index created with number_of_routing_shards=8.
	split := opensearchutil.ShardMap{RoutingNumShards: 8, PrimaryShards: 4}
	derived := opensearchutil.DefaultShardMap(4)

	cluster := func(known bool) *recordingRouter { return &recordingRouter{shardMap: split, known: known} }

	t.Run("the cluster's shard map wins over the derivation", func(t *testing.T) {
		t.Parallel()

		first := cluster(true)
		second := &recordingRouter{shardMap: derived, known: true}
		target, ok := lookupOne(opensearchutil.ChainShardRouter(first, second), "idx", "doc-1")

		require.True(t, ok)
		require.Equal(t, split.Shard("doc-1"), target.Shard)
		require.Equal(t, "cluster", target.Node.Name)
		require.Zero(t, second.lookups, "the derivation must not be consulted when the cluster knows the index")
	})

	t.Run("falls back to the derivation when the cluster does not know", func(t *testing.T) {
		t.Parallel()

		first := cluster(false)
		second := &recordingRouter{shardMap: derived, known: true}
		target, ok := lookupOne(opensearchutil.ChainShardRouter(first, second), "idx", "doc-1")

		require.True(t, ok)
		require.Equal(t, derived.Shard("doc-1"), target.Shard)
		require.Equal(t, 1, first.lookups)
	})

	t.Run("nobody knows", func(t *testing.T) {
		t.Parallel()

		_, ok := lookupOne(opensearchutil.ChainShardRouter(cluster(false), cluster(false)), "idx", "id")
		require.False(t, ok)
	})

	t.Run("write nodes come from the first router that has any", func(t *testing.T) {
		t.Parallel()

		withNodes := &recordingRouter{nodes: []opensearchutil.Node{{Name: "n1"}, {Name: "n2"}}}
		other := &recordingRouter{nodes: []opensearchutil.Node{{Name: "x"}}}
		got := opensearchutil.ChainShardRouter(cluster(false), withNodes, other).WriteNodes()

		require.Equal(t, []opensearchutil.Node{{Name: "n1"}, {Name: "n2"}}, got)
	})

	t.Run("no write nodes when no router has any", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, opensearchutil.ChainShardRouter(cluster(false), &recordingRouter{}).WriteNodes())
	})

	t.Run("nil routers are skipped and none disables routing", func(t *testing.T) {
		t.Parallel()

		only := &recordingRouter{shardMap: derived, known: true}
		_, ok := lookupOne(opensearchutil.ChainShardRouter(nil, only, nil), "idx", "id")
		require.True(t, ok)

		require.Nil(t, opensearchutil.ChainShardRouter())
		require.Nil(t, opensearchutil.ChainShardRouter(nil, nil))
	})
}
