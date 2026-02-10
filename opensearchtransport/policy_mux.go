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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// Compile-time interface compliance checks
var (
	_ Policy             = (*MuxPolicy)(nil)
	_ policyConfigurable = (*MuxPolicy)(nil)
)

// ErrNoRouteMatched is returned when no route matches the request pattern.
var ErrNoRouteMatched = errors.New("no route matched request")

const openSearchSystemQueryPrefix = "/_"

var (
	//nolint:gochecknoglobals // Shared empty header to avoid allocations in policyResponseWriter
	emptyHeader = make(http.Header)

	//nolint:gochecknoglobals // Shared empty header to avoid allocations in validMuxPattern
	httpMethods = map[string]struct{}{
		http.MethodGet:     {},
		http.MethodHead:    {},
		http.MethodPost:    {},
		http.MethodPut:     {},
		http.MethodPatch:   {},
		http.MethodDelete:  {},
		http.MethodConnect: {},
		http.MethodOptions: {},
		http.MethodTrace:   {},
	}
)

// Route represents a pattern-to-policy mapping for HTTP request routing.
type Route interface {
	// Policy returns the policy to use for matching requests
	Policy() Policy
}

// RouteMux represents a route that can be handled by http.ServeMux
type RouteMux struct {
	Pattern string // HTTP pattern (e.g., "POST /_bulk", "GET /_search", "/")
	policy  Policy // Policy to use for matching requests
}

// NewRouteMux creates a new ServeMux-compatible route with validation.
func NewRouteMux(pattern string, policy Policy) (Route, error) {
	if policy == nil {
		return nil, errors.New("policy cannot be nil")
	}

	if _, err := validMuxPattern(pattern); err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	return &RouteMux{
		Pattern: pattern,
		policy:  policy,
	}, nil
}

// mustNewRouteMux creates a new ServeMux-compatible route, panicking on error.
// Used for internal route construction where patterns are known to be valid.
func mustNewRouteMux(pattern string, policy Policy) Route {
	route, err := NewRouteMux(pattern, policy)
	if err != nil {
		panic("invalid RouteMux: " + err.Error())
	}
	return route
}

// Policy returns the underlying policy used by this RouteMux.
func (r *RouteMux) Policy() Policy { return r.policy }

// MuxPolicy is a connection policy multiplexer that routes requests to different policies
// based on HTTP patterns, using separate ServeMux instances for system vs index endpoints.
type MuxPolicy struct {
	systemMux      *http.ServeMux      // ServeMux for system endpoints (/_cluster, /_snapshot, etc.)
	indexMux       *http.ServeMux      // ServeMux for index endpoints (/{index}/_search, etc.)
	uniquePolicies map[Policy]struct{} // Set of unique policies for lifecycle management
	isEnabled      atomic.Bool         // Cached state from DiscoveryUpdate
}

// NewMuxPolicy creates a new policy multiplexer with the given routes.
//
// Routes are automatically sorted using a dual-ServeMux approach: patterns starting with "/_"
// go to systemMux, all other patterns go to indexMux. This design solves a fundamental conflict
// in Go's http.ServeMux pattern matching.
//
// ## Problem Solved:
// OpenSearch has valid API patterns that conflict in Go's ServeMux:
//   - POST /_snapshot/{repository}/_mount  (system endpoint)
//   - POST /{index}/_explain/{id}          (index endpoint)
//
// Both patterns could match "POST /_snapshot/_explain/_mount", and Go's ServeMux
// cannot determine which is more specific, causing a panic during registration.
//
// ## Solution:
// We separate the patterns into two distinct ServeMux instances based on path prefix:
//   - System endpoints (/_cluster, /_snapshot, /_search, etc.) -> systemMux
//   - Index endpoints (/{index}/_search, /{index}/_explain/{id}, etc.) -> indexMux
//
// At request time, we route to the appropriate ServeMux based on a simple path prefix check.
// This eliminates conflicts while maintaining fast ServeMux pattern matching performance.
func NewMuxPolicy(routes []Route) Policy {
	systemMux := http.NewServeMux()
	indexMux := http.NewServeMux()

	// Collect unique policies for lifecycle management
	uniquePolicies := make(map[Policy]struct{})

	// Sort routes by type and path prefix
	for _, route := range routes {
		uniquePolicies[route.Policy()] = struct{}{}

		switch r := route.(type) {
		case *RouteMux:
			queryPath, err := validMuxPattern(r.Pattern)
			if err != nil {
				panic(fmt.Sprintf("invalid pattern: %v", err))
			}

			policy := r.Policy() // Capture for closure
			handler := func(w http.ResponseWriter, req *http.Request) {
				if pw, ok := w.(*policyResponseWriter); ok {
					pw.policy = policy
				}
			}

			// Route to appropriate ServeMux based on path prefix
			if strings.HasPrefix(queryPath, openSearchSystemQueryPrefix) {
				systemMux.HandleFunc(r.Pattern, handler)
			} else {
				indexMux.HandleFunc(r.Pattern, handler)
			}

		default:
			panic(fmt.Sprintf("unsupported Route type: %T", r))
		}
	}

	return &MuxPolicy{
		systemMux:      systemMux,
		indexMux:       indexMux,
		uniquePolicies: uniquePolicies,
	}
}

func validMuxPattern(pattern string) (string, error) {
	if pattern == "" {
		return "", errors.New("pattern cannot be empty")
	}

	routeFields := strings.Fields(pattern)
	if len(routeFields) != 2 {
		return "", fmt.Errorf("route pattern must include HTTP method: %q", pattern)
	}
	if _, found := httpMethods[routeFields[0]]; !found {
		return "", fmt.Errorf("route pattern method invalid: %q", pattern)
	}

	return routeFields[1], nil
}

// policyResponseWriter is a custom ResponseWriter that captures the policy.
type policyResponseWriter struct {
	policy Policy
}

// Header returns an empty header map for the policy response writer.
func (w *policyResponseWriter) Header() http.Header { return emptyHeader }

// Write is a no-op for the policy response writer.
func (w *policyResponseWriter) Write([]byte) (int, error) { return 0, nil }

// WriteHeader is a no-op for the policy response writer.
func (w *policyResponseWriter) WriteHeader(statusCode int) {}

// DiscoveryUpdate updates all sub-policies and caches the enabled state.
func (p *MuxPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	var firstError error
	hasEnabledPolicy := false

	// Update all unique policies (no ordering dependency)
	for policy := range p.uniquePolicies {
		if err := policy.DiscoveryUpdate(added, removed, unchanged); err != nil && firstError == nil {
			firstError = err
		}

		// Cache if any policy is enabled
		if !hasEnabledPolicy && policy.IsEnabled() {
			hasEnabledPolicy = true
		}
	}

	p.isEnabled.Store(hasEnabledPolicy)
	return firstError
}

// IsEnabled uses cached state to quickly determine if this mux can route requests.
func (p *MuxPolicy) IsEnabled() bool {
	return p.isEnabled.Load()
}

// Eval routes the request based on HTTP patterns and delegates to the matching policy.
func (p *MuxPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	// Try fast ServeMux first
	pw := &policyResponseWriter{}
	if strings.HasPrefix(req.URL.Path, openSearchSystemQueryPrefix) {
		if p.systemMux == nil {
			//nolint:nilnil // Intentional: (nil, nil) signals "no routes configured"
			return nil, nil
		}

		p.systemMux.ServeHTTP(pw, req)
	} else {
		if p.indexMux == nil {
			//nolint:nilnil // Intentional: (nil, nil) signals "no routes configured"
			return nil, nil
		}

		p.indexMux.ServeHTTP(pw, req)
	}

	// If ServeMux found a match, use it
	if pw.policy != nil {
		return pw.policy.Eval(ctx, req)
	}

	// No matching route found - return nil, nil to allow fallthrough
	//nolint:nilnil // Intentional: (nil, nil) signals "no matching route"
	return nil, nil
}

// CheckDead delegates to all unique sub-policies.
func (p *MuxPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	var firstError error
	for policy := range p.uniquePolicies {
		if err := policy.CheckDead(ctx, healthCheck); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}

// configurePolicySettings configures pool settings for all unique sub-policies.
func (p *MuxPolicy) configurePolicySettings(config policyConfig) error {
	var firstError error

	// Configure all unique policies
	for policy := range p.uniquePolicies {
		if configurablePolicy, ok := policy.(policyConfigurable); ok {
			if err := configurablePolicy.configurePolicySettings(config); err != nil && firstError == nil {
				firstError = err
			}
		}
	}

	return firstError
}
