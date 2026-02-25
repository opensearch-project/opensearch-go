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
	_ Policy             = (*CoordinatorPolicy)(nil)
	_ policyConfigurable = (*CoordinatorPolicy)(nil)
	_ PoolReporter       = (*CoordinatorPolicy)(nil)
	_ policyTyped        = (*CoordinatorPolicy)(nil)
	_ policyOverrider    = (*CoordinatorPolicy)(nil)
)

// CoordinatorPolicy implements routing to coordinating-only nodes.
type CoordinatorPolicy struct {
	pool        *multiServerPool // Pool of coordinating-only connections
	policyState atomic.Int32     // Bitfield: psEnabled|psDisabled|psEnvEnabled|psEnvDisabled
}

func (p *CoordinatorPolicy) policyTypeName() string      { return "coordinator" }
func (p *CoordinatorPolicy) setEnvOverride(enabled bool) { psSetEnvOverride(&p.policyState, enabled) }

// NewCoordinatorPolicy creates a policy that routes to coordinating-only nodes.
func NewCoordinatorPolicy() Policy {
	return &CoordinatorPolicy{
		pool: nil, // Will be created when policy settings are configured
	}
}

// configurePolicySettings configures pool settings for this policy (leaf policy - no sub-policies).
func (p *CoordinatorPolicy) configurePolicySettings(config policyConfig) error {
	// Create pool with proper settings if we don't have one yet
	if p.pool == nil {
		config.name = "coordinator"
		p.pool = createPoolFromConfig(config)
	}
	return nil
}

// CheckDead syncs the pool based on Connection.mu.isDead state.
// Subsequent policies just sync their pools without doing actual health checks.
func (p *CoordinatorPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if p.pool == nil {
		return nil
	}

	return p.pool.checkDead(ctx, healthCheck)
}

// RotateStandby rotates standby connections into active in this policy's pool.
func (p *CoordinatorPolicy) RotateStandby(ctx context.Context, count int) (int, error) {
	if p.pool == nil {
		return 0, nil
	}

	return p.pool.rotateStandby(ctx, count)
}

// DiscoveryUpdate updates the coordinating-only connection pool based on cluster topology changes.
// Adds are processed before removes so that the pool is never empty during a topology
// change where old seed URLs are replaced by discovered node addresses.
func (p *CoordinatorPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	if p.policyState.Load()&psEnvDisabled != 0 {
		return nil
	}

	if p.pool == nil {
		return nil
	}

	// Short-circuit if no changes
	if added == nil && removed == nil {
		return nil
	}

	p.pool.Lock()
	defer p.pool.Unlock()

	// Add new coordinating-only connections to dead list
	for _, newConn := range added {
		if _, hasRole := newConn.Roles[RoleCoordinatingOnly]; hasRole {
			newConn.state.Store(int64(newConnState(lcDead)))
			p.pool.appendToDeadWithLock(newConn)
		}
	}

	// Remove old connections
	if removed != nil {
		removedConns := make(map[string]struct{}, len(removed))
		for _, node := range removed {
			removedConns[node.URL.String()] = struct{}{}
		}

		// Remove connections from ready list, tracking active count
		{
			activeCountBefore := p.pool.mu.activeCount

			filtered := p.pool.mu.ready[:0]
			activeCount := 0
			for i, conn := range p.pool.mu.ready {
				if _, found := removedConns[conn.URL.String()]; !found {
					filtered = append(filtered, conn)
					if i < p.pool.mu.activeCount {
						activeCount++
					}
				}
			}
			p.pool.mu.ready = filtered
			p.pool.mu.activeCount = activeCount

			// If removal shrunk the active partition and standby exists,
			// schedule graceful (warmed) promotions to fill the gap.
			gap := activeCountBefore - p.pool.mu.activeCount
			p.pool.promoteStandbyGracefullyWithLock(p.pool.poolCtx(), gap)
		}

		// Remove connections from dead list
		{
			filtered := p.pool.mu.dead[:0]
			for _, conn := range p.pool.mu.dead {
				if _, found := removedConns[conn.URL.String()]; !found {
					filtered = append(filtered, conn)
				}
			}
			p.pool.mu.dead = filtered
		}
	}

	// Update cached state
	hasCoords := len(p.pool.mu.ready) > 0 || len(p.pool.mu.dead) > 0
	psSetEnabled(&p.policyState, hasCoords)

	return nil
}

// IsEnabled uses cached state to quickly determine if coordinating-only nodes are available.
func (p *CoordinatorPolicy) IsEnabled() bool {
	return psIsEnabled(p.policyState.Load())
}

// Eval attempts to route to coordinating-only nodes.
// Returns (nil, nil) if no coordinating-only nodes are available (allows fallthrough).
func (p *CoordinatorPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if p.policyState.Load()&psEnvDisabled != 0 {
		//nolint:nilnil // Intentional: force-disabled policy returns no match
		return nil, nil
	}

	if p.policyState.Load()&psEnabled == 0 {
		//nolint:nilnil // Intentional: (nil, nil) signals "policy not applicable, try next"
		return nil, nil // No coordinating-only nodes, allow fallthrough
	}

	return p.pool, nil
}

// PoolSnapshot returns a point-in-time snapshot of this policy's pool.
func (p *CoordinatorPolicy) PoolSnapshot() PoolSnapshot {
	if p.pool == nil {
		return PoolSnapshot{Name: "coordinator"}
	}
	snap := p.pool.snapshot()
	snap.Enabled = psIsEnabled(p.policyState.Load())
	return snap
}
