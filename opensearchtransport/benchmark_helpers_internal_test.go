// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"time"
)

// Benchmark helper functions

// createBenchConnection creates a connection for benchmarking.
// Unlike test connections, bench connections start alive (not dead) for immediate use.
func createBenchConnection(urlStr string, id string, roles ...string) *Connection {
	u, _ := url.Parse(urlStr)
	conn := &Connection{
		URL:   u,
		ID:    id,
		Roles: make(roleSet),
	}
	for _, role := range roles {
		conn.Roles[role] = struct{}{}
	}
	// Connections start alive for benchmarking (no health check needed)
	return conn
}

// configureBenchPolicy configures a policy for benchmarking.
func configureBenchPolicy(p Policy, connections []*Connection) {
	// Configure policy settings
	if configurable, ok := p.(policyConfigurable); ok {
		config := policyConfig{
			resurrectTimeoutInitial:      60 * time.Second,
			resurrectTimeoutFactorCutoff: 5,
		}
		_ = configurable.configurePolicySettings(config)
	}

	// Add connections via discovery update
	_ = p.DiscoveryUpdate(connections, nil, nil)

	// Mark all connections as alive for immediate use
	//nolint:nestif // Benchmark helper with type-specific initialization
	if policy, ok := p.(*RoundRobinPolicy); ok && policy.pool != nil {
		policy.pool.Lock()
		policy.pool.mu.live = append(policy.pool.mu.live, connections...)
		policy.pool.mu.dead = nil
		policy.pool.Unlock()
	} else if policy, ok := p.(*RolePolicy); ok && policy.pool != nil {
		// Filter connections by role
		policy.pool.Lock()
		for _, conn := range connections {
			if policy.connectionMatchesRoles(conn) {
				policy.pool.mu.live = append(policy.pool.mu.live, conn)
			}
		}
		policy.pool.mu.dead = nil
		policy.hasMatchingRoles.Store(len(policy.pool.mu.live) > 0)
		policy.pool.Unlock()
	} else if policy, ok := p.(*CoordinatorPolicy); ok && policy.pool != nil {
		// Filter connections for coordinating-only
		policy.pool.Lock()
		for _, conn := range connections {
			if len(conn.Roles) == 0 || conn.Roles.has(RoleCoordinatingOnly) {
				policy.pool.mu.live = append(policy.pool.mu.live, conn)
			}
		}
		policy.pool.mu.dead = nil
		policy.hasCoordinators.Store(len(policy.pool.mu.live) > 0)
		policy.pool.Unlock()
	} else if policy, ok := p.(*PolicyChain); ok {
		// Recursively configure sub-policies
		for _, subPolicy := range policy.policies {
			configureBenchPolicy(subPolicy, connections)
		}
	}
}

// configureBenchRouter configures a router for benchmarking.
func configureBenchRouter(r Router, connections []*Connection) {
	// If it's a PolicyChain, configure the policies first
	if chain, ok := r.(*PolicyChain); ok {
		// Configure policy settings first (creates pools)
		for _, policy := range chain.policies {
			if configurable, ok := policy.(policyConfigurable); ok {
				config := policyConfig{
					resurrectTimeoutInitial:      60 * time.Second,
					resurrectTimeoutFactorCutoff: 5,
				}
				_ = configurable.configurePolicySettings(config)
			}
		}
	}

	// Update router with connections (now pools exist)
	_ = r.DiscoveryUpdate(connections, nil, nil)

	// Mark all connections as alive for immediate use
	if chain, ok := r.(*PolicyChain); ok {
		for _, policy := range chain.policies {
			markPolicyConnectionsAlive(policy, connections)
		}
	}
}

// markPolicyConnectionsAlive marks connections as alive for benchmarking (skip health checks).
//
//nolint:nestif // Benchmark helper with type-specific initialization
func markPolicyConnectionsAlive(p Policy, connections []*Connection) {
	if policy, ok := p.(*RoundRobinPolicy); ok && policy.pool != nil {
		policy.pool.Lock()
		policy.pool.mu.live = append(policy.pool.mu.live, connections...)
		policy.pool.mu.dead = nil
		policy.pool.Unlock()
	} else if policy, ok := p.(*RolePolicy); ok && policy.pool != nil {
		// Filter connections by role
		policy.pool.Lock()
		for _, conn := range connections {
			if policy.connectionMatchesRoles(conn) {
				policy.pool.mu.live = append(policy.pool.mu.live, conn)
			}
		}
		policy.pool.mu.dead = nil
		policy.hasMatchingRoles.Store(len(policy.pool.mu.live) > 0)
		policy.pool.Unlock()
	} else if policy, ok := p.(*CoordinatorPolicy); ok && policy.pool != nil {
		// Filter connections for coordinating-only
		policy.pool.Lock()
		for _, conn := range connections {
			if len(conn.Roles) == 0 || conn.Roles.has(RoleCoordinatingOnly) {
				policy.pool.mu.live = append(policy.pool.mu.live, conn)
			}
		}
		policy.pool.mu.dead = nil
		policy.hasCoordinators.Store(len(policy.pool.mu.live) > 0)
		policy.pool.Unlock()
	} else if policy, ok := p.(*PolicyChain); ok {
		// Recursively mark sub-policies
		for _, subPolicy := range policy.policies {
			markPolicyConnectionsAlive(subPolicy, connections)
		}
	} else if policy, ok := p.(*IfEnabledPolicy); ok {
		// Mark sub-policies in IfEnabledPolicy
		markPolicyConnectionsAlive(policy.truePolicy, connections)
		if policy.falsePolicy != nil {
			markPolicyConnectionsAlive(policy.falsePolicy, connections)
		}
	} else if policy, ok := p.(*MuxPolicy); ok {
		// Mark sub-policies in MuxPolicy unique policies
		for subPolicy := range policy.uniquePolicies {
			markPolicyConnectionsAlive(subPolicy, connections)
		}
	}
}
