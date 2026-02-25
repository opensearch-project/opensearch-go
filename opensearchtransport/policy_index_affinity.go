// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// affinityCounterFloor is the minimum value used for the decay counter
	// in score calculations. Prevents division-by-zero-like effects when a
	// node has received no recent traffic.
	affinityCounterFloor = 1.0

	// Shard cost multipliers used in the [shardCostMultiplier] tables and
	// [warmupPenaltyMax]. Lower value = preferred node.
	costPreferred    = 1.0  // best-case: node hosts the ideal shard type
	costAlternate    = 2.0  // acceptable: node can serve but may proxy
	costRelocating   = 8.0  // shard moving, may require proxy hop
	costInitializing = 16.0 // shard not yet ready to serve
	costUnknown      = 32.0 // no shard data, heavily penalized
)

// shardCostIndex identifies a shard state position in a [shardCostMultiplier].
type shardCostIndex int

const (
	// shardCostUnknown is the zero value: no shard data available.
	// A zero-initialized [shardCostMultiplier] produces 0.0 for unknown,
	// so tables must be explicitly constructed.
	shardCostUnknown shardCostIndex = iota

	// shardCostReplica: node hosts only replica shards for this index.
	shardCostReplica

	// shardCostPrimary: node hosts only primary shards for this index.
	shardCostPrimary

	// shardCostInitializing: node has initializing shards (reserved for
	// future use; discovery currently filters to STARTED shards only).
	shardCostInitializing

	// shardCostRelocating: node has relocating shards (reserved for
	// future use; discovery currently filters to STARTED shards only).
	shardCostRelocating
)

// shardCostMultiplier holds per-shard-state score multipliers used in
// [affinityScore]. The appropriate table is selected at policy construction
// time based on whether the route handles reads or writes.
//
// Lower multiplier = preferred node. Index via [shardCostIndex] constants.
type shardCostMultiplier [5]float64

// shardCostForReads prefers replica-hosting nodes. Replicas serve reads
// from a lock-free Lucene snapshot that doesn't contend with writes.
//
//nolint:gochecknoglobals // Package-level constant table used by affinityScore.
var shardCostForReads = shardCostMultiplier{
	shardCostUnknown:      costUnknown,      // no data, heavily penalized
	shardCostReplica:      costPreferred,    // preferred for reads
	shardCostPrimary:      costAlternate,    // primaries contend with writes
	shardCostInitializing: costInitializing, // shard not yet ready
	shardCostRelocating:   costRelocating,   // shard moving, may proxy
}

// shardCostForWrites prefers primary-hosting nodes. Writes always go to
// the primary shard first; routing to a replica-only node forces a
// coordinator proxy hop.
//
//nolint:gochecknoglobals // Package-level constant table used by affinityScore.
var shardCostForWrites = shardCostMultiplier{
	shardCostUnknown:      costUnknown,      // no data, heavily penalized
	shardCostReplica:      costAlternate,    // replica must proxy to primary
	shardCostPrimary:      costPreferred,    // preferred -- write lands directly
	shardCostInitializing: costInitializing, // shard not yet ready
	shardCostRelocating:   costRelocating,   // shard moving, may proxy
}

// forNode returns the shard cost multiplier for a node based on its shard
// composition for the target index.
//
// The lookup is categorical: if the node hosts the preferred shard type
// (as encoded by the table), it gets the preferred cost. Mixed nodes that
// host both primaries and replicas get the best (lowest) cost since they
// can serve both reads and writes locally. Load-based differentiation
// between nodes is handled by the CPU-time decay counter, not by this
// multiplier.
func (m *shardCostMultiplier) forNode(node *shardNodeInfo) float64 {
	if node == nil {
		return m[shardCostUnknown]
	}
	total := node.Primaries + node.Replicas
	if total == 0 {
		return m[shardCostUnknown]
	}
	if node.Primaries == 0 {
		return m[shardCostReplica]
	}
	if node.Replicas == 0 {
		return m[shardCostPrimary]
	}
	// Mixed node: hosts both primaries and replicas. It can serve reads
	// from replicas and writes to primaries locally. Use the better cost;
	// the CPU counter differentiates actual load between mixed nodes.
	return min(m[shardCostReplica], m[shardCostPrimary])
}

// Compile-time interface compliance checks.
var (
	_ Policy             = (*IndexAffinityPolicy)(nil)
	_ policyConfigurable = (*IndexAffinityPolicy)(nil)
	_ policyTyped        = (*IndexAffinityPolicy)(nil)
	_ policyOverrider    = (*IndexAffinityPolicy)(nil)
)

// IndexAffinityPolicy routes requests to a consistent subset of nodes based
// on the target index name. It uses rendezvous hashing for stable node
// selection and RTT-based scoring for AZ-aware load distribution.
//
// When a request targets an index (e.g., GET /my-index/_search), the policy:
//  1. Extracts the index name from the URL path
//  2. Looks up (or creates) a routing slot in the index cache
//  3. Selects top-K nodes via rendezvous hash (K = fan-out for that index)
//  4. Picks the best candidate by affinity score (RTT * decay counter)
//  5. Returns an affinityPool wrapping the selected node
//
// For system endpoints (no index in path), the policy returns (nil, nil)
// to let the next policy in the chain handle the request.
type IndexAffinityPolicy struct {
	cache      *indexSlotCache
	shardCosts *shardCostMultiplier // shard cost table (nil = shardCostForReads)
	jitter     atomic.Int64         // counter-based rotation within K-slot set
	decay      float64              // decay factor for affinity counter
	observer   atomic.Pointer[ConnectionObserver]

	mu struct {
		sync.RWMutex
		activeConns []*Connection // RTT-sorted active connections
	}

	policyState atomic.Int32 // Bitfield: psEnabled|psDisabled|psEnvEnabled|psEnvDisabled
	config      policyConfig
}

func (p *IndexAffinityPolicy) policyTypeName() string      { return "index_affinity" }
func (p *IndexAffinityPolicy) setEnvOverride(enabled bool) { psSetEnvOverride(&p.policyState, enabled) }

// NewIndexAffinityPolicy creates an index affinity routing policy.
func NewIndexAffinityPolicy(cacheCfg indexSlotCacheConfig) *IndexAffinityPolicy {
	if cacheCfg.decayFactor <= 0 || cacheCfg.decayFactor >= 1 {
		cacheCfg.decayFactor = defaultDecayFactor
	}
	return &IndexAffinityPolicy{
		cache:      newIndexSlotCache(cacheCfg),
		shardCosts: &shardCostForReads,
		decay:      cacheCfg.decayFactor,
	}
}

// configurePolicySettings implements policyConfigurable.
func (p *IndexAffinityPolicy) configurePolicySettings(config policyConfig) error {
	p.config = config
	if config.observer != nil {
		p.observer.Store(config.observer)
	}
	return nil
}

// IsEnabled returns true if the policy has active connections.
func (p *IndexAffinityPolicy) IsEnabled() bool {
	return psIsEnabled(p.policyState.Load())
}

// Eval extracts the index name from the request path and routes to
// a consistent node subset. Returns (nil, nil) for non-index requests.
func (p *IndexAffinityPolicy) Eval(_ context.Context, req *http.Request) (ConnectionPool, error) {
	if p.policyState.Load()&psEnvDisabled != 0 {
		//nolint:nilnil // Intentional: force-disabled policy returns no match
		return nil, nil
	}

	indexName := extractIndexFromPath(req.URL.Path)
	if indexName == "" {
		//nolint:nilnil // Intentional: no index in path, fall through to next policy
		return nil, nil
	}

	p.mu.RLock()
	conns := p.mu.activeConns
	p.mu.RUnlock()

	if len(conns) == 0 {
		//nolint:nilnil // No connections available
		return nil, nil
	}

	slot := p.cache.getOrCreate(indexName)
	fanOut := p.cache.effectiveFanOut(slot, indexName, len(conns))
	shardNodes := slot.shardNodeNameSet()

	// Select top-K via rendezvous hash with jitter rotation.
	bp := getConnSlice(fanOut)
	candidates := rendezvousTopK(indexName, "", conns, fanOut, &p.jitter, shardNodes, bp)
	if len(candidates) == 0 {
		putConnSlice(bp)
		//nolint:nilnil // No candidates
		return nil, nil
	}

	// Update tier-span equalization: find the max RTT bucket across the
	// candidate set and feed it to the slot's MIAD tracker. This drives
	// cost inflation in recordCPUTime so traffic distributes evenly
	// across all RTT tiers in the fan-out set.
	var maxBucket rttBucket
	for _, c := range candidates {
		if b := c.rttRing.medianBucket(); b > maxBucket {
			maxBucket = b
		}
	}
	slot.updateSmoothedMaxBucket(float64(maxBucket))

	// Select best candidate with warmup-aware skip/accept.
	var scoresBuf [8]float64
	scores := scoresBuf[:len(candidates)]
	if len(candidates) > len(scoresBuf) {
		scores = make([]float64, len(candidates))
	}
	best := affinitySelect(candidates, slot, p.shardCosts, scores)

	if obs := observerFromAtomic(&p.observer); obs != nil {
		obs.OnAffinityRoute(buildAffinityRouteEvent(
			indexName, indexName, fanOut, len(conns), candidates, best, slot, p.shardCosts,
		))
	}

	putConnSlice(bp)

	return getAffinityPool(best), nil
}

// CheckDead is a no-op for affinity policies. Lifecycle management is
// handled by the underlying pool that owns the connections.
func (p *IndexAffinityPolicy) CheckDead(_ context.Context, _ HealthCheckFunc) error {
	return nil
}

// RotateStandby is a no-op. Lifecycle is managed by the underlying pool.
func (p *IndexAffinityPolicy) RotateStandby(_ context.Context, _ int) (int, error) {
	return 0, nil
}

// affinitySnapshot implements affinitySnapshotProvider.
func (p *IndexAffinityPolicy) affinitySnapshot() AffinitySnapshot {
	return p.cache.snapshot()
}

// affinityCache implements [affinityCacheProvider].
func (p *IndexAffinityPolicy) affinityCache() *indexSlotCache {
	return p.cache
}

// affinityDiscoveryUpdate is the shared implementation for DiscoveryUpdate on
// affinity policies. It deduplicates connections, applies additions/removals,
// sorts by RTT, and updates the caller's connection list under the provided lock.
func affinityDiscoveryUpdate(
	mu *sync.RWMutex,
	activeConns *[]*Connection,
	policyState *atomic.Int32,
	added, removed []*Connection,
) error {
	if policyState.Load()&psEnvDisabled != 0 {
		return nil
	}

	if added == nil && removed == nil {
		return nil
	}

	// Rebuild the full active connection list.
	// Collect all connections from the three lists, dedup by URL.
	seen := make(map[string]struct{})
	var all []*Connection

	mu.RLock()
	for _, c := range *activeConns {
		key := c.URLString
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			all = append(all, c)
		}
	}
	mu.RUnlock()

	// Add new connections.
	for _, c := range added {
		key := c.URLString
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			all = append(all, c)
		}
	}

	// Remove old connections.
	if removed != nil {
		removedSet := make(map[string]struct{}, len(removed))
		for _, c := range removed {
			removedSet[c.URLString] = struct{}{}
		}
		filtered := all[:0]
		for _, c := range all {
			if _, found := removedSet[c.URLString]; !found {
				filtered = append(filtered, c)
			}
		}
		all = filtered
	}

	// Sort by RTT bucket ascending (nearest first). This is the invariant
	// that rendezvousTopK depends on for tier-based slot filling.
	sortConnectionsByRTT(all)

	mu.Lock()
	*activeConns = all
	mu.Unlock()

	psSetEnabled(policyState, len(all) > 0)

	return nil
}

// DiscoveryUpdate rebuilds the active connection list from topology changes.
func (p *IndexAffinityPolicy) DiscoveryUpdate(added, removed, _ []*Connection) error {
	return affinityDiscoveryUpdate(&p.mu.RWMutex, &p.mu.activeConns, &p.policyState, added, removed)
}

// affinityScore computes the node selection score for a connection.
// Lower score = preferred node.
//
// The score is: rttBucket * max(decayCounter, 1.0) * shardCostMultiplier
//
// Warmup state is NOT included in the score. Instead, warming connections
// are handled by [affinitySelect] which tries candidates in score order
// and uses [tryWarmupSkip] to gate actual traffic via the S-curve ramp.
// This avoids a circular dependency where warmup penalty prevents selection,
// which prevents warmup advancement.
func affinityScore(conn *Connection, node *shardNodeInfo, costs *shardCostMultiplier) float64 {
	rtt := float64(conn.rttRing.medianBucket())
	counter := conn.affinityCounter.load()
	if counter < affinityCounterFloor {
		counter = affinityCounterFloor
	}
	return rtt * counter * costs.forNode(node)
}

const (
	// warmupPenaltyMax is the worst-case multiplier applied at the start of
	// warmup (remaining == total). Matches costUnknown so a freshly-promoted
	// connection scores equivalently to a node with no shard data, then
	// linearly approaches 1.0 as warmup progresses.
	warmupPenaltyMax = costUnknown
)

// warmupPenalty returns a multiplier in [1.0, warmupPenaltyMax] based on
// how far through warmup the connection is. Fully warmed = 1.0 (no penalty).
// Used by the observer event for reporting; not included in the selection score.
func warmupPenalty(cs connState) float64 {
	lcMgr := cs.lifecycleManager()
	total := lcMgr.rounds()
	if total <= 0 {
		return 1.0
	}
	remaining := cs.roundManager().rounds()
	fraction := float64(remaining) / float64(total)
	return 1.0 + (warmupPenaltyMax-1.0)*fraction
}

// affinitySelect picks the best connection from scored candidates with
// warmup-aware skip/accept. Candidates are tried in ascending score order:
//
//   - Not warming → selected immediately.
//   - Warming → [tryWarmupSkip]:
//   - warmupAccepted or warmupInactive → selected (S-curve ramp-up).
//   - warmupSkipped → try next candidate.
//   - All candidates skip → starvation prevention: pick the one closest
//     to its next accept point (lowest remaining skip count).
//
// This avoids the circular dependency where a warmup penalty in the score
// prevents selection, which prevents warmup advancement. Warmup rounds
// advance naturally at a rate proportional to how often the connection
// would win scoring — good candidates warm up fast, poor ones warm up
// slowly via background health checks.
//
// The scores buffer must have len >= len(candidates). It is populated by
// this function and can be used by the caller for observer reporting.
func affinitySelect(candidates []*Connection, slot *indexSlot, costs *shardCostMultiplier, scores []float64) *Connection {
	n := len(candidates)
	if n == 0 {
		return nil
	}

	// Compute scores once upfront.
	for i, c := range candidates {
		scores[i] = affinityScore(c, slot.shardNodeInfoFor(c.Name), costs)
	}

	// Try candidates in ascending score order (lowest = best).
	// For small fan-out sets (typically 1-3), repeated linear scans
	// with a bitmask are faster than sorting.
	tried := uint64(0)
	var bestWarming *Connection
	bestWarmingSkip := int(^uint(0) >> 1) // max int sentinel

	for range n {
		// Find untried candidate with lowest score.
		bestIdx := -1
		bestScore := math.MaxFloat64
		for i := range n {
			if tried&(1<<uint(i)) != 0 {
				continue
			}
			if scores[i] < bestScore {
				bestScore = scores[i]
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			break
		}
		tried |= 1 << uint(bestIdx)
		c := candidates[bestIdx]

		if !c.loadConnState().isWarmingUp() {
			return c
		}

		switch c.tryWarmupSkip() {
		case warmupAccepted, warmupInactive:
			return c
		case warmupSkipped:
			remSkip := c.loadConnState().roundManager().skipCount()
			if remSkip <= bestWarmingSkip {
				bestWarmingSkip = remSkip
				bestWarming = c
			}
		}
	}

	// Starvation prevention: all candidates are warming and skipped.
	// Return the one closest to its next accept point.
	if bestWarming != nil {
		return bestWarming
	}
	return candidates[0] // Defensive; shouldn't reach here.
}

// sortConnectionsByRTT sorts connections by [rttBucket] ascending.
// Ties are broken by URL string for deterministic ordering.
func sortConnectionsByRTT(conns []*Connection) {
	// Use a simple insertion sort -- connection lists are typically small (3-50).
	for i := 1; i < len(conns); i++ {
		key := conns[i]
		keyBucket := key.rttRing.medianBucket()
		keyURL := key.URLString
		j := i - 1
		for j >= 0 {
			jBucket := conns[j].rttRing.medianBucket()
			if jBucket < keyBucket || (jBucket == keyBucket && conns[j].URLString <= keyURL) {
				break
			}
			conns[j+1] = conns[j]
			j--
		}
		conns[j+1] = key
	}
}

// extractIndexFromPath returns the index name from a request URL path.
// Returns "" for system endpoints (paths starting with /_) and root path.
//
// Examples:
//
//	"/my-index/_search"       -> "my-index"
//	"/my-index/_doc/123"      -> "my-index"
//	"/_cluster/health"        -> ""  (system endpoint)
//	"/"                       -> ""
//	"/my-index"               -> "my-index"
func extractIndexFromPath(path string) string {
	// Strip leading slash.
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	if path == "" {
		return ""
	}

	// System endpoints start with underscore after the leading slash.
	if path[0] == '_' {
		return ""
	}

	// Take the first path segment as the index name.
	if before, _, ok := strings.Cut(path, "/"); ok {
		return before
	}
	return path
}
