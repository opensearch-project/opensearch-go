// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"sync"
)

// affinityPool is a single-use pool that wraps a pre-selected connection.
// It is returned by affinity policies (IndexAffinityPolicy, DocumentAffinityPolicy)
// after scoring and selecting the best node for a given key.
//
// The pool is stateless -- OnSuccess and OnFailure are no-ops because lifecycle
// management is handled by the parent multiServerPool that owns the connection.
//
// Instances are recycled via affinityPoolPool to avoid per-request heap allocations.
// Next() returns the pool to the sync.Pool after extracting the connection,
// which is safe because Next() is called exactly once per routing decision.
type affinityPool struct {
	conn *Connection
}

// Compile-time check that affinityPool implements ConnectionPool.
var _ ConnectionPool = (*affinityPool)(nil)

//nolint:gochecknoglobals // sync.Pool must be package-level
var affinityPoolPool = sync.Pool{
	New: func() any { return &affinityPool{} },
}

// getAffinityPool returns an affinityPool from the pool, configured with
// the given connection.
func getAffinityPool(conn *Connection) *affinityPool {
	p := affinityPoolPool.Get().(*affinityPool)
	p.conn = conn
	return p
}

// Next returns the pre-selected connection and recycles this pool object.
// If the connection was demoted between Eval() and Next(), returns ErrNoConnections.
//
// If the connection has a warmup in progress, Next advances it via
// tryWarmupSkip. Since affinity routing has already selected the best node,
// we always serve the request regardless of the skip/accept result -- but
// advancing the counter ensures warmup eventually completes and the
// lcNeedsWarmup flag is cleared.
func (p *affinityPool) Next() (*Connection, error) {
	conn := p.conn
	p.conn = nil
	affinityPoolPool.Put(p)
	if conn == nil {
		return nil, ErrNoConnections
	}
	cs := conn.loadConnState()
	if cs.lifecycle()&(lcActive|lcStandby) == 0 {
		return nil, ErrNoConnections
	}

	// Advance warmup if in progress. We always serve the request (affinity
	// already picked this node), but we need to tick the warmup counter so
	// it eventually completes and clears lcNeedsWarmup.
	if cs.isWarmingUp() {
		conn.tryWarmupSkip()
	}

	return conn, nil
}

// OnSuccess is a no-op. Lifecycle is managed by the parent pool.
func (p *affinityPool) OnSuccess(*Connection) {}

// OnFailure is a no-op. Lifecycle is managed by the parent pool.
func (p *affinityPool) OnFailure(*Connection) error { return nil }

// URLs returns the URL of the pre-selected connection.
func (p *affinityPool) URLs() []*url.URL {
	return []*url.URL{p.conn.URL}
}
