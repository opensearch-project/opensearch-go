// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"cmp"
	"slices"
	"strings"
	"sync"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// noRoutingKeyErr is the miss for a request that carries no routing key at all:
// [ErrNoRoutingKey], or nil when shard-exact routing is disabled, which is not a
// miss.
func noRoutingKeyErr(features routingFeatures) error {
	if !features.shardExactEnabled() {
		return nil
	}

	return ErrNoRoutingKey
}

// calcSingleKeyCost resolves a single routing key to the shard it targets and
// returns the connections hosting that shard. The 1:1 relationship between key
// and shard makes this a direct lookup with no intermediate data structures.
//
// routingValue is the request's routing value, or "" for none; docID is the
// document ID, or "" when the request has none. The server routes by the ID
// when there is no routing value, and a partitioned index adds an offset
// derived from the ID, so on such an index a request without an ID has no
// known shard and the result is -1.
//
// The error is nil exactly when shard-exact candidates are returned. Otherwise
// the pooledConns is zero-valued, the shard number is -1, and the error is
// [ErrNoRoutingKey] when there is neither a routing value nor an ID,
// [ErrShardMapUnknown] when no shard map data is available,
// [ErrDocumentIDRequired] on a partitioned index without an ID, or
// [ErrShardNoNode] when no connection hosts the shard. Shard-exact routing
// disabled via features returns no candidates and a nil error, since that is not
// a miss. Callers must call Release on the returned pooledConns when done.
//
// Per-shard cost is derived from the returned [shardNodes] by the caller via
// [shardCostMultiplier.forShard].
func calcSingleKeyCost(
	features routingFeatures,
	slot *indexSlot,
	routingValue string,
	docID string,
	conns []*Connection,
) (pooledConns, int, *shardNodes, error) {
	if !features.shardExactEnabled() {
		return pooledConns{}, -1, nil, nil
	}

	if routingValue == "" && docID == "" {
		return pooledConns{}, -1, nil, ErrNoRoutingKey
	}

	sm := slot.shardMap.Load()
	if sm == nil || sm.NumberOfPrimaryShards == 0 || sm.RoutingNumShards == 0 || len(sm.Shards) == 0 {
		return pooledConns{}, -1, nil, ErrShardMapUnknown
	}

	shardNum, ok := sm.shardForDocument(routingValue, docID)
	if !ok {
		return pooledConns{}, -1, nil, ErrDocumentIDRequired
	}

	shardCopy := sm.Shards[shardNum]
	if shardCopy == nil {
		return pooledConns{}, -1, nil, ErrShardNoNode
	}

	// Build a set of node names hosting this shard.
	nodeNames := acquireNodeSet()
	if shardCopy.Primary != "" {
		nodeNames.Add(shardCopy.Primary)
	}
	for _, r := range shardCopy.Replicas {
		nodeNames.Add(r)
	}

	if nodeNames.Len() == 0 {
		nodeNames.Release()
		return pooledConns{}, -1, nil, ErrShardNoNode
	}

	// Resolve node names to connections. Filter out connections that
	// need a /_cat/shards refresh (stale shard data).
	bp := getConnSlice(nodeNames.Len())
	for _, c := range conns {
		if c.needsCatUpdate() {
			continue
		}
		if nodeNames.Contains(c.Name) {
			*bp = append(*bp, c)
		}
	}
	nodeNames.Release()

	if len(*bp) == 0 {
		putConnSlice(bp)
		return pooledConns{}, -1, nil, ErrShardNoNode
	}

	return pooledConns{p: bp}, shardNum, shardCopy, nil
}

// needsShardSetCost reports whether a request is costed by [calcMultiKeyCost]
// rather than [calcSingleKeyCost]: a comma-separated routing list, or a single
// routing value without a document ID on a partitioned index, which targets
// several shards (see [shardhash.ForDocument]).
func needsShardSetCost(slot *indexSlot, routingValue, docID string) bool {
	if strings.Contains(routingValue, routingValueSeparator) {
		return true
	}

	sm := slot.shardMap.Load()

	return routingValue != "" && docID == "" && sm != nil && sm.RoutingPartitionSize > 1
}

// calcMultiKeyCost resolves routing values, either a comma-separated list or
// one value on a partitioned index, to the set of shards they target and
// computes a per-node extra cost based on how many target shards each node is
// missing. When the target shards are spread across the cluster, the ideal
// coordinator is the node hosting the most of them - it can serve those locally
// and only proxies the rest.
//
// The target set is the union over all keys, as the server builds it: a shard
// reached by several keys or partition offsets counts once. On a partitioned
// index each key targets one shard per partition offset (see
// [shardhash.ForRoutingOffset]).
//
// Returns candidates sorted by hit-count descending and a parallel extraCost
// slice where extraCost[i] = totalShards - hitsForNode[i], with totalShards the
// number of distinct target shards. A node hosting all target shards has
// extraCost=0; a node hosting one of five has extraCost=4.
//
// The error is nil exactly when candidates are returned. Otherwise the result is
// empty and the error is [ErrNoRoutingKey] when there is no routing value,
// [ErrShardMapUnknown] when no shard map data is available, or [ErrShardNoNode]
// when no connection hosts any target shard. Shard-exact routing disabled via
// features returns no candidates and a nil error, since that is not a miss.
func calcMultiKeyCost(
	features routingFeatures,
	slot *indexSlot,
	routingValue string,
	conns []*Connection,
) (pooledConns, pooledFloats, error) {
	if !features.shardExactEnabled() {
		return pooledConns{}, pooledFloats{}, nil
	}
	if routingValue == "" {
		return pooledConns{}, pooledFloats{}, ErrNoRoutingKey
	}

	sm := slot.shardMap.Load()
	if sm == nil || sm.NumberOfPrimaryShards == 0 || sm.RoutingNumShards == 0 || len(sm.Shards) == 0 {
		return pooledConns{}, pooledFloats{}, ErrShardMapUnknown
	}

	rvBuf := acquireRoutingValues()
	routingValues := splitRoutingValues(routingValue, rvBuf)
	offsets := max(sm.RoutingPartitionSize, 1)
	var targetsBuf [16]int // spills to the heap only for a large target set
	targets := targetsBuf[:0]

	hits := acquireNodeHits()
	for _, rv := range routingValues {
		for i := range offsets {
			shardNum := shardhash.ForRoutingOffset(rv, i, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
			if slices.Contains(targets, shardNum) {
				continue
			}
			targets = append(targets, shardNum)

			shard := sm.Shards[shardNum]
			if shard == nil {
				continue
			}
			if shard.Primary != "" {
				(*hits)[shard.Primary]++
			}
			for _, r := range shard.Replicas {
				(*hits)[r]++
			}
		}
	}
	totalShards := len(targets)
	releaseRoutingValues(rvBuf)

	if len(*hits) == 0 {
		releaseNodeHits(hits)
		return pooledConns{}, pooledFloats{}, ErrShardNoNode
	}

	scored := acquireScoredConns()
	for _, c := range conns {
		if c.needsCatUpdate() {
			continue
		}
		if h, ok := (*hits)[c.Name]; ok {
			*scored = append(*scored, scoredConn{conn: c, hits: h})
		}
	}
	releaseNodeHits(hits)

	if len(*scored) == 0 {
		releaseScoredConns(scored)
		return pooledConns{}, pooledFloats{}, ErrShardNoNode
	}

	slices.SortFunc(*scored, func(a, b scoredConn) int {
		return cmp.Compare(b.hits, a.hits)
	})

	n := len(*scored)
	result := acquireConns(n)
	extraCost := acquireFloats(n)
	for i, s := range *scored {
		result.Slice()[i] = s.conn
		extraCost.Slice()[i] = float64(totalShards - s.hits)
	}
	releaseScoredConns(scored)

	return result, extraCost, nil
}

type scoredConn struct {
	conn *Connection
	hits int
}

//nolint:gochecknoglobals // sync.Pool must be package-level
var nodeHitsPool = sync.Pool{
	New: func() any {
		m := make(map[string]int, 16)
		return &m
	},
}

func acquireNodeHits() *map[string]int {
	return nodeHitsPool.Get().(*map[string]int) //nolint:forcetypeassert // pool only stores *map[string]int
}

func releaseNodeHits(m *map[string]int) {
	clear(*m)
	nodeHitsPool.Put(m)
}

//nolint:gochecknoglobals // sync.Pool must be package-level
var scoredConnsPool = sync.Pool{
	New: func() any {
		s := make([]scoredConn, 0, 16)
		return &s
	},
}

func acquireScoredConns() *[]scoredConn {
	return scoredConnsPool.Get().(*[]scoredConn) //nolint:forcetypeassert // pool only stores *[]scoredConn
}

func releaseScoredConns(s *[]scoredConn) {
	clear((*s)[:cap(*s)])
	*s = (*s)[:0]
	scoredConnsPool.Put(s)
}
