// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Compile-time interface compliance checks.
var (
	_ Policy             = (*DocumentAffinityPolicy)(nil)
	_ policyConfigurable = (*DocumentAffinityPolicy)(nil)
	_ policyTyped        = (*DocumentAffinityPolicy)(nil)
	_ policyOverrider    = (*DocumentAffinityPolicy)(nil)
)

// DocumentAffinityPolicy routes document-level requests to a consistent node
// based on the composite key {index}/{docID}. This provides finer-grained
// affinity than IndexAffinityPolicy for workloads with hot documents.
//
// Matches requests to document endpoints:
//
//	/{index}/_doc/{id}
//	/{index}/_source/{id}
//	/{index}/_update/{id}
//	/{index}/_explain/{id}
//	/{index}/_termvectors/{id}
//
// For non-document requests, returns (nil, nil) to fall through.
type DocumentAffinityPolicy struct {
	cache    *indexSlotCache // shared with IndexAffinityPolicy
	jitter   atomic.Int64
	decay    float64
	observer atomic.Pointer[ConnectionObserver]

	mu struct {
		sync.RWMutex
		activeConns []*Connection
	}

	policyState atomic.Int32 // Bitfield: psEnabled|psDisabled|psEnvEnabled|psEnvDisabled
	config      policyConfig
}

func (p *DocumentAffinityPolicy) policyTypeName() string { return "document_affinity" }
func (p *DocumentAffinityPolicy) setEnvOverride(enabled bool) {
	psSetEnvOverride(&p.policyState, enabled)
}

// NewDocumentAffinityPolicy creates a document affinity routing policy.
// The cache should be shared with IndexAffinityPolicy so fan-out and
// shard placement data is consistent.
func NewDocumentAffinityPolicy(cache *indexSlotCache, decay float64) *DocumentAffinityPolicy {
	if decay <= 0 || decay >= 1 {
		decay = defaultDecayFactor
	}
	return &DocumentAffinityPolicy{
		cache: cache,
		decay: decay,
	}
}

// configurePolicySettings implements policyConfigurable.
func (p *DocumentAffinityPolicy) configurePolicySettings(config policyConfig) error {
	p.config = config
	if config.observer != nil {
		p.observer.Store(config.observer)
	}
	return nil
}

// IsEnabled returns true if the policy has active connections.
func (p *DocumentAffinityPolicy) IsEnabled() bool {
	return psIsEnabled(p.policyState.Load())
}

// documentEndpoints lists the path segments that indicate a document-level
// operation (segment immediately after the index name).
//
//nolint:gochecknoglobals // Package-level constant map used by extractDocumentFromPath.
var documentEndpoints = map[string]struct{}{
	"_doc":         {},
	"_source":      {},
	"_update":      {},
	"_explain":     {},
	"_termvectors": {},
}

// Eval extracts {index}/{docID} from the request path and routes to a
// consistent node. Returns (nil, nil) for non-document requests.
func (p *DocumentAffinityPolicy) Eval(_ context.Context, req *http.Request) (ConnectionPool, error) {
	if p.policyState.Load()&psEnvDisabled != 0 {
		//nolint:nilnil // Intentional: force-disabled policy returns no match
		return nil, nil
	}

	indexName, docID := extractDocumentFromPath(req.URL.Path)
	if indexName == "" || docID == "" {
		//nolint:nilnil // Not a document request, fall through
		return nil, nil
	}

	p.mu.RLock()
	conns := p.mu.activeConns
	p.mu.RUnlock()

	if len(conns) == 0 {
		//nolint:nilnil // No connections available
		return nil, nil
	}

	// Use the index slot for fan-out and shard data, but hash on index/docID.
	slot := p.cache.getOrCreate(indexName)
	fanOut := p.cache.effectiveFanOut(slot, indexName, len(conns))
	shardNodes := slot.shardNodeNameSet()

	bp := getConnSlice(fanOut)
	candidates := rendezvousTopK(indexName, docID, conns, fanOut, &p.jitter, shardNodes, bp)
	if len(candidates) == 0 {
		putConnSlice(bp)
		//nolint:nilnil // No candidates
		return nil, nil
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
	best := affinitySelect(candidates, slot, &shardCostForReads, scores)

	if obs := observerFromAtomic(&p.observer); obs != nil {
		key := indexName + "/" + docID
		obs.OnAffinityRoute(buildAffinityRouteEvent(
			indexName, key, fanOut, len(conns), candidates, best, slot, &shardCostForReads,
		))
	}

	putConnSlice(bp)

	return getAffinityPool(best), nil
}

// CheckDead is a no-op. Lifecycle is managed by the underlying pool.
func (p *DocumentAffinityPolicy) CheckDead(_ context.Context, _ HealthCheckFunc) error {
	return nil
}

// RotateStandby is a no-op. Lifecycle is managed by the underlying pool.
func (p *DocumentAffinityPolicy) RotateStandby(_ context.Context, _ int) (int, error) {
	return 0, nil
}

// affinitySnapshot implements affinitySnapshotProvider.
func (p *DocumentAffinityPolicy) affinitySnapshot() AffinitySnapshot {
	return p.cache.snapshot()
}

// affinityCache implements [affinityCacheProvider].
func (p *DocumentAffinityPolicy) affinityCache() *indexSlotCache {
	return p.cache
}

// DiscoveryUpdate rebuilds the active connection list.
func (p *DocumentAffinityPolicy) DiscoveryUpdate(added, removed, _ []*Connection) error {
	return affinityDiscoveryUpdate(&p.mu.RWMutex, &p.mu.activeConns, &p.policyState, added, removed)
}

// extractDocumentFromPath returns the index name and document ID from a
// document-level request path. Returns ("", "") for non-document paths.
//
// Matches: /{index}/_doc/{id}, /{index}/_source/{id}, etc.
func extractDocumentFromPath(path string) (string, string) {
	// Strip leading slash.
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	if path == "" || path[0] == '_' {
		return "", ""
	}

	// Split: index / endpoint / id
	// Need at least 3 segments.
	before, after, ok := strings.Cut(path, "/") // after index
	if !ok {
		return "", ""
	}
	indexName := before
	rest := after

	before, after, ok = strings.Cut(rest, "/") // after endpoint
	if !ok {
		return "", ""
	}
	endpoint := before
	docID := after

	// Strip trailing slash or query from docID.
	if idx := strings.IndexAny(docID, "/?"); idx >= 0 {
		docID = docID[:idx]
	}

	if docID == "" {
		return "", ""
	}

	if _, ok := documentEndpoints[endpoint]; !ok {
		return "", ""
	}

	return indexName, docID
}
