// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"slices"
	"sync"
)

// calcSingleKeyCost resolves a single routing key to the shard it targets and
// returns the connections hosting that shard. The 1:1 relationship between key
// and shard makes this a direct lookup with no intermediate data structures.
//
// Returns nil when shard-exact routing is disabled via features, shard map data
// is unavailable, the routing value is empty, or no connections match.
// Per-shard cost is derived from the returned [shardNodes] by the caller via
// [shardCostMultiplier.forShard].
func calcSingleKeyCost( //nolint:nonamedreturns // named returns document the three result values
	features routingFeatures,
	slot *indexSlot,
	routingValue string,
	conns []*Connection,
) (candidates []*Connection, shardNum int, shard *shardNodes) {
	if !features.shardExactEnabled() {
		return nil, -1, nil
	}

	if routingValue == "" {
		return nil, -1, nil
	}

	sm := slot.shardMap.Load()
	if sm == nil || sm.NumberOfPrimaryShards == 0 || sm.RoutingNumShards == 0 || len(sm.Shards) == 0 {
		return nil, -1, nil
	}

	shardNum = shardForRouting(routingValue, sm.RoutingNumShards, sm.NumberOfPrimaryShards)

	shardCopy := sm.Shards[shardNum]
	if shardCopy == nil {
		return nil, -1, nil
	}

	// Build a set of node names hosting this shard.
	nodeNames := make(map[string]struct{}, 1+len(shardCopy.Replicas))
	if shardCopy.Primary != "" {
		nodeNames[shardCopy.Primary] = struct{}{}
	}
	for _, r := range shardCopy.Replicas {
		nodeNames[r] = struct{}{}
	}

	if len(nodeNames) == 0 {
		return nil, -1, nil
	}

	// Resolve node names to connections. Filter out connections that
	// need a /_cat/shards refresh (stale shard data).
	for _, c := range conns {
		if c.needsCatUpdate() {
			continue
		}
		if _, ok := nodeNames[c.Name]; ok {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return nil, -1, nil
	}

	return candidates, shardNum, shardCopy
}

// calcMultiKeyCost resolves multiple comma-separated routing keys to their
// target shards and computes a per-node extra cost based on how many target
// shards each node is missing. When multiple keys target different shards
// spread across the cluster, the ideal coordinator is the node hosting the
// most target shards — it can serve those locally and only proxies the rest.
//
// Returns candidates sorted by hit-count descending and a parallel extraCost
// slice where extraCost[i] = totalKeys - hitsForNode[i]. A node hosting all
// target shards has extraCost=0; a node hosting one of five has extraCost=4.
//
// Returns nil, nil when shard-exact routing is disabled, shard data is
// unavailable, or no connections match.
func calcMultiKeyCost(
	features routingFeatures,
	slot *indexSlot,
	routingValue string,
	conns []*Connection,
) (pooledConns, pooledFloats) {
	if !features.shardExactEnabled() || routingValue == "" {
		return pooledConns{}, pooledFloats{}
	}

	sm := slot.shardMap.Load()
	if sm == nil || sm.NumberOfPrimaryShards == 0 || sm.RoutingNumShards == 0 || len(sm.Shards) == 0 {
		return pooledConns{}, pooledFloats{}
	}

	rvBuf := acquireRoutingValues()
	routingValues := splitRoutingValues(routingValue, rvBuf)
	totalKeys := len(routingValues)

	hits := acquireNodeHits()
	for _, rv := range routingValues {
		shardNum := shardForRouting(rv, sm.RoutingNumShards, sm.NumberOfPrimaryShards)
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
	releaseRoutingValues(rvBuf)

	if len(*hits) == 0 {
		releaseNodeHits(hits)
		return pooledConns{}, pooledFloats{}
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
		return pooledConns{}, pooledFloats{}
	}

	slices.SortFunc(*scored, func(a, b scoredConn) int {
		return b.hits - a.hits
	})

	n := len(*scored)
	result := acquireConns(n)
	extraCost := acquireFloats(n)
	for i, s := range *scored {
		result.Slice()[i] = s.conn
		extraCost.Slice()[i] = float64(totalKeys - s.hits)
	}
	releaseScoredConns(scored)

	return result, extraCost
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
	*s = (*s)[:0]
	scoredConnsPool.Put(s)
}
