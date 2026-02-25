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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Measurable defines the interface for transports supporting metrics.
type Measurable interface {
	Metrics() (Metrics, error)
}

// Metrics represents the transport metrics.
type Metrics struct {
	Requests  int         `json:"requests"`
	Failures  int         `json:"failures"`
	Responses map[int]int `json:"responses"`

	// Connection pool state.
	// LiveConnections is the number of non-dead connections (active + standby).
	// Named 'Live' for JSON API compatibility; corresponds to the internal ready list.
	LiveConnections int `json:"live_connections"`
	DeadConnections int `json:"dead_connections"`

	// Connection lifecycle counters
	ConnectionsPromoted int `json:"connections_promoted"` // Dead -> Ready (resurrected successfully)
	ConnectionsDemoted  int `json:"connections_demoted"`  // Ready -> Dead (marked dead)
	ZombieConnections   int `json:"zombie_connections"`   // Taken from dead list and forcibly retried

	// Client capabilities and health
	HealthChecks        int `json:"health_checks"`         // Baseline GET / health checks performed
	ClusterHealthChecks int `json:"cluster_health_checks"` // GET /_cluster/health?local=true health checks performed
	HealthChecksSuccess int `json:"health_checks_success"` // Successful health check outcomes
	HealthChecksFailed  int `json:"health_checks_failed"`  // Failed health check outcomes
	OverloadedServers   int `json:"overloaded_servers"`    // Number of servers client thinks are overloaded

	// Standby pool state
	StandbyConnections int `json:"standby_connections"` // Current standby pool size
	StandbyPromotions  int `json:"standby_promotions"`  // Standby -> Active transitions
	StandbyDemotions   int `json:"standby_demotions"`   // Active -> Standby transitions

	Connections []fmt.Stringer `json:"connections"`

	// Per-pool breakdown (only populated when router with policies is active)
	Pools []PoolSnapshot `json:"pools,omitempty"`

	// Affinity routing cache state (only populated when affinity routing is active)
	Affinity *AffinitySnapshot `json:"affinity,omitempty"`
}

// AffinitySnapshot is a point-in-time summary of the affinity routing cache.
type AffinitySnapshot struct {
	Indexes []IndexAffinityState   `json:"indexes,omitempty"`
	Config  AffinitySnapshotConfig `json:"config"`
}

// IndexAffinityState is per-index routing state from the affinity cache.
type IndexAffinityState struct {
	Name        string     `json:"name"`
	FanOut      int        `json:"fan_out"`
	ShardNodes  int        `json:"shard_nodes"`
	RequestRate float64    `json:"request_rate"`
	IdleSince   *time.Time `json:"idle_since,omitempty"`
}

// AffinitySnapshotConfig holds the effective configuration values for
// the affinity routing cache.
type AffinitySnapshotConfig struct {
	MinFanOut       int     `json:"min_fan_out"`
	MaxFanOut       int     `json:"max_fan_out"`
	DecayFactor     float64 `json:"decay_factor"`
	FanOutPerReq    float64 `json:"fan_out_per_request"`
	IdleEvictionTTL string  `json:"idle_eviction_ttl"`
}

// sortIndexAffinityStates sorts index states by name for deterministic output.
func sortIndexAffinityStates(states []IndexAffinityState) {
	slices.SortFunc(states, func(a, b IndexAffinityState) int {
		return strings.Compare(a.Name, b.Name)
	})
}

// affinitySnapshotProvider is implemented by policies that hold an
// indexSlotCache and can produce an AffinitySnapshot.
type affinitySnapshotProvider interface {
	affinitySnapshot() AffinitySnapshot
}

// collectAffinitySnapshot walks a policy tree and returns the first
// AffinitySnapshot found. Returns nil if no provider exists in the tree.
func collectAffinitySnapshot(v any) *AffinitySnapshot {
	if provider, ok := v.(affinitySnapshotProvider); ok {
		snap := provider.affinitySnapshot()
		return &snap
	}
	if walker, ok := v.(policyTreeWalker); ok {
		for _, child := range walker.childPolicies() {
			if snap := collectAffinitySnapshot(child); snap != nil {
				return snap
			}
		}
	}
	return nil
}

// ConnectionMetric represents metric information for a connection.
type ConnectionMetric struct {
	URL              string     `json:"url"`
	Failures         int        `json:"failures,omitempty"`
	IsDead           bool       `json:"dead,omitempty"`
	IsStandby        bool       `json:"standby,omitempty"`
	IsOverloaded     bool       `json:"overloaded,omitempty"`
	NeedsCatUpdate   bool       `json:"needs_cat_update,omitempty"`
	IsWarmingUp      bool       `json:"warming_up,omitempty"`
	IsHealthChecking bool       `json:"health_checking,omitempty"`
	Weight           int        `json:"weight,omitempty"`
	DeadSince        *time.Time `json:"dead_since,omitempty"`
	OverloadedSince  *time.Time `json:"overloaded_since,omitempty"`
	State            ConnState  `json:"state"`

	// Affinity routing fields (populated when RTT ring or affinity counter has data)
	RTTBucket    *int64   `json:"rtt_bucket,omitempty"`
	RTTMedian    *string  `json:"rtt_median,omitempty"`
	AffinityLoad *float64 `json:"affinity_load,omitempty"`

	Meta struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	} `json:"meta"`
}

// PoolSnapshot is a point-in-time snapshot of one connection pool's partitions.
type PoolSnapshot struct {
	Name                string `json:"name"`
	Enabled             bool   `json:"enabled"`
	ActiveCount         int    `json:"active_count"`
	StandbyCount        int    `json:"standby_count"`
	DeadCount           int    `json:"dead_count"`
	ActiveListCap       int    `json:"active_list_cap"`
	WarmingCount        int    `json:"warming_count"`
	HealthCheckingCount int    `json:"health_checking_count"`

	// Per-pool request counters
	Requests      int64 `json:"requests"`       // Connections returned by Next()
	Successes     int64 `json:"successes"`      // Resurrections via OnSuccess()
	Failures      int64 `json:"failures"`       // Demotions via OnFailure()
	WarmupSkips   int64 `json:"warmup_skips"`   // Requests skipped during warmup
	WarmupAccepts int64 `json:"warmup_accepts"` // Requests accepted during warmup
}

// String returns the pool snapshot as a compact string.
func (ps PoolSnapshot) String() string {
	enabledStr := "on"
	if !ps.Enabled {
		enabledStr = "off"
	}
	return fmt.Sprintf("%q (%s, cap=%d): active=%d standby=%d dead=%d warming=%d checking=%d | req=%d ok=%d fail=%d skip=%d accept=%d",
		ps.Name, enabledStr, ps.ActiveListCap, ps.ActiveCount, ps.StandbyCount, ps.DeadCount, ps.WarmingCount, ps.HealthCheckingCount,
		ps.Requests, ps.Successes, ps.Failures, ps.WarmupSkips, ps.WarmupAccepts)
}

// metrics represents the inner state of metrics.
type metrics struct {
	requests atomic.Int64
	failures atomic.Int64

	// Connection lifecycle counters
	connectionsPromoted atomic.Int64 // Dead -> Ready (resurrected successfully)
	connectionsDemoted  atomic.Int64 // Ready -> Dead (marked dead)
	zombieConnections   atomic.Int64 // Taken from dead list and forcibly retried

	// Health check counters
	healthChecks        atomic.Int64 // Baseline GET / health checks performed
	clusterHealthChecks atomic.Int64 // GET /_cluster/health?local=true health checks performed
	healthChecksSuccess atomic.Int64 // Successful health check outcomes (from DefaultHealthCheck)
	healthChecksFailed  atomic.Int64 // Failed health check outcomes (from DefaultHealthCheck)

	// Standby pool lifecycle counters
	standbyPromotions atomic.Int64 // Standby -> Active
	standbyDemotions  atomic.Int64 // Active -> Standby

	mu struct {
		sync.RWMutex
		responses map[int]int
	}
}

// incrementResponse increments the counter for the given status code.
func (m *metrics) incrementResponse(statusCode int) {
	m.mu.Lock()
	m.mu.responses[statusCode]++
	m.mu.Unlock()
}

// Metrics returns the transport metrics.
func (c *Client) Metrics() (Metrics, error) {
	if c.metrics == nil {
		return Metrics{}, errors.New("transport metrics not enabled")
	}

	// Build responses map with pre-allocated capacity (READ operation)
	c.metrics.mu.RLock()
	responses := make(map[int]int, len(c.metrics.mu.responses))
	maps.Copy(responses, c.metrics.mu.responses)
	c.metrics.mu.RUnlock()

	// Get connections from current connection pool
	var ready, dead []*Connection
	var singleConns []*Connection
	c.mu.RLock()
	if c.mu.connectionPool != nil {
		switch pool := c.mu.connectionPool.(type) {
		case *multiServerPool:
			ready, dead = pool.connectionsByState()
		case *singleServerPool:
			singleConns = pool.connections()
		}
	}
	c.mu.RUnlock()

	m := Metrics{
		Requests:  int(c.metrics.requests.Load()),
		Failures:  int(c.metrics.failures.Load()),
		Responses: responses,

		LiveConnections: len(ready) + len(singleConns),
		DeadConnections: len(dead),

		ConnectionsPromoted: int(c.metrics.connectionsPromoted.Load()),
		ConnectionsDemoted:  int(c.metrics.connectionsDemoted.Load()),
		ZombieConnections:   int(c.metrics.zombieConnections.Load()),

		HealthChecks:        int(c.metrics.healthChecks.Load()),
		ClusterHealthChecks: int(c.metrics.clusterHealthChecks.Load()),
		HealthChecksSuccess: int(c.metrics.healthChecksSuccess.Load()),
		HealthChecksFailed:  int(c.metrics.healthChecksFailed.Load()),
		OverloadedServers:   0, // Set below when iterating connections

		StandbyPromotions: int(c.metrics.standbyPromotions.Load()),
		StandbyDemotions:  int(c.metrics.standbyDemotions.Load()),
	}

	// Build per-connection metrics. Each connection's connState atomic
	// determines isDead/isStandby/isOverloaded -- no positional tricks needed.
	overloadedCount := 0
	standbyCount := 0

	for _, conn := range singleConns {
		m.Connections = append(m.Connections, buildConnectionMetric(conn))
	}
	for _, conn := range ready {
		cm := buildConnectionMetric(conn)
		if cm.IsOverloaded {
			overloadedCount++
		}
		if cm.IsStandby {
			standbyCount++
		}
		m.Connections = append(m.Connections, cm)
	}
	for _, conn := range dead {
		cm := buildConnectionMetric(conn)
		if cm.IsOverloaded {
			overloadedCount++
		}
		m.Connections = append(m.Connections, cm)
	}
	m.OverloadedServers = overloadedCount
	m.StandbyConnections = standbyCount

	// Collect per-pool snapshots from the router's policy tree.
	// Also include the flat pool snapshot if available.
	if c.router != nil {
		m.Pools = collectPoolSnapshots(c.router)
		m.Affinity = collectAffinitySnapshot(c.router)
	}
	c.mu.RLock()
	if c.mu.connectionPool != nil {
		if pool, ok := c.mu.connectionPool.(*multiServerPool); ok {
			snap := pool.snapshot()
			snap.Enabled = true // flat/client pool is always enabled
			m.Pools = append(m.Pools, snap)
		}
	}
	c.mu.RUnlock()

	return m, nil
}

// buildConnectionMetric creates a ConnectionMetric from a Connection.
// State flags (isDead, isStandby, isOverloaded) are derived from the connection's
// connState atomic -- no positional or parameter-based inference needed.
func buildConnectionMetric(c *Connection) ConnectionMetric {
	state := c.loadConnState()
	lc := state.lifecycle()

	c.mu.Lock()
	deadSince := c.mu.deadSince
	overloadedAt := c.mu.overloadedAt
	c.mu.Unlock()

	cm := ConnectionMetric{
		URL:              c.URL.String(),
		IsDead:           lc.has(lcUnknown) && lc&(lcActive|lcStandby) == 0,
		IsStandby:        lc.has(lcStandby),
		IsOverloaded:     lc.has(lcOverloaded),
		NeedsCatUpdate:   lc.has(lcNeedsCatUpdate),
		IsWarmingUp:      state.isWarmingUp(),
		IsHealthChecking: lc.has(lcHealthChecking),
		Failures:         int(c.failures.Load()),
		Weight:           c.effectiveWeight(),
		State:            ConnState{packed: int64(state)},
	}

	if !deadSince.IsZero() {
		deadSinceCopy := deadSince
		cm.DeadSince = &deadSinceCopy
	}

	if cm.IsOverloaded && !overloadedAt.IsZero() {
		overloadedAtCopy := overloadedAt
		cm.OverloadedSince = &overloadedAtCopy
	}

	if c.ID != "" {
		cm.Meta.ID = c.ID
	}

	if c.Name != "" {
		cm.Meta.Name = c.Name
	}

	if len(c.Roles) > 0 {
		cm.Meta.Roles = c.Roles.toSlice()
	}

	// Populate affinity routing fields when data is available.
	if bucket := c.RTTBucket(); bucket >= 0 {
		cm.RTTBucket = &bucket
		median := c.RTTMedian().String()
		cm.RTTMedian = &median
	}
	if load := c.AffinityLoad(); load > 0 {
		cm.AffinityLoad = &load
	}

	return cm
}

// String returns the metrics as a string.
func (m Metrics) String() string {
	var (
		i int
		b strings.Builder
	)
	b.WriteString("{")

	b.WriteString("Requests:")
	b.WriteString(strconv.Itoa(m.Requests))

	b.WriteString(" Failures:")
	b.WriteString(strconv.Itoa(m.Failures))

	b.WriteString(" HealthChecks:")
	b.WriteString(strconv.Itoa(m.HealthChecks))

	b.WriteString(" ClusterHealthChecks:")
	b.WriteString(strconv.Itoa(m.ClusterHealthChecks))

	b.WriteString(" HealthChecksSuccess:")
	b.WriteString(strconv.Itoa(m.HealthChecksSuccess))

	b.WriteString(" HealthChecksFailed:")
	b.WriteString(strconv.Itoa(m.HealthChecksFailed))

	if len(m.Responses) > 0 {
		b.WriteString(" Responses: ")
		b.WriteString("[")

		for code, num := range m.Responses {
			b.WriteString(strconv.Itoa(code))
			b.WriteString(":")
			b.WriteString(strconv.Itoa(num))
			if i+1 < len(m.Responses) {
				b.WriteString(", ")
			}
			i++
		}
		b.WriteString("]")
	}

	b.WriteString(" Connections: [")
	for i, c := range m.Connections {
		b.WriteString(c.String())
		if i+1 < len(m.Connections) {
			b.WriteString(", ")
		}
	}
	b.WriteString("]")

	if len(m.Pools) > 0 {
		b.WriteString(" Pools: [")
		for i, p := range m.Pools {
			b.WriteString(p.String())
			if i+1 < len(m.Pools) {
				b.WriteString(", ")
			}
		}
		b.WriteString("]")
	}

	b.WriteString("}")
	return b.String()
}

// String returns the connection information as a string.
func (cm ConnectionMetric) String() string {
	var b strings.Builder
	b.WriteString("{")
	b.WriteString(cm.URL)

	// Show lifecycle state
	fmt.Fprintf(&b, " state=%s", cm.State)

	// Show roles if known
	if len(cm.Meta.Roles) > 0 {
		fmt.Fprintf(&b, " roles=%v", cm.Meta.Roles)
	}

	// Show name if known
	if cm.Meta.Name != "" {
		fmt.Fprintf(&b, " name=%s", cm.Meta.Name)
	}

	if cm.Failures > 0 {
		fmt.Fprintf(&b, " failures=%d", cm.Failures)
	}
	if cm.DeadSince != nil {
		fmt.Fprintf(&b, " dead_since=%s", cm.DeadSince.Local().Format(time.Stamp))
	}
	if cm.OverloadedSince != nil {
		fmt.Fprintf(&b, " overloaded_since=%s", cm.OverloadedSince.Local().Format(time.Stamp))
	}
	b.WriteString("}")
	return b.String()
}
