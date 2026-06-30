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

//go:build !integration

package opensearchtransport

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMetricsAlwaysAllocated(t *testing.T) {
	tests := []struct {
		name string
		urls []*url.URL
	}{
		{name: "single server", urls: []*url.URL{{Scheme: "http", Host: "foo1"}}},
		{name: "multi server", urls: []*url.URL{
			{Scheme: "http", Host: "foo1"},
			{Scheme: "http", Host: "foo2"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp, err := New(Config{URLs: tc.urls})
			require.NoError(t, err)
			require.NotNil(t, tp.metrics, "metrics struct always allocated")
		})
	}
}

func TestDetailedCallbacksAlwaysRegistered(t *testing.T) {
	tests := []struct {
		name       string
		urls       []*url.URL
		withRouter bool
		wantCBs    bool
	}{
		{
			name:       "multi server with router registers callbacks",
			urls:       []*url.URL{{Scheme: "http", Host: "foo1"}, {Scheme: "http", Host: "foo2"}},
			withRouter: true,
			wantCBs:    true,
		},
		{
			name:       "no router registers no callbacks",
			urls:       []*url.URL{{Scheme: "http", Host: "foo1"}},
			withRouter: false,
			wantCBs:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{URLs: tc.urls}
			if tc.withRouter {
				router, err := NewDefaultRouter()
				require.NoError(t, err)
				cfg.Router = router
			}
			tp, err := New(cfg)
			require.NoError(t, err)
			total := len(tp.metrics.policyCallbacks) +
				len(tp.metrics.connMetricCallbacks) +
				len(tp.metrics.snapshotCallbacks)
			if tc.wantCBs {
				require.Positive(t, total, "a router with policies registers metric callbacks")
			} else {
				require.Zero(t, total, "no policies means no callbacks")
			}
		})
	}
}

func TestDetailedCallbacksPerPolicy(t *testing.T) {
	type configurable interface {
		configurePolicySettings(policyConfig) error
	}
	newIndexRouter := func() configurable { p, err := NewIndexRouter(); require.NoError(t, err); return p }
	newDocRouter := func() configurable { p, err := NewDocRouter(); require.NoError(t, err); return p }

	tests := []struct {
		name      string
		newPolicy func() configurable
	}{
		{name: "coordinator", newPolicy: func() configurable { return NewCoordinatorPolicy().(configurable) }},
		{name: "doc router", newPolicy: newDocRouter},
		{name: "index router", newPolicy: newIndexRouter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := createTestConfig()
			cfg.metrics = &metrics{}

			require.NoError(t, tc.newPolicy().configurePolicySettings(cfg))

			total := len(cfg.metrics.policyCallbacks) +
				len(cfg.metrics.connMetricCallbacks) +
				len(cfg.metrics.snapshotCallbacks)
			require.Positive(t, total, "each policy registers a metric callback")
		})
	}
}

func TestMetrics(t *testing.T) {
	t.Run("Metrics()", func(t *testing.T) {
		tp, _ := New(
			Config{
				URLs: []*url.URL{
					{Scheme: "http", Host: "foo1"},
					{Scheme: "http", Host: "foo2"},
					{Scheme: "http", Host: "foo3"},
				},
				DisableRetry: true,
			},
		)

		tp.metrics.requests.Store(3)
		tp.metrics.failures.Store(4)
		tp.metrics.incrementResponse(200)
		tp.metrics.incrementResponse(404)
		tp.metrics.incrementResponse(404)

		// Set some lifecycle counters
		tp.metrics.connectionsPromoted.Store(2)
		tp.metrics.connectionsDemoted.Store(1)
		tp.metrics.zombieConnections.Store(5)

		req, _ := http.NewRequest(http.MethodHead, "/", nil)
		resp, err := tp.Stream(req)
		if err == nil {
			defer resp.Body.Close()
		}

		m, err := tp.Metrics()
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if m.Requests != 4 {
			t.Errorf("Unexpected output, want=4, got=%d", m.Requests)
		}
		if m.Failures != 5 {
			t.Errorf("Unexpected output, want=5, got=%d", m.Failures)
		}
		if len(m.Responses) != 2 {
			t.Errorf("Unexpected output: %+v", m.Responses)
		}
		if len(m.Connections) != 3 {
			t.Errorf("Unexpected output: %+v", m.Connections)
		}

		// Verify new metrics fields
		// Note: One connection is dead after Perform() fails, so we have 2 ready, 1 dead
		if m.LiveConnections != 2 {
			t.Errorf("Expected 2 ready connections, got %d", m.LiveConnections)
		}
		if m.DeadConnections != 1 {
			t.Errorf("Expected 1 dead connection, got %d", m.DeadConnections)
		}
		if m.ConnectionsPromoted != 2 {
			t.Errorf("Expected 2 promoted connections, got %d", m.ConnectionsPromoted)
		}
		// ConnectionsDemoted includes both the test value (1) and the actual demotion from Perform() (1)
		if m.ConnectionsDemoted != 2 {
			t.Errorf("Expected 2 demoted connections, got %d", m.ConnectionsDemoted)
		}
		if m.ZombieConnections != 5 {
			t.Errorf("Expected 5 zombie connections, got %d", m.ZombieConnections)
		}
		if m.HealthChecks != 0 {
			t.Errorf("Expected 0 health checks, got %d", m.HealthChecks)
		}
		if m.ClusterHealthChecks != 0 {
			t.Errorf("Expected 0 cluster health checks, got %d", m.ClusterHealthChecks)
		}
		if m.HealthChecksSuccess != 0 {
			t.Errorf("Expected 0 successful health checks, got %d", m.HealthChecksSuccess)
		}
		if m.HealthChecksFailed != 0 {
			t.Errorf("Expected 0 failed health checks, got %d", m.HealthChecksFailed)
		}
		if m.OverloadedServers != 0 {
			t.Errorf("Expected 0 overloaded servers, got %d", m.OverloadedServers)
		}
	})

	t.Run("Metrics() surfaces lock-free timestamps end-to-end", func(t *testing.T) {
		// Proves the lock-free deadSince/overloadedAt atomics thread through
		// buildConnectionMetric into the public snapshot. DeadSince needs the
		// connection to read as dead and OverloadedSince needs the lcOverloaded
		// bit, so set both the timestamps and the matching lifecycle bits.
		tp, err := New(Config{
			URLs:         []*url.URL{{Scheme: "http", Host: "foo1"}},
			DisableRetry: true,
		})
		require.NoError(t, err)

		pool, ok := tp.mu.connectionPool.(*singleServerPool)
		require.True(t, ok, "single URL should yield a singleServerPool, got %T", tp.mu.connectionPool)
		conn := pool.connection

		deadAt := time.Date(2022, time.January, 2, 3, 4, 5, 600700800, time.UTC)
		overAt := time.Date(2022, time.January, 2, 3, 4, 6, 0, time.UTC)
		conn.mu.Lock()
		conn.storeDeadSince(deadAt)
		conn.storeOverloadedAt(overAt)
		// lcUnknown without lcActive/lcStandby => IsDead; lcOverloaded => IsOverloaded.
		conn.state.Store(int64(newConnState(lcDead | lcUnknown | lcOverloaded)))
		conn.mu.Unlock()

		m, err := tp.Metrics()
		require.NoError(t, err)
		require.Len(t, m.Connections, 1)

		cm, ok := m.Connections[0].(ConnectionMetric)
		require.True(t, ok, "connection metric should be a ConnectionMetric, got %T", m.Connections[0])
		require.True(t, cm.IsDead, "connection with lcUnknown and no active/standby bit reads as dead")
		require.True(t, cm.IsOverloaded, "lcOverloaded bit should surface as IsOverloaded")

		require.NotNil(t, cm.DeadSince, "DeadSince should be populated from the lock-free read")
		require.True(t, deadAt.Equal(*cm.DeadSince), "DeadSince want %v, got %v", deadAt, *cm.DeadSince)
		require.NotNil(t, cm.OverloadedSince, "OverloadedSince should be populated from the lock-free read")
		require.True(t, overAt.Equal(*cm.OverloadedSince), "OverloadedSince want %v, got %v", overAt, *cm.OverloadedSince)
	})

	t.Run("Metrics() races cleanly with Stream", func(t *testing.T) {
		// Snapshotting no longer takes each connection's mutex (#892), so
		// Metrics() must not race with Stream's OnSuccess/OnFailure writers.
		// Run under `go test -race`.
		tp, err := New(Config{
			URLs: []*url.URL{
				{Scheme: "http", Host: "foo1"},
				{Scheme: "http", Host: "foo2"},
				{Scheme: "http", Host: "foo3"},
			},
			DisableRetry: true,
		})
		require.NoError(t, err)

		var wg sync.WaitGroup
		const performers, snapshotters = 4, 2
		wg.Add(performers + snapshotters)
		for range performers {
			go func() {
				defer wg.Done()
				for range 200 {
					req, _ := http.NewRequest(http.MethodHead, "/", nil)
					if resp, perr := tp.Stream(req); perr == nil {
						resp.Body.Close()
					}
				}
			}()
		}
		for range snapshotters {
			go func() {
				defer wg.Done()
				for range 200 {
					if _, merr := tp.Metrics(); merr != nil {
						t.Errorf("Metrics() returned error during concurrent load: %v", merr)
						return
					}
				}
			}()
		}
		wg.Wait()
	})

	t.Run("Metrics() surfaces callback errors via errors.Join", func(t *testing.T) {
		// A failing callback is the only way Metrics() returns a non-nil error
		// for an initialized transport. Each callback kind appends to
		// callbackErrs, joined via errors.Join. Verify every kind surfaces, and
		// that multiple errors join together.
		errConn := errors.New("conn callback boom")
		errPolicy := errors.New("policy callback boom")
		errSnapshot := errors.New("snapshot callback boom")

		tests := []struct {
			name    string
			inject  func(m *metrics)
			wantErr []error
		}{
			{
				name: "connMetric callback error",
				inject: func(m *metrics) {
					m.connMetricCallbacks = append(m.connMetricCallbacks,
						func([]*Connection, []ConnectionMetric) error { return errConn })
				},
				wantErr: []error{errConn},
			},
			{
				name: "policy callback error",
				inject: func(m *metrics) {
					m.policyCallbacks = append(m.policyCallbacks,
						func() (PolicySnapshot, error) { return PolicySnapshot{}, errPolicy })
				},
				wantErr: []error{errPolicy},
			},
			{
				name: "snapshot callback error",
				inject: func(m *metrics) {
					m.snapshotCallbacks = append(m.snapshotCallbacks,
						func(*Metrics) error { return errSnapshot })
				},
				wantErr: []error{errSnapshot},
			},
			{
				name: "all callback kinds error and join",
				inject: func(m *metrics) {
					m.connMetricCallbacks = append(m.connMetricCallbacks,
						func([]*Connection, []ConnectionMetric) error { return errConn })
					m.policyCallbacks = append(m.policyCallbacks,
						func() (PolicySnapshot, error) { return PolicySnapshot{}, errPolicy })
					m.snapshotCallbacks = append(m.snapshotCallbacks,
						func(*Metrics) error { return errSnapshot })
				},
				wantErr: []error{errConn, errPolicy, errSnapshot},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tp, err := New(Config{URLs: []*url.URL{{Scheme: "http", Host: "foo1"}}})
				require.NoError(t, err)
				tc.inject(tp.metrics)

				_, err = tp.Metrics()
				require.Error(t, err)
				for _, want := range tc.wantErr {
					require.ErrorIs(t, err, want)
				}
			})
		}
	})

	t.Run("Metrics() returns nil error when callbacks succeed", func(t *testing.T) {
		// Guards the happy path of the error contract: registered callbacks that
		// all succeed must leave callbackErrs empty so errors.Join returns nil.
		tp, err := New(Config{URLs: []*url.URL{{Scheme: "http", Host: "foo1"}}})
		require.NoError(t, err)
		tp.metrics.connMetricCallbacks = append(tp.metrics.connMetricCallbacks,
			func([]*Connection, []ConnectionMetric) error { return nil })
		tp.metrics.policyCallbacks = append(tp.metrics.policyCallbacks,
			func() (PolicySnapshot, error) { return PolicySnapshot{}, nil })
		tp.metrics.snapshotCallbacks = append(tp.metrics.snapshotCallbacks,
			func(*Metrics) error { return nil })

		_, err = tp.Metrics()
		require.NoError(t, err)
	})

	t.Run("String()", func(t *testing.T) {
		// Active connection: lcReady|lcActive -- healthy, serving requests
		activeState := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		m := ConnectionMetric{
			URL:   "http://foo1",
			State: activeState,
		}
		require.Equal(t, "{http://foo1 state=ready+active (000000000101)}", m.String())

		// Dead connection: lcUnknown|lcNeedsWarmup -- awaiting resurrection
		tt, _ := time.Parse(time.RFC3339, "2010-11-11T11:00:00Z")
		m = ConnectionMetric{
			URL:       "http://foo2",
			IsDead:    true,
			Failures:  123,
			DeadSince: &tt,
			State:     ConnState{packed: int64(newConnState(lcDead | lcNeedsWarmup))},
		}

		match, err := regexp.MatchString(
			`\{http://foo2 state=unknown\+needsWarmup \(\d+\) failures=123 dead_since=Nov 11 \d+:00:00\}`,
			m.String(),
		)
		require.NoError(t, err)
		require.True(t, match, "Unexpected output: %s", m)
	})

	t.Run("incrementResponse", func(t *testing.T) {
		tests := []struct {
			name  string
			codes []int       // sequence of incrementResponse calls
			want  map[int]int // expected responsesSnapshot
		}{
			{name: "single code", codes: []int{200}, want: map[int]int{200: 1}},
			{name: "repeated and mixed codes", codes: []int{200, 404, 200}, want: map[int]int{200: 2, 404: 1}},
			{name: "below range to overflow", codes: []int{99}, want: map[int]int{statusOverflow: 1}},
			{name: "low boundary in-range", codes: []int{statusMin}, want: map[int]int{statusMin: 1}},          // 100
			{name: "high boundary in-range", codes: []int{statusMax - 1}, want: map[int]int{statusMax - 1: 1}}, // 599
			{name: "at range max to overflow", codes: []int{statusMax}, want: map[int]int{statusOverflow: 1}},  // 600
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				m := &metrics{}
				for _, code := range tc.codes {
					m.incrementResponse(code)
				}
				require.Equal(t, tc.want, m.responsesSnapshot())
			})
		}
	})

	t.Run("incrementResponse concurrent", func(t *testing.T) {
		tests := []struct {
			name    string
			code    int // status incremented from every goroutine
			wantKey int // snapshot key the count lands under
		}{
			{name: "in-range bucket", code: 200, wantKey: 200},
			{name: "overflow bucket", code: 600, wantKey: statusOverflow},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				m := &metrics{}
				const n = 500
				var wg sync.WaitGroup
				wg.Add(n)
				for range n {
					go func() {
						defer wg.Done()
						m.incrementResponse(tc.code)
					}()
				}
				wg.Wait()
				require.Equal(t, n, m.responsesSnapshot()[tc.wantKey], "concurrent increment count") // (MEASURED — count of concurrent increments)
			})
		}
	})
}

// TestMetricsDetailedSnapshot verifies the detailed path (connection
// enumeration + policy snapshots) runs unconditionally.
func TestMetricsDetailedSnapshot(t *testing.T) {
	tests := []struct {
		name         string
		urls         []*url.URL
		withRouter   bool
		wantConns    int
		wantPolicies bool
	}{
		{
			name:      "single server enumerates its connection",
			urls:      []*url.URL{{Scheme: "http", Host: "foo1"}},
			wantConns: 1,
		},
		{
			// TestMain forces OPENSEARCH_GO_ROUTER=false, so the router
			// must be built explicitly for policy callbacks to register.
			name:         "multi server with router populates policies",
			urls:         []*url.URL{{Scheme: "http", Host: "foo1"}, {Scheme: "http", Host: "foo2"}},
			withRouter:   true,
			wantConns:    2,
			wantPolicies: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{URLs: tc.urls, DisableRetry: true}
			if tc.withRouter {
				router, err := NewDefaultRouter()
				require.NoError(t, err)
				cfg.Router = router
			}
			tp, err := New(cfg)
			require.NoError(t, err)

			m, err := tp.Metrics()
			require.NoError(t, err)
			require.Len(t, m.Connections, tc.wantConns, "connections enumerated in the snapshot")
			if tc.wantPolicies {
				require.NotEmpty(t, m.Policies, "policy snapshots populated when a router is active")
			} else {
				require.Empty(t, m.Policies, "no router means no policy snapshots")
			}
		})
	}
}
