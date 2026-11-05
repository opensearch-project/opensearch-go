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
	"sync/atomic"
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

	withShards := func(shards map[int]*shardNodes) *indexSlot {
		slot := &indexSlot{clock: realClock{}}
		slot.shardMap.Store(&indexShardMap{
			NumberOfPrimaryShards: partitionedPrimaryShards,
			RoutingNumShards:      partitionedRoutingNumShards,
			RoutingPartitionSize:  partitionSize,
			Shards:                shards,
		})

		return slot
	}
	hosted := withShards(map[int]*shardNodes{
		wantShard: {Primary: "nodeA"},
		shardhash.ForRouting(routing, partitionedRoutingNumShards, partitionedPrimaryShards): {Primary: "nodeB"},
	})
	conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB")}

	tests := []struct {
		name         string
		slot         *indexSlot
		routing, id  string
		wantShard    int
		wantNodeName string
		wantErr      error
	}{
		{name: "routing and id", slot: hosted, routing: routing, id: id, wantShard: wantShard, wantNodeName: "nodeA"},
		{name: "no id cannot claim a shard", slot: hosted, routing: routing, id: "", wantShard: -1, wantErr: ErrDocumentIDRequired},
		{
			name: "target shard has no entry in the shard map", slot: withShards(map[int]*shardNodes{wantShard + 1: {Primary: "nodeA"}}),
			routing: routing, id: id, wantShard: -1, wantErr: ErrShardNoNode,
		},
		{
			name: "target shard entry names no primary and no replica", slot: withShards(map[int]*shardNodes{wantShard: {}}),
			routing: routing, id: id, wantShard: -1, wantErr: ErrShardNoNode,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf, shardNum, _, err := calcSingleKeyCost(routingFeatures(0), tt.slot, tt.routing, tt.id, conns)
			defer buf.Release()

			require.Equal(t, tt.wantShard, shardNum)
			requireErrorIs(t, tt.wantErr, err)
			if tt.wantNodeName == "" {
				require.Empty(t, buf.Slice())

				return
			}
			require.Len(t, buf.Slice(), 1)
			require.Equal(t, tt.wantNodeName, buf.Slice()[0].Name)
		})
	}
}

// requireErrorIs requires err to be nil when want is nil, and to match want
// otherwise.
func requireErrorIs(t *testing.T, want, err error) {
	t.Helper()

	if want == nil {
		require.NoError(t, err)

		return
	}
	require.ErrorIs(t, err, want)
}

// partitionedSlot builds a slot whose index has the given geometry and
// partition size, with each shard in placement hosted by its named primary.
func partitionedSlot(primaries, routingPartitionSize int, placement map[int]string) *indexSlot {
	shards := make(map[int]*shardNodes, len(placement))
	for shard, node := range placement {
		shards[shard] = &shardNodes{Primary: node}
	}
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(&indexShardMap{
		NumberOfPrimaryShards: primaries,
		RoutingNumShards:      partitionedRoutingNumShards,
		RoutingPartitionSize:  routingPartitionSize,
		Shards:                shards,
	})

	return slot
}

// keyOffset names the shard of a routing key at a partition offset.
type keyOffset struct {
	key    string
	offset int
}

// calcMultiKeyCost costs each distinct target shard once, however many keys or
// partition offsets reach it. Every target shard is hosted by nodeA except those
// in onB, which nodeB hosts.
func TestCalcMultiKeyCostTargetShards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		primaries            int
		routingPartitionSize int
		routing              string
		onB                  []keyOffset
		unplaced             []keyOffset // target shards dropped from the shard map
		wantExtraCost        map[string]float64
		wantErr              error
	}{
		{
			name: "partitioned, one key's offsets on distinct shards", primaries: partitionedPrimaryShards, routingPartitionSize: partitionSize,
			routing: "user-7", onB: []keyOffset{{"user-7", 3}},
			wantExtraCost: map[string]float64{"nodeA": 1, "nodeB": 3},
		},
		{
			// With 5 primaries every offset of the key lands on one shard.
			name: "partitioned, one key's offsets collide on a shard", primaries: 5, routingPartitionSize: partitionSize,
			routing:       "user-7",
			wantExtraCost: map[string]float64{"nodeA": 0},
		},
		{
			// k1 and k2 share a shard, k3 has another: two target shards, one each.
			name: "unpartitioned, keys share a shard", primaries: 5,
			routing: "k1,k2,k3", onB: []keyOffset{{"k3", 0}},
			wantExtraCost: map[string]float64{"nodeA": 1, "nodeB": 1},
		},
		{
			// user-7 targets shards 285-288 and u682 shards 287-290; 289 and 290
			// are nodeB's, so six shards are targeted and nodeA hosts four.
			name: "partitioned, keys' partitions overlap", primaries: partitionedPrimaryShards, routingPartitionSize: partitionSize,
			routing: "user-7,u682", onB: []keyOffset{{"u682", 2}, {"u682", 3}},
			wantExtraCost: map[string]float64{"nodeA": 2, "nodeB": 4},
		},
		{
			// k3's shard has no entry; it still counts as a target, so nodeB, which
			// hosts only k1's shard, is one short.
			name: "a target shard missing from the shard map is skipped", primaries: 5,
			routing: "k1,k3", onB: []keyOffset{{"k1", 0}}, unplaced: []keyOffset{{"k3", 0}},
			wantExtraCost: map[string]float64{"nodeB": 1},
		},
		{
			// The map holds a shard, but not one the routing value targets.
			name: "no target shard has a host", primaries: 5,
			routing: "k1", onB: []keyOffset{{"k3", 0}}, unplaced: []keyOffset{{"k1", 0}},
			wantExtraCost: map[string]float64{}, wantErr: ErrShardNoNode,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			shardOf := func(ko keyOffset) int {
				return shardhash.ForRoutingOffset(ko.key, ko.offset, partitionedRoutingNumShards, tt.primaries)
			}
			placement := map[int]string{}
			for key := range strings.SplitSeq(tt.routing, routingValueSeparator) {
				for offset := range max(tt.routingPartitionSize, 1) {
					placement[shardOf(keyOffset{key, offset})] = "nodeA"
				}
			}
			for _, ko := range tt.onB {
				placement[shardOf(ko)] = "nodeB"
			}
			for _, ko := range tt.unplaced {
				delete(placement, shardOf(ko))
			}
			slot := partitionedSlot(tt.primaries, tt.routingPartitionSize, placement)
			conns := []*Connection{newTestConn(t, "nodeA"), newTestConn(t, "nodeB")}

			cBuf, ecBuf, err := calcMultiKeyCost(routingFeatures(0), slot, tt.routing, conns)
			defer cBuf.Release()
			defer ecBuf.Release()

			got := map[string]float64{}
			for i, c := range cBuf.Slice() {
				got[c.Name] = ecBuf.Slice()[i]
			}
			require.Equal(t, tt.wantExtraCost, got)
			requireErrorIs(t, tt.wantErr, err)
		})
	}
}

func TestNeedsShardSetCost(t *testing.T) {
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

			require.Equal(t, tt.want, needsShardSetCost(tt.slot, tt.routing, tt.id))
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

func TestFetchRoutingNumShardsCarriesForwardTheCachedShardMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cached indexShardMap
	}{
		{
			name: "partitioned segment-replicated index",
			cached: indexShardMap{
				NumberOfPrimaryShards: 8, RoutingNumShards: 1024, RoutingPartitionSize: 4, Replication: ReplicationSegment,
			},
		},
		{
			name:   "unpartitioned document-replicated index",
			cached: indexShardMap{NumberOfPrimaryShards: 8, RoutingNumShards: 1024, Replication: ReplicationDocument},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
			var metadataRequests atomic.Int32
			tr.transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				if strings.HasPrefix(req.URL.Path, "/_cluster/state/metadata") {
					metadataRequests.Add(1)
				}

				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
			})
			cached := tt.cached
			r.cache.getOrCreate("idx").shardMap.Store(&cached)

			placement := &indexShardPlacement{}
			tr.fetchRoutingNumShards(t.Context(), map[string]*indexShardPlacement{"idx": placement})

			require.Equal(t, int32(0), metadataRequests.Load(), "a cached shard map needs no metadata fetch")
			require.Equal(t, tt.cached.RoutingNumShards, placement.RoutingNumShards)
			require.Equal(t, tt.cached.RoutingPartitionSize, placement.RoutingPartitionSize)
			require.Equal(t, tt.cached.Replication, placement.Replication)
			require.Equal(t, tt.cached.NumberOfPrimaryShards, placement.NumberOfPrimaryShards)
		})
	}
}

// Requests that name several shards (a comma-separated routing list, or one
// routing value without an ID on a partitioned index) go to the node hosting
// most of them, and a router that cannot do that says why on the RouteEvent.
func TestRoutersCostShardSets(t *testing.T) {
	t.Parallel()

	const (
		keyA = "user-7"
		keyB = "u682"
	)
	shardA := shardhash.ForRouting(keyA, partitionedRoutingNumShards, partitionedPrimaryShards)
	shardB := shardhash.ForRouting(keyB, partitionedRoutingNumShards, partitionedPrimaryShards)
	require.NotEqual(t, shardA, shardB)

	var partitionTargets [partitionSize]int
	for i := range partitionTargets {
		partitionTargets[i] = shardhash.ForRoutingOffset(keyA, i, partitionedRoutingNumShards, partitionedPrimaryShards)
	}

	shardMap := func(partition int, shards map[int]*shardNodes) *indexShardMap {
		return &indexShardMap{
			NumberOfPrimaryShards: partitionedPrimaryShards,
			RoutingNumShards:      partitionedRoutingNumShards,
			RoutingPartitionSize:  partition,
			Shards:                shards,
		}
	}
	// node1 hosts both of the comma list's shards, node0 only one.
	listMap := shardMap(0, map[int]*shardNodes{shardA: {Primary: "node0", Replicas: []string{"node1"}}, shardB: {Primary: "node1"}})
	// node1 hosts three of the four partition shards, node0 one.
	partitionMap := shardMap(partitionSize, map[int]*shardNodes{
		partitionTargets[0]: {Primary: "node1"}, partitionTargets[1]: {Primary: "node1"},
		partitionTargets[2]: {Primary: "node1"}, partitionTargets[3]: {Primary: "node0"},
	})
	unhosted := shardMap(0, map[int]*shardNodes{shardA: {Primary: "gone"}, shardB: {Primary: "gone"}})

	tests := []struct {
		name      string
		doc       bool // route through DocRouter instead of the pool router
		path      string
		shardMap  *indexShardMap
		wantNil   bool   // the router leaves the request to the inner policy
		wantNode  string // "" accepts any node
		wantExact bool
		wantErr   error
		features  routingFeatures
	}{
		{
			name: "doc router, routing list", doc: true, path: "/idx/_doc/1?routing=" + keyA + "," + keyB,
			shardMap: listMap, wantNode: "node1", wantExact: true,
		},
		{
			name: "doc router, routing list on shards no node hosts", doc: true, path: "/idx/_doc/1?routing=" + keyA + "," + keyB,
			shardMap: unhosted, wantErr: ErrShardNoNode,
		},
		{
			name: "doc router sees no request without a document ID", doc: true, path: "/idx/_search?routing=" + keyA,
			shardMap: partitionMap, wantNil: true,
		},
		{
			name: "pool router, routing list", path: "/idx/_doc/1?routing=" + keyA + "," + keyB,
			shardMap: listMap, wantNode: "node1", wantExact: true,
		},
		{
			name: "pool router, routing without an ID on a partitioned index", path: "/idx/_search?routing=" + keyA,
			shardMap: partitionMap, wantNode: "node1", wantExact: true,
		},
		{
			name: "pool router, routing list on shards no node hosts", path: "/idx/_doc/1?routing=" + keyA + "," + keyB,
			shardMap: unhosted, wantErr: ErrShardNoNode,
		},
		{name: "pool router, index without a shard map", path: "/idx/_search?routing=" + keyA, wantErr: ErrShardMapUnknown},
		{name: "pool router, cluster-level request", path: "/_cluster/health", wantErr: ErrNoRoutingKey},
		{
			name: "pool router, cluster-level request with shard-exact routing disabled", path: "/_cluster/health",
			features: routingSkipShardExact,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := make([]*Connection, 3)
			for i := range conns {
				id := "node" + string(rune('0'+i))
				// node1, the node hosting most shards, is the farthest, so only a cost
				// for the shards a node lacks makes it the pick.
				rtt := 200 * time.Microsecond
				if i == 1 {
					rtt = 1 * time.Millisecond
				}
				conns[i] = makeTestConn(t, "http://"+id+":9200", id, rtt)
				conns[i].Name = id
			}
			cache := testIndexSlotCache()
			cache.features = tt.features
			if tt.shardMap != nil {
				cache.getOrCreate("idx").shardMap.Store(tt.shardMap)
			}

			var router Policy
			var observer *atomic.Pointer[ConnectionObserver]
			if tt.doc {
				d := newDocRouter(cache, defaultDecayFactor)
				require.NoError(t, d.DiscoveryUpdate(conns, nil, nil))
				router, observer = d, &d.observer
			} else {
				p := wrapWithRouter(&mockPolicy{hop: NextHop{Conn: conns[0]}, enabled: true}, cache, &shardCostForReads, "get", nil).(*poolRouter)
				p.mu.Lock()
				p.mu.sortedConns = conns
				p.mu.Unlock()
				router, observer = p, &p.observer
			}
			rec := newRecordingObserver()
			var obs ConnectionObserver = rec
			observer.Store(&obs)

			req, err := http.NewRequest(http.MethodGet, tt.path, nil)
			require.NoError(t, err)
			hop, err := router.Eval(t.Context(), req)
			require.NoError(t, err)

			if tt.wantNil {
				require.Nil(t, hop.Conn)
				require.Empty(t, rec.getRouteEvents())

				return
			}
			require.NotNil(t, hop.Conn)
			if tt.wantNode != "" {
				require.Equal(t, tt.wantNode, hop.Conn.Name)
			}
			events := rec.getRouteEvents()
			require.Len(t, events, 1)
			require.Equal(t, tt.wantExact, events[0].ShardExactMatch)
			requireErrorIs(t, tt.wantErr, events[0].Err)
		})
	}
}
