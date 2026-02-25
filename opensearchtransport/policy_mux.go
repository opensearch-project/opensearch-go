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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Compile-time interface compliance checks
var (
	_ Policy             = (*MuxPolicy)(nil)
	_ policyConfigurable = (*MuxPolicy)(nil)
	_ policyTyped        = (*MuxPolicy)(nil)
	_ policyOverrider    = (*MuxPolicy)(nil)
)

// ErrNoRouteMatched is returned when no route matches the request pattern.
var ErrNoRouteMatched = errors.New("no route matched request")

// routeAttr is a bitfield of per-route attributes applied when the route matches.
// Attributes are set at route construction time and evaluated by [MuxPolicy.Eval].
type routeAttr uint32

const (
	// routeAttrPreferLocal injects ?preference=_local on matched requests.
	// This tells the server to prefer shard copies local to the receiving node,
	// complementing client-side affinity routing. Only appropriate for data
	// operations that accept the preference parameter (search, get, count, etc.).
	routeAttrPreferLocal routeAttr = 1 << iota
)

const openSearchSystemQueryPrefix = "/_"

// isSystemPath reports whether path targets an OpenSearch system endpoint
// (paths starting with /_). System endpoints include /_cluster, /_cat,
// /_nodes, /_ingest, /_search (cross-index), etc. These endpoints do not
// target a specific index and generally do not accept shard-level parameters
// like ?preference.
func isSystemPath(path string) bool {
	return strings.HasPrefix(path, openSearchSystemQueryPrefix)
}

//nolint:gochecknoglobals // Shared HTTP methods map for validMuxPattern
var httpMethods = map[string]struct{}{
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

// Route represents a pattern-to-policy mapping for HTTP request routing.
type Route interface {
	// Policy returns the policy to use for matching requests
	Policy() Policy
	// Attrs returns the per-route attribute bitfield.
	Attrs() routeAttr
}

// RouteMux represents a route that can be handled by http.ServeMux
type RouteMux struct {
	Pattern string    // HTTP pattern (e.g., "POST /_bulk", "GET /_search", "/")
	policy  Policy    // Policy to use for matching requests
	attrs   routeAttr // Per-route attributes applied when the route matches
}

// NewRouteMux creates a new ServeMux-compatible route with validation.
func NewRouteMux(pattern string, policy Policy) (Route, error) {
	return NewRouteMuxAttrs(pattern, policy, 0)
}

// NewRouteMuxAttrs creates a new ServeMux-compatible route with attributes.
func NewRouteMuxAttrs(pattern string, policy Policy, attrs routeAttr) (Route, error) {
	if policy == nil {
		return nil, errors.New("policy cannot be nil")
	}

	if _, err := validMuxPattern(pattern); err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	return &RouteMux{
		Pattern: pattern,
		policy:  policy,
		attrs:   attrs,
	}, nil
}

// mustNewRouteMux creates a new ServeMux-compatible route, panicking on error.
// Used for internal route construction where patterns are known to be valid.
func mustNewRouteMux(pattern string, policy Policy) Route {
	return mustNewRouteMuxAttrs(pattern, policy, 0)
}

// mustNewRouteMuxAttrs creates a new ServeMux-compatible route with attributes,
// panicking on error. Used for internal route construction.
func mustNewRouteMuxAttrs(pattern string, policy Policy, attrs routeAttr) Route {
	route, err := NewRouteMuxAttrs(pattern, policy, attrs)
	if err != nil {
		panic("invalid RouteMux: " + err.Error())
	}
	return route
}

// Policy returns the underlying policy used by this RouteMux.
func (r *RouteMux) Policy() Policy { return r.policy }

// Attrs returns the per-route attribute bitfield.
func (r *RouteMux) Attrs() routeAttr { return r.attrs }

// MuxPolicy is a connection policy multiplexer that routes requests to different policies
// based on HTTP patterns, using separate ServeMux instances for system vs index endpoints.
type MuxPolicy struct {
	systemMux      *http.ServeMux      // ServeMux for system endpoints (/_cluster, /_snapshot, etc.)
	indexMux       *http.ServeMux      // ServeMux for index endpoints (/{index}/_search, etc.)
	uniquePolicies map[Policy]struct{} // Set of unique policies for lifecycle management
	policyState    atomic.Int32        // Bitfield: psEnabled|psDisabled|psEnvEnabled|psEnvDisabled

	// Object pools for policyResponseWriter resources
	headerPool         sync.Pool // Pool for reusing http.Header
	responseWriterPool sync.Pool // Pool for reusing policyResponseWriter
}

func (p *MuxPolicy) policyTypeName() string      { return "mux" }
func (p *MuxPolicy) setEnvOverride(enabled bool) { psSetEnvOverride(&p.policyState, enabled) }

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
			attrs := r.Attrs()   // Capture for closure
			handler := func(w http.ResponseWriter, req *http.Request) {
				if pw, ok := w.(*policyResponseWriter); ok {
					pw.policy = policy
					pw.attrs = attrs
				}
			}

			// Route to appropriate ServeMux based on path prefix
			if isSystemPath(queryPath) {
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
		headerPool: sync.Pool{
			New: func() any {
				return make(http.Header)
			},
		},
		responseWriterPool: sync.Pool{
			New: func() any {
				return &policyResponseWriter{}
			},
		},
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

// policyResponseWriter is a custom ResponseWriter that captures the policy
// and route attributes from a matched ServeMux handler.
type policyResponseWriter struct {
	muxPolicy *MuxPolicy // Reference to parent MuxPolicy for pool access
	policy    Policy
	attrs     routeAttr
	header    http.Header
}

// Header returns the header map for the policy response writer.
func (w *policyResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = w.muxPolicy.headerPool.Get().(http.Header)
	}
	return w.header
}

// Write is a no-op for the policy response writer.
func (w *policyResponseWriter) Write([]byte) (int, error) { return 0, nil }

// WriteHeader is a no-op for the policy response writer.
func (w *policyResponseWriter) WriteHeader(statusCode int) {}

// release clears the header and returns it to the pool, and clears the policy field.
func (w *policyResponseWriter) release() {
	if w.header != nil {
		clear(w.header)
		w.muxPolicy.headerPool.Put(w.header)
		w.header = nil
	}
	w.policy = nil
	w.attrs = 0
}

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

	psSetEnabled(&p.policyState, hasEnabledPolicy)
	return firstError
}

// IsEnabled uses cached state to quickly determine if this mux can route requests.
func (p *MuxPolicy) IsEnabled() bool {
	return psIsEnabled(p.policyState.Load())
}

// Eval routes the request based on HTTP patterns and delegates to the matching policy.
func (p *MuxPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	if p.policyState.Load()&psEnvDisabled != 0 {
		//nolint:nilnil // Intentional: force-disabled policy returns no match
		return nil, nil
	}

	// Get writer from pool
	pw := p.responseWriterPool.Get().(*policyResponseWriter)
	pw.muxPolicy = p // Set reference to parent for pool access
	defer func() {
		pw.release()
		p.responseWriterPool.Put(pw)
	}()

	// Handle nil request (e.g., when called from OnSuccess/OnFailure)
	// In these cases, we can't determine routing so return nil
	if req == nil {
		//nolint:nilnil // Intentional: (nil, nil) signals "cannot route without request"
		return nil, nil
	}

	if isSystemPath(req.URL.Path) {
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
		pool, err := pw.policy.Eval(ctx, req)
		if pool != nil && err == nil {
			if pw.attrs&routeAttrPreferLocal != 0 {
				injectPreference(req, preferenceLocal)
			}
		}
		return pool, err
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

// RotateStandby delegates to all unique sub-policies, summing successful rotations.
func (p *MuxPolicy) RotateStandby(ctx context.Context, count int) (int, error) {
	var (
		total int
		errs  []error
	)
	for policy := range p.uniquePolicies {
		n, err := policy.RotateStandby(ctx, count)
		total += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
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

// childPolicies returns the unique sub-policies for tree walking.
// Sorted by policySortKey for deterministic path assignment.
func (p *MuxPolicy) childPolicies() []Policy {
	policies := make([]Policy, 0, len(p.uniquePolicies))
	for policy := range p.uniquePolicies {
		policies = append(policies, policy)
	}
	slices.SortFunc(policies, func(a, b Policy) int {
		return strings.Compare(policySortKey(a), policySortKey(b))
	})
	return policies
}

// poolSnapshots collects pool snapshots from all unique sub-policies.
func (p *MuxPolicy) poolSnapshots() []PoolSnapshot {
	result := make([]PoolSnapshot, 0, len(p.uniquePolicies))
	for policy := range p.uniquePolicies {
		result = append(result, collectPoolSnapshots(policy)...)
	}
	return result
}
