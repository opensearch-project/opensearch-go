// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "time"

// AffinityRouteEvent is a point-in-time snapshot of an affinity routing
// decision captured at the moment a request is routed to a node. All fields
// are safe to retain after the callback returns.
type AffinityRouteEvent struct {
	// IndexName is the target index extracted from the request path.
	IndexName string

	// Key is the full routing key used for rendezvous hashing.
	// For index affinity this is the same as IndexName.
	// For document affinity this is "{index}/{docID}".
	Key string

	// FanOut is the effective fan-out (K) used for this routing decision.
	FanOut int

	// TotalNodes is the number of active connections available for selection.
	TotalNodes int

	// CandidateCount is the number of nodes returned by rendezvous hashing
	// (may be less than FanOut if fewer nodes are available).
	CandidateCount int

	// Selected holds the score breakdown for the node that won.
	Selected AffinityCandidate

	// Candidates holds the score breakdown for all K candidates, including
	// the selected node. Ordered by rendezvous hash rank (not by score).
	Candidates []AffinityCandidate

	// Timestamp is when the routing decision was made.
	Timestamp time.Time
}

// AffinityCandidate holds the score breakdown for one node evaluated
// during affinity routing.
type AffinityCandidate struct {
	// URL is the connection's address.
	URL string

	// ID is the node's unique identifier.
	ID string

	// Name is the node's human-readable name.
	Name string

	// RTTBucket is the median RTT bucket for this node.
	// -1 indicates no RTT data is available.
	RTTBucket int64

	// DecayCounter is the current value of the CPU-time accumulator.
	// Reflects the exponentially decaying sum of estimated server-side
	// processing time per processor deposited by this client.
	DecayCounter float64

	// ShardCostMultiplier is the shard-role-based score multiplier.
	// Determined by the shard cost table for the request direction (read vs write)
	// and the node's shard composition for the target index.
	ShardCostMultiplier float64

	// WarmupPenalty is the warmup penalty multiplier for observability.
	// 1.0 for fully-warmed connections, up to warmupPenaltyMax at the
	// start of warmup. Reported for diagnostics only — not included in
	// the selection Score (warmup is handled by skip/accept gating).
	WarmupPenalty float64

	// Score is the composite affinity score:
	//   RTTBucket * max(DecayCounter,1.0) * ShardCostMultiplier.
	// Lower score = preferred node. Warmup state is handled separately by
	// [affinitySelect] via skip/accept gating, not included in the score.
	Score float64
}

// newAffinityCandidate creates an AffinityCandidate snapshot from a connection
// and its shard info using the given cost table.
func newAffinityCandidate(conn *Connection, node *shardNodeInfo, costs *shardCostMultiplier) AffinityCandidate {
	bucket := conn.rttRing.medianBucket()
	counter := conn.affinityCounter.load()
	scm := costs.forNode(node)

	// Compute score using the same floor as affinityScore.
	flooredCounter := counter
	if flooredCounter < affinityCounterFloor {
		flooredCounter = affinityCounterFloor
	}

	// Compute warmup penalty to match affinityScore exactly.
	wp := 1.0
	cs := conn.loadConnState()
	if cs.isWarmingUp() {
		wp = warmupPenalty(cs)
	}

	// Expose -1 for unknown RTT in the candidate, but use the raw
	// rttBucketUnknown value for the score calculation (matches affinityScore).
	displayBucket := bucket.Int64()
	if bucket.IsUnknown() {
		displayBucket = -1
	}

	return AffinityCandidate{
		URL:                 conn.URLString,
		ID:                  conn.ID,
		Name:                conn.Name,
		RTTBucket:           displayBucket,
		DecayCounter:        counter,
		ShardCostMultiplier: scm,
		WarmupPenalty:       wp,
		Score:               float64(bucket) * flooredCounter * scm,
	}
}

// buildAffinityRouteEvent constructs an AffinityRouteEvent from a completed
// routing decision. Called from the three Eval() sites after the best
// candidate is selected.
func buildAffinityRouteEvent(
	indexName, key string,
	fanOut, totalNodes int,
	candidates []*Connection,
	best *Connection,
	slot *indexSlot,
	costs *shardCostMultiplier,
) AffinityRouteEvent {
	cs := make([]AffinityCandidate, len(candidates))
	var selected AffinityCandidate
	for i, c := range candidates {
		cs[i] = newAffinityCandidate(c, slot.shardNodeInfoFor(c.Name), costs)
		if c == best {
			selected = cs[i]
		}
	}

	return AffinityRouteEvent{
		IndexName:      indexName,
		Key:            key,
		FanOut:         fanOut,
		TotalNodes:     totalNodes,
		CandidateCount: len(candidates),
		Selected:       selected,
		Candidates:     cs,
		Timestamp:      time.Now().UTC(),
	}
}

// ShardMapInvalidationEvent is emitted when a routing failure flags a
// connection's shard placement as stale. The connection is excluded from
// affinity routing candidate sets until a /_cat/shards refresh completes.
type ShardMapInvalidationEvent struct {
	// ConnURL is the URL of the connection that was flagged.
	ConnURL string

	// ConnName is the node name of the flagged connection.
	ConnName string

	// Reason describes what triggered the invalidation.
	Reason string // "transport_error" or "http_status_retry"

	// Timestamp is when the invalidation occurred.
	Timestamp time.Time
}
