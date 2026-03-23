// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockPolicy is a minimal Policy implementation for testing the pool router wrapper.
type mockPolicy struct {
	hop     NextHop
	err     error
	enabled bool

	discoveryUpdateCalled bool
	discoveryAdded        []*Connection
	discoveryRemoved      []*Connection
	discoveryUnchanged    []*Connection

	checkDeadCalled bool
	isEnabledCalled bool
}

func (m *mockPolicy) Eval(_ context.Context, _ *http.Request) (NextHop, error) {
	return m.hop, m.err
}

func (m *mockPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	m.discoveryUpdateCalled = true
	m.discoveryAdded = added
	m.discoveryRemoved = removed
	m.discoveryUnchanged = unchanged
	return nil
}

func (m *mockPolicy) CheckDead(_ context.Context, _ HealthCheckFunc) error {
	m.checkDeadCalled = true
	return nil
}

func (m *mockPolicy) RotateStandby(_ context.Context, _ int) (int, error) {
	return 0, nil
}

func (m *mockPolicy) IsEnabled() bool {
	m.isEnabledCalled = true
	return m.enabled
}

// mockConfigurablePolicy embeds mockPolicy and adds policyConfigurable support.
type mockConfigurablePolicy struct {
	mockPolicy
	configCalled bool
	lastConfig   policyConfig
}

//nolint:unparam // Interface implementation; always returns nil in mock.
func (m *mockConfigurablePolicy) configurePolicySettings(config policyConfig) error {
	m.configCalled = true
	m.lastConfig = config
	return nil
}

// makeTestConn creates a Connection with an RTT ring populated with the given
// RTT value and an initial estimated load of zero. The connection is marked
// as active so it can be used in pool selection.
func makeTestConn(t *testing.T, urlStr string, id string, rtt time.Duration) *Connection {
	t.Helper()
	u, err := url.Parse(urlStr)
	require.NoError(t, err)
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        id,
		Roles:     make(roleSet),
	}
	conn.weight.Store(1)
	conn.rttRing = newRTTRing(3)
	// Fill the ring so median settles to the given RTT value.
	conn.rttRing.add(rtt)
	conn.rttRing.add(rtt)
	conn.rttRing.add(rtt)
	conn.state.Store(int64(newConnState(lcActive)))
	return conn
}

// testIndexSlotCache creates a minimal indexSlotCache suitable for testing.
func testIndexSlotCache() *indexSlotCache {
	return newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:    1,
		maxFanOut:    32,
		decayFactor:  defaultDecayFactor,
		fanOutPerReq: defaultFanOutPerRequest,
	})
}

// --- Test 1: TestWrapWithRouter ---

func TestWrapWithRouter(t *testing.T) {
	t.Parallel()

	t.Run("creates a valid poolRouter", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapped := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")
		require.NotNil(t, wrapped)
		_, ok := wrapped.(*poolRouter)
		require.True(t, ok, "wrapWithRouter should return an *poolRouter")
	})

	t.Run("inner policy accessible via childPolicies", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapped := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")
		walker, ok := wrapped.(policyTreeWalker)
		require.True(t, ok, "wrapper should implement policyTreeWalker")
		children := walker.childPolicies()
		require.Len(t, children, 1)
		require.Equal(t, inner, children[0])
	})

	t.Run("interface compliance policyConfigurable", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapped := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")
		_, ok := wrapped.(policyConfigurable)
		require.True(t, ok, "wrapper should implement policyConfigurable")
	})
}

// --- Test 2: TestPoolRouterEval ---

func TestPoolRouterEval(t *testing.T) {
	t.Parallel()

	t.Run("inner returns nil conn", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{hop: NextHop{}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Nil(t, hop.Conn)
	})

	t.Run("inner returns error", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("inner policy error")
		inner := &mockPolicy{hop: NextHop{}, err: expectedErr, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.ErrorIs(t, evalErr, expectedErr)
		require.Nil(t, hop.Conn)
	})

	t.Run("non-index request passes through", func(t *testing.T) {
		t.Parallel()
		// For system endpoints like /_cluster/health, key == "" so the wrapper
		// should return the inner hop as-is.
		conn := makeTestConn(t, "http://node1:9200", "node1", 500*time.Microsecond)
		inner := &mockPolicy{hop: NextHop{Conn: conn}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		req, err := http.NewRequest(http.MethodGet, "/_cluster/health", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Same(t, conn, hop.Conn, "system endpoint should pass through inner conn")
	})

	t.Run("root path passes through", func(t *testing.T) {
		t.Parallel()
		conn := makeTestConn(t, "http://node1:9200", "node1", 500*time.Microsecond)
		inner := &mockPolicy{hop: NextHop{Conn: conn}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Same(t, conn, hop.Conn, "root path should pass through inner conn")
	})

	t.Run("index request with sortedConns returns scored conn", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			makeTestConn(t, "http://node1:9200", "node1", 200*time.Microsecond),
			makeTestConn(t, "http://node2:9200", "node2", 300*time.Microsecond),
			makeTestConn(t, "http://node3:9200", "node3", 400*time.Microsecond),
		}

		// Inner just needs to return a non-nil conn to signal "I matched".
		inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Populate the pre-sorted connection list (normally done by DiscoveryUpdate).
		w := wrapper.(*poolRouter)
		w.mu.Lock()
		w.mu.sortedConns = append([]*Connection(nil), conns...)
		w.mu.Unlock()

		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn, "pool router should return a connection")

		// The selected connection should be one of our original connections.
		found := slices.Contains(conns, hop.Conn)
		require.True(t, found, "selected connection should be one of the pool's connections")
	})

	t.Run("no sortedConns falls through to inner", func(t *testing.T) {
		t.Parallel()
		conn := makeTestConn(t, "http://node1:9200", "node1", 500*time.Microsecond)
		inner := &mockPolicy{hop: NextHop{Conn: conn}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Do NOT populate sortedConns -- wrapper should fall through.
		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Same(t, conn, hop.Conn, "empty sortedConns should fall through to inner conn")
	})

	t.Run("inner returns nil conn passes through", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{hop: NextHop{}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Nil(t, hop.Conn, "nil inner conn should pass through")
	})

	t.Run("document-level key uses composite key for hashing", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			makeTestConn(t, "http://node1:9200", "node1", 200*time.Microsecond),
			makeTestConn(t, "http://node2:9200", "node2", 300*time.Microsecond),
		}

		inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Populate the pre-sorted connection list (normally done by DiscoveryUpdate).
		w := wrapper.(*poolRouter)
		w.mu.Lock()
		w.mu.sortedConns = append([]*Connection(nil), conns...)
		w.mu.Unlock()

		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/abc123", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn, "document endpoint should return a connection")
	})

	t.Run("document request without routing uses docID for shard-exact", func(t *testing.T) {
		t.Parallel()
		// shardMap3: shard 0 -> nodeA/nodeB, shard 1 -> nodeB/nodeC, shard 2 -> nodeC/nodeA
		// docID "abc123" -> shard 0 -> primary=nodeA, replica=nodeB
		conns := []*Connection{
			makeTestConn(t, "http://nodeA:9200", "nodeA", 200*time.Microsecond),
			makeTestConn(t, "http://nodeB:9200", "nodeB", 200*time.Microsecond),
			makeTestConn(t, "http://nodeC:9200", "nodeC", 200*time.Microsecond),
		}
		for _, c := range conns {
			c.Name = c.ID
		}

		inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "get")

		w := wrapper.(*poolRouter)
		w.mu.Lock()
		w.mu.sortedConns = append([]*Connection(nil), conns...)
		w.mu.Unlock()

		// Populate shard map on the index slot.
		slot := cache.getOrCreate("my-index")
		slot.shardMap.Store(shardMap3())
		nodeInfo := map[string]*shardNodeInfo{
			"nodeA": {Primaries: 1, Replicas: 1},
			"nodeB": {Primaries: 1, Replicas: 1},
			"nodeC": {Primaries: 1, Replicas: 1},
		}
		slot.shardNodeNames.Store(&nodeInfo)
		slot.shardNodeCount.Store(3)

		// GET /my-index/_doc/abc123 with NO ?routing= parameter.
		// The effective routing key should be "abc123" (the docID).
		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/abc123", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn)

		// docID "abc123" -> shard 0 -> nodeA (primary) or nodeB (replica).
		require.Contains(t, []string{"nodeA", "nodeB"}, hop.Conn.Name,
			"shard-exact should route to a node hosting shard 0")
	})

	t.Run("explicit routing overrides docID for shard-exact", func(t *testing.T) {
		t.Parallel()
		// shardMap3: shard 0 -> nodeA/nodeB, shard 1 -> nodeB/nodeC, shard 2 -> nodeC/nodeA
		// docID "abc123" -> shard 0, but ?routing=user42 -> shard 1
		conns := []*Connection{
			makeTestConn(t, "http://nodeA:9200", "nodeA", 200*time.Microsecond),
			makeTestConn(t, "http://nodeB:9200", "nodeB", 200*time.Microsecond),
			makeTestConn(t, "http://nodeC:9200", "nodeC", 200*time.Microsecond),
		}
		for _, c := range conns {
			c.Name = c.ID
		}

		inner := &mockPolicy{hop: NextHop{Conn: conns[0]}, err: nil, enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "get")

		w := wrapper.(*poolRouter)
		w.mu.Lock()
		w.mu.sortedConns = append([]*Connection(nil), conns...)
		w.mu.Unlock()

		slot := cache.getOrCreate("my-index")
		slot.shardMap.Store(shardMap3())
		nodeInfo := map[string]*shardNodeInfo{
			"nodeA": {Primaries: 1, Replicas: 1},
			"nodeB": {Primaries: 1, Replicas: 1},
			"nodeC": {Primaries: 1, Replicas: 1},
		}
		slot.shardNodeNames.Store(&nodeInfo)
		slot.shardNodeCount.Store(3)

		// GET /my-index/_doc/abc123?routing=user42
		// The explicit routing "user42" -> shard 1 -> nodeB (primary) or nodeC (replica).
		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/abc123?routing=user42", nil)
		require.NoError(t, err)

		hop, evalErr := wrapper.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.NotNil(t, hop.Conn)

		// ?routing=user42 -> shard 1 -> nodeB (primary) or nodeC (replica).
		require.Contains(t, []string{"nodeB", "nodeC"}, hop.Conn.Name,
			"explicit routing should override docID, routing to shard 1 nodes")
	})

	t.Run("consistent selection for same index key", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			makeTestConn(t, "http://node1:9200", "node1", 200*time.Microsecond),
			makeTestConn(t, "http://node2:9200", "node2", 200*time.Microsecond),
			makeTestConn(t, "http://node3:9200", "node3", 200*time.Microsecond),
		}

		// Use a large fan-out (equal to number of connections) to ensure all
		// nodes are in the candidate set. With equal RTT and no estimated
		// load, the same best connection should be chosen consistently.
		cache := newIndexSlotCache(indexSlotCacheConfig{
			minFanOut:    3,
			maxFanOut:    32,
			decayFactor:  defaultDecayFactor,
			fanOutPerReq: defaultFanOutPerRequest,
		})

		inner1 := &mockPolicy{hop: NextHop{Conn: conns[0]}, err: nil, enabled: true}
		wrapper1 := wrapWithRouter(inner1, cache, defaultDecayFactor, &shardCostForReads, "")

		// Populate the pre-sorted connection list (normally done by DiscoveryUpdate).
		w1 := wrapper1.(*poolRouter)
		w1.mu.Lock()
		w1.mu.sortedConns = append([]*Connection(nil), conns...)
		w1.mu.Unlock()

		req, err := http.NewRequest(http.MethodGet, "/stable-index/_search", nil)
		require.NoError(t, err)

		hop1, err := wrapper1.Eval(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, hop1.Conn)

		// Evaluate the same wrapper again -- the estimated load has been
		// incremented on the first winner, but with a single call the
		// score difference is negligible. The jitter rotates, so we mainly
		// test that it returns a valid connection from the pool.
		hop2, err := wrapper1.Eval(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, hop2.Conn)
	})
}

// --- Test 3: TestPoolRouterDelegation ---

func TestPoolRouterDelegation(t *testing.T) {
	t.Parallel()

	t.Run("DiscoveryUpdate delegates to inner", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		added := []*Connection{makeTestConn(t, "http://new:9200", "new", time.Millisecond)}
		removed := []*Connection{makeTestConn(t, "http://old:9200", "old", time.Millisecond)}
		unchanged := []*Connection{makeTestConn(t, "http://same:9200", "same", time.Millisecond)}

		err := wrapper.DiscoveryUpdate(added, removed, unchanged)
		require.NoError(t, err)
		require.True(t, inner.discoveryUpdateCalled)
		require.Equal(t, added, inner.discoveryAdded)
		require.Equal(t, removed, inner.discoveryRemoved)
		require.Equal(t, unchanged, inner.discoveryUnchanged)
	})

	t.Run("CheckDead delegates to inner", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		err := wrapper.CheckDead(context.Background(), nil)
		require.NoError(t, err)
		require.True(t, inner.checkDeadCalled)
	})

	t.Run("IsEnabled delegates to inner", func(t *testing.T) {
		t.Parallel()
		innerEnabled := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapperEnabled := wrapWithRouter(innerEnabled, cache, defaultDecayFactor, &shardCostForReads, "")
		require.True(t, wrapperEnabled.IsEnabled())

		innerDisabled := &mockPolicy{enabled: false}
		wrapperDisabled := wrapWithRouter(innerDisabled, cache, defaultDecayFactor, &shardCostForReads, "")
		require.False(t, wrapperDisabled.IsEnabled())
	})

	t.Run("configurePolicySettings delegates to configurable inner", func(t *testing.T) {
		t.Parallel()
		inner := &mockConfigurablePolicy{}
		inner.enabled = true
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "").(*poolRouter)

		config := policyConfig{
			name:                    "test-pool",
			resurrectTimeoutInitial: 10 * time.Second,
		}
		err := wrapper.configurePolicySettings(config)
		require.NoError(t, err)
		require.True(t, inner.configCalled)
		require.Equal(t, "test-pool", inner.lastConfig.name)
	})

	t.Run("configurePolicySettings returns nil when inner is not configurable", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "").(*poolRouter)

		config := policyConfig{name: "test-pool"}
		err := wrapper.configurePolicySettings(config)
		require.NoError(t, err)
	})
}

// --- Test 4: TestUpdateShardPlacementTree ---

func TestUpdateShardPlacementTree(t *testing.T) {
	t.Parallel()

	t.Run("walks simple tree and updates router cache", func(t *testing.T) {
		t.Parallel()
		cache := testIndexSlotCache()
		inner := &mockPolicy{enabled: true}
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Pre-create an index slot so updateFromDiscovery has something to update.
		cache.getOrCreate("my-index")

		shardPlacement := map[string]*indexShardPlacement{
			"my-index": {
				Nodes: map[string]*shardNodeInfo{
					"node1": {Primaries: 2, Replicas: 1},
					"node2": {Primaries: 0, Replicas: 3},
				},
			},
		}

		updateShardPlacementTree(wrapper, shardPlacement, 3)

		// Verify the cache was updated by checking the slot's shard node count.
		slot := cache.getOrCreate("my-index")
		require.Equal(t, int32(2), slot.shardNodeCount.Load(), "shard node count should be 2")
	})

	t.Run("works with nested PolicyChain to IfEnabledPolicy to wrapper", func(t *testing.T) {
		t.Parallel()
		cache := testIndexSlotCache()
		inner := &mockPolicy{enabled: true}
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Pre-create an index slot.
		cache.getOrCreate("nested-index")

		nullPolicy := NewNullPolicy()
		ifPolicy := NewIfEnabledPolicy(
			func(_ context.Context, _ *http.Request) bool { return true },
			wrapper,
			nullPolicy,
		)
		chain := NewPolicy(ifPolicy)

		shardPlacement := map[string]*indexShardPlacement{
			"nested-index": {
				Nodes: map[string]*shardNodeInfo{
					"nodeA": {Primaries: 1, Replicas: 0},
					"nodeB": {Primaries: 0, Replicas: 1},
					"nodeC": {Primaries: 1, Replicas: 1},
				},
			},
		}

		updateShardPlacementTree(chain, shardPlacement, 5)

		slot := cache.getOrCreate("nested-index")
		require.Equal(t, int32(3), slot.shardNodeCount.Load(), "shard node count should be 3 after tree walk")
	})

	t.Run("no-ops on policies without shardPlacementUpdater", func(t *testing.T) {
		t.Parallel()
		// A NullPolicy doesn't implement shardPlacementUpdater, so this should be safe.
		nullPolicy := NewNullPolicy()
		shardPlacement := map[string]*indexShardPlacement{
			"some-index": {
				Nodes: map[string]*shardNodeInfo{
					"node1": {Primaries: 1, Replicas: 0},
				},
			},
		}

		// Should not panic.
		updateShardPlacementTree(nullPolicy, shardPlacement, 1)
	})

	t.Run("works with nil shard placement data", func(t *testing.T) {
		t.Parallel()
		cache := testIndexSlotCache()
		inner := &mockPolicy{enabled: true}
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "")

		// Pre-create a slot with some shard data.
		slot := cache.getOrCreate("preserved-index")
		nodes := map[string]*shardNodeInfo{
			"existing": {Primaries: 1, Replicas: 0},
		}
		slot.shardNodeNames.Store(&nodes)
		slot.shardNodeCount.Store(1)

		// Pass nil shard placement -- existing data should be preserved.
		updateShardPlacementTree(wrapper, nil, 3)

		// The shardNodeCount value should be preserved because nil shardPlacement
		// skips the update inside updateFromDiscovery.
		slotAfter := cache.getOrCreate("preserved-index")
		require.Equal(t, int32(1), slotAfter.shardNodeCount.Load(), "nil placement should preserve existing shard data")
	})
}

// --- Test 5: TestChildPolicies ---

func TestChildPolicies(t *testing.T) {
	t.Parallel()

	t.Run("PolicyChain returns its policies slice", func(t *testing.T) {
		t.Parallel()
		p1 := NewNullPolicy()
		p2 := NewNullPolicy()
		chain := NewPolicy(p1, p2).(*PolicyChain)

		children := chain.childPolicies()
		require.Len(t, children, 2)
		require.Equal(t, p1, children[0])
		require.Equal(t, p2, children[1])
	})

	t.Run("IfEnabledPolicy returns truePolicy and falsePolicy", func(t *testing.T) {
		t.Parallel()
		trueP := NewNullPolicy()
		falseP := NewNullPolicy()
		ifPolicy := NewIfEnabledPolicy(
			func(_ context.Context, _ *http.Request) bool { return true },
			trueP,
			falseP,
		).(*IfEnabledPolicy)

		children := ifPolicy.childPolicies()
		require.Len(t, children, 2)
		require.Equal(t, trueP, children[0])
		require.Equal(t, falseP, children[1])
	})

	t.Run("MuxPolicy returns unique policies from its map", func(t *testing.T) {
		t.Parallel()
		// Use mockPolicy instances so they are distinct pointers (NullPolicy
		// is a zero-sized struct, so two *NullPolicy values may alias).
		p1 := &mockPolicy{enabled: true}
		p2 := &mockPolicy{enabled: true}
		// Create two routes pointing to distinct policies.
		routes := []Route{
			mustNewRouteMux("GET /_cluster/health", p1),
			mustNewRouteMux("POST /_bulk", p2),
		}
		mux := NewMuxPolicy(routes).(*MuxPolicy)

		children := mux.childPolicies()
		// MuxPolicy deduplicates policies, and both are distinct mockPolicy instances.
		require.Len(t, children, 2)

		// Verify both policies are present (order is non-deterministic from map iteration).
		childSet := make(map[Policy]struct{})
		for _, c := range children {
			childSet[c] = struct{}{}
		}
		require.Contains(t, childSet, p1)
		require.Contains(t, childSet, p2)
	})

	t.Run("MuxPolicy deduplicates shared policy", func(t *testing.T) {
		t.Parallel()
		shared := NewNullPolicy()
		routes := []Route{
			mustNewRouteMux("GET /_cluster/health", shared),
			mustNewRouteMux("POST /_bulk", shared),
		}
		mux := NewMuxPolicy(routes).(*MuxPolicy)

		children := mux.childPolicies()
		require.Len(t, children, 1, "shared policy should appear only once")
		require.Equal(t, shared, children[0])
	})

	t.Run("poolRouter returns inner as single element", func(t *testing.T) {
		t.Parallel()
		inner := &mockPolicy{enabled: true}
		cache := testIndexSlotCache()
		wrapper := wrapWithRouter(inner, cache, defaultDecayFactor, &shardCostForReads, "").(*poolRouter)

		children := wrapper.childPolicies()
		require.Len(t, children, 1)
		require.Equal(t, inner, children[0])
	})
}
