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
	"net/url"
)

// HealthCheckFunc defines the signature for health check functions.
type HealthCheckFunc func(ctx context.Context, url *url.URL) (*http.Response, error)

// Policy defines the interface for individual routing policies.
// Policies return (pool, nil) for matches, (nil, error) for errors,
// and (nil, nil) for "no match, try next policy".
type Policy interface {
	// DiscoveryUpdate is called when node discovery is run and is the callback used to update
	// a policy's route cache. DiscoveryUpdate will be called every time node discovery is run
	// to provide the ability to update existing connections, in addition to recording when
	// there are changes to the cluster's topology from new nodes being added or old nodes being removed.
	// DiscoveryUpdate() is called on every configured policies in the router, regardless of whether
	// or IsEnabled is true.
	// added: new nodes being added to the cluster, removed: nodes being removed from cluster,
	// unchanged: existing nodes that remain between discovery runs.
	// Most calls will have nil added/removed with unchanged containing the full node list.
	// Policies typically only need to handle added != nil and removed != nil cases.
	DiscoveryUpdate(added, removed, unchanged []*Connection) error

	// CheckDead is called periodically by the router's health checker to sync dead connections.
	// The first policy should perform actual health checks on dead connections.
	// Subsequent policies should resurrect connections based on the state of Connection.mu.isDead.
	CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error

	// IsEnabled performs a quick check if this policy can be evaluated.
	// This should use cached state for maximum performance.
	IsEnabled() bool

	// Eval evaluates the policy and returns a connection pool if applicable.
	Eval(ctx context.Context, req *http.Request) (ConnectionPool, error)
}

// poolFactoryConfigurable is a package-internal interface for policies that need pool factory configuration.
// This allows injecting client-specific pool configuration (like timeout settings) after policy creation.
type poolFactoryConfigurable interface {
	configurePoolFactories(factory func() *statusConnectionPool) error
}
