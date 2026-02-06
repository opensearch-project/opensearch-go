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
	"slices"
)

// Compile-time interface compliance checks
var (
	_ Policy                  = (*PolicyChain)(nil)
	_ poolFactoryConfigurable = (*PolicyChain)(nil)
)

// NewPolicy creates a policy that tries sub-policies in order.
// Returns a PolicyChain that implements the Policy interface.
func NewPolicy(policies ...Policy) Policy {
	return &PolicyChain{policies: policies}
}

// Route tries each policy in sequence until one returns a connection pool.
// Gets a connection from the first matching pool.
func (r *PolicyChain) Route(ctx context.Context, req *http.Request) (*Connection, error) {
	for _, policy := range r.policies {
		// Quick check if policy is enabled before evaluation
		if !policy.IsEnabled() {
			continue
		}

		pool, err := policy.Eval(ctx, req)
		switch {
		case err != nil:
			return nil, err // Error occurred, stop
		case pool != nil:
			// Found a pool, get connection from it
			return pool.Next()
		default:
			// pool == nil && err == nil: no match, try next policy
			continue
		}
	}

	return nil, ErrNoConnections // No policies matched
}

// DiscoveryUpdate notifies all policies that node discovery has occurred.
// Each policy updates its own state and connection pools based on topology changes.
// Logs all errors encountered, but continues updating remaining policies.
// Returns the first error encountered.
func (r *PolicyChain) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	var firstError error
	hasEnabledPolicy := false

	// Update policies in reverse order (lowest level first) so dependencies work correctly
	for _, policy := range slices.Backward(r.policies) {
		if err := policy.DiscoveryUpdate(added, removed, unchanged); err != nil {
			if firstError == nil {
				firstError = err // Capture first error to return
			}
			// Log error if debug logging is enabled
			if debugLogger != nil {
				_ = debugLogger.Logf("PolicyChain: policy DiscoveryUpdate failed: %v", err)
			}
		}

		// Cache if any policy is enabled
		if !hasEnabledPolicy && policy.IsEnabled() {
			hasEnabledPolicy = true
		}
	}

	r.isEnabled.Store(hasEnabledPolicy)
	return firstError
}

// CheckDead triggers health checks across all configured policies.
// The first policy performs actual health checks, subsequent policies sync their pools.
// Logs all errors encountered, but continues checking remaining policies.
// Returns the first error encountered.
func (r *PolicyChain) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	var firstError error
	for _, policy := range r.policies {
		if err := policy.CheckDead(ctx, healthCheck); err != nil {
			if firstError == nil {
				firstError = err // Capture first error to return
			}
			// Log error if debug logging is enabled
			if debugLogger != nil {
				_ = debugLogger.Logf("PolicyChain: policy CheckDead failed: %v", err)
			}
		}
	}
	return firstError
}

// IsEnabled uses cached state for O(1) lookup (when PolicyChain is used as Policy).
func (r *PolicyChain) IsEnabled() bool {
	return r.isEnabled.Load()
}

// Eval tries each sub-policy in sequence until one returns a connection pool or error.
// Returns (nil, nil) only if all sub-policies return (nil, nil).
func (r *PolicyChain) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	for _, policy := range r.policies {
		pool, err := policy.Eval(ctx, req)
		switch {
		case err != nil:
			return nil, err // Error occurred, stop
		case pool != nil:
			return pool, nil // Found connection pool, use it
		default:
			// pool == nil && err == nil: no match, try next policy
			continue
		}
	}

	// All policies returned (nil, nil), so we return (nil, nil) for fallthrough
	return nil, nil
}

// configurePoolFactories configures pool factories for all sub-policies.
func (r *PolicyChain) configurePoolFactories(factory func() *statusConnectionPool) error {
	var firstError error

	// Store factory for when PolicyChain is used as a Policy
	r.poolFactory = factory

	// Configure all sub-policies
	for _, policy := range r.policies {
		if configurablePolicy, ok := policy.(poolFactoryConfigurable); ok {
			if err := configurablePolicy.configurePoolFactories(factory); err != nil && firstError == nil {
				firstError = err
			}
		}
	}

	return firstError
}