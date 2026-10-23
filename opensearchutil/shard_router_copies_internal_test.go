// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// copiesTransportRouter is a transport that lists several copies of a shard.
type copiesTransportRouter struct {
	targets []opensearchtransport.ShardTarget
	err     error
}

func (f copiesTransportRouter) LookupShard(_, _ string, dst *[]*opensearchtransport.ShardTarget) error {
	*dst = (*dst)[:0]
	if f.err != nil {
		return f.err
	}

	for i := range f.targets {
		*dst = append(*dst, &f.targets[i])
	}

	return nil
}

func (copiesTransportRouter) WriteNodes() []*opensearchtransport.Connection { return nil }

// errRouter is a ShardRouter that always fails with err.
type errRouter struct{ err error }

func (r errRouter) LookupShard(_, _ string, dst *[]*ShardTarget) error {
	*dst = (*dst)[:0]

	return r.err
}

func (errRouter) WriteNodes() []Node { return nil }

func TestStaticShardRouterLookupShard(t *testing.T) {
	t.Parallel()

	router := StaticShardRouter{
		"good": DefaultShardMap(5),
		"bad":  {RoutingNumShards: 7, PrimaryShards: 5},
	}

	tests := []struct {
		name    string
		index   string
		wantErr error
	}{
		{name: "a known index lists its primary", index: "good"},
		{name: "an absent index is unknown", index: "absent", wantErr: ErrShardMapUnknown},
		{name: "an unusable shard map is unknown", index: "bad", wantErr: ErrShardMapUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dst := []*ShardTarget{{Shard: 99}}
			err := router.LookupShard(tt.index, "doc-1", &dst)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, dst)

				return
			}

			require.NoError(t, err)
			require.Len(t, dst, 1)
			require.True(t, dst[0].Primary)
			require.Equal(t, router["good"].Shard("doc-1"), dst[0].Shard)
		})
	}
}

func TestChainShardRouterLookupShardErrors(t *testing.T) {
	t.Parallel()

	reason := errors.New("discovery is off")
	known := StaticShardRouter{"idx": DefaultShardMap(5)}

	tests := []struct {
		name    string
		routers []ShardRouter
		wantErr error
		wantLen int
	}{
		{
			name:    "the first error is returned when every router fails",
			routers: []ShardRouter{errRouter{reason}, errRouter{ErrShardMapUnknown}},
			wantErr: reason,
		},
		{
			name:    "a later router answers after an earlier one fails",
			routers: []ShardRouter{errRouter{reason}, known},
			wantLen: 1,
		},
		{
			name:    "an earlier answer wins",
			routers: []ShardRouter{known, errRouter{reason}},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dst := []*ShardTarget{{Shard: 99}}
			err := ChainShardRouter(tt.routers...).LookupShard("idx", "doc-1", &dst)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, dst)

				return
			}

			require.NoError(t, err)
			require.Len(t, dst, tt.wantLen)
		})
	}
}

func TestClientShardRouterLookupShardCopies(t *testing.T) {
	t.Parallel()

	n1 := &opensearchtransport.Connection{Name: "n1"}
	n2 := &opensearchtransport.Connection{Name: "n2"}
	reason := errors.New("no router")

	t.Run("every copy is carried with its role and node", func(t *testing.T) {
		t.Parallel()

		r := clientShardRouter{tr: copiesTransportRouter{targets: []opensearchtransport.ShardTarget{
			{Shard: 3, Node: n1, Primary: true, Generation: 7},
			{Shard: 3, Node: n2, Generation: 7},
			{Shard: 3, Generation: 7},
		}}}

		var dst []*ShardTarget
		require.NoError(t, r.LookupShard("idx", "doc", &dst))
		require.Len(t, dst, 3)

		require.True(t, dst[0].Primary)
		require.Same(t, n1, dst[0].Node.Conn)
		require.False(t, dst[1].Primary)
		require.Equal(t, "n2", dst[1].Node.Name)
		require.Empty(t, dst[2].Node.Name)

		for _, target := range dst {
			require.Equal(t, 3, target.Shard)
			require.EqualValues(t, 7, target.Generation)
		}
	})

	t.Run("the transport's error is returned and dst is reset", func(t *testing.T) {
		t.Parallel()

		for _, err := range []error{reason, opensearchtransport.ErrShardMapUnknown} {
			dst := []*ShardTarget{{Shard: 99}}
			require.ErrorIs(t, clientShardRouter{tr: copiesTransportRouter{err: err}}.LookupShard("idx", "doc", &dst), err)
			require.Empty(t, dst)
		}
	})
}

// Not parallel: testing.AllocsPerRun counts allocations process-wide.
func TestClientShardRouterLookupShardReusesDst(t *testing.T) {
	n1 := &opensearchtransport.Connection{Name: "n1"}
	r := clientShardRouter{tr: copiesTransportRouter{targets: []opensearchtransport.ShardTarget{
		{Shard: 3, Node: n1, Primary: true},
		{Shard: 3, Node: n1},
	}}}

	var dst []*ShardTarget
	require.NoError(t, r.LookupShard("idx", "doc", &dst))
	require.Len(t, dst, 2)

	first := append([]*ShardTarget(nil), dst...)
	require.NoError(t, r.LookupShard("idx", "doc", &dst))
	for i := range first {
		require.Same(t, first[i], dst[i], "copy %d reuses its ShardTarget", i)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_ = r.LookupShard("idx", "doc", &dst)
	})
	require.Zero(t, allocs, "a lookup into a grown dst allocates nothing")
}
