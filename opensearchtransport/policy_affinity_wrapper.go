// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Compile-time interface compliance checks.
var (
	_ Policy             = (*affinityPolicyWrapper)(nil)
	_ policyConfigurable = (*affinityPolicyWrapper)(nil)
	_ policyTyped        = (*affinityPolicyWrapper)(nil)
	_ policyOverrider    = (*affinityPolicyWrapper)(nil)
)

// affinityPolicyWrapper wraps any Policy and applies index/document affinity
// selection to the pool returned by the inner policy's Eval().
//
// When the inner policy returns a multiServerPool (e.g., a role-based pool
// of data nodes), the wrapper:
//  1. Reads the pool's active connections (under RLock)
//  2. Extracts the index (and optionally document ID) from the request path
//  3. Selects top-K nodes via rendezvous hash within the role pool
//  4. Picks the best candidate by affinity score (RTT * decay counter)
//  5. Returns an affinityPool wrapping the selected node
//
// For non-index requests or when the inner policy returns nil, the wrapper
// passes through transparently.
type affinityPolicyWrapper struct {
	inner       Policy
	cache       *indexSlotCache
	shardCosts  *shardCostMultiplier
	jitter      atomic.Int64
	decay       float64
	observer    atomic.Pointer[ConnectionObserver]
	policyState atomic.Int32 // Bitfield: env override bits only (dynamic state from inner)

	// mu guards sortedConns -- a pre-sorted (by RTT) snapshot of active
	// connections from the inner policy's pool. Rebuilt on DiscoveryUpdate;
	// read (without copy) on every Eval.
	mu struct {
		sync.RWMutex
		sortedConns []*Connection
	}
}

func (p *affinityPolicyWrapper) policyTypeName() string { return "affinity" }
func (p *affinityPolicyWrapper) setEnvOverride(enabled bool) {
	psSetEnvOverride(&p.policyState, enabled)
}

// wrapWithAffinity wraps a policy with affinity selection. The costs
// parameter selects the shard cost table: [shardCostForReads] for read
// routes or [shardCostForWrites] for write routes (e.g., bulk ingest).
func wrapWithAffinity(inner Policy, cache *indexSlotCache, decay float64, costs *shardCostMultiplier) Policy {
	return &affinityPolicyWrapper{
		inner:      inner,
		cache:      cache,
		decay:      decay,
		shardCosts: costs,
	}
}

// Eval delegates to the inner policy, then applies affinity selection on
// the pre-sorted connection list maintained by DiscoveryUpdate.
func (p *affinityPolicyWrapper) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if p.policyState.Load()&psEnvDisabled != 0 {
		//nolint:nilnil // Intentional: force-disabled policy returns no match
		return nil, nil
	}

	pool, err := p.inner.Eval(ctx, req)
	if pool == nil || err != nil {
		return pool, err
	}

	// Determine the affinity key from the request path.
	// Document-level key ({index}/{docID}) takes priority over index-level.
	var keyA, keyB, indexName string
	if idx, docID := extractDocumentFromPath(req.URL.Path); idx != "" && docID != "" {
		keyA = idx
		keyB = docID
		indexName = idx
	} else {
		indexName = extractIndexFromPath(req.URL.Path)
		keyA = indexName
	}

	if keyA == "" {
		// No index in path -- use the pool as-is (normal round-robin).
		return pool, nil
	}

	// Read the pre-sorted connection snapshot (rebuilt on DiscoveryUpdate).
	p.mu.RLock()
	conns := p.mu.sortedConns
	p.mu.RUnlock()

	if len(conns) == 0 {
		return pool, nil
	}

	// Look up (or create) the index slot for fan-out and shard data.
	slot := p.cache.getOrCreate(indexName)
	fanOut := p.cache.effectiveFanOut(slot, indexName, len(conns))
	shardNodes := slot.shardNodeNameSet()

	// Select top-K via rendezvous hash with jitter rotation.
	bp := getConnSlice(fanOut)
	candidates := rendezvousTopK(keyA, keyB, conns, fanOut, &p.jitter, shardNodes, bp)
	if len(candidates) == 0 {
		putConnSlice(bp)
		return pool, nil
	}

	// Update tier-span equalization (see IndexAffinityPolicy.Eval).
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
		key := keyA
		if keyB != "" {
			key = keyA + "/" + keyB
		}
		obs.OnAffinityRoute(buildAffinityRouteEvent(
			indexName, key, fanOut, len(conns), candidates, best, slot, p.shardCosts,
		))
	}

	putConnSlice(bp)

	// Verify the selected connection is still active (dirty read).
	// If it was demoted since the last DiscoveryUpdate, fall through
	// to the inner pool's Next() which handles lazy eviction.
	if best.loadConnState().lifecycle()&(lcActive|lcStandby) == 0 {
		return pool, nil
	}

	return getAffinityPool(best), nil
}

// DiscoveryUpdate delegates to the inner policy, then rebuilds the
// pre-sorted connection snapshot from the inner policy's pool.
func (p *affinityPolicyWrapper) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	err := p.inner.DiscoveryUpdate(added, removed, unchanged)

	// Rebuild the sorted snapshot from the inner policy's pool.
	p.rebuildSortedConns()

	return err
}

// rebuildSortedConns reads active connections from the inner policy's pool
// and stores an RTT-sorted copy. Called after discovery updates and
// health check state changes.
func (p *affinityPolicyWrapper) rebuildSortedConns() {
	conns := extractActiveConnsFromPolicy(p.inner)

	if len(conns) > 0 {
		sortConnectionsByRTT(conns)
	}

	p.mu.Lock()
	p.mu.sortedConns = conns
	p.mu.Unlock()
}

// extractActiveConnsFromPolicy extracts active connections from a policy's
// underlying pool. Walks through IfEnabledPolicy and other wrappers to
// find the first pool with active connections.
func extractActiveConnsFromPolicy(policy Policy) []*Connection {
	switch p := policy.(type) {
	case *RolePolicy:
		if p.pool == nil {
			return nil
		}
		p.pool.mu.RLock()
		n := p.pool.mu.activeCount
		if n == 0 {
			p.pool.mu.RUnlock()
			return nil
		}
		conns := make([]*Connection, n)
		copy(conns, p.pool.mu.ready[:n])
		p.pool.mu.RUnlock()
		return conns
	case *RoundRobinPolicy:
		if p.pool == nil {
			return nil
		}
		p.pool.mu.RLock()
		n := p.pool.mu.activeCount
		if n == 0 {
			p.pool.mu.RUnlock()
			return nil
		}
		conns := make([]*Connection, n)
		copy(conns, p.pool.mu.ready[:n])
		p.pool.mu.RUnlock()
		return conns
	case *IfEnabledPolicy:
		// Try the true branch first, then false.
		if conns := extractActiveConnsFromPolicy(p.truePolicy); len(conns) > 0 {
			return conns
		}
		if p.falsePolicy != nil {
			return extractActiveConnsFromPolicy(p.falsePolicy)
		}
		return nil
	case *PolicyChain:
		for _, sub := range p.policies {
			if conns := extractActiveConnsFromPolicy(sub); len(conns) > 0 {
				return conns
			}
		}
		return nil
	default:
		return nil
	}
}

// CheckDead delegates to the inner policy.
func (p *affinityPolicyWrapper) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	return p.inner.CheckDead(ctx, healthCheck)
}

// RotateStandby delegates to the inner policy.
func (p *affinityPolicyWrapper) RotateStandby(ctx context.Context, count int) (int, error) {
	return p.inner.RotateStandby(ctx, count)
}

// IsEnabled delegates to the inner policy unless env-overridden.
func (p *affinityPolicyWrapper) IsEnabled() bool {
	if p.policyState.Load()&psEnvDisabled != 0 {
		return false
	}
	return p.inner.IsEnabled()
}

// configurePolicySettings stores the observer and passes config through
// to the inner policy.
func (p *affinityPolicyWrapper) configurePolicySettings(config policyConfig) error {
	if config.observer != nil {
		p.observer.Store(config.observer)
	}
	if configurable, ok := p.inner.(policyConfigurable); ok {
		return configurable.configurePolicySettings(config)
	}
	return nil
}

// childPolicies returns the inner policy for tree walking.
func (p *affinityPolicyWrapper) childPolicies() []Policy {
	return []Policy{p.inner}
}

// poolSnapshots collects snapshots from the inner policy.
func (p *affinityPolicyWrapper) poolSnapshots() []PoolSnapshot {
	return collectPoolSnapshots(p.inner)
}

// affinitySnapshot implements affinitySnapshotProvider.
func (p *affinityPolicyWrapper) affinitySnapshot() AffinitySnapshot {
	return p.cache.snapshot()
}

// affinityCache implements [affinityCacheProvider].
func (p *affinityPolicyWrapper) affinityCache() *indexSlotCache {
	return p.cache
}

// updateShardPlacement updates the affinity cache with shard placement data
// from the discovery cycle. Called after /_cat/shards returns shard-to-node
// mappings.
func (p *affinityPolicyWrapper) updateShardPlacement(shardPlacement map[string]*indexShardPlacement, activeNodeCount int) {
	p.cache.updateFromDiscovery(shardPlacement, activeNodeCount, time.Now())
}

// shardPlacementUpdater is an optional interface implemented by policies that
// need shard placement data from the discovery cycle. The discovery flow walks
// the policy tree and calls updateShardPlacement on any policy that implements
// this interface.
type shardPlacementUpdater interface {
	updateShardPlacement(shardPlacement map[string]*indexShardPlacement, activeNodeCount int)
}

// updateShardPlacementTree walks a policy tree and calls updateShardPlacement
// on any node that implements shardPlacementUpdater. Uses the same walk pattern
// as collectPoolSnapshots.
func updateShardPlacementTree(v any, shardPlacement map[string]*indexShardPlacement, activeNodeCount int) {
	if updater, ok := v.(shardPlacementUpdater); ok {
		updater.updateShardPlacement(shardPlacement, activeNodeCount)
	}
	if collector, ok := v.(policyTreeWalker); ok {
		for _, child := range collector.childPolicies() {
			updateShardPlacementTree(child, shardPlacement, activeNodeCount)
		}
	}
}

// policyTreeWalker is implemented by policies that contain sub-policies.
// Used for recursive tree walks (shard placement updates, etc.).
type policyTreeWalker interface {
	childPolicies() []Policy
}
