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
		name         string
		enable       bool
		wantDetailed bool
	}{
		{name: "disabled still allocates", enable: false, wantDetailed: false},
		{name: "enabled sets detailed", enable: true, wantDetailed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp, err := New(Config{
				URLs:          []*url.URL{{Scheme: "http", Host: "foo1"}},
				EnableMetrics: tc.enable,
			})
			require.NoError(t, err)
			require.NotNil(t, tp.metrics, "metrics struct always allocated")
			require.Equal(t, tc.wantDetailed, tp.metrics.detailed, "detailed matches EnableMetrics")
		})
	}
}

func TestDetailedCallbacksGated(t *testing.T) {
	tests := []struct {
		name    string
		enable  bool
		wantCBs bool
	}{
		{name: "detailed off registers no callbacks", enable: false, wantCBs: false},
		{name: "detailed on registers callbacks", enable: true, wantCBs: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The router is off by default in v4, so build it explicitly or no
			// policies (and no callbacks) would be registered.
			router, err := NewDefaultRouter()
			require.NoError(t, err)
			tp, err := New(Config{
				URLs: []*url.URL{
					{Scheme: "http", Host: "foo1"},
					{Scheme: "http", Host: "foo2"},
				},
				EnableMetrics: tc.enable,
				Router:        router,
			})
			require.NoError(t, err)
			total := len(tp.metrics.policyCallbacks) +
				len(tp.metrics.connMetricCallbacks) +
				len(tp.metrics.snapshotCallbacks)
			if tc.wantCBs {
				require.Positive(t, total, "detailed on should register callbacks")
			} else {
				require.Zero(t, total, "detailed off should register no callbacks")
			}
		})
	}
}

func TestDetailedCallbacksPerPolicy(t *testing.T) {
	newDocRouter := func() policyConfigurable { p, err := NewDocRouter(); require.NoError(t, err); return p }
	newIndexRouter := func() policyConfigurable { p, err := NewIndexRouter(); require.NoError(t, err); return p }

	tests := []struct {
		name      string
		newPolicy func() policyConfigurable
	}{
		{name: "coordinator", newPolicy: func() policyConfigurable { return NewCoordinatorPolicy().(policyConfigurable) }},
		{name: "doc router", newPolicy: newDocRouter},
		{name: "index router", newPolicy: newIndexRouter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestPolicyConfig(t)
			cfg.metrics = &metrics{detailed: true}

			require.NoError(t, tc.newPolicy().configurePolicySettings(cfg))

			total := len(cfg.metrics.policyCallbacks) +
				len(cfg.metrics.connMetricCallbacks) +
				len(cfg.metrics.snapshotCallbacks)
			require.Positive(t, total, "each policy registers a metric callback when detailed is on")
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
				DisableRetry:  true,
				EnableMetrics: true,
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
		resp, err := tp.Perform(req)
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

	t.Run("Metrics() counters on, detailed off", func(t *testing.T) {
		tp, _ := New(Config{
			URLs:         []*url.URL{{Scheme: "http", Host: "foo1"}},
			DisableRetry: true,
			// EnableMetrics unset -> detailed off
		})

		req, _ := http.NewRequest(http.MethodHead, "/", nil)
		if resp, err := tp.Perform(req); err == nil {
			resp.Body.Close()
		}

		m, err := tp.Metrics()
		require.NoError(t, err, "Metrics returns the always-on counters without error")
		require.GreaterOrEqual(t, m.Requests, 1, "request counter populated with detailed off")
		require.Empty(t, m.Connections, "detailed-only connection enumeration absent when detailed off")
		require.Nil(t, m.Policies, "detailed-only policy snapshots absent when detailed off")
	})

	t.Run("Metrics() detailed on enumerates single-server connection", func(t *testing.T) {
		tp, err := New(Config{
			URLs:          []*url.URL{{Scheme: "http", Host: "foo1"}},
			DisableRetry:  true,
			EnableMetrics: true,
		})
		require.NoError(t, err)

		m, err := tp.Metrics()
		require.NoError(t, err)
		require.Len(t, m.Connections, 1, "single-server connection enumerated on the detailed path")
		require.Equal(t, 1, m.LiveConnections, "single-server connection counted live")
	})

	t.Run("Metrics() errors when metrics struct is absent", func(t *testing.T) {
		// Defensive path: a custom transport could embed *Client without the
		// standard constructor, leaving metrics nil.
		c := &Client{}
		_, err := c.Metrics()
		require.Error(t, err, "nil metrics struct yields an error")
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
}

func TestIncrementResponse(t *testing.T) {
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
}

func TestIncrementResponseConcurrent(t *testing.T) {
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
}
