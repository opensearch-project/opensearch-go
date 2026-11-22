// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"math/rand/v2"
)

// selectNoIndex picks the connection for a request whose path names no index,
// such as POST /_bulk, whose body may span many indices. No node is a better
// home for such a request than another, so the choice is by score alone, and
// the scores decide when traffic leaves the nearest RTT tier.
//
// Two distinct nodes are drawn at random from the nearest tier, the power of
// two choices, which spreads requests across it. One node is also drawn at
// random from the farther tiers, as a challenger, and wins when it scores
// below the better near node. An idle near node scores below any idle farther
// node, since its RTT bucket is lower, so at idle every request stays near; as
// the near nodes get busy their scores rise, and a farther node wins once it
// scores lower. This is the crossover guides/transport-routing.md "AZ-Aware
// Crossover" describes.
//
// conns must be sorted by RTT bucket ascending, as [sortConnectionsByRTT]
// leaves it.
//
// The draws are random rather than a counter, or the best node of a tier,
// because every input to the score but this client's in-flight count is
// cluster state that every client reads alike: a deterministic choice sends a
// fleet of idle clients to the same node at once.
//
// The two near nodes are compared with each score weighted by the node's
// write-coordination load against the cluster mean ([coordLoadFactor]), so a
// node coordinating more than its share of the cluster's writes loses more of
// its draws. The challenger is compared without the weight. The load is
// polled, so every client sees the same value for up to a poll interval. It
// only picks between the two drawn near nodes, so no client sends everything to
// the node that looked idlest at the last poll, and it never moves every idle
// client into a farther tier at once.
//
// A request that is being retried skips the nodes it tried in this pass, and a
// warming node that declines its turn gives way to the next best of the drawn
// nodes; when every drawn node declines, or none can be drawn, the full
// candidate set is ranked as [connScoreSelect] ranks it.
func selectNoIndex(
	conns []*Connection,
	costs *shardCostMultiplier,
	poolName string,
	poolInfoReady bool,
	scoreFunc connScoreFunc,
	cluster *clusterCoordLoad,
	att *routeAttempt,
) *Connection {
	if len(conns) == 0 {
		return nil
	}

	nearEnd := nearestTierEnd(conns)
	a, b := drawNoIndexPair(conns[:nearEnd], att)
	challenger := drawUntried(conns[nearEnd:], att)

	score := func(c *Connection) float64 { return noIndexScore(c, poolName, poolInfoReady, scoreFunc, att) }
	ranked := func() *Connection {
		return selectNoIndexRanked(conns, costs, poolName, poolInfoReady, scoreFunc, att)
	}

	// Order the drawn nodes best first. The near pair goes in weighted-score
	// order, and in draw order, which is random, on a tie. The challenger goes
	// ahead of every near node whose unweighted score is higher than its own.
	var drawn [3]*Connection
	var scores [3]float64 // unweighted
	n := 0
	if a != nil {
		sa := score(a)
		drawn[0], scores[0] = a, sa
		n = 1
		if b != nil {
			sb := score(b)
			drawn[1], scores[1] = b, sb
			n = 2
			if sb*coordLoadFactor(b, cluster) < sa*coordLoadFactor(a, cluster) {
				drawn[0], drawn[1] = drawn[1], drawn[0]
				scores[0], scores[1] = scores[1], scores[0]
			}
		}
	}
	if challenger != nil {
		sc := score(challenger)
		i := n
		for i > 0 && sc < scores[i-1] {
			drawn[i], scores[i] = drawn[i-1], scores[i-1]
			i--
		}
		drawn[i], scores[i] = challenger, sc
		n++
	}
	if n == 0 {
		return ranked()
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

	return ranked()
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

// untried reports whether the request has not tried c in this pass.
func untried(att *routeAttempt, c *Connection) bool {
	_, inPass := att.lookup(c)
	return !inPass
}

// scanUntried returns the first node of conns, from a random start, that is
// not skip and that the request has not tried in this pass, or nil when there
// is none.
func scanUntried(conns []*Connection, att *routeAttempt, skip *Connection) *Connection {
	start := rand.IntN(len(conns)) //nolint:gosec // load spreading; cryptographic randomness not needed
	for i := range conns {
		if c := conns[(start+i)%len(conns)]; c != skip && untried(att, c) {
			return c
		}
	}
	return nil
}

// drawUntried returns a random node of conns that the request has not tried in
// this pass, or nil when there is none. It draws a bounded number of times,
// then scans, so a mostly-tried set still finds its untried node.
func drawUntried(conns []*Connection, att *routeAttempt) *Connection {
	if len(conns) == 0 {
		return nil
	}
	for range 2 {
		c := conns[rand.IntN(len(conns))] //nolint:gosec // load spreading; cryptographic randomness not needed
		if untried(att, c) {
			return c
		}
	}
	return scanUntried(conns, att, nil)
}

// drawNoIndexPair draws up to two distinct nodes of tier that the request has
// not tried in this pass, in random order, so that the first wins a tie. It
// returns nil for a node it cannot find: both nil when every node of tier was
// tried, and the second nil when only one was not.
//
// With exactly two nodes in tier the pair is fixed, so it takes them and draws
// once, only to order them. Otherwise it draws at random a bounded number of
// times, since att may have tried some of the nodes, and then falls back to
// [drawUntried] for a node it did not find.
func drawNoIndexPair(tier []*Connection, att *routeAttempt) (*Connection, *Connection) {
	switch len(tier) {
	case 0:
		return nil, nil
	case 1:
		if untried(att, tier[0]) {
			return tier[0], nil
		}
		return nil, nil
	case 2:
		a, b := tier[0], tier[1]
		if rand.IntN(2) == 0 { //nolint:gosec // load spreading; cryptographic randomness not needed
			a, b = b, a
		}
		switch {
		case untried(att, a) && untried(att, b):
			return a, b
		case untried(att, a):
			return a, nil
		case untried(att, b):
			return b, nil
		}
		return nil, nil
	}

	var a *Connection
	for range 2 * len(tier) {
		c := tier[rand.IntN(len(tier))] //nolint:gosec // load spreading; cryptographic randomness not needed
		switch {
		case !untried(att, c):
		case a == nil:
			a = c
		case c != a:
			return a, c
		}
	}
	if a == nil {
		return drawUntried(tier, att), nil
	}
	// One untried node found; look for a second other than it.
	return a, scanUntried(tier, att, a)
}

// noIndexScore is the score [connScoreSelect] gives conn with no shard data,
// where every node costs costUnknown.
func noIndexScore(
	conn *Connection,
	poolName string,
	poolInfoReady bool,
	scoreFunc connScoreFunc,
	att *routeAttempt,
) float64 {
	var score float64
	if scoreFunc != nil {
		score = scoreFunc(conn, costUnknown, 0, poolName, poolInfoReady)
	} else {
		score = calcConnDefaultScore(conn, costUnknown, poolName, poolInfoReady)
	}
	if tries, inPass := att.lookup(conn); inPass {
		return math.Inf(1)
	} else if tries > 0 {
		score *= float64(1 + tries)
	}

	return score
}

// selectNoIndexRanked is the fallback of [selectNoIndex]: [connScoreSelect]
// over every candidate.
func selectNoIndexRanked(
	conns []*Connection,
	costs *shardCostMultiplier,
	poolName string,
	poolInfoReady bool,
	scoreFunc connScoreFunc,
	att *routeAttempt,
) *Connection {
	scores := acquireFloats(len(conns))
	best := connScoreSelect(conns, nil, nil, costs, poolName, poolInfoReady, scores.Slice(), scoreFunc, nil, 0, att)
	scores.Release()

	return best
}
