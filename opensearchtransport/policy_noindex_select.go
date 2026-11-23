// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math/rand/v2"
)

// selectNoIndex picks the connection for a request whose path names no index,
// such as POST /_bulk, whose body may span many indices. No node is a better
// home for such a request than another, so the choice is by score alone, and
// the scores decide when traffic leaves the nearest RTT tier.
//
// Two distinct nodes are drawn at random from the nearest tier, the power of
// two choices, which spreads requests across it. One node is also drawn at
// random from the farther tiers, as a challenger. The lowest of the scores
// wins. As the near nodes get busy or coordinate more writes their scores
// rise, and a farther node wins once it scores lower. This is the crossover
// guides/routing.md "AZ-Aware Crossover" describes.
//
// conns must be sorted by RTT bucket ascending, as [sortConnectionsByRTT]
// leaves it.
//
// The draws are random rather than a counter, or the best node of a tier,
// because every input to the score but this client's in-flight count is
// cluster state that every client reads alike: a deterministic choice sends a
// fleet of idle clients to the same node at once.
//
// Each score is weighted by the node's write-coordination load against the
// cluster mean ([coordLoadFactor]), so a node coordinating more than its share
// of the cluster's writes loses more of its draws. The load is polled, so every
// client sees the same value for up to a poll interval; it only weights the
// drawn nodes, so no client sends everything to the node that looked idlest at
// the last poll. Idle clients score alike, so when the near nodes coordinate
// enough that an idle far node scores lower, they cross into the farther tier
// within a poll interval of each other.
//
// A warming node that declines its turn gives way to the next best of the
// drawn nodes; when every drawn node declines, the full candidate set is
// ranked as [connScoreSelect] ranks it.
func selectNoIndex(
	conns []*Connection,
	costs *shardCostMultiplier,
	poolName string,
	poolInfoReady bool,
	scoreFunc connScoreFunc,
	cluster *clusterCoordLoad,
) *Connection {
	if len(conns) == 0 {
		return nil
	}

	nearEnd := nearestTierEnd(conns)
	a, b := drawNoIndexPair(conns[:nearEnd])
	challenger := drawOne(conns[nearEnd:])

	var drawn [3]*Connection
	var scores [3]float64
	n := 0
	for _, c := range [3]*Connection{a, b, challenger} {
		if c == nil {
			continue
		}
		drawn[n] = c
		scores[n] = noIndexScore(c, poolName, poolInfoReady, scoreFunc) * coordLoadFactor(c, cluster)
		n++
	}

	// Try the drawn nodes best first. A stable insertion sort keeps equal
	// scores in the order they were drawn, which is random.
	for i := 1; i < n; i++ {
		for j := i; j > 0 && scores[j] < scores[j-1]; j-- {
			scores[j], scores[j-1] = scores[j-1], scores[j]
			drawn[j], drawn[j-1] = drawn[j-1], drawn[j]
		}
	}
	for _, c := range drawn[:n] {
		if !c.loadConnState().isWarmingUp() {
			return c
		}
		switch c.tryWarmupSkip() {
		case warmupAccepted, warmupInactive:
			return c
		case warmupSkipped:
		}
	}

	scoresBuf := acquireFloats(len(conns))
	best := connScoreSelect(conns, nil, nil, costs, poolName, poolInfoReady, scoresBuf.Slice(), scoreFunc, nil, 0)
	scoresBuf.Release()

	return best
}

// nearestTierEnd returns the length of the prefix of conns, sorted by RTT
// bucket ascending, that shares the first connection's bucket.
func nearestTierEnd(conns []*Connection) int {
	tier := conns[0].rttRing.medianBucket()
	for i := 1; i < len(conns); i++ {
		if conns[i].rttRing.medianBucket() != tier {
			return i
		}
	}
	return len(conns)
}

// drawOne returns a random node of conns, or nil when conns is empty.
func drawOne(conns []*Connection) *Connection {
	if len(conns) == 0 {
		return nil
	}
	return conns[rand.IntN(len(conns))] //nolint:gosec // load spreading; cryptographic randomness not needed
}

// drawNoIndexPair draws two distinct nodes of tier at random, in random order,
// so that the first wins a tie. The second is nil when tier has one node, and
// both are nil when it has none.
func drawNoIndexPair(tier []*Connection) (*Connection, *Connection) {
	switch len(tier) {
	case 0:
		return nil, nil
	case 1:
		return tier[0], nil
	}
	//nolint:gosec // load spreading; cryptographic randomness not needed
	i := rand.IntN(len(tier))
	//nolint:gosec // load spreading; cryptographic randomness not needed
	j := rand.IntN(len(tier) - 1)
	if j >= i {
		j++
	}
	return tier[i], tier[j]
}

// noIndexScore is the score [connScoreSelect] gives conn with no shard data,
// where every node costs costUnknown.
func noIndexScore(conn *Connection, poolName string, poolInfoReady bool, scoreFunc connScoreFunc) float64 {
	if scoreFunc != nil {
		return scoreFunc(conn, costUnknown, 0, poolName, poolInfoReady)
	}
	return calcConnDefaultScore(conn, costUnknown, poolName, poolInfoReady)
}
