// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import (
	"context"
	"net/http"
	"sync/atomic"
)

// Compile-time interface compliance checks
var (
	_ Policy                  = (*CoordinatorPolicy)(nil)
	_ poolFactoryConfigurable = (*CoordinatorPolicy)(nil)
)

// CoordinatorPolicy implements routing to coordinating-only nodes.
type CoordinatorPolicy struct {
	pool            *statusConnectionPool            // Pool of coordinating-only connections
	poolFactory     func() *statusConnectionPool     // Factory for creating pools with proper settings
	hasCoordinators atomic.Bool                      // Cached state from DiscoveryUpdate
}

// NewCoordinatorPolicy creates a policy that routes to coordinating-only nodes.
func NewCoordinatorPolicy() Policy {
	return &CoordinatorPolicy{
		pool:        nil, // Will be created when poolFactory is set
		poolFactory: nil, // Will be set by client via configurePoolFactories
	}
}

// configurePoolFactories configures pool factories for this policy (leaf policy - no sub-policies).
func (p *CoordinatorPolicy) configurePoolFactories(factory func() *statusConnectionPool) error {
	p.poolFactory = factory

	// Create pool with proper settings if we don't have one yet
	if p.pool == nil {
		p.pool = factory()
		return nil
	}

	// Recreate the current pool with new settings, preserving connections
	p.pool.mu.Lock()
	liveConns := make([]*Connection, len(p.pool.mu.live))
	deadConns := make([]*Connection, len(p.pool.mu.dead))
	copy(liveConns, p.pool.mu.live)
	copy(deadConns, p.pool.mu.dead)
	metrics := p.pool.metrics
	p.pool.mu.Unlock()

	// Create new pool with proper settings
	newPool := factory()
	newPool.mu.live = liveConns
	newPool.mu.dead = deadConns
	newPool.metrics = metrics
	newPool.nextLive.Store(p.pool.nextLive.Load())

	p.pool = newPool
	return nil
}

// coldStart moves all connections from dead to live to handle initial cluster setup.
// This should be called once after the initial DiscoveryUpdate during client initialization.
func (p *CoordinatorPolicy) coldStart() {
	if p.pool == nil {
		return
	}

	p.pool.Lock()
	defer p.pool.Unlock()

	// Move all dead connections to live for cold start
	for _, conn := range p.pool.mu.dead {
		conn.mu.Lock()
		p.pool.resurrectWithLock(conn)
		conn.mu.Unlock()
	}
}

// CheckDead syncs the pool based on Connection.mu.isDead state.
// Subsequent policies just sync their pools without doing actual health checks.
func (p *CoordinatorPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if p.pool == nil {
		return nil
	}

	return p.pool.checkDead(ctx, healthCheck)
}

// DiscoveryUpdate updates the coordinating-only connection pool based on cluster topology changes.
func (p *CoordinatorPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	if p.pool == nil {
		return nil
	}

	// Short-circuit if no changes
	if added == nil && removed == nil {
		return nil
	}

	// Build map of removed connection URLs for O(1) lookup
	removedConns := make(map[string]struct{}, len(removed))
	for _, node := range removed {
		removedConns[node.URL.String()] = struct{}{}
	}

	// Helper function to efficiently remove connections from a slice
	removeConns := func(conns []*Connection, removedConns map[string]struct{}) []*Connection {
		if len(removedConns) == 0 {
			return conns
		}

		filtered := conns[:0]
		for _, conn := range conns {
			_, found := removedConns[conn.URL.String()]
			if !found {
				filtered = append(filtered, conn)
			}
		}
		return filtered
	}

	p.pool.Lock()
	defer p.pool.Unlock()

	// Remove connections from both live and dead lists
	p.pool.mu.live = removeConns(p.pool.mu.live, removedConns)
	p.pool.mu.dead = removeConns(p.pool.mu.dead, removedConns)

	// Add new coordinating-only connections to dead list
	for _, newConn := range added {
		if _, hasRole := newConn.Roles[RoleCoordinatingOnly]; hasRole {
			p.pool.mu.dead = append(p.pool.mu.dead, newConn)
		}
	}

	// Update cached state
	hasCoords := len(p.pool.mu.live) > 0 || len(p.pool.mu.dead) > 0
	p.hasCoordinators.Store(hasCoords)

	return nil
}

// IsEnabled uses cached state to quickly determine if coordinating-only nodes are available.
func (p *CoordinatorPolicy) IsEnabled() bool {
	return p.hasCoordinators.Load()
}

// Eval attempts to route to coordinating-only nodes.
// Returns (nil, nil) if no coordinating-only nodes are available (allows fallthrough).
func (p *CoordinatorPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if !p.hasCoordinators.Load() {
		return nil, nil // No coordinating-only nodes, allow fallthrough
	}

	return p.pool, nil
}
