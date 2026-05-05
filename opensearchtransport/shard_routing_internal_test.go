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
	"runtime"
	"slices"
	"strings"
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

// --- calcSingleKeyCost unit tests -----------------------------------------

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
			buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, routing, conns)
			candidates := buf.Slice()
			defer buf.Release()

			// Independent shard computation via murmur3.
			expectedShard := shardForRouting(routing, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
			require.Equal(t, expectedShard, shardNum,
				"shard mismatch: calcSingleKeyCost=%d, murmur3=%d", shardNum, expectedShard)

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

	buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, "", []*Connection{connA})
	candidates := buf.Slice()
	defer buf.Release()
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
	buf, shardNum, _ := calcSingleKeyCost(routingSkipShardExact, slot, "test-routing", []*Connection{connA})
	candidates := buf.Slice()
	defer buf.Release()
	require.Nil(t, candidates)
	require.Equal(t, -1, shardNum)
}

func TestShardExactCandidates_NilShardMap(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	// No shardMap stored.
	connA := newTestConn(t, "nodeA")

	buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, "abc", []*Connection{connA})
	candidates := buf.Slice()
	defer buf.Release()
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

	buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, routingForShard1, conns)
	candidates := buf.Slice()
	defer buf.Release()
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
		buf, shardNum, _ := calcSingleKeyCost(routingFeatures(0), slot, routing, conns)
		candidates := buf.Slice()
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
		buf.Release()
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

// --- calcMultiKeyCost unit tests ---------------------------------------------

// routingValueForShard finds a routing value that maps to the given target shard.
//
// The prefix parameter is the key to making these tests efficient across runs.
// The generated string is "prefix-0", so the caller must choose a prefix where
// "prefix-0" murmur3-hashes to targetShard. On first use with a new shard map,
// run the test — if it fails, the error message tells you the correct value to
// use as the prefix. Once calibrated, the function returns immediately at i=0
// with zero search overhead.
//
// Fires t.Fatalf when i != 0: this means the prefix is stale (shard map
// changed, or the prefix was never calibrated). The error includes the
// caller's file:line so you know exactly which call site to fix.
func routingValueForShard(t *testing.T, sm *indexShardMap, targetShard int, prefix string) string {
	t.Helper()
	for i := 0; ; i++ {
		r := fmt.Sprintf("%s-%d", prefix, i)
		if shardForRouting(r, sm.RoutingNumShards, sm.NumberOfPrimaryShards) == targetShard {
			if i != 0 {
				_, file, line, _ := runtime.Caller(1)
				t.Fatalf("FIXME developer bug or regression at %s:%d: "+
					"routingValueForShard(shard=%d, prefix=%q) required %d iterations to find %q; "+
					"update the prefix at the call site so it matches at i=0",
					file, line, targetShard, prefix, i, r)
			}
			return r
		}
	}
}

// routingValuesForShard finds count routing values that all map to the given
// target shard. Same calibration rule as routingValueForShard.
func routingValuesForShard(t *testing.T, sm *indexShardMap, targetShard int, prefix string, count int) []string {
	t.Helper()
	var results []string
	for i := 0; len(results) < count; i++ {
		r := fmt.Sprintf("%s-%d", prefix, i)
		if shardForRouting(r, sm.RoutingNumShards, sm.NumberOfPrimaryShards) == targetShard {
			if i != len(results) {
				_, file, line, _ := runtime.Caller(1)
				t.Fatalf("FIXME developer bug or regression at %s:%d: "+
					"routingValuesForShard(shard=%d, prefix=%q)[%d] required i=%d to find %q; "+
					"update the prefix at the call site so result[%d] matches at i=%d",
					file, line, targetShard, prefix, len(results), i, r, len(results), len(results))
			}
			results = append(results, r)
		}
	}
	return results
}

func TestCalcMultiKeyCost_PicksHighestHitNode(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	connC := newTestConn(t, "nodeC")
	conns := []*Connection{connA, connB, connC}

	// Shard 0: nodeA (primary), nodeB (replica)
	// Shard 2: nodeC (primary), nodeA (replica)
	// nodeA hosts both -> highest hit count (2), extraCost=0.
	routeToShard0 := routingValueForShard(t, sm, 0, "rt0-4")
	routeToShard2 := routingValueForShard(t, sm, 2, "rt2-5")

	routingValue := routeToShard0 + routingValueSeparator + routeToShard2
	cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routingValue, conns)
	candidates, extraCost := cBuf.Slice(), ecBuf.Slice()
	defer cBuf.Release()
	defer ecBuf.Release()

	require.NotNil(t, candidates)
	require.Equal(t, "nodeA", candidates[0].Name)
	require.InDelta(t, 0.0, extraCost[0], 1e-9)
}

func TestCalcMultiKeyCost_AllKeysOnSameShard(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	connC := newTestConn(t, "nodeC")
	conns := []*Connection{connA, connB, connC}

	// Two routing values both on shard 1 (nodeB primary, nodeC replica).
	routes := routingValuesForShard(t, sm, 1, "s1pair-9", 2)

	routingValue := routes[0] + routingValueSeparator + routes[1]
	cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routingValue, conns)
	candidates, extraCost := cBuf.Slice(), ecBuf.Slice()
	defer cBuf.Release()
	defer ecBuf.Release()

	require.NotNil(t, candidates)
	// Both keys hit shard 1 -> nodeB gets hits=2, nodeC gets hits=2, extraCost=0 for both.
	require.Len(t, candidates, 2)
	require.InDelta(t, 0.0, extraCost[0], 1e-9)
	require.InDelta(t, 0.0, extraCost[1], 1e-9)
	names := map[string]bool{candidates[0].Name: true, candidates[1].Name: true}
	require.True(t, names["nodeB"])
	require.True(t, names["nodeC"])
}

func TestCalcMultiKeyCost_ExtraCostReflectsRemoteHops(t *testing.T) {
	t.Parallel()

	sm := shardMap3()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	connA := newTestConn(t, "nodeA")
	connB := newTestConn(t, "nodeB")
	connC := newTestConn(t, "nodeC")
	conns := []*Connection{connA, connB, connC}

	// Shard 0: nodeA (primary), nodeB (replica)
	// Shard 2: nodeC (primary), nodeA (replica)
	// 2 keys total. nodeA hosts both (extraCost=0), nodeB hosts 1 (extraCost=1),
	// nodeC hosts 1 (extraCost=1).
	routeToShard0 := routingValueForShard(t, sm, 0, "rt0-4")
	routeToShard2 := routingValueForShard(t, sm, 2, "rt2-5")

	routingValue := routeToShard0 + routingValueSeparator + routeToShard2
	cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routingValue, conns)
	candidates, extraCost := cBuf.Slice(), ecBuf.Slice()
	defer cBuf.Release()
	defer ecBuf.Release()

	require.Len(t, candidates, 3)
	// First candidate is nodeA (hits=2, extraCost=0).
	require.Equal(t, "nodeA", candidates[0].Name)
	require.InDelta(t, 0.0, extraCost[0], 1e-9)
	// Remaining have extraCost=1.
	require.InDelta(t, 1.0, extraCost[1], 1e-9)
	require.InDelta(t, 1.0, extraCost[2], 1e-9)
}

func TestCalcMultiKeyCost_DisabledByFeatureFlag(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(shardMap3())
	connA := newTestConn(t, "nodeA")

	candidates, extraCost := calcMultiKeyCost(routingSkipShardExact, slot, "a,b", []*Connection{connA})
	require.Nil(t, candidates.Slice())
	require.Nil(t, extraCost.Slice())
}

func TestCalcMultiKeyCost_NilShardMap(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	connA := newTestConn(t, "nodeA")

	candidates, extraCost := calcMultiKeyCost(routingFeatures(0), slot, "a,b", []*Connection{connA})
	require.Nil(t, candidates.Slice())
	require.Nil(t, extraCost.Slice())
}

func TestCalcMultiKeyCost_EmptyRoutingValue(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(shardMap3())
	connA := newTestConn(t, "nodeA")

	candidates, extraCost := calcMultiKeyCost(routingFeatures(0), slot, "", []*Connection{connA})
	require.Nil(t, candidates.Slice())
	require.Nil(t, extraCost.Slice())
}

func TestCalcMultiKeyCost_E2E_IndexRouter(t *testing.T) {
	t.Parallel()
	f := newShardRoutingE2EFixture(t)

	sm := f.shardMap

	// Shard 0: nodeA (primary), nodeB (replica)
	// Shard 2: nodeC (primary), nodeA (replica)
	// nodeA hosts both -> should be selected (extraCost=0, best score).
	routeToShard0 := routingValueForShard(t, sm, 0, "rt0-4")
	routeToShard2 := routingValueForShard(t, sm, 2, "rt2-5")

	req := f.request("/my-index/_search", routeToShard0+","+routeToShard2)
	hop, err := f.policy.Eval(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, hop.Conn)

	require.Equal(t, "nodeA", hop.Conn.Name)
}

// --- calcMultiKeyCost with high replica count (5 replicas) ----------------

// shardMap5x6 builds a 5-primary-shard placement map with 5 replicas per shard
// (6 copies total per shard). 8 nodes total. The placement is designed so nodes
// have varying shard-host counts, enabling tests to verify hit-count ordering.
//
//	Shard 0: primary=n0, replicas=[n1, n2, n3, n4, n5]
//	Shard 1: primary=n1, replicas=[n2, n3, n4, n5, n6]
//	Shard 2: primary=n2, replicas=[n3, n4, n5, n6, n7]
//	Shard 3: primary=n3, replicas=[n4, n5, n6, n7, n0]
//	Shard 4: primary=n4, replicas=[n5, n6, n7, n0, n1]
//
// Node shard-hosting summary (how many of the 5 shards each node hosts):
//
//	n0: shards 0,3,4 = 3     n4: shards 0,1,2,3,4 = 5
//	n1: shards 0,1,4 = 3     n5: shards 0,1,2,3,4 = 5
//	n2: shards 0,1,2 = 3     n6: shards 1,2,3,4   = 4
//	n3: shards 0,1,2,3 = 4   n7: shards 2,3,4     = 3
func shardMap5x6() *indexShardMap {
	return &indexShardMap{
		NumberOfPrimaryShards: 5,
		RoutingNumShards:      5,
		Shards: map[int]*shardNodes{
			0: {Primary: "n0", Replicas: []string{"n1", "n2", "n3", "n4", "n5"}},
			1: {Primary: "n1", Replicas: []string{"n2", "n3", "n4", "n5", "n6"}},
			2: {Primary: "n2", Replicas: []string{"n3", "n4", "n5", "n6", "n7"}},
			3: {Primary: "n3", Replicas: []string{"n4", "n5", "n6", "n7", "n0"}},
			4: {Primary: "n4", Replicas: []string{"n5", "n6", "n7", "n0", "n1"}},
		},
	}
}

func TestCalcMultiKeyCost_HighReplicaCount(t *testing.T) {
	t.Parallel()

	sm := shardMap5x6()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	conns := make([]*Connection, 8)
	for i := range conns {
		conns[i] = newTestConn(t, fmt.Sprintf("n%d", i))
	}

	tests := []struct {
		name string
		// shards targeted by routing values (each entry = one routing key hitting that shard)
		targetShards []int
		// wantFirstOneOf is the expected first candidate (highest hit count).
		// When multiple nodes tie, any of them is acceptable.
		wantFirstOneOf []string
		// wantCount is how many candidate nodes should be returned.
		wantCount int
		// wantFirstExtraCost is the expected extraCost for the first candidate.
		wantFirstExtraCost float64
	}{
		{
			// All 5 shards targeted: n4 and n5 host all 5 -> extraCost=0.
			name:               "5_shards_all_targeted",
			targetShards:       []int{0, 1, 2, 3, 4},
			wantFirstOneOf:     []string{"n4", "n5"},
			wantCount:          8,
			wantFirstExtraCost: 0,
		},
		{
			// 4 shards targeted (0,1,2,3): n3,n4,n5 host all 4 -> extraCost=0.
			name:               "4_shards_targeted",
			targetShards:       []int{0, 1, 2, 3},
			wantFirstOneOf:     []string{"n3", "n4", "n5"},
			wantCount:          8,
			wantFirstExtraCost: 0,
		},
		{
			// 3 shards targeted (0,1,2): n2,n3,n4,n5 host all 3 -> extraCost=0.
			name:               "3_shards_targeted_012",
			targetShards:       []int{0, 1, 2},
			wantFirstOneOf:     []string{"n2", "n3", "n4", "n5"},
			wantCount:          8,
			wantFirstExtraCost: 0,
		},
		{
			// 2 shards targeted (0,4): n0,n1,n4,n5 host both -> extraCost=0.
			name:               "2_shards_targeted_04",
			targetShards:       []int{0, 4},
			wantFirstOneOf:     []string{"n0", "n1", "n4", "n5"},
			wantCount:          8,
			wantFirstExtraCost: 0,
		},
		{
			// 1 shard targeted (2): all 6 nodes hosting shard 2 have extraCost=0.
			name:               "1_shard_targeted_2",
			targetShards:       []int{2},
			wantFirstOneOf:     []string{"n2", "n3", "n4", "n5", "n6", "n7"},
			wantCount:          6,
			wantFirstExtraCost: 0,
		},
		{
			// 0 shards targeted: empty routing -> nil result.
			name:         "0_shards_no_routing",
			targetShards: []int{},
			wantCount:    0,
		},
	}

	// Prefixes calibrated for shardMap5x6 (5 shards, routingNumShards=5).
	// routingValueForShard will fire FIXME if any prefix is stale.
	shardPrefixes := []string{"s5a0", "s5a1", "s5a2", "s5a4", "s5a12"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var parts []string
			for _, shard := range tt.targetShards {
				rv := routingValueForShard(t, sm, shard, shardPrefixes[shard])
				parts = append(parts, rv)
			}

			if len(parts) == 0 {
				candidates, extraCost := calcMultiKeyCost(routingFeatures(0), slot, "", conns)
				require.Nil(t, candidates.Slice())
				require.Nil(t, extraCost.Slice())
				return
			}

			routingValue := strings.Join(parts, routingValueSeparator)
			cBuf, ecBuf := calcMultiKeyCost(routingFeatures(0), slot, routingValue, conns)
			candidates, extraCost := cBuf.Slice(), ecBuf.Slice()
			defer cBuf.Release()
			defer ecBuf.Release()

			require.Len(t, candidates, tt.wantCount,
				"expected %d candidates, got %d", tt.wantCount, len(candidates))
			require.Len(t, extraCost, tt.wantCount)

			if len(tt.wantFirstOneOf) > 0 {
				found := slices.Contains(tt.wantFirstOneOf, candidates[0].Name)
				require.True(t, found,
					"first candidate %q not in expected set %v", candidates[0].Name, tt.wantFirstOneOf)
				require.InDelta(t, tt.wantFirstExtraCost, extraCost[0], 1e-9)
			}
		})
	}
}
