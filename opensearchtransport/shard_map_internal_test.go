// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// shardMapTransport builds a transport with a router, whose connection pool
// holds conns.
func shardMapTransport(conns ...*Connection) (*Transport, *IndexRouter) {
	r := newIndexRouter(indexSlotCacheConfig{})
	tr := &Transport{router: NewRouter(r)}
	tr.mu.connectionPool = testPool(conns)

	return tr, r
}

// routableConn is a connection the transport may hand a request to.
func routableConn(t *testing.T, name string, rtt time.Duration, roles ...string) *Connection {
	t.Helper()

	c := newTestConnRTT(t, name, rtt)
	c.setLifecycleBit(lcViable)
	c.Roles = newRoleSet(roles)

	return c
}

func TestTransportShardMap(t *testing.T) {
	t.Parallel()

	type result struct {
		routingNumShards int
		primaryShards    int
		ok               bool
	}

	tests := []struct {
		name  string
		setup func() *Transport
		index string
		want  result
	}{
		{
			name:  "no router",
			setup: func() *Transport { return &Transport{} },
			index: "idx",
		},
		{
			name: "unknown index is registered, not answered",
			setup: func() *Transport {
				tr, _ := shardMapTransport()
				return tr
			},
			index: "idx",
		},
		{
			name: "routing_num_shards not yet fetched",
			setup: func() *Transport {
				tr, r := shardMapTransport()
				r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 5})
				return tr
			},
			index: "idx",
		},
		{
			name: "shard map cached",
			setup: func() *Transport {
				tr, r := shardMapTransport()
				r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 5, RoutingNumShards: 640})
				return tr
			},
			index: "idx",
			want:  result{routingNumShards: 640, primaryShards: 5, ok: true},
		},
		{
			name: "other index is not answered",
			setup: func() *Transport {
				tr, r := shardMapTransport()
				r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 5, RoutingNumShards: 640})
				return tr
			},
			index: "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, ok := tt.setup().ShardMap(tt.index)
			require.Equal(t, tt.want, result{m.RoutingNumShards, m.PrimaryShards, ok})
		})
	}
}

func TestTransportShardMapRegistersIndex(t *testing.T) {
	t.Parallel()

	tr, r := shardMapTransport()

	_, ok := tr.ShardMap("idx")
	require.False(t, ok, "the shard map is unknown until discovery fetches it")

	slot := r.cache.slotFor("idx")
	require.NotNil(t, slot, "the lookup must register the index so discovery fetches its shard map")
	require.InDelta(t, 0.0, slot.requestDecay.load(), 1e-9, "a lookup is not a request")
	require.EqualValues(t, r.cache.minFanOut, slot.fanOut.Load(), "a lookup must not change fan-out")

	// Discovery stores the fetched shard map on the slot the lookup registered.
	slot.shardMap.Store(&indexShardMap{NumberOfPrimaryShards: 32, RoutingNumShards: 1024})

	m, ok := tr.ShardMap("idx")
	require.True(t, ok)
	require.Equal(t, ShardMap{RoutingNumShards: 1024, PrimaryShards: 32}, m)
}

func TestIndexSlotCacheRegisterClearsIdle(t *testing.T) {
	t.Parallel()

	cache := newIndexSlotCache(indexSlotCacheConfig{})
	slot := cache.register("idx")
	slot.idleSince.Store(12345)

	require.Same(t, slot, cache.register("idx"))
	require.Zero(t, slot.idleSince.Load(), "register marks the slot active so idle eviction spares it")
}

func TestShardMapShard(t *testing.T) {
	t.Parallel()

	m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
	tests := []struct {
		name string
		key  string
	}{
		{name: "a single character", key: "a"},
		{name: "a document id", key: "doc-1"},
		{name: "a tenant key with a colon", key: "tenant:acme"},
		{name: "a short hex word", key: "cafe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := m.Shard(tt.key)
			require.GreaterOrEqual(t, got, 0)
			require.Less(t, got, 5)
			require.Equal(t, shardForRouting(tt.key, 640, 5), got)
		})
	}
}

func TestTransportLookupShard(t *testing.T) {
	t.Parallel()

	const key = "doc-1"
	m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
	shard := m.Shard(key)

	tests := []struct {
		name string
		// conns builds the transport's connections.
		conns func() []*Connection
		// nodes is the placement the transport has cached for the shard.
		nodes    *shardNodes
		noMap    bool
		wantOK   bool
		wantNode string
	}{
		{
			name: "primary is ready",
			conns: func() []*Connection {
				return []*Connection{routableConn(t, "n1", time.Millisecond), routableConn(t, "n2", time.Millisecond)}
			},
			nodes:    &shardNodes{Primary: "n2", Replicas: []string{"n1"}},
			wantOK:   true,
			wantNode: "n2",
		},
		{
			name:   "unknown shard map",
			conns:  func() []*Connection { return []*Connection{routableConn(t, "n1", time.Millisecond)} },
			noMap:  true,
			wantOK: false,
		},
		{
			name:   "no placement for the shard",
			conns:  func() []*Connection { return []*Connection{routableConn(t, "n1", time.Millisecond)} },
			nodes:  nil,
			wantOK: true,
		},
		{
			name:   "primary is not in the pool",
			conns:  func() []*Connection { return []*Connection{routableConn(t, "n1", time.Millisecond)} },
			nodes:  &shardNodes{Primary: "gone"},
			wantOK: true,
		},
		{
			name: "primary is not ready",
			conns: func() []*Connection {
				c := routableConn(t, "n1", time.Millisecond)
				c.clearLifecycleBit(lcActive)
				return []*Connection{c}
			},
			nodes:  &shardNodes{Primary: "n1"},
			wantOK: true,
		},
		{
			name: "placement is stale after a failure",
			conns: func() []*Connection {
				c := routableConn(t, "n1", time.Millisecond)
				c.setNeedsCatUpdate()
				return []*Connection{c}
			},
			nodes:  &shardNodes{Primary: "n1"},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, r := shardMapTransport(tt.conns()...)
			if !tt.noMap {
				sm := &indexShardMap{NumberOfPrimaryShards: m.PrimaryShards, RoutingNumShards: m.RoutingNumShards}
				if tt.nodes != nil {
					sm.Shards = map[int]*shardNodes{shard: tt.nodes}
				}
				r.cache.getOrCreate("idx").shardMap.Store(sm)
			}

			target, ok := lookupPrimary(tr, "idx", key)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}

			require.Equal(t, shard, target.Shard)
			if tt.wantNode == "" {
				require.Nil(t, target.Node)
				return
			}
			require.NotNil(t, target.Node)
			require.Equal(t, tt.wantNode, target.Node.Name)
		})
	}
}

func TestTransportLookupShardFollowsAMovedShard(t *testing.T) {
	t.Parallel()

	const key = "doc-1"
	m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
	shard := m.Shard(key)

	tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond), routableConn(t, "n2", time.Millisecond))
	slot := r.cache.getOrCreate("idx")
	place := func(primary string) {
		slot.shardMap.Store(&indexShardMap{
			NumberOfPrimaryShards: m.PrimaryShards,
			RoutingNumShards:      m.RoutingNumShards,
			Shards:                map[int]*shardNodes{shard: {Primary: primary}},
		})
	}

	place("n1")
	target, ok := lookupPrimary(tr, "idx", key)
	require.True(t, ok)
	require.Equal(t, "n1", target.Node.Name)

	place("n2") // Discovery sees the shard relocate.
	moved, ok := lookupPrimary(tr, "idx", key)
	require.True(t, ok)
	require.Equal(t, shard, moved.Shard, "the shard does not change when it moves")
	require.Equal(t, "n2", moved.Node.Name, "the node does")
}

func TestTransportWriteNodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		conns func() []*Connection
		want  []string
	}{
		{
			name: "nearest first",
			conns: func() []*Connection {
				return []*Connection{
					routableConn(t, "far", 40*time.Millisecond),
					routableConn(t, "near", time.Millisecond),
					routableConn(t, "mid", 10*time.Millisecond),
				}
			},
			want: []string{"near", "mid", "far"},
		},
		{
			name: "a node with a rejecting write pool is last",
			conns: func() []*Connection {
				near := routableConn(t, "near", time.Millisecond)
				near.pools.getOrCreate(poolWrite).overloaded.Store(true)
				return []*Connection{near, routableConn(t, "far", 40*time.Millisecond)}
			},
			want: []string{"far", "near"},
		},
		{
			name: "a busier node ranks behind an idle one at the same distance",
			conns: func() []*Connection {
				busy := routableConn(t, "busy", time.Millisecond)
				busy.addInFlight(poolWrite)
				busy.addInFlight(poolWrite)
				return []*Connection{busy, routableConn(t, "idle", time.Millisecond)}
			},
			want: []string{"idle", "busy"},
		},
		{
			name: "only ingest nodes when there are any",
			conns: func() []*Connection {
				return []*Connection{
					routableConn(t, "data", time.Millisecond, RoleData),
					routableConn(t, "ingest", 20*time.Millisecond, RoleIngest),
				}
			},
			want: []string{"ingest"},
		},
		{
			name: "every ready node when none is ingest",
			conns: func() []*Connection {
				return []*Connection{
					routableConn(t, "a", 2*time.Millisecond, RoleData),
					routableConn(t, "b", time.Millisecond, RoleData),
				}
			},
			want: []string{"b", "a"},
		},
		{
			name: "a node that is not ready is left out",
			conns: func() []*Connection {
				down := routableConn(t, "down", time.Millisecond)
				down.clearLifecycleBit(lcActive)
				return []*Connection{down, routableConn(t, "up", 5*time.Millisecond)}
			},
			want: []string{"up"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, _ := shardMapTransport(tt.conns()...)
			tr.poolInfoReady.Store(true)

			var got []string
			for _, conn := range tr.WriteNodes() {
				got = append(got, conn.Name)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSingleServerPoolConnectionByName(t *testing.T) {
	t.Parallel()

	conn := routableConn(t, "only", time.Millisecond)
	pool := newSingleServerPool(conn, nil)

	tests := []struct {
		name string
		node string
		want *Connection
	}{
		{name: "the pool's node", node: "only", want: conn},
		{name: "another node", node: "other"},
		{name: "a name that only shares a prefix", node: "on"},
		{name: "no name", node: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pool.connectionByName(tt.node)
			if tt.want == nil {
				require.Nil(t, got)
				return
			}
			require.Same(t, tt.want, got)
		})
	}
}

// TestTransportShardRoutingOverSingleServerPool: a transport with one connection,
// which keeps a single-server pool, answers shard lookups and ranks write nodes
// as it does with a pool of many.
func TestTransportShardRoutingOverSingleServerPool(t *testing.T) {
	t.Parallel()

	const key = "doc-1"
	m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
	shard := m.Shard(key)

	tests := []struct {
		name string
		// primary is the node the cached placement names for the shard.
		primary string
		// ready is false for a connection the pool cannot hand a request to.
		ready    bool
		wantNode string
	}{
		{name: "the shard's primary is the pool's node", primary: "only", ready: true, wantNode: "only"},
		{name: "the shard's primary is another node", primary: "other", ready: true},
		{name: "the pool's node is not ready", primary: "only", ready: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := routableConn(t, "only", time.Millisecond)
			tr, r := shardMapTransport()
			tr.mu.connectionPool = newSingleServerPool(conn, nil)
			if !tt.ready {
				conn.clearLifecycleBit(lcActive)
			}

			r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
				NumberOfPrimaryShards: m.PrimaryShards,
				RoutingNumShards:      m.RoutingNumShards,
				Shards:                map[int]*shardNodes{shard: {Primary: tt.primary}},
			})

			target, ok := lookupPrimary(tr, "idx", key)
			require.True(t, ok)
			require.Equal(t, shard, target.Shard)
			if tt.wantNode == "" {
				require.Nil(t, target.Node)
			} else {
				require.NotNil(t, target.Node)
				require.Equal(t, tt.wantNode, target.Node.Name)
			}

			conns := tr.WriteNodes()
			nodes := make([]string, 0, len(conns))
			for _, c := range conns {
				nodes = append(nodes, c.Name)
			}
			if tt.ready {
				require.Equal(t, []string{"only"}, nodes)
			} else {
				require.Empty(t, nodes)
			}
		})
	}
}

func TestConnectionWritePool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(conn *Connection)
		want  PoolWindow
	}{
		{
			name:  "before the node reports its pools, the initial window",
			setup: func(*Connection) {},
			want:  PoolWindow{Window: defaultSyntheticCwndMultiplier * defaultServerCoreCount},
		},
		{
			name:  "before the node reports its pools, four per allocated processor",
			setup: func(conn *Connection) { conn.storeAllocatedProcessors(2) },
			want:  PoolWindow{Window: defaultSyntheticCwndMultiplier * 2},
		},
		{
			name: "after the node reports a window, requests in flight and an overloaded pool",
			setup: func(conn *Connection) {
				pc := conn.pools.getOrCreate(poolWrite)
				pc.cwnd.Store(6)
				pc.mu.Lock()
				pc.mu.maxCwnd = 16
				pc.mu.Unlock()
				conn.addInFlight(poolWrite)
				conn.addInFlight(poolWrite)
				pc.overloaded.Store(true)
			},
			want: PoolWindow{Window: 6, MaxWindow: 16, InFlight: 2, Overloaded: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := routableConn(t, "n1", time.Millisecond)
			tt.setup(conn)

			require.Equal(t, tt.want, conn.WritePool())
		})
	}
}

func TestTransportShardMapUnavailable(t *testing.T) {
	t.Parallel()

	withRouter := func(features discoveryFeatures) *Transport {
		return &Transport{router: NewRouter(newIndexRouter(indexSlotCacheConfig{})), discoveryFeatures: features}
	}

	tests := []struct {
		name string
		tr   *Transport
		want error
	}{
		{name: "router and discovery on", tr: withRouter(0), want: nil},
		{name: "no router", tr: &Transport{}, want: errShardMapNoRouter},
		{name: "cat shards off", tr: withRouter(discoverySkipCatShards), want: errShardMapNoCatShards},
		{name: "routing_num_shards off", tr: withRouter(discoverySkipRoutingNumShards), want: errShardMapNoRoutingNumShards},
		{name: "unrelated discovery calls off", tr: withRouter(discoverySkipClusterHealth | discoverySkipNodeStats), want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.tr.ShardMapUnavailable())
		})
	}
}

// fixedRouter routes every request to the connection pick returns, so a test
// knows where ordinary routing sends a request.
type fixedRouter struct{ pick func() *Connection }

func (r fixedRouter) Route(context.Context, *http.Request) (NextHop, error) {
	return NextHop{Conn: r.pick(), PoolName: poolWrite}, nil
}
func (fixedRouter) OnSuccess(*Connection)                            {}
func (fixedRouter) OnFailure(*Connection) error                      { return nil }
func (fixedRouter) DiscoveryUpdate(_, _, _ []*Connection) error      { return nil }
func (fixedRouter) CheckDead(context.Context, HealthCheckFunc) error { return nil }
func (fixedRouter) RotateStandby(context.Context, int) (int, error)  { return 0, nil }

// pinHarness is a transport over two nodes that records which host each request
// reached. Ordinary routing always picks node-a.
type pinHarness struct {
	tr *Transport

	mu struct {
		sync.Mutex
		hosts []string
		// failFirstOn, when set, answers 503 to the first request that reaches it.
		failFirstOn string
		failed      bool
	}
}

func newPinHarness(t *testing.T) *pinHarness {
	t.Helper()

	h := &pinHarness{}
	tr, err := New(Config{
		URLs: []*url.URL{
			{Scheme: "http", Host: "node-a:9200"},
			{Scheme: "http", Host: "node-b:9200"},
		},
		NodeStatsInterval: -1,
		Router:            fixedRouter{pick: func() *Connection { return h.node(t, "node-a:9200") }},
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/_bulk" {
				// The transport's own health checks share the mock.
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
			}

			h.mu.Lock()
			defer h.mu.Unlock()

			h.mu.hosts = append(h.mu.hosts, req.URL.Host)
			status := http.StatusOK
			if req.URL.Host == h.mu.failFirstOn && !h.mu.failed {
				h.mu.failed = true
				status = http.StatusServiceUnavailable
			}

			return &http.Response{StatusCode: status, Body: io.NopCloser(http.NoBody)}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })
	h.tr = tr

	return h
}

func (h *pinHarness) node(t *testing.T, host string) *Connection {
	t.Helper()

	for _, conn := range h.tr.readyConnections() {
		if conn.URL.Host == host {
			return conn
		}
	}
	require.Failf(t, "no ready connection", "host %q", host)

	return nil
}

func (h *pinHarness) send(t *testing.T, conn *Connection) {
	t.Helper()

	ctx := t.Context()
	if conn != nil {
		ctx = WithWriteNode(ctx, conn)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/_bulk", nil)
	require.NoError(t, err)

	res, err := h.tr.Stream(req)
	require.NoError(t, err)
	_ = res.Body.Close()
}

func (h *pinHarness) hosts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]string(nil), h.mu.hosts...)
}

func TestWithWriteNode(t *testing.T) {
	t.Parallel()

	const (
		nodeA = "node-a:9200" // where ordinary routing always sends a request
		nodeB = "node-b:9200"
	)

	tests := []struct {
		name string
		// pin is the host the requests are pinned to; empty sends them unpinned.
		pin string
		// setup runs before the requests, with the pinned connection if there is one.
		setup func(h *pinHarness, pinned *Connection)
		// sends is how many requests are sent.
		sends int
		want  []string
		why   string
	}{
		{
			name:  "requests go to the pinned node",
			pin:   nodeB,
			sends: 5,
			want:  []string{nodeB, nodeB, nodeB, nodeB, nodeB},
		},
		{
			name:  "an unpinned request is routed as usual",
			sends: 1,
			want:  []string{nodeA},
		},
		{
			name: "a pin on a node that is out of service is ignored",
			pin:  nodeB,
			setup: func(_ *pinHarness, pinned *Connection) {
				pinned.clearLifecycleBit(lcActive | lcStandby)
			},
			sends: 1,
			want:  []string{nodeA},
		},
		{
			name: "a retry does not go back to the pinned node",
			pin:  nodeB,
			setup: func(h *pinHarness, _ *Connection) {
				h.mu.failFirstOn = nodeB
			},
			sends: 1,
			want:  []string{nodeB, nodeA},
			why:   "the retry follows ordinary routing instead of the pin, whose node just failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newPinHarness(t)
			var pinned *Connection
			if tt.pin != "" {
				pinned = h.node(t, tt.pin)
			}
			if tt.setup != nil {
				tt.setup(h, pinned)
			}

			for range tt.sends {
				h.send(t, pinned)
			}
			require.Equal(t, tt.want, h.hosts(), tt.why)
		})
	}
}

func TestMergeShardMapMovesTheGenerationWithTheGeometry(t *testing.T) {
	t.Parallel()

	slot := &indexSlot{clock: realClock{}}
	merge := func(primaries, routing int, primary string) uint64 {
		slot.mergeShardMap(&indexShardPlacement{
			NumberOfPrimaryShards: primaries,
			RoutingNumShards:      routing,
			ShardToNodes:          map[int]*shardNodes{0: {Primary: primary}},
		})

		return slot.shardMap.Load().Generation
	}

	first := merge(5, 640, "n1")
	require.NotZero(t, first, "a map built by a merge is versioned")

	require.Equal(t, first, merge(5, 640, "n2"), "a shard that moves to another node keeps the geometry")

	resized := merge(10, 640, "n2")
	require.NotEqual(t, first, resized, "a different shard count is a different geometry")

	reshaped := merge(10, 1280, "n2")
	require.NotEqual(t, resized, reshaped, "so is a different routing factor")

	require.NotEqual(t, first, merge(5, 640, "n2"), "a geometry that comes back is a new generation, not the old one")
}

func TestTransportLookupShardReportsTheGeneration(t *testing.T) {
	t.Parallel()

	tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
	slot := r.cache.getOrCreate("idx")
	slot.mergeShardMap(&indexShardPlacement{
		NumberOfPrimaryShards: 5,
		RoutingNumShards:      640,
		ShardToNodes:          map[int]*shardNodes{0: {Primary: "n1"}},
	})

	target, ok := lookupPrimary(tr, "idx", "doc-1")
	require.True(t, ok)
	require.NotZero(t, target.Generation)
	require.Equal(t, slot.shardMap.Load().Generation, target.Generation)
}

// lookupPrimary returns the first copy LookupShard lists, the primary, and whether
// the shard map of index is known.
func lookupPrimary(tr *Transport, index, key string) (ShardTarget, bool) {
	var targets []*ShardTarget
	if err := tr.LookupShard(index, key, &targets); err != nil || len(targets) == 0 {
		return ShardTarget{}, false
	}

	return *targets[0], true
}
