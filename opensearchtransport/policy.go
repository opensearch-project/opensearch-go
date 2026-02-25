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
	"time"
)

// HealthCheckFunc defines the signature for health check functions.
// The conn parameter provides the Connection being health-checked, allowing the function
// to read connection state (e.g., clusterHealthState) to choose the appropriate endpoint.
type HealthCheckFunc func(ctx context.Context, conn *Connection, url *url.URL) (*http.Response, error)

// policyConfig contains configuration settings for policy connection pools.
// This unexported struct allows policies to create pools with consistent settings
// and provides a single place to add new configuration options in the future.
type policyConfig struct {
	name                         string          // Pool identity for metrics/debug
	ctx                          context.Context //nolint:containedctx // Long-lived pool context, not a request context.
	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutMax          time.Duration
	resurrectTimeoutFactorCutoff int
	minimumResurrectTimeout      time.Duration
	jitterScale                  float64
	serverMaxNewConnsPerSec      float64
	clientsPerServer             float64
	healthCheck                  HealthCheckFunc
	observer                     *ConnectionObserver // nil means no observer

	// Standby pool configuration
	activeListCap          *int  // nil = auto-scale; non-nil = user-specified cap value
	standbyPromotionChecks int64 // consecutive health checks before standby->ready
}

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

	// RotateStandby rotates standby connections into active across all owned pools.
	// Each rotation health-checks a standby and, if healthy, promotes it to active.
	// Returns the total number of successful rotations and any errors encountered
	// during rotation (including partial failures). Called synchronously from
	// DiscoverNodes -- blocks until rotations complete or no candidates remain.
	RotateStandby(ctx context.Context, count int) (int, error)

	// IsEnabled performs a quick check if this policy can be evaluated.
	// This should use cached state for maximum performance.
	IsEnabled() bool

	// Eval evaluates the policy and returns a connection pool if applicable.
	Eval(ctx context.Context, req *http.Request) (ConnectionPool, error)
}

// policyConfigurable is a package-internal interface for policies that need configuration.
// This allows injecting client-specific pool settings (like timeout settings) after policy creation.
type policyConfigurable interface {
	configurePolicySettings(config policyConfig) error
}

// PoolReporter is implemented by leaf policies that own a multiServerPool.
// It returns a point-in-time snapshot of the pool's partitions and request counters.
type PoolReporter interface {
	PoolSnapshot() PoolSnapshot
}

// poolSnapshotCollector is an internal interface for policies that recursively
// collect pool snapshots from their children. Wrapper policies (PolicyChain,
// MuxPolicy, IfEnabledPolicy) implement this to walk the tree.
type poolSnapshotCollector interface {
	poolSnapshots() []PoolSnapshot
}

// collectPoolSnapshots walks a policy (or any value) and collects pool snapshots.
// Leaf policies implement PoolReporter; wrapper policies implement poolSnapshotCollector.
func collectPoolSnapshots(v any) []PoolSnapshot {
	var result []PoolSnapshot
	if reporter, ok := v.(PoolReporter); ok {
		result = append(result, reporter.PoolSnapshot())
	}
	if collector, ok := v.(poolSnapshotCollector); ok {
		result = append(result, collector.poolSnapshots()...)
	}
	return result
}

// createPoolFromConfig creates a new multiServerPool with the given configuration.
// This is a helper function for leaf policies that manage their own connection pools.
func createPoolFromConfig(config policyConfig) *multiServerPool {
	// Derive effective activeListCap from config pointer.
	var effectiveCap int
	if config.activeListCap != nil {
		effectiveCap = *config.activeListCap
	}

	pool := &multiServerPool{
		name:                         config.name,
		ctx:                          config.ctx,
		resurrectTimeoutInitial:      config.resurrectTimeoutInitial,
		resurrectTimeoutMax:          config.resurrectTimeoutMax,
		resurrectTimeoutFactorCutoff: config.resurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      config.minimumResurrectTimeout,
		jitterScale:                  config.jitterScale,
		serverMaxNewConnsPerSec:      config.serverMaxNewConnsPerSec,
		clientsPerServer:             config.clientsPerServer,
		healthCheck:                  config.healthCheck,
		activeListCap:                effectiveCap,
		activeListCapConfig:          config.activeListCap,
		standbyPromotionChecks:       config.standbyPromotionChecks,
	}
	if config.observer != nil {
		pool.observer.Store(config.observer)
	}
	return pool
}
