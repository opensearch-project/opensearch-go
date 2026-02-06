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
)

// Compile-time interface compliance checks
var (
	_ Policy                  = (*RoundRobinPolicy)(nil)
	_ poolFactoryConfigurable = (*RoundRobinPolicy)(nil)
)

// RoundRobinPolicy implements round-robin routing across all available connections.
type RoundRobinPolicy struct {
	pool        *statusConnectionPool            // Embedded connection pool for round-robin selection
	poolFactory func() *statusConnectionPool     // Factory for creating pools with proper settings
}

// NewRoundRobinPolicy creates a new round-robin routing policy.
func NewRoundRobinPolicy() Policy {
	policy := &RoundRobinPolicy{
		pool:        nil, // Will be created when poolFactory is set
		poolFactory: nil, // Will be set by client via configurePoolFactories
	}
	return policy
}

// configurePoolFactories configures pool factories for this policy (leaf policy - no sub-policies).
func (p *RoundRobinPolicy) configurePoolFactories(factory func() *statusConnectionPool) error {
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

// CheckDead performs actual health checks on dead connections and resurrects healthy ones.
// As the first policy, RoundRobinPolicy is responsible for actual HTTP health checks.
func (p *RoundRobinPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if p.pool == nil {
		return nil
	}

	return p.pool.checkDead(ctx, healthCheck)
}

// triggerHealthChecks performs health checks on dead connections
func (p *RoundRobinPolicy) triggerHealthChecks() {
	p.pool.RLock()
	deadConns := make([]*Connection, len(p.pool.mu.dead))
	copy(deadConns, p.pool.mu.dead)
	p.pool.RUnlock()

	// Health check each dead connection
	for _, conn := range deadConns {
		if p.healthCheck(conn) {
			// Connection is healthy, resurrect it
			p.pool.Lock()
			conn.mu.Lock()
			p.pool.resurrectWithLock(conn)
			conn.mu.Unlock()
			p.pool.Unlock()
		}
	}
}

// healthCheck performs a basic connectivity test
func (p *RoundRobinPolicy) healthCheck(conn *Connection) bool {
	// TODO: Implement actual health check (HTTP GET to /_cluster/health or similar)
	// For now, assume all connections are healthy during cold start
	return true
}

// DiscoveryUpdate updates the internal connection pool based on cluster topology changes.
func (p *RoundRobinPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
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

	// Add new connections to dead list - they start dead until proven alive via health checks
	p.pool.mu.dead = append(p.pool.mu.dead, added...)

	return nil
}

// IsEnabled always returns true as round-robin can always be used as a fallback.
func (p *RoundRobinPolicy) IsEnabled() bool {
	return p.pool != nil && len(p.pool.URLs()) > 0
}

// Eval returns the round-robin connection pool for all available connections.
func (p *RoundRobinPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if p.pool == nil {
		return nil, nil // No connections available
	}
	return p.pool, nil
}
