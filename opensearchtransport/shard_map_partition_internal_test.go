// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// The partitioned test index has 640 shards (routing factor 1) and
// routing_partition_size 4, so the partition offset moves the shard by up to 3.
const (
	partitionedRoutingNumShards = 640
	partitionedPrimaryShards    = 640
	partitionSize               = 4
)

// docWithOffset finds a document id whose partition offset is not zero, so the
// partitioned shard differs from the one the routing value alone names.
func docWithOffset(t *testing.T, routing string) string {
	t.Helper()

	for _, id := range []string{"doc-1", "doc-2", "doc-3", "doc-4", "doc-5", "doc-6"} {
		shard, _ := shardhash.ForDocument(routing, id, partitionedRoutingNumShards, partitionedPrimaryShards, partitionSize)
		if shard != shardhash.ForRouting(routing, partitionedRoutingNumShards, partitionedPrimaryShards) {
			return id
		}
	}
	require.FailNow(t, "no test id with a non-zero partition offset")

	return ""
}

func TestGetRoutingMetaReadsTheRoutingPartitionSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		want     int
	}{
		{name: "partitioned", settings: `{"index":{"number_of_shards":"8","routing_partition_size":"4"}}`, want: 4},
		{name: "absent", settings: `{"index":{"number_of_shards":"8"}}`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var query string
			tr, err := New(Config{
				URLs:              []*url.URL{{Scheme: "http", Host: "node-a:9200"}},
				NodeStatsInterval: -1,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if !strings.HasPrefix(req.URL.Path, "/_cluster/state/metadata/") {
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
					}
					query = req.URL.RawQuery
					body := `{"metadata":{"indices":{"idx":{"routing_num_shards":1024,"settings":` + tt.settings + `}}}}`

					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tr.Close() })

			meta, err := tr.getRoutingMeta(t.Context(), []string{"idx"})
			require.NoError(t, err)
			require.Contains(t, query, "settings.index.routing_partition_size", "the filter must ask for the setting")
			require.Equal(t, tt.want, meta["idx"].RoutingPartitionSize)
		})
	}
}

func TestTransportLookupPartitionedIndex(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	id := docWithOffset(t, routing)
	wantShard, _ := shardhash.ForDocument(routing, id, partitionedRoutingNumShards, partitionedPrimaryShards, partitionSize)

	tests := []struct {
		name string
		call func(tr *Transport, dst *[]*ShardTarget) error
		// wantShard is the shard of dst[0], or -1 when the lookup must fail.
		wantShard int
	}{
		{
			name:      "key-only lookup cannot name a shard",
			call:      func(tr *Transport, dst *[]*ShardTarget) error { return tr.LookupShard("idx", routing, dst) },
			wantShard: -1,
		},
		{
			name:      "routing and id name the shard",
			call:      func(tr *Transport, dst *[]*ShardTarget) error { return tr.LookupDocShard("idx", routing, id, dst) },
			wantShard: wantShard,
		},
		{
			name:      "no id on a partitioned index",
			call:      func(tr *Transport, dst *[]*ShardTarget) error { return tr.LookupDocShard("idx", routing, "", dst) },
			wantShard: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
			r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
				NumberOfPrimaryShards: partitionedPrimaryShards,
				RoutingNumShards:      partitionedRoutingNumShards,
				RoutingPartitionSize:  partitionSize,
				Shards:                map[int]*shardNodes{wantShard: {Primary: "n1"}},
			})

			var dst []*ShardTarget
			err := tt.call(tr, &dst)
			if tt.wantShard < 0 {
				require.ErrorIs(t, err, ErrDocumentIDRequired)
				require.ErrorIs(t, err, ErrShardMapUnknown)
				require.Empty(t, dst)

				return
			}
			require.NoError(t, err)
			require.Len(t, dst, 1)
			require.Equal(t, tt.wantShard, dst[0].Shard)
			require.Equal(t, "n1", dst[0].Node.Name)
		})
	}
}

func TestTransportLookupDocShardUnpartitionedMatchesLookupShard(t *testing.T) {
	t.Parallel()

	tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
	r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 5, RoutingNumShards: 640})

	var byKey, byDoc []*ShardTarget
	require.NoError(t, tr.LookupShard("idx", "k", &byKey))
	require.NoError(t, tr.LookupDocShard("idx", "k", "other-id", &byDoc))
	require.Equal(t, byKey[0].Shard, byDoc[0].Shard)

	require.NoError(t, tr.LookupDocShard("idx", "", "k", &byDoc))
	require.Equal(t, byKey[0].Shard, byDoc[0].Shard)
}

func TestTransportShardMapCarriesTheRoutingPartitionSize(t *testing.T) {
	t.Parallel()

	tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
	r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 8, RoutingNumShards: 1024, RoutingPartitionSize: 4})

	m, ok := tr.ShardMap("idx")
	require.True(t, ok)
	require.Equal(t, ShardMap{RoutingNumShards: 1024, PrimaryShards: 8, RoutingPartitionSize: 4}, m)
}

func TestMergeShardMapKeepsTheRoutingPartitionSize(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	placement := func(rps int) *indexShardPlacement {
		return &indexShardPlacement{
			NumberOfPrimaryShards: 1,
			RoutingNumShards:      1,
			RoutingPartitionSize:  rps,
			ShardToNodes:          map[int]*shardNodes{0: {Primary: "n1"}},
		}
	}

	slot.mergeShardMap(placement(4))
	first := slot.shardMap.Load()
	require.Equal(t, 4, first.RoutingPartitionSize)

	slot.mergeShardMap(placement(4))
	require.Equal(t, first.Generation, slot.shardMap.Load().Generation, "same geometry keeps the generation")

	slot.mergeShardMap(placement(0))
	require.NotEqual(t, first.Generation, slot.shardMap.Load().Generation, "a new partition size is a new geometry")
}

func TestCalcSingleKeyCostPartitionedIndex(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	id := docWithOffset(t, routing)
	wantShard, _ := shardhash.ForDocument(routing, id, partitionedRoutingNumShards, partitionedPrimaryShards, partitionSize)

	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(&indexShardMap{
		NumberOfPrimaryShards: partitionedPrimaryShards,
		RoutingNumShards:      partitionedRoutingNumShards,
		RoutingPartitionSize:  partitionSize,
		Shards: map[int]*shardNodes{
			wantShard: {Primary: "nodeA"},
			shardhash.ForRouting(routing, partitionedRoutingNumShards, partitionedPrimaryShards): {Primary: "nodeB"},
		},
	})
	conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB")}

	tests := []struct {
		name         string
		routing, id  string
		wantShard    int
		wantNodeName string
	}{
		{name: "routing and id", routing: routing, id: id, wantShard: wantShard, wantNodeName: "nodeA"},
		{name: "no id cannot claim a shard", routing: routing, id: "", wantShard: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, tt.routing, tt.id, conns)
			defer buf.Release()

			require.Equal(t, tt.wantShard, shardNum)
			if tt.wantNodeName == "" {
				require.Empty(t, buf.Slice())

				return
			}
			require.Len(t, buf.Slice(), 1)
			require.Equal(t, tt.wantNodeName, buf.Slice()[0].Name)
		})
	}
}

// partitionedSlot builds a slot whose index has the given geometry and
// partition size, with each shard in placement hosted by its named primary.
func partitionedSlot(primaries, rps int, placement map[int]string) *indexSlot {
	shards := make(map[int]*shardNodes, len(placement))
	for shard, node := range placement {
		shards[shard] = &shardNodes{Primary: node}
	}
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(&indexShardMap{
		NumberOfPrimaryShards: primaries,
		RoutingNumShards:      partitionedRoutingNumShards,
		RoutingPartitionSize:  rps,
		Shards:                shards,
	})

	return slot
}

func TestCalcMultiKeyCostPartitionedIndex(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	var targets [partitionSize]int
	for i := range targets {
		targets[i] = shardhash.ForRoutingOffset(routing, i, partitionedRoutingNumShards, partitionedPrimaryShards)
	}

	// nodeA hosts three of the four partition shards and nodeB the fourth.
	slot := partitionedSlot(partitionedPrimaryShards, partitionSize, map[int]string{
		targets[0]: "nodeA", targets[1]: "nodeA", targets[2]: "nodeA", targets[3]: "nodeB",
	})
	conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB")}

	cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routing, conns)
	defer cBuf.Release()
	defer ecBuf.Release()

	require.Len(t, cBuf.Slice(), 2)
	require.Equal(t, "nodeA", cBuf.Slice()[0].Name)
	require.Equal(t, "nodeB", cBuf.Slice()[1].Name)
	require.Equal(t, []float64{1, 3}, ecBuf.Slice())
}

// With fewer primaries than routing_partition_size the offsets collide on a
// shard; a key counts each distinct shard once.
func TestCalcMultiKeyCostPartitionedIndexCountsDistinctShards(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	distinct := map[int]struct{}{}
	for i := range partitionSize {
		distinct[shardhash.ForRoutingOffset(routing, i, partitionedRoutingNumShards, 5)] = struct{}{}
	}
	require.Less(t, len(distinct), partitionSize, "routing value must collide on this geometry")

	placement := map[int]string{}
	for shard := range distinct {
		placement[shard] = "nodeA"
	}
	slot := partitionedSlot(5, partitionSize, placement)

	cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routing, []*Connection{newTestConn(t, "nodeA")})
	defer cBuf.Release()
	defer ecBuf.Release()

	require.Len(t, cBuf.Slice(), 1)
	require.Equal(t, []float64{0}, ecBuf.Slice())
}

func TestNeedsMultiKeyCost(t *testing.T) {
	t.Parallel()

	partitioned := partitionedSlot(partitionedPrimaryShards, partitionSize, nil)
	plain := partitionedSlot(partitionedPrimaryShards, 0, nil)

	tests := []struct {
		name        string
		slot        *indexSlot
		routing, id string
		want        bool
	}{
		{"comma list", plain, "a,b", "", true},
		{"single routing, unpartitioned", plain, "a", "", false},
		{"single routing and id, partitioned", partitioned, "a", "doc", false},
		{"single routing without id, partitioned", partitioned, "a", "", true},
		{"no routing, partitioned", partitioned, "", "", false},
		{"no shard map", &indexSlot{clock: realClock{}}, "a", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, needsMultiKeyCost(tt.slot, tt.routing, tt.id))
		})
	}
}

func TestIndexRouterPartitionedSearchPrefersTheNodeHostingMostPartitionShards(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	var targets [partitionSize]int
	for i := range targets {
		targets[i] = shardhash.ForRoutingOffset(routing, i, partitionedRoutingNumShards, partitionedPrimaryShards)
	}

	cache := newIndexSlotCache(indexSlotCacheConfig{})
	policy := newIndexRouter(indexSlotCacheConfig{})
	policy.cache = cache
	conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB"), newTestConn(t, "nodeC")}
	require.NoError(t, policy.DiscoveryUpdate(conns, nil, nil))

	slot := cache.getOrCreate("tenants")
	slot.shardMap.Store(partitionedSlot(partitionedPrimaryShards, partitionSize, map[int]string{
		// Ordinary routing lands on nodeB here, so only the partition-aware path picks nodeC.
		targets[0]: "nodeC", targets[1]: "nodeC", targets[2]: "nodeC", targets[3]: "nodeA",
	}).shardMap.Load())

	req := &http.Request{URL: &url.URL{Path: "/tenants/_search", RawQuery: "routing=" + routing}}
	hop, err := policy.Eval(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)
	require.Equal(t, "nodeC", hop.Conn.Name)
}
