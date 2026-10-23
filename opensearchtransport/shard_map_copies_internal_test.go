// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// copiesTransport builds a transport whose index "idx" has the given shard
// placement for every shard, and returns the shard key resolves to.
func copiesTransport(t *testing.T, nodes *shardNodes, conns ...*Connection) (*Transport, int) {
	t.Helper()

	m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
	shard := m.Shard("doc-1")

	tr, r := shardMapTransport(conns...)
	r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
		NumberOfPrimaryShards: m.PrimaryShards,
		RoutingNumShards:      m.RoutingNumShards,
		Generation:            9,
		Shards:                map[int]*shardNodes{shard: nodes},
	})

	return tr, shard
}

func notReady(c *Connection) *Connection {
	c.clearLifecycleBit(lcActive)

	return c
}

func TestTransportLookupShardCopies(t *testing.T) {
	t.Parallel()

	type copyOf struct {
		node    string
		primary bool
	}

	tests := []struct {
		name  string
		conns func() []*Connection
		nodes *shardNodes
		want  []copyOf
	}{
		{
			name: "the primary comes first, then each ready replica in order",
			conns: func() []*Connection {
				return []*Connection{
					routableConn(t, "n1", time.Millisecond),
					routableConn(t, "n2", time.Millisecond),
					routableConn(t, "n3", time.Millisecond),
				}
			},
			nodes: &shardNodes{Primary: "n2", Replicas: []string{"n3", "n1"}},
			want:  []copyOf{{"n2", true}, {"n3", false}, {"n1", false}},
		},
		{
			name: "a replica that is not ready is left out",
			conns: func() []*Connection {
				return []*Connection{
					routableConn(t, "n1", time.Millisecond),
					notReady(routableConn(t, "n2", time.Millisecond)),
					routableConn(t, "n3", time.Millisecond),
				}
			},
			nodes: &shardNodes{Primary: "n1", Replicas: []string{"n2", "n3"}},
			want:  []copyOf{{"n1", true}, {"n3", false}},
		},
		{
			name: "a replica that is not in the pool is left out",
			conns: func() []*Connection {
				return []*Connection{routableConn(t, "n1", time.Millisecond)}
			},
			nodes: &shardNodes{Primary: "n1", Replicas: []string{"gone"}},
			want:  []copyOf{{"n1", true}},
		},
		{
			name: "a replica with a stale placement is left out",
			conns: func() []*Connection {
				stale := routableConn(t, "n2", time.Millisecond)
				stale.setNeedsCatUpdate()

				return []*Connection{routableConn(t, "n1", time.Millisecond), stale}
			},
			nodes: &shardNodes{Primary: "n1", Replicas: []string{"n2"}},
			want:  []copyOf{{"n1", true}},
		},
		{
			name: "a primary that is not ready is listed with no node, before the replicas",
			conns: func() []*Connection {
				return []*Connection{
					notReady(routableConn(t, "n1", time.Millisecond)),
					routableConn(t, "n2", time.Millisecond),
				}
			},
			nodes: &shardNodes{Primary: "n1", Replicas: []string{"n2"}},
			want:  []copyOf{{"", true}, {"n2", false}},
		},
		{
			name: "a shard with no placement lists only its primary, with no node",
			conns: func() []*Connection {
				return []*Connection{routableConn(t, "n1", time.Millisecond)}
			},
			nodes: nil,
			want:  []copyOf{{"", true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, shard := copiesTransport(t, tt.nodes, tt.conns()...)

			var dst []*ShardTarget
			require.NoError(t, tr.LookupShard("idx", "doc-1", &dst))

			got := make([]copyOf, len(dst))
			for i, target := range dst {
				got[i].primary = target.Primary
				if target.Node != nil {
					got[i].node = target.Node.Name
				}

				require.Equal(t, shard, target.Shard)
				require.EqualValues(t, 9, target.Generation)
			}

			require.Equal(t, tt.want, got)
		})
	}
}

func TestTransportLookupShardErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func() *Transport
		index string
		is    error
	}{
		{
			name: "an index with no shard map",
			setup: func() *Transport {
				tr, _ := shardMapTransport(routableConn(t, "n1", time.Millisecond))

				return tr
			},
			index: "other",
			is:    ErrShardMapUnknown,
		},
		{
			name: "an index whose shard map has no geometry",
			setup: func() *Transport {
				tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
				r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{})

				return tr
			},
			index: "idx",
			is:    ErrShardMapUnknown,
		},
		{
			name:  "a transport with no router reports why",
			setup: func() *Transport { return &Transport{} },
			index: "idx",
			is:    errShardMapNoRouter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stale := &ShardTarget{Shard: 4}
			dst := []*ShardTarget{stale, {Shard: 5}}

			err := tt.setup().LookupShard(tt.index, "doc-1", &dst)
			require.ErrorIs(t, err, tt.is)
			require.Empty(t, dst, "dst is reset on an error")
		})
	}
}

// Not parallel: testing.AllocsPerRun counts allocations process-wide.
func TestTransportLookupShardReusesDst(t *testing.T) {
	tr, _ := copiesTransport(t, &shardNodes{Primary: "n1", Replicas: []string{"n2", "n3"}},
		routableConn(t, "n1", time.Millisecond),
		routableConn(t, "n2", time.Millisecond),
		routableConn(t, "n3", time.Millisecond),
	)

	var dst []*ShardTarget
	require.NoError(t, tr.LookupShard("idx", "doc-1", &dst))
	require.Len(t, dst, 3)

	first := append([]*ShardTarget(nil), dst...)

	require.NoError(t, tr.LookupShard("idx", "doc-1", &dst))
	require.Len(t, dst, 3)
	for i := range first {
		require.Same(t, first[i], dst[i], "copy %d reuses its ShardTarget", i)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_ = tr.LookupShard("idx", "doc-1", &dst)
	})
	require.Zero(t, allocs, "a lookup into a grown dst allocates nothing")
}
