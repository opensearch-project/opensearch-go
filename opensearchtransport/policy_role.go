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
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

// Compile-time interface compliance checks
var (
	_ Policy                  = (*RolePolicy)(nil)
	_ poolFactoryConfigurable = (*RolePolicy)(nil)
)

const (
	// RoleSeparator is used to join multiple roles into a single key
	RoleSeparator = ","
)

// ErrInvalidRole indicates a role name contains invalid characters.
type ErrInvalidRole struct {
	Role      string
	Separator string
}

func (e ErrInvalidRole) Error() string {
	return fmt.Sprintf("role name cannot contain %q: %q", e.Separator, e.Role)
}

// NormalizeRoles deduplicates, sorts, and joins roles into a canonical key.
// This ensures consistent key generation across selectors and discovery.
// Validates that role names don't contain the separator to ensure clean key space.
func NormalizeRoles(roles []string) (string, error) {
	if len(roles) == 0 {
		return "", nil
	}

	// Deduplicate roles using a map and validate no separator chars
	uniqueRoles := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if strings.Contains(role, RoleSeparator) {
			// Invalid role name - contains our separator
			return "", ErrInvalidRole{Role: role, Separator: RoleSeparator}
		}
		uniqueRoles[role] = struct{}{}
	}

	if len(uniqueRoles) == 0 {
		return "", nil
	}

	// Convert to sorted slice
	sortedRoles := make([]string, 0, len(uniqueRoles))
	for role := range uniqueRoles {
		sortedRoles = append(sortedRoles, role)
	}
	slices.Sort(sortedRoles)

	// Join with separator for readable keys
	return strings.Join(sortedRoles, RoleSeparator), nil
}

// RolePolicy implements routing based on required node roles.
type RolePolicy struct {
	requiredRoleKey  string                           // Normalized role key for this policy
	pool             *statusConnectionPool            // Single pool for connections matching required roles
	poolFactory      func() *statusConnectionPool     // Factory for creating pools with proper settings
	hasMatchingRoles atomic.Bool                      // Cached state from DiscoveryUpdate
}

// NewRolePolicy creates a new role-based routing policy.
// Routes requests only to nodes that have ALL of the specified roles.
//
// Common role combinations:
//   - "data" - nodes that can store and search data
//   - "ingest" - nodes that can process documents before indexing
//   - "cluster_manager" - nodes that can manage cluster state (avoid for client requests)
//   - RoleCoordinatingOnly - dedicated client/coordinating nodes (empty roles)
//
// Multiple roles require nodes to have ALL specified roles:
//   NewRolePolicy("data", "ingest") // nodes with BOTH data AND ingest roles
//
// Returns ErrInvalidRole if role names contain the RoleSeparator character.
// Use with NewRouter() for policy chaining and fallback behavior.
func NewRolePolicy(roles ...string) (Policy, error) {
	if len(roles) == 0 {
		return nil, ErrInvalidRole{Role: "<empty>", Separator: "no roles specified"}
	}

	// Normalize roles (deduplicate, sort, validate)
	roleKey, err := NormalizeRoles(roles)
	if err != nil {
		return nil, err
	}

	return &RolePolicy{
		requiredRoleKey: roleKey,
		pool:            nil, // Will be created when poolFactory is set
		poolFactory:     nil, // Will be set by client via configurePoolFactories
	}, nil
}

// RequiredRoleKey returns the normalized role key for this policy.
func (p *RolePolicy) RequiredRoleKey() string {
	return p.requiredRoleKey
}

// configurePoolFactories configures pool factories for this policy (leaf policy - no sub-policies).
func (p *RolePolicy) configurePoolFactories(factory func() *statusConnectionPool) error {
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

// DiscoveryUpdate updates the role-based connection pools based on cluster topology changes.
func (p *RolePolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	var firstError error

	if added != nil {
		if err := p.discoveryUpdateAdd(added); err != nil && firstError == nil {
			firstError = err
		}
	}

	if removed != nil {
		if err := p.discoveryUpdateRemove(removed); err != nil && firstError == nil {
			firstError = err
		}
	}

	return firstError
}

// discoveryUpdateAdd handles adding new connections that match this policy's required roles.
func (p *RolePolicy) discoveryUpdateAdd(added []*Connection) error {
	for _, conn := range added {
		if p.connectionMatchesRoles(conn) {
			// Add matching connection to dead list - will be health checked later
			p.pool.Lock()
			p.pool.mu.dead = append(p.pool.mu.dead, conn)
			// Update hasMatching state while holding the lock
			hasConnections := len(p.pool.mu.live) > 0 || len(p.pool.mu.dead) > 0
			p.hasMatchingRoles.Store(hasConnections)
			p.pool.Unlock()
		}
	}

	return nil
}

// discoveryUpdateRemove handles removing connections from this policy's pool.
func (p *RolePolicy) discoveryUpdateRemove(removed []*Connection) error {
	// Build map of removed connection URLs for O(1) lookup
	removedConns := make(map[string]struct{}, len(removed))
	for _, node := range removed {
		removedConns[node.URL.String()] = struct{}{}
	}

	// First check if we have any connections to remove (using RLock)
	p.pool.RLock()
	hasConnectionsToRemove := false
	for _, conn := range p.pool.mu.live {
		if _, found := removedConns[conn.URL.String()]; found {
			hasConnectionsToRemove = true
			break
		}
	}
	if !hasConnectionsToRemove {
		for _, conn := range p.pool.mu.dead {
			if _, found := removedConns[conn.URL.String()]; found {
				hasConnectionsToRemove = true
				break
			}
		}
	}
	p.pool.RUnlock()

	// If no connections to remove, exit early
	if !hasConnectionsToRemove {
		return nil
	}

	// Helper function to efficiently remove connections from a slice
	removeConns := func(conns []*Connection) []*Connection {
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

	// Now acquire write lock and actually remove connections
	p.pool.Lock()
	p.pool.mu.live = removeConns(p.pool.mu.live)
	p.pool.mu.dead = removeConns(p.pool.mu.dead)
	// Update hasMatching state while holding the lock
	hasConnections := len(p.pool.mu.live) > 0 || len(p.pool.mu.dead) > 0
	p.hasMatchingRoles.Store(hasConnections)
	p.pool.Unlock()

	return nil
}

// connectionMatchesRoles checks if a connection matches this policy's required roles.
func (p *RolePolicy) connectionMatchesRoles(conn *Connection) bool {
	if p.requiredRoleKey == "" {
		return false
	}

	// Handle coordinating-only nodes - check both empty roles and explicit coordinating_only role
	if p.requiredRoleKey == RoleCoordinatingOnly {
		return len(conn.Roles) == 0 || conn.Roles.has(RoleCoordinatingOnly)
	}

	// For regular roles, check if connection has all required roles
	requiredRoles := strings.Split(p.requiredRoleKey, RoleSeparator)
	for _, requiredRole := range requiredRoles {
		if !conn.Roles.has(requiredRole) {
			return false
		}
	}
	return true
}

// IsEnabled uses cached state to quickly determine if matching roles are available.
func (p *RolePolicy) IsEnabled() bool {
	return p.hasMatchingRoles.Load()
}

// Eval returns the connection pool for this role-based policy.
// Returns (nil, nil) if no matching connections are found (allows fallthrough).
func (p *RolePolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if p.hasMatchingRoles.Load() {
		return p.pool, nil
	}
	// No matching connections found, allow fallthrough
	return nil, nil
}

// CheckDead syncs the pool based on Connection.mu.isDead state.
func (p *RolePolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	return p.pool.checkDead(ctx, healthCheck)
}

// mustRolePolicy creates a new role-based policy or panics if creation fails.
// This is a helper function for creating built-in policies with known-valid roles.
func mustRolePolicy(role string) Policy {
	policy, err := NewRolePolicy(role)
	if err != nil {
		panic(fmt.Sprintf("failed to create role policy for %q: %v", role, err))
	}
	return policy
}

// generateRoleCombinations creates all possible combinations of roles for O(1) lookups.
// For roles ["search", "ingest"], it generates: ["search", "ingest", "ingest,search"]
// Roles are sorted to ensure consistent keys, using comma separator to match RoleBasedSelector.
func generateRoleCombinations(roles []string) []string {
	if len(roles) == 0 {
		return []string{}
	}

	// De-duplicate and sort input roles for consistent output
	uniqueRoles := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		uniqueRoles[role] = struct{}{}
	}

	// Convert back to sorted slice
	dedupedRoles := make([]string, 0, len(uniqueRoles))
	for role := range uniqueRoles {
		dedupedRoles = append(dedupedRoles, role)
	}
	slices.Sort(dedupedRoles)

	// Pre-allocate combinations slice with exact capacity: 2^n - 1
	n := len(dedupedRoles)
	combinations := make([]string, 0, (1<<n)-1)

	// Generate all possible combinations (2^n - 1, excluding empty set)
	for i := 1; i < (1 << n); i++ {
		combo := make([]string, 0, n) // Pre-size to maximum possible combination size
		for j := 0; j < n; j++ {
			if i&(1<<j) > 0 {
				combo = append(combo, dedupedRoles[j])
			}
		}

		// Roles are already sorted from dedupedRoles, so combo is automatically sorted
		combinations = append(combinations, strings.Join(combo, RoleSeparator))
	}

	return combinations
}
