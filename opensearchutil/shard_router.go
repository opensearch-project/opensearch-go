// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"math"
	"math/bits"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// maxDefaultRoutingShardsLog2 is log2 of the shard count the server aims for
// when it picks routing_num_shards for an index: 1024, the largest
// numShards * 2^n that does not exceed it
// (MetadataCreateIndexService.calculateNumRoutingShards).
const maxDefaultRoutingShardsLog2 = 10

// ShardMap is the routing half of an index's shard map: the two numbers that
// decide which shard a routing value belongs to.
type ShardMap = opensearchtransport.ShardMap

// Node is a node a bulk request can be sent to.
type Node struct {
	// Name identifies the node. Batches for one node are buffered together.
	Name string
	// Conn is the transport's connection to the node. A request is sent to it
	// directly when it is set, and routed as usual when it is nil.
	Conn *opensearchtransport.Connection
	// Pool reports the state of the node's write thread pool as the transport
	// tracks it. When nil, Conn's is used, and a node with neither is treated as
	// one whose window is unknown.
	Pool PoolWindowFunc
}

// PoolWindowFunc reports the state of a node's write thread pool.
type PoolWindowFunc func() opensearchtransport.PoolWindow

// pool returns the node's write pool window, or the zero value when unknown.
func (n Node) pool() opensearchtransport.PoolWindow {
	switch {
	case n.Pool != nil:
		return n.Pool()
	case n.Conn != nil:
		return n.Conn.WritePool()
	default:
		return opensearchtransport.PoolWindow{}
	}
}

// ShardTarget is where a document with a given routing value lives.
type ShardTarget struct {
	// Shard is the shard number.
	Shard int
	// Node hosts the shard's primary. Its Name is empty when the placement is not
	// known, in which case the indexer sends the document's batch to the node it
	// chooses.
	Node Node
	// Generation identifies the shard geometry the router placed the document
	// with: the shard count and the routing factor that decide which shard a
	// routing value belongs to. It changes when the geometry does, as when an index
	// is recreated, split or shrunk, and it is otherwise stable, whichever node
	// hosts the shard. The indexer compares it from one lookup to the next to know
	// that a document may no longer belong to the shard its earlier actions went
	// to; see [ErrShardMapChanged]. A router that cannot tell reports 0, which the
	// indexer treats as unversioned and never as a change.
	Generation uint64
}

// ShardRouter maps documents to shards and shards to nodes, and ranks nodes for
// documents that have no shard. The indexer uses it to buffer a document with
// the other documents bound for the same node, and to keep every document of
// one shard in order.
//
// [opensearchtransport.Transport] provides it from the shard map and the node
// ranking it already keeps for routing; see [ClientShardRouter]. The interface
// is defined here, in the consumer package, so the transport satisfies it
// without importing this package.
type ShardRouter interface {
	// LookupShard maps a routing value in index to its shard, and to the node
	// hosting the shard. It returns false when the index's shard map is not
	// known, in which case documents for the index are not tied to a shard.
	LookupShard(index, routing string) (ShardTarget, bool)

	// WriteNodes returns the nodes a document that has no shard should go to,
	// best first. The indexer fills the first one it finds with room before it
	// moves on.
	WriteNodes() []Node
}

type noShardRouter struct{}

// LookupShard implements [ShardRouter]; it knows no shard.
func (noShardRouter) LookupShard(string, string) (ShardTarget, bool) { return ShardTarget{}, false }

// WriteNodes implements [ShardRouter]; it knows no node.
func (noShardRouter) WriteNodes() []Node { return nil }

// NoShardRouter is a [ShardRouter] that knows nothing. With it the indexer ties
// no document to a shard or a node: it buffers documents together and leaves
// every request to the transport's own routing. Set it as
// BulkIndexerConfig.ShardRouter to turn shard-aware buffering off.
//
//nolint:gochecknoglobals // A stateless value, like http.NoBody.
var NoShardRouter ShardRouter = noShardRouter{}

// StaticShardRouter serves fixed shard maps by index name, for tests and for
// callers who know their own mappings. It places no document on a node, so it is
// useful behind a [ChainShardRouter] that does.
//
// An index absent from the map, or mapped to a shard map that cannot be used
// (a non-positive shard count, or a routing_num_shards that is not a multiple
// of it), is reported as unknown. The map must not be modified while an indexer
// is using it.
type StaticShardRouter map[string]ShardMap

// LookupShard implements [ShardRouter].
func (r StaticShardRouter) LookupShard(index, routing string) (ShardTarget, bool) {
	m, found := r[index]
	if !found || !validShardMap(m) {
		return ShardTarget{}, false
	}

	return ShardTarget{Shard: m.Shard(routing)}, true
}

// WriteNodes implements [ShardRouter]; a static router knows no nodes.
func (StaticShardRouter) WriteNodes() []Node { return nil }

// validShardMap reports whether m can be used to compute a shard: it divides by
// PrimaryShards, and the server requires RoutingNumShards to be a multiple of it.
func validShardMap(m ShardMap) bool {
	return m.PrimaryShards > 0 &&
		m.RoutingNumShards >= m.PrimaryShards &&
		m.RoutingNumShards%m.PrimaryShards == 0
}

// DefaultShardMap derives the shard map the server assigns to an index created
// with primaryShards shards and no explicit number_of_routing_shards. See
// [DefaultRoutingNumShards] for when the derivation does not hold.
func DefaultShardMap(primaryShards int) ShardMap {
	return ShardMap{
		RoutingNumShards: DefaultRoutingNumShards(primaryShards),
		PrimaryShards:    primaryShards,
	}
}

// DefaultRoutingNumShards returns the routing_num_shards the server assigns to
// a new index of numShards primary shards when number_of_routing_shards is not
// set:
//
//	numShards * 2^max(1, 10 - ceil(log2(numShards)))
//
// so 32 shards give 1024 and 5 shards give 640. It returns 0 when numShards is
// not positive, and when the result would not fit in an int.
//
// The derivation holds only for an index that was never resized. A split,
// shrink or clone keeps or rewrites routing_num_shards, so a value reported by
// the cluster, such as the one the transport caches, always takes precedence
// over this one.
//
// Server reference: MetadataCreateIndexService.calculateNumRoutingShards.
func DefaultRoutingNumShards(numShards int) int {
	if numShards <= 0 {
		return 0
	}

	// bits.Len(n-1) is ceil(log2(n)) for n >= 1.
	log2NumShards := bits.Len(uint(numShards - 1))
	numSplits := max(1, maxDefaultRoutingShardsLog2-log2NumShards)
	if numShards > math.MaxInt>>numSplits {
		return 0
	}

	return numShards << numSplits
}

// chainShardRouter asks each router in order.
type chainShardRouter []ShardRouter

// ChainShardRouter returns a [ShardRouter] that asks each router in order and
// answers a lookup with the first one that knows the index, and a node ranking
// with the first one that has nodes. Nil routers are skipped, and it returns nil
// when none remain.
//
// Order the cluster's answer first and a derived one after it, for example
// [ClientShardRouter] followed by a [StaticShardRouter] built from
// [DefaultShardMap]. The cluster's shard map must win whenever it is available,
// because a split or an explicit number_of_routing_shards makes the derivation
// wrong.
func ChainShardRouter(routers ...ShardRouter) ShardRouter {
	chain := make(chainShardRouter, 0, len(routers))
	for _, r := range routers {
		if r != nil {
			chain = append(chain, r)
		}
	}

	if len(chain) == 0 {
		return nil
	}

	return chain
}

// LookupShard implements [ShardRouter].
func (c chainShardRouter) LookupShard(index, routing string) (ShardTarget, bool) {
	for _, r := range c {
		if target, ok := r.LookupShard(index, routing); ok {
			return target, true
		}
	}

	return ShardTarget{}, false
}

// WriteNodes implements [ShardRouter].
func (c chainShardRouter) WriteNodes() []Node {
	for _, r := range c {
		if nodes := r.WriteNodes(); len(nodes) > 0 {
			return nodes
		}
	}

	return nil
}

// transportRouter is what a transport offers; [opensearchtransport.Transport]
// provides it.
type transportRouter interface {
	LookupShard(index, routing string) (opensearchtransport.ShardTarget, bool)
	WriteNodes() []*opensearchtransport.Connection
}

// transportDiagnoser is implemented by a transport that can say why it will
// never answer.
type transportDiagnoser interface {
	ShardMapUnavailable() error
}

// clientShardRouter adapts a transport to [ShardRouter].
type clientShardRouter struct {
	tr transportRouter
}

func nodeOf(conn *opensearchtransport.Connection) Node {
	return Node{Name: conn.Name, Conn: conn}
}

// LookupShard implements [ShardRouter].
func (r clientShardRouter) LookupShard(index, routing string) (ShardTarget, bool) {
	target, ok := r.tr.LookupShard(index, routing)
	if !ok {
		return ShardTarget{}, false
	}

	out := ShardTarget{Shard: target.Shard, Generation: target.Generation}
	if target.Node != nil {
		out.Node = nodeOf(target.Node)
	}

	return out, true
}

// WriteNodes implements [ShardRouter].
func (r clientShardRouter) WriteNodes() []Node {
	conns := r.tr.WriteNodes()
	nodes := make([]Node, len(conns))
	for i, conn := range conns {
		nodes[i] = nodeOf(conn)
	}

	return nodes
}

// unavailable reports why the transport can never answer, or nil.
func (r clientShardRouter) unavailable() error {
	if d, ok := r.tr.(transportDiagnoser); ok {
		return d.ShardMapUnavailable()
	}

	return nil
}

// ClientShardRouter returns the [ShardRouter] the client's transport offers, or
// nil when it offers none. [opensearchtransport.Transport] provides it from the
// shard map it keeps for shard-exact routing and the ranking it uses for write
// requests; a custom or wrapping transport that does not, yields nil.
//
// A BulkIndexer uses this router when BulkIndexerConfig.ShardRouter is nil. Pass
// it to [ChainShardRouter] to put a [StaticShardRouter] behind it.
func ClientShardRouter(client *opensearchapi.Client) ShardRouter {
	r := clientRouter(client)
	if r == nil {
		return nil
	}

	return r
}

// clientRouter is [ClientShardRouter] with its concrete type, for the indexer's
// own use.
func clientRouter(client *opensearchapi.Client) *clientShardRouter {
	if client == nil || client.Client == nil {
		return nil
	}

	tr, ok := client.Client.Transport.(transportRouter)
	if !ok {
		return nil
	}

	return &clientShardRouter{tr: tr}
}
