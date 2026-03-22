// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- shard map fixture -------------------------------------------------------

// shardMap3 builds a 3-primary-shard placement map used across tests.
// Uses RoutingNumShards == NumberOfPrimaryShards (routingFactor=1) to
// simplify test assertions. Real clusters have larger RoutingNumShards
// (e.g., 768 for 3 shards) but the routing algorithm is the same.
//
//	Shard 0 -> nodeA (primary), nodeB (replica)
//	Shard 1 -> nodeB (primary), nodeC (replica)
//	Shard 2 -> nodeC (primary), nodeA (replica)
func shardMap3() *indexShardMap {
	return &indexShardMap{
		NumberOfPrimaryShards: 3,
		RoutingNumShards:      3,
		Shards: map[int]*shardNodes{
			0: {Primary: "nodeA", Replicas: []string{"nodeB"}},
			1: {Primary: "nodeB", Replicas: []string{"nodeC"}},
			2: {Primary: "nodeC", Replicas: []string{"nodeA"}},
		},
	}
}

// nodesForShard returns the set of node names hosting shard shardNum.
func nodesForShard(sm *indexShardMap, shardNum int) map[string]struct{} {
	sc := sm.Shards[shardNum]
	if sc == nil {
		return nil
	}
	names := make(map[string]struct{}, 1+len(sc.Replicas))
	if sc.Primary != "" {
		names[sc.Primary] = struct{}{}
	}
	for _, r := range sc.Replicas {
		names[r] = struct{}{}
	}
	return names
}

// --- shardExactCandidates unit tests -----------------------------------------

func TestShardExactCandidates_CorrectNodes(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	connC := newTestConn(t, "nodeC")
	conns := []*Connection{connA, connB, connC}

	// Test many routing values. For each one, verify:
	//   1. The returned shard number matches our independent murmur3 computation.
	//   2. Every candidate hosts that shard.
	//   3. Every hosting node with a live connection appears in candidates.
	routingValues := []string{
		"user42", "order-12345", "abc", "hello", "session_9999",
		"tenant:acme", "0", "1", "2", "3", "documents/id/nested",
		"test-routing-value", "deterministic-key",
	}
	for _, routing := range routingValues {
		t.Run("routing="+routing, func(t *testing.T) {
			t.Parallel()
			candidates, shardNum, _ := shardExactCandidates(routingFeatures(0), slot, routing, conns)

			// Independent shard computation via murmur3.
			expectedShard := shardForRouting(routing, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
			require.Equal(t, expectedShard, shardNum,
				"shard mismatch: shardExactCandidates=%d, murmur3=%d", shardNum, expectedShard)

			expectedNodes := nodesForShard(sm, expectedShard)
			require.NotEmpty(t, expectedNodes)
			require.NotNil(t, candidates)

			// Every candidate must host the target shard.
			for _, c := range candidates {
				_, ok := expectedNodes[c.Name]
				require.True(t, ok,
					"node %s returned as candidate but does not host shard %d (hosts: %v)",
					c.Name, expectedShard, expectedNodes)
			}

			// Every hosting node with a connection must appear.
			for name := range expectedNodes {
				found := false
				for _, c := range candidates {
					if c.Name == name {
						found = true
						break
					}
				}
				require.True(t, found,
					"node %s hosts shard %d but missing from candidates", name, expectedShard)
			}
		})
	}
}

func TestShardExactCandidates_EmptyRouting(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(shardMap3())
	connA := newTestConn(t, "nodeA")

	candidates, shardNum, _ := shardExactCandidates(routingFeatures(0), slot, "", []*Connection{connA})
	require.Nil(t, candidates)
	require.Equal(t, -1, shardNum)
}

func TestShardExactCandidates_DisabledByFeatureFlag(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(shardMap3())
	connA := newTestConn(t, "nodeA")

	// With routingSkipShardExact set, shard-exact routing is disabled
	// even when shard map and routing value are valid.
	candidates, shardNum, _ := shardExactCandidates(routingSkipShardExact, slot, "test-routing", []*Connection{connA})
	require.Nil(t, candidates)
	require.Equal(t, -1, shardNum)
}

func TestShardExactCandidates_NilShardMap(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	// No shardMap stored.
	connA := newTestConn(t, "nodeA")

	candidates, shardNum, _ := shardExactCandidates(routingFeatures(0), slot, "abc", []*Connection{connA})
	require.Nil(t, candidates)
	require.Equal(t, -1, shardNum)
}

func TestShardExactCandidates_MissingConnection(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	// Only nodeA and nodeB connected; nodeC is missing.
	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	conns := []*Connection{connA, connB}

	// Find a routing value that maps to shard 1 (nodeB primary, nodeC replica).
	var routingForShard1 string
	for i := range 10000 {
		r := fmt.Sprintf("probe-%d", i)
		if shardForRouting(r, sm.RoutingNumShards, sm.NumberOfPrimaryShards) == 1 {
			routingForShard1 = r
			break
		}
	}
	require.NotEmpty(t, routingForShard1, "could not find a routing value mapping to shard 1")

	candidates, shardNum, _ := shardExactCandidates(routingFeatures(0), slot, routingForShard1, conns)
	require.Equal(t, 1, shardNum)
	require.NotNil(t, candidates)

	// Only nodeB should be returned (nodeC is not connected).
	require.Len(t, candidates, 1)
	require.Equal(t, "nodeB", candidates[0].Name)
}

// --- End-to-end: Eval -> murmur3 -> node selection ----------------------------

// shardRoutingE2EFixture sets up an IndexRouter with 3 nodes,
// a 3-shard index, and shard placement data. Returns the policy, the
// shard map, and a helper that builds a request for a given path+routing.
type shardRoutingE2EFixture struct {
	policy   *IndexRouter
	shardMap *indexShardMap
}

func newShardRoutingE2EFixture(t *testing.T) *shardRoutingE2EFixture {
	t.Helper()

	sm := shardMap3()
	cache := newIndexSlotCache(indexSlotCacheConfig{})
	policy := NewIndexRouter(indexSlotCacheConfig{})
	policy.cache = cache

	connA := newTestConnRTT(t, "nodeA", 1*time.Millisecond)
	connB := newTestConnRTT(t, "nodeB", 2*time.Millisecond)
	connC := newTestConnRTT(t, "nodeC", 3*time.Millisecond)
	err := policy.DiscoveryUpdate([]*Connection{connA, connB, connC}, nil, nil)
	require.NoError(t, err)

	// Populate shard map and per-node info.
	slot := cache.getOrCreate("my-index")
	slot.shardMap.Store(sm)
	nodeInfo := map[string]*shardNodeInfo{
		"nodeA": {Primaries: 1, Replicas: 1},
		"nodeB": {Primaries: 1, Replicas: 1},
		"nodeC": {Primaries: 1, Replicas: 1},
	}
	slot.shardNodeNames.Store(&nodeInfo)
	slot.shardNodeCount.Store(3)

	return &shardRoutingE2EFixture{policy: policy, shardMap: sm}
}

func (f *shardRoutingE2EFixture) request(path, routing string) *http.Request {
	rq := "routing=" + routing
	return &http.Request{
		URL: &url.URL{Path: path, RawQuery: rq},
	}
}

func TestShardExactRouting_SearchPath(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	routingValues := []string{"user42", "order-999", "key-abc", "hello", "tenant:prod", "42"}
	for _, routing := range routingValues {
		t.Run("routing="+routing, func(t *testing.T) {
			t.Parallel()
			req := f.request("/my-index/_search", routing)
			hop, err := f.policy.Eval(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, hop.Conn)

			expectedShard := shardForRouting(routing, f.shardMap.RoutingNumShards, f.shardMap.NumberOfPrimaryShards)
			expectedNodes := nodesForShard(f.shardMap, expectedShard)

			// The selected node must host the target shard.
			_, ok := expectedNodes[hop.Conn.Name]
			require.True(t, ok,
				"routing=%q shard=%d: node %s does not host that shard (expected %v)",
				routing, expectedShard, hop.Conn.Name, expectedNodes)
		})
	}
}

func TestShardExactRouting_DocPath(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	// Document paths via IndexRouter: /{index}/_doc/{id}
	// With ?routing=, the routing value (not the doc ID) determines the shard.
	for _, routing := range []string{"user42", "order-999"} {
		t.Run("routing="+routing, func(t *testing.T) {
			t.Parallel()
			req := f.request("/my-index/_doc/some-doc-id", routing)
			hop, err := f.policy.Eval(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, hop.Conn)

			expectedShard := shardForRouting(routing, f.shardMap.RoutingNumShards, f.shardMap.NumberOfPrimaryShards)
			expectedNodes := nodesForShard(f.shardMap, expectedShard)

			_, ok := expectedNodes[hop.Conn.Name]
			require.True(t, ok,
				"routing=%q shard=%d: node %s not in %v",
				routing, expectedShard, hop.Conn.Name, expectedNodes)
		})
	}
}

// TestShardExactRouting_DocIDAsDefaultRouting verifies the common case:
// document-level requests WITHOUT ?routing= use the doc ID as the effective
// routing value for murmur3 shard-exact routing. This matches OpenSearch
// server behavior where _id is the default routing value.
func TestShardExactRouting_DocIDAsDefaultRouting(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	cache := newIndexSlotCache(indexSlotCacheConfig{})

	// Use DocRouter (not Index) to exercise the doc-level path.
	docPolicy := NewDocRouter(cache, defaultDecayFactor)

	connA := newTestConnRTT(t, "nodeA", 1*time.Millisecond)
	connB := newTestConnRTT(t, "nodeB", 2*time.Millisecond)
	connC := newTestConnRTT(t, "nodeC", 3*time.Millisecond)
	err := docPolicy.DiscoveryUpdate([]*Connection{connA, connB, connC}, nil, nil)
	require.NoError(t, err)

	slot := cache.getOrCreate("my-index")
	slot.shardMap.Store(sm)
	nodeInfo := map[string]*shardNodeInfo{
		"nodeA": {Primaries: 1, Replicas: 1},
		"nodeB": {Primaries: 1, Replicas: 1},
		"nodeC": {Primaries: 1, Replicas: 1},
	}
	slot.shardNodeNames.Store(&nodeInfo)
	slot.shardNodeCount.Store(3)

	// Test several doc IDs WITHOUT ?routing= parameter.
	// The doc ID becomes the effective routing value.
	docIDs := []string{"doc-001", "user:alice", "order-12345", "abc", "zzz"}
	for _, docID := range docIDs {
		t.Run("docID="+docID, func(t *testing.T) {
			t.Parallel()
			req := &http.Request{
				URL: &url.URL{
					Path: "/my-index/_doc/" + docID,
					// No ?routing= parameter --the common case.
				},
			}

			hop, err := docPolicy.Eval(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, hop.Conn,
				"DocRouter should route doc-level requests")

			// The effective routing is the doc ID itself.
			expectedShard := shardForRouting(docID, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
			expectedNodes := nodesForShard(sm, expectedShard)

			_, ok := expectedNodes[hop.Conn.Name]
			require.True(t, ok,
				"docID=%q -> shard %d: node %s does not host that shard (expected %v)",
				docID, expectedShard, hop.Conn.Name, expectedNodes)
		})
	}
}

// TestShardExactRouting_ExplicitRoutingOverridesDocID verifies that ?routing=
// overrides the doc ID for shard selection in document-level requests.
func TestShardExactRouting_ExplicitRoutingOverridesDocID(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	cache := newIndexSlotCache(indexSlotCacheConfig{})
	docPolicy := NewDocRouter(cache, defaultDecayFactor)

	connA := newTestConnRTT(t, "nodeA", 1*time.Millisecond)
	connB := newTestConnRTT(t, "nodeB", 2*time.Millisecond)
	connC := newTestConnRTT(t, "nodeC", 3*time.Millisecond)
	err := docPolicy.DiscoveryUpdate([]*Connection{connA, connB, connC}, nil, nil)
	require.NoError(t, err)

	slot := cache.getOrCreate("my-index")
	slot.shardMap.Store(sm)
	nodeInfo := map[string]*shardNodeInfo{
		"nodeA": {Primaries: 1, Replicas: 1},
		"nodeB": {Primaries: 1, Replicas: 1},
		"nodeC": {Primaries: 1, Replicas: 1},
	}
	slot.shardNodeNames.Store(&nodeInfo)
	slot.shardNodeCount.Store(3)

	routing := "tenant:acme"
	docID := "completely-different-id"

	// Confirm routing and docID map to different shards (they almost certainly do).
	routingShard := shardForRouting(routing, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
	docIDShard := shardForRouting(docID, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
	if routingShard == docIDShard {
		t.Skip("routing and docID happen to map to the same shard; pick different values")
	}

	req := &http.Request{
		URL: &url.URL{
			Path:     "/my-index/_doc/" + docID,
			RawQuery: "routing=" + routing,
		},
	}

	hop, err := docPolicy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)

	// The routing value determines the shard, NOT the doc ID.
	expectedNodes := nodesForShard(sm, routingShard)
	_, ok := expectedNodes[hop.Conn.Name]
	require.True(t, ok,
		"routing=%q should determine shard %d, but selected node %s hosts shard %d (doc=%q)",
		routing, routingShard, hop.Conn.Name, docIDShard, docID)
}

func TestShardExactRouting_Deterministic(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	routing := "deterministic-key"
	expectedShard := shardForRouting(routing, f.shardMap.RoutingNumShards, f.shardMap.NumberOfPrimaryShards)
	expectedNodes := nodesForShard(f.shardMap, expectedShard)

	for i := range 50 {
		req := f.request("/my-index/_search", routing)
		hop, err := f.policy.Eval(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, hop.Conn)

		_, ok := expectedNodes[hop.Conn.Name]
		require.True(t, ok,
			"iter %d: routing=%q shard=%d: node %s not in %v",
			i, routing, expectedShard, hop.Conn.Name, expectedNodes)
	}
}

func TestShardExactRouting_FallbackWhenNoShardMap(t *testing.T) {
	t.Parallel()

	policy := NewIndexRouter(indexSlotCacheConfig{})
	connA := newTestConnRTT(t, "nodeA", 1*time.Millisecond)
	err := policy.DiscoveryUpdate([]*Connection{connA}, nil, nil)
	require.NoError(t, err)

	// No shard map -> should fall back to rendezvous hash.
	req := &http.Request{
		URL: &url.URL{Path: "/my-index/_search", RawQuery: "routing=user42"},
	}

	hop, err := policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)
}

func TestShardExactRouting_NoRoutingParam(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	req := &http.Request{
		URL: &url.URL{Path: "/my-index/_search", RawQuery: "pretty=true"},
	}

	hop, err := f.policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)
}

// TestShardExactRouting_AllShardsReachable verifies that across many routing
// values, every shard is hit at least once (no dead-zone bug in the
// murmur3 -> floorMod -> shard lookup chain).
func TestShardExactRouting_AllShardsReachable(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	connC := newTestConn(t, "nodeC")
	conns := []*Connection{connA, connB, connC}

	shardsHit := make(map[int]bool)
	for i := range 200 {
		routing := fmt.Sprintf("probe-%d", i)
		candidates, shardNum, _ := shardExactCandidates(routingFeatures(0), slot, routing, conns)
		require.NotNil(t, candidates)
		require.GreaterOrEqual(t, shardNum, 0)
		require.Less(t, shardNum, sm.NumberOfPrimaryShards)
		shardsHit[shardNum] = true

		// Spot-check: candidates must host this shard.
		expectedNodes := nodesForShard(sm, shardNum)
		for _, c := range candidates {
			_, ok := expectedNodes[c.Name]
			require.True(t, ok,
				"routing=%q shard=%d: candidate %s not in %v", routing, shardNum, c.Name, expectedNodes)
		}
	}

	for shard := 0; shard < sm.NumberOfPrimaryShards; shard++ {
		require.True(t, shardsHit[shard],
			"shard %d never hit across 200 routing values", shard)
	}
}

// TestShardExactRouting_Observer verifies the observer event carries the
// correct shard-exact routing metadata.
func TestShardExactRouting_Observer(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	var captured RouteEvent
	obs := &shardRoutingObserver{onRoute: func(e RouteEvent) { captured = e }}
	var iface ConnectionObserver = obs
	f.policy.observer.Store(&iface)

	routing := "observer-test"
	expectedShard := shardForRouting(routing, f.shardMap.RoutingNumShards, f.shardMap.NumberOfPrimaryShards)

	req := f.request("/my-index/_search", routing)
	hop, err := f.policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)

	require.Equal(t, routing, captured.RoutingValue)
	require.Equal(t, expectedShard, captured.TargetShard)
	require.True(t, captured.ShardExactMatch)
	require.Equal(t, "my-index", captured.IndexName)
	require.Equal(t, hop.Conn.URLString, captured.Selected.URL)

	// Verify the observer's selected node hosts the correct shard.
	expectedNodes := nodesForShard(f.shardMap, expectedShard)
	_, ok := expectedNodes[hop.Conn.Name]
	require.True(t, ok,
		"observer event selected node %s does not host shard %d", hop.Conn.Name, expectedShard)
}

// shardRoutingObserver captures RouteEvent for test verification.
type shardRoutingObserver struct {
	BaseConnectionObserver
	onRoute func(RouteEvent)
}

func (o *shardRoutingObserver) OnRoute(e RouteEvent) {
	if o.onRoute != nil {
		o.onRoute(e)
	}
}
