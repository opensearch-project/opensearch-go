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
	_ Policy                  = (*NullPolicy)(nil)
	_ poolFactoryConfigurable = (*NullPolicy)(nil)
)

// NullPolicy is a policy that always returns no connections.
// This is used as a terminating policy when you want to explicitly
// return "no connections available" rather than "try next policy".
type NullPolicy struct{}

// NewNullPolicy creates a new null policy that always returns no connections.
func NewNullPolicy() Policy {
	return &NullPolicy{}
}

// DiscoveryUpdate is a no-op for null policy.
func (p *NullPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	return nil
}

// configurePoolFactories is a no-op for null policy (no pools, no sub-policies).
func (p *NullPolicy) configurePoolFactories(factory func() *statusConnectionPool) error {
	return nil
}

// CheckDead is a no-op for null policy.
func (p *NullPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	return nil
}

// IsEnabled always returns true since null policy can always "provide" no connections.
func (p *NullPolicy) IsEnabled() bool {
	return true
}

// Eval always returns (nil, nil) indicating no connections are available.
func (p *NullPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	return nil, nil
}