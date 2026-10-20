// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
)

// ShardMap is the routing half of an index's shard map: the two numbers that
// decide which shard a routing value belongs to. It is a snapshot; an index's
// shard count does not change once the index exists, so it does not go stale.
type ShardMap struct {
	// RoutingNumShards is the index's routing_num_shards, the hash modulus.
	RoutingNumShards int
	// PrimaryShards is the index's number_of_shards.
	PrimaryShards int
}

// Shard returns the shard the routing value maps to, the way the server
// computes it. A document with no routing value is routed by its ID.
func (m ShardMap) Shard(routing string) int {
	return shardForRouting(routing, m.RoutingNumShards, m.PrimaryShards)
}

// shardMapCache returns the router's index cache, or nil when the transport
// has none.
func (tr *Transport) shardMapCache() *indexSlotCache {
	if tr.router == nil {
		return nil
	}

	policy, isPolicy := tr.router.(Policy)
	if !isPolicy {
		return nil
	}

	return findRouterCache(policy)
}

// ShardMap returns the shard map the transport has cached for index, and false
// when it is not known: the transport has no router, discovery has not fetched
// the index's routing_num_shards yet, or that fetch is disabled by
// OPENSEARCH_GO_DISCOVERY_CONFIG.
//
// The first lookup of an index registers it with the router's index cache, so
// the next discovery cycle (DiscoverNodesInterval, or [Transport.DiscoverNodes])
// fetches its shard map; until then the answer is false. Registration is needed
// because an index named only in a bulk action line never reaches the cache by
// way of request routing. A lookup does not count as a request for the index and
// does not change its fan-out, and an index nobody asks about for the cache's
// idle eviction period is dropped again.
func (tr *Transport) ShardMap(index string) (ShardMap, bool) {
	cache := tr.shardMapCache()
	if cache == nil {
		return ShardMap{}, false
	}

	sm := cache.register(index).shardMap.Load()
	if sm == nil || sm.RoutingNumShards <= 0 || sm.NumberOfPrimaryShards <= 0 {
		return ShardMap{}, false
	}

	return ShardMap{RoutingNumShards: sm.RoutingNumShards, PrimaryShards: sm.NumberOfPrimaryShards}, true
}

// ShardTarget is where a document with a given routing value lives.
type ShardTarget struct {
	// Shard is the shard number.
	Shard int
	// Node is a node that hosts the shard's primary and is ready to take a
	// request, or nil when the placement is not known or the node is not usable.
	// Send to it with [WithWriteNode]; nil leaves the choice to the router.
	Node *Connection
	// Generation identifies the shard geometry Shard was computed with: the primary
	// shard count and the routing factor. It changes when the index's geometry does,
	// as when the index is recreated, split or shrunk, and not when a shard moves
	// to another node. It is never 0.
	Generation uint64
}

// LookupShard maps a routing value in index to its shard and, when the transport
// knows where the shard's primary is, to the node hosting it. It returns false
// when the index's shard map is not known; see [Transport.ShardMap].
//
// The node is looked up in the transport's current placement on every call, so
// a shard that has moved is found where it is now. A caller that holds on to the
// result decides for itself how long to trust it.
func (tr *Transport) LookupShard(index, routing string) (ShardTarget, bool) {
	cache := tr.shardMapCache()
	if cache == nil {
		return ShardTarget{}, false
	}

	sm := cache.register(index).shardMap.Load()
	if sm == nil || sm.RoutingNumShards <= 0 || sm.NumberOfPrimaryShards <= 0 {
		return ShardTarget{}, false
	}

	shard := shardForRouting(routing, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
	target := ShardTarget{Shard: shard, Generation: sm.Generation}

	if nodes := sm.Shards[shard]; nodes != nil && nodes.Primary != "" {
		if conn := tr.readyConnectionByName(nodes.Primary); conn != nil && !conn.needsCatUpdate() {
			target.Node = conn
		}
	}

	return target, true
}

// connectionNamer is implemented by the built-in connection pools.
type connectionNamer interface {
	connectionByName(name string) *Connection
}

// readyConnectionByName returns the named node's connection when it is in
// service and proven reachable. It does not allocate.
func (tr *Transport) readyConnectionByName(name string) *Connection {
	tr.mu.RLock()
	pool := tr.mu.connectionPool
	tr.mu.RUnlock()

	namer, ok := pool.(connectionNamer)
	if !ok {
		return nil
	}

	conn := namer.connectionByName(name)
	if conn == nil || !conn.isReady() || !conn.availableForRouting() {
		return nil
	}

	return conn
}

// connectionLister is implemented by the built-in connection pools.
type connectionLister interface {
	connections() []*Connection
}

// readyConnections returns the connections that are in service and proven
// reachable.
func (tr *Transport) readyConnections() []*Connection {
	tr.mu.RLock()
	pool := tr.mu.connectionPool
	tr.mu.RUnlock()

	lister, ok := pool.(connectionLister)
	if !ok {
		return nil
	}

	conns := lister.connections()
	ready := conns[:0:0]
	for _, conn := range conns {
		if conn.isReady() && conn.availableForRouting() {
			ready = append(ready, conn)
		}
	}

	return ready
}

// WriteNodes returns the nodes a write that has no particular shard should go
// to, best first. It is the router's own ranking for write requests, by round
// trip time and by how much of the node's write pool is in use: the nearest node
// that has room first, and a node whose write pool is rejecting requests last.
//
// When any node has the ingest role, only those nodes are listed, as the router
// does for bulk requests; otherwise every ready node is.
func (tr *Transport) WriteNodes() []*Connection {
	ready := tr.readyConnections()

	ingest := ready[:0:0]
	for _, conn := range ready {
		if _, ok := conn.Roles[RoleIngest]; ok {
			ingest = append(ingest, conn)
		}
	}
	if len(ingest) > 0 {
		ready = ingest
	}

	poolInfoReady := tr.poolInfoReady.Load()
	score := func(conn *Connection) float64 {
		rtt, utilization, overloaded := connBaseScore(conn, poolWrite, poolInfoReady)
		if overloaded {
			return math.MaxFloat64
		}

		return rtt * utilization
	}

	slices.SortStableFunc(ready, func(a, b *Connection) int {
		if c := cmp.Compare(score(a), score(b)); c != 0 {
			return c
		}

		return cmp.Compare(a.URLString, b.URLString)
	})

	return ready
}

// ShardMapUnavailable reports why [Transport.ShardMap] and
// [Transport.LookupShard] can never answer on this transport, or nil when they
// can. A caller that depends on the shard map, such as a bulk indexer sending
// each shard's documents to the node that hosts it, uses it to say so once at
// start-up instead of silently losing the benefit.
//
// It reports a router that is not there (OPENSEARCH_GO_ROUTER=false, or a
// transport built without one), and the discovery calls that
// OPENSEARCH_GO_DISCOVERY_CONFIG can switch off: the shard map is read from
// /_cat/shards and from /_cluster/state/metadata, so turning off either leaves
// every index unknown. A nil error does not promise that a given index is
// known, only that discovery will fetch it.
func (tr *Transport) ShardMapUnavailable() error {
	switch {
	case tr.router == nil:
		return errShardMapNoRouter
	case !tr.discoveryFeatures.catShardsEnabled():
		return errShardMapNoCatShards
	case !tr.discoveryFeatures.routingNumShardsEnabled():
		return errShardMapNoRoutingNumShards
	}

	return nil
}

var (
	errShardMapNoRouter = errors.New(
		"the transport has no router, so no shard map is collected (see " + envRouter + ")")
	errShardMapNoCatShards = errors.New(
		"discovery does not fetch /_cat/shards (" + envDiscoveryConfig + " has -cat_shards), so no shard map is collected")
	errShardMapNoRoutingNumShards = errors.New(
		"discovery does not fetch routing_num_shards (" + envDiscoveryConfig + " has -routing_num_shards), so no shard map is collected")
)

// PoolWindow is a snapshot of a node's view of one of its thread pools, as the
// transport tracks it.
type PoolWindow struct {
	// Window is the number of requests the transport currently allows in flight
	// to the pool. It grows while the pool keeps up and halves when the pool
	// queues or rejects, driven by the node's thread pool statistics.
	Window int
	// MaxWindow is the pool's configured size, the ceiling of Window, or 0 when
	// the node has not reported it.
	MaxWindow int
	// InFlight is the number of requests the transport has outstanding to the
	// pool on this node.
	InFlight int
	// Overloaded is set when the pool rejected requests since the last statistics
	// poll, or answered 429.
	Overloaded bool
}

// WritePool returns the node's write thread pool window. A node whose pool
// information has not arrived yet reports the transport's initial window.
func (conn *Connection) WritePool() PoolWindow {
	w := PoolWindow{
		Window:     int(conn.loadCwnd(poolWrite, true)),
		InFlight:   int(conn.loadInFlight(poolWrite)),
		Overloaded: conn.isPoolOverloaded(poolWrite),
	}

	if pc := conn.pools.get(poolWrite); pc != nil {
		pc.mu.Lock()
		w.MaxWindow = int(pc.mu.maxCwnd)
		pc.mu.Unlock()
	}

	return w
}

// writeNodeKey is the context key for [WithWriteNode].
type writeNodeKey struct{}

// WithWriteNode returns a context that sends a write request to conn, a node
// from [Transport.LookupShard] or [Transport.WriteNodes], in place of the node
// the router would choose. The request is accounted to conn's write pool as any
// routed write is.
//
// It is a preference. The router chooses as usual when conn is not in service,
// and on every retry after the first attempt, since a node that just failed is
// not one to go back to. The transport still retries, marks nodes dead and
// updates its windows as for any request.
func WithWriteNode(ctx context.Context, conn *Connection) context.Context {
	return context.WithValue(ctx, writeNodeKey{}, conn)
}

// pinnedWriteNode returns the node a context asks for, if it is usable.
func pinnedWriteNode(ctx context.Context) *Connection {
	conn, _ := ctx.Value(writeNodeKey{}).(*Connection)
	if conn == nil || !conn.isReady() || !conn.availableForRouting() {
		return nil
	}

	return conn
}
