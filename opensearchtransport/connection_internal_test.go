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
	"math"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSingleServerPoolNext(t *testing.T) {
	t.Run("Single URL", func(t *testing.T) {
		pool := &singleServerPool{
			connection: &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}},
		}

		for range 7 {
			c, err := pool.Next()
			if err != nil {
				t.Errorf("Unexpected error: %s", err)
			}

			if c.URL.String() != "http://foo1" {
				t.Errorf("Unexpected URL, want=http://foo1, got=%s", c.URL)
			}
		}
	})
}

func TestSingleServerPoolOnSuccess(t *testing.T) {
	t.Run("Noop", func(t *testing.T) {
		pool := &singleServerPool{
			connection: &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}},
		}

		// OnSuccess should be a no-op and not return an error
		pool.OnSuccess(&Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}})
		// Test passes if no panic or error occurs
	})
}

func TestSingleServerPoolURLs(t *testing.T) {
	t.Run("Return single URL", func(t *testing.T) {
		expectedURL := &url.URL{Scheme: "http", Host: "foo1"}
		pool := &singleServerPool{
			connection: &Connection{URL: expectedURL},
		}

		urls := pool.URLs()
		if len(urls) != 1 {
			t.Errorf("Expected 1 URL, got %d", len(urls))
		}

		if urls[0].String() != expectedURL.String() {
			t.Errorf("Expected %s, got %s", expectedURL.String(), urls[0].String())
		}
	})
}

func TestSingleServerPoolConnections(t *testing.T) {
	t.Run("Return single connection", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
		pool := &singleServerPool{
			connection: conn,
		}

		connections := pool.connections()
		if len(connections) != 1 {
			t.Errorf("Expected 1 connection, got %d", len(connections))
		}

		if connections[0] != conn {
			t.Errorf("Expected same connection instance")
		}
	})
}

func TestSingleServerPoolOnFailure(t *testing.T) {
	t.Run("Noop", func(t *testing.T) {
		pool := &singleServerPool{
			connection: &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}},
		}

		if err := pool.OnFailure(&Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}); err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
	})
}

func TestMultiServerPoolOnSuccess(t *testing.T) {
	t.Run("Move connection to ready list and mark it as healthy", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		// Initialize pool with proper timeout values for consistency
		pool := &multiServerPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		}
		pool.mu.dead = func() []*Connection {
			conn := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
			conn.failures.Store(3)
			conn.mu.Lock()
			conn.markAsDeadWithLock()
			conn.mu.Unlock()
			return []*Connection{conn}
		}()

		conn := pool.mu.dead[0]

		pool.OnSuccess(conn)

		conn.mu.Lock()
		isDead := !conn.mu.deadSince.IsZero()
		conn.mu.Unlock()
		if isDead {
			t.Errorf("Expected the connection to be ready; %s", conn)
		}

		conn.mu.Lock()
		deadSince := conn.mu.deadSince
		conn.mu.Unlock()
		if !deadSince.IsZero() {
			t.Errorf("Unexpected value for DeadSince: %s", deadSince)
		}

		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection, got: %d", len(pool.mu.ready))
		}

		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected 0 dead connections, got: %d", len(pool.mu.dead))
		}
	})
}

func TestMultiServerPoolOnFailure(t *testing.T) {
	t.Run("Remove connection, mark it, and sort dead connections", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		// Initialize pool with health check that prevents resurrection during test
		pool := &multiServerPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
			jitterScale:                  defaultJitterScale,
			// Health check that always fails to prevent automatic resurrection during test
			healthCheck: func(context.Context, *Connection, *url.URL) (*http.Response, error) {
				return nil, errors.New("health check disabled for test")
			},
		}
		pool.mu.ready = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
		}
		for _, conn := range pool.mu.ready {
			conn.state.Store(int64(newConnState(lcActive)))
		}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = func() []*Connection {
			conn3 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo3"}}
			// failures is 0 by default
			conn4 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo4"}}
			conn4.failures.Store(99)
			return []*Connection{conn3, conn4}
		}()

		conn := pool.mu.ready[0]

		if err := pool.OnFailure(conn); err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		conn.mu.Lock()
		isDead := !conn.mu.deadSince.IsZero()
		deadSince := conn.mu.deadSince
		conn.mu.Unlock()

		if !isDead {
			t.Errorf("Expected the connection to be dead")
		}

		if deadSince.IsZero() {
			t.Errorf("Unexpected value for DeadSince: %s", deadSince)
		}

		pool.mu.Lock()
		defer pool.mu.Unlock()
		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection, got: %d", len(pool.mu.ready))
		}

		if len(pool.mu.dead) != 3 {
			t.Errorf("Expected 3 dead connections, got: %d", len(pool.mu.dead))
		}

		expected := []string{
			"http://foo4",
			"http://foo1",
			"http://foo3",
		}

		for i, u := range expected {
			if pool.mu.dead[i].URL.String() != u {
				t.Errorf("Unexpected value for item %d in pool.mu.dead: %s", i, pool.mu.dead[i].URL.String())
			}
		}
	})

	t.Run("Short circuit when the connection is already dead", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		// Initialize pool with health check that prevents resurrection during test
		pool := &multiServerPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
			jitterScale:                  defaultJitterScale,
			// Health check that always fails to prevent automatic resurrection during test
			healthCheck: func(context.Context, *Connection, *url.URL) (*http.Response, error) {
				return nil, errors.New("health check disabled for test")
			},
		}
		pool.mu.ready = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
			{URL: &url.URL{Scheme: "http", Host: "foo3"}},
		}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = []*Connection{}

		for _, c := range pool.mu.ready {
			c.state.Store(int64(newConnState(lcActive)))
		}

		conn := pool.mu.ready[0]
		conn.state.Store(int64(newConnState(lcDead)))
		conn.mu.Lock()
		conn.mu.deadSince = time.Now().UTC()
		conn.mu.Unlock()

		if err := pool.OnFailure(conn); err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected the dead list to be empty, got: %v", pool.mu.dead)
		}
	})
}

func TestConnection(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		conn := &Connection{
			URL: &url.URL{Scheme: "http", Host: "foo1"},
		}
		conn.failures.Store(10)
		conn.mu.Lock()
		conn.mu.deadSince = time.Now().UTC()
		conn.mu.deadSince = time.Now().UTC()
		conn.mu.Unlock()

		match, err := regexp.MatchString(
			`<http://foo1> dead=true age=.+ failures=10`,
			conn.String(),
		)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if !match {
			t.Errorf("Unexpected output: %s", conn.String())
		}
	})
}

// newTestPolicyConfig creates a policyConfig with default timeout settings suitable for testing.
// This config can be used with policy.configurePolicySettings() in tests that create policies directly.
//
// This function is only intended for use in tests and should not be used in production code.
func newTestPolicyConfig(t *testing.T) policyConfig {
	t.Helper()
	return policyConfig{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
		jitterScale:                  defaultJitterScale,
		serverMaxNewConnsPerSec:      float64(defaultServerCoreCount) * serverMaxNewConnsPerSecMultiplier,
		clientsPerServer:             float64(defaultServerCoreCount),
	}
}

// configureTestPolicySettings configures policy settings using test defaults.
// This is a convenience function for tests that create policies directly without going through
// the client initialization flow.
//
// This function is only intended for use in tests and should not be used in production code.
func configureTestPolicySettings(t *testing.T, policy Policy) error {
	t.Helper()
	if configurablePolicy, ok := policy.(policyConfigurable); ok {
		return configurablePolicy.configurePolicySettings(newTestPolicyConfig(t))
	}
	return nil
}

func TestPolicySnapshot_HealthCheckingCount(t *testing.T) {
	t.Parallel()

	conns := make([]*Connection, 4)
	for i := range conns {
		conns[i] = &Connection{URL: &url.URL{Scheme: "http", Host: "node" + string(rune('0'+i)) + ":9200"}}
	}

	// 2 in ready (1 health-checking), 2 in dead (1 health-checking)
	conns[0].state.Store(int64(newConnState(lcReady | lcActive)))
	conns[1].state.Store(int64(newConnState(lcReady | lcActive | lcHealthChecking)))
	conns[2].state.Store(int64(newConnState(lcDead)))
	conns[3].state.Store(int64(newConnState(lcDead | lcHealthChecking)))

	cp := &multiServerPool{
		name:          "test",
		activeListCap: 2,
	}
	cp.mu.ready = conns[:2]
	cp.mu.activeCount = 2
	cp.mu.dead = conns[2:]

	snap := cp.snapshot()
	if snap.HealthCheckingCount != 2 {
		t.Fatalf("expected HealthCheckingCount=2, got %d", snap.HealthCheckingCount)
	}
	if snap.ActiveCount != 2 {
		t.Fatalf("expected ActiveCount=2, got %d", snap.ActiveCount)
	}
	if snap.DeadCount != 2 {
		t.Fatalf("expected DeadCount=2, got %d", snap.DeadCount)
	}
}

func TestWeightedPoolDuplicatePointers(t *testing.T) {
	makeWeightedConn := func(name string, weight int) *Connection {
		u, _ := url.Parse("http://" + name)
		c := &Connection{URL: u, Name: name}
		c.weight.Store(int32(min(weight, math.MaxInt32))) //nolint:gosec // test values are small
		c.state.Store(int64(newConnState(lcReady | lcActive)))
		return c
	}

	t.Run("appendToReadyActiveWithLock inserts weight copies", func(t *testing.T) {
		pool := &multiServerPool{name: "test"}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{}

		c1 := makeWeightedConn("node1", 1)
		c2 := makeWeightedConn("node2", 3)

		pool.mu.Lock()
		pool.appendToReadyActiveWithLock(c1)
		pool.appendToReadyActiveWithLock(c2)
		pool.mu.Unlock()

		require.Equal(t, 4, pool.mu.activeCount) // 1 + 3
		require.Len(t, pool.mu.ready, 4)

		// Count occurrences
		c1Count, c2Count := 0, 0
		for _, c := range pool.mu.ready {
			switch c.Name {
			case "node1":
				c1Count++
			case "node2":
				c2Count++
			}
		}
		require.Equal(t, 1, c1Count)
		require.Equal(t, 3, c2Count)
	})

	t.Run("removeFromReadyWithLock removes all copies", func(t *testing.T) {
		pool := &multiServerPool{name: "test"}

		c1 := makeWeightedConn("node1", 1)
		c2 := makeWeightedConn("node2", 3)

		pool.mu.ready = []*Connection{c1, c2, c2, c2}
		pool.mu.activeCount = 4
		pool.mu.dead = []*Connection{}

		pool.mu.Lock()
		pool.removeFromReadyWithLock(c2)
		pool.mu.Unlock()

		require.Equal(t, 1, pool.mu.activeCount)
		require.Len(t, pool.mu.ready, 1)
		require.Equal(t, "node1", pool.mu.ready[0].Name)
	})

	t.Run("appendToReadyStandbyWithLock appends weight copies", func(t *testing.T) {
		pool := &multiServerPool{name: "test"}

		c1 := makeWeightedConn("active", 1)
		c2 := makeWeightedConn("standby", 2)

		pool.mu.ready = []*Connection{c1}
		pool.mu.activeCount = 1
		pool.mu.dead = []*Connection{}

		pool.mu.Lock()
		pool.appendToReadyStandbyWithLock(c2)
		pool.mu.Unlock()

		require.Equal(t, 1, pool.mu.activeCount)
		require.Len(t, pool.mu.ready, 3) // 1 active + 2 standby
	})

	t.Run("weighted round-robin distribution", func(t *testing.T) {
		pool := &multiServerPool{name: "test"}
		pool.mu.ready = []*Connection{}
		pool.mu.dead = []*Connection{}

		c1 := makeWeightedConn("small", 1) // 1 copy
		c2 := makeWeightedConn("big", 2)   // 2 copies

		pool.mu.Lock()
		pool.appendToReadyActiveWithLock(c1)
		pool.appendToReadyActiveWithLock(c2)
		pool.mu.Unlock()

		require.Equal(t, 3, pool.mu.activeCount) // 1 + 2

		// Run 300 round-robin selections and verify ~1:2 ratio
		hits := map[string]int{}
		for i := range 300 {
			idx := i % pool.mu.activeCount
			hits[pool.mu.ready[idx].Name]++
		}
		require.Equal(t, 100, hits["small"])
		require.Equal(t, 200, hits["big"])
	})

	t.Run("effectiveWeight defaults zero to 1", func(t *testing.T) {
		c := &Connection{}
		require.Equal(t, 1, c.effectiveWeight())

		c.weight.Store(3)
		require.Equal(t, 3, c.effectiveWeight())

		c.weight.Store(-1)
		require.Equal(t, 1, c.effectiveWeight())
	})
}
