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

// ShardTarget is one copy of the shard a document with a given key lives on.
type ShardTarget struct {
	// Shard is the shard number.
	Shard int
	// Node is a node that hosts this copy of the shard and is ready to take a
	// request, or nil when the placement is not known or the node is not usable.
	// Send to it with [WithWriteNode]; nil leaves the choice to the router.
	Node *Connection
	// Primary reports that this copy is the shard's primary.
	Primary bool
	// SegmentReplication reports that the index uses segment replication, so a
	// replica can trail the primary: a realtime read that must see the latest
	// write goes to the primary.
	SegmentReplication bool
	// Generation identifies the shard geometry Shard was computed with: the primary
	// shard count and the routing factor. It changes when the index's geometry does,
	// as when the index is recreated, split or shrunk, and not when a shard moves
	// to another node. It is never 0.
	Generation uint64
}

// ErrShardMapUnknown is returned by [Transport.LookupShard] when the index's
// shard map is not known yet; see [Transport.ShardMap].
var ErrShardMapUnknown = errors.New("the shard map of the index is not known")

// LookupShard maps key in index to its shard and fills *dst with the shard's
// copies: the primary first, then each replica whose node is ready to take a
// request. The primary is always listed, with a nil Node when the transport does
// not know where it is or the node is not usable; a replica on a node that is not
// usable is left out.
//
// key is what the server hashes to pick the shard: the document's routing value
// when it has one, otherwise its ID.
//
// *dst is reset to length zero and refilled. The ShardTargets it points to are
// reused when its backing array already holds some, so a caller that keeps and
// reuses the slice, as from a sync.Pool, makes the lookup allocate nothing once
// the slice has grown to the largest number of copies. The pointers are only
// valid until the caller next passes *dst to LookupShard; copy a ShardTarget to
// keep it.
//
// It returns [ErrShardMapUnknown] when the index's shard map is not known yet
// (see [Transport.ShardMap]), and the reason from [Transport.ShardMapUnavailable]
// when the transport can never answer. *dst is empty on an error.
//
// Nodes are looked up in the transport's current placement on every call, so a
// shard that has moved is found where it is now. A caller that holds on to the
// result decides for itself how long to trust it.
func (tr *Transport) LookupShard(index, key string, dst *[]*ShardTarget) error {
	*dst = (*dst)[:0]

	cache := tr.shardMapCache()
	if cache == nil {
		if err := tr.ShardMapUnavailable(); err != nil {
			return err
		}

		return ErrShardMapUnknown
	}

	sm := cache.register(index).shardMap.Load()
	if sm == nil || sm.RoutingNumShards <= 0 || sm.NumberOfPrimaryShards <= 0 {
		return ErrShardMapUnknown
	}

	shard := shardForRouting(key, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
	nodes := sm.Shards[shard]

	primary := nextShardTarget(dst)
	*primary = ShardTarget{Shard: shard, Primary: true, SegmentReplication: sm.SegmentReplication, Generation: sm.Generation}

	if nodes == nil {
		return nil
	}

	if nodes.Primary != "" {
		if conn := tr.readyConnectionByName(nodes.Primary); conn != nil && !conn.needsCatUpdate() {
			primary.Node = conn
		}
	}

	for _, name := range nodes.Replicas {
		conn := tr.readyConnectionByName(name)
		if conn == nil || conn.needsCatUpdate() {
			continue
		}

		replica := nextShardTarget(dst)
		*replica = ShardTarget{Shard: shard, Node: conn, SegmentReplication: sm.SegmentReplication, Generation: sm.Generation}
	}

	return nil
}

// nextShardTarget extends *dst by one element and returns it, reusing the
// pointer already in the backing array when there is one.
func nextShardTarget(dst *[]*ShardTarget) *ShardTarget {
	n := len(*dst)
	if n < cap(*dst) {
		*dst = (*dst)[:n+1]
		if (*dst)[n] == nil {
			(*dst)[n] = new(ShardTarget)
		}

		return (*dst)[n]
	}

	t := new(ShardTarget)
	*dst = append(*dst, t)

	return t
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
		// Listed last, not left out: the caller still sends to a refusing node
		// when no other node will take the write.
		if conn.isPoolOverloaded(poolWrite) {
			return math.MaxFloat64
		}
		rtt, utilization := connBaseScore(conn, poolWrite, poolInfoReady)

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
// can. A caller that depends on the shard map, such as a bulk client sending
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
// information has not arrived yet, so that its pool size is unknown, reports the
// transport's initial window, as the routers use: four requests per allocated
// processor.
func (conn *Connection) WritePool() PoolWindow {
	return conn.poolWindow(poolWrite)
}

// GetPool returns the node's get thread pool window, which serves get and mget
// requests, in the same terms as [Connection.WritePool].
func (conn *Connection) GetPool() PoolWindow {
	return conn.poolWindow(poolGet)
}

func (conn *Connection) poolWindow(name string) PoolWindow {
	var maxWindow int
	if pc := conn.pools.get(name); pc != nil {
		pc.mu.Lock()
		maxWindow = int(pc.mu.maxCwnd)
		pc.mu.Unlock()
	}

	return PoolWindow{
		Window:     int(conn.loadCwnd(name, maxWindow > 0)),
		MaxWindow:  maxWindow,
		InFlight:   int(conn.loadInFlight(name)),
		Overloaded: conn.isPoolOverloaded(name),
	}
}

// pinnedNodeKey is the context key for [WithWriteNode] and [WithReadNode].
type pinnedNodeKey struct{}

// pinnedNode is a node a context asks for and the thread pool the request is
// accounted to.
type pinnedNode struct {
	conn *Connection
	pool string
}

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
	return context.WithValue(ctx, pinnedNodeKey{}, pinnedNode{conn: conn, pool: poolWrite})
}

// WithReadNode is [WithWriteNode] for a get or mget request: it sends the
// request to conn, a node from [Transport.LookupShard], and accounts it to the
// node's get pool. The same preference rules apply.
func WithReadNode(ctx context.Context, conn *Connection) context.Context {
	return context.WithValue(ctx, pinnedNodeKey{}, pinnedNode{conn: conn, pool: poolGet})
}

// pinnedRequestNode returns the node a context asks for, if it is usable, and
// the pool to account the request to.
func pinnedRequestNode(ctx context.Context) (*Connection, string) {
	pin, _ := ctx.Value(pinnedNodeKey{}).(pinnedNode)
	if pin.conn == nil || !pin.conn.isReady() || !pin.conn.availableForRouting() {
		return nil, ""
	}

	return pin.conn, pin.pool
}
