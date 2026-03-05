// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

// shardExactCandidates returns the connections hosting the specific shard
// determined by murmur3-hashing the routing value. Returns nil when shard-exact
// routing is disabled via features, shard map data is unavailable, the routing
// value is empty, or no connections match the shard-hosting nodes.
//
// When successful, also returns the computed shard number and the per-shard
// placement data (primary + replica node names) for per-shard cost scoring.
func shardExactCandidates( //nolint:nonamedreturns // named returns document the three result values
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
