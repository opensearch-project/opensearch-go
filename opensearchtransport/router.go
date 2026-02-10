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
	_ Router             = (*PolicyChain)(nil)
	_ Policy             = (*PolicyChain)(nil)
	_ policyConfigurable = (*PolicyChain)(nil)
)

// Router defines the interface for request routing.
type Router interface {
	Route(ctx context.Context, req *http.Request) (*Connection, error)
	DiscoveryUpdate(added, removed, unchanged []*Connection) error
	CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error // Health check dead connections across all policies
}

// PolicyChain implements both Router and Policy interfaces by trying policies in sequence until one matches.
type PolicyChain struct {
	policies  []Policy
	isEnabled atomic.Bool // Cached state from DiscoveryUpdate (for Policy interface)
}

// NewRouter creates a router that tries policies in order.
func NewRouter(policies ...Policy) Router {
	return &PolicyChain{policies: policies}
}
