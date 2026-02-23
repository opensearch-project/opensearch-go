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
	"testing"
	"time"
)

func TestStatusConnectionPoolResurrect(t *testing.T) {
	t.Run("Mark the connection as dead and add/remove it to the lists", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = func() []*Connection {
			conn := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
			conn.mu.Lock()
			conn.markAsDeadWithLock()
			conn.mu.Unlock()
			return []*Connection{conn}
		}()

		conn := pool.mu.dead[0]
		conn.mu.Lock()
		pool.resurrectWithLock(conn)
		isDead := !conn.mu.deadSince.IsZero()
		conn.mu.Unlock()

		if isDead {
			t.Errorf("Expected connection to be ready, got dead=true")
		}

		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected no dead connections, got: %v", pool.mu.dead)
		}

		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection, got: %v", pool.mu.ready)
		}
	})

	t.Run("Short circuit removal when the connection is not in the dead list", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.dead = func() []*Connection {
			conn := &Connection{URL: &url.URL{Scheme: "http", Host: "bar"}}
			conn.mu.Lock()
			conn.markAsDeadWithLock()
			conn.mu.Unlock()
			return []*Connection{conn}
		}()

		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
		conn.mu.Lock()
		defer conn.mu.Unlock()
		conn.markAsDeadWithLock()
		pool.resurrectWithLock(conn)

		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection, got: %v", pool.mu.ready)
		}

		if len(pool.mu.dead) != 1 {
			t.Errorf("Expected 1 dead connection, got: %v", pool.mu.dead)
		}
	})

	t.Run("Schedule resurrect", func(t *testing.T) {
		// Channel to signal when health check is called
		healthCheckCalled := make(chan struct{})

		// Create round-robin selector
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      0,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      0, // Allow immediate resurrection for test
			jitterScale:                  defaultJitterScale,
			// Mock health check function that always succeeds for tests
			healthCheck: func(ctx context.Context, _ *Connection, u *url.URL) (*http.Response, error) {
				close(healthCheckCalled)
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Proto:      "HTTP/1.1",
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(http.Header),
					Body:       http.NoBody,
				}, nil
			},
		}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = func() []*Connection {
			conn := &Connection{
				URL: &url.URL{Scheme: "http", Host: "foo1"},
			}
			conn.failures.Store(100)
			conn.mu.Lock()
			conn.mu.deadSince = time.Now().UTC()
			conn.mu.deadSince = time.Now().UTC()
			conn.mu.Unlock()
			return []*Connection{conn}
		}()

		conn := pool.mu.dead[0]

		pool.scheduleResurrect(context.Background(), conn)

		// Wait for the health check to be called
		<-healthCheckCalled

		// Give the goroutine time to complete resurrection after health check
		// The goroutine needs to: reacquire locks, call resurrectWithLock(), and release locks
		time.Sleep(100 * time.Millisecond)

		pool.mu.Lock()
		defer pool.mu.Unlock()

		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection, got: %d", len(pool.mu.ready))
		}
		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected no dead connections, got: %d", len(pool.mu.dead))
		}
	})
}

func TestCalculateResurrectTimeout(t *testing.T) {
	// Helper to create a pool with the given ready/dead connection counts.
	makePool := func(nLive, nDead int) *statusConnectionPool {
		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutMax:          defaultResurrectTimeoutMax,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
			jitterScale:                  0, // Disable jitter for deterministic tests
			serverMaxNewConnsPerSec:      float64(defaultServerCoreCount) * serverMaxNewConnsPerSecMultiplier,
			clientsPerServer:             float64(defaultServerCoreCount),
		}
		pool.mu.ready = make([]*Connection, nLive)
		for i := range nLive {
			pool.mu.ready[i] = &Connection{URL: &url.URL{Scheme: "http", Host: "live"}}
		}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = make([]*Connection, nDead)
		for i := range nDead {
			pool.mu.dead[i] = &Connection{URL: &url.URL{Scheme: "http", Host: "dead"}}
		}
		return pool
	}

	makeConn := func(failures int64) *Connection {
		c := &Connection{URL: &url.URL{Scheme: "http", Host: "test"}}
		c.failures.Store(failures)
		return c
	}

	t.Run("All healthy: full backoff applies", func(t *testing.T) {
		// 3 ready, 0 dead -> healthRatio = 1.0, no rate limit (0 dead)
		pool := makePool(3, 0)
		conn := makeConn(1)

		timeout := pool.calculateResurrectTimeout(conn)
		// failures=1: baseTimeout = 5s * 2^0 = 5s, healthRatio = 1.0 -> 5s
		if timeout != 5*time.Second {
			t.Errorf("Expected 5s, got %s", timeout)
		}
	})

	t.Run("Partially degraded: scaled by health ratio", func(t *testing.T) {
		// 2 ready, 1 dead -> healthRatio = 2/3, rateLimit = 1/30 = 33ms
		pool := makePool(2, 1)
		conn := makeConn(1)

		timeout := pool.calculateResurrectTimeout(conn)
		// healthTimeout = 5s * 2/3 = 3.333s, dominates over 33ms rate limit
		ratio := 2.0 / 3.0
		expected := time.Duration(float64(5*time.Second) * ratio)
		if timeout != expected {
			t.Errorf("Expected %s, got %s", expected, timeout)
		}
	})

	t.Run("All dead small cluster: minimum floor dominates", func(t *testing.T) {
		// 0 ready, 3 dead -> healthTimeout = 0, rateLimit = (0*8)/32 = 0, min = 500ms
		pool := makePool(0, 3)
		conn := makeConn(1)

		timeout := pool.calculateResurrectTimeout(conn)
		// max(0, 0, 500ms) = 500ms -- aggressive, we need capacity
		if timeout != defaultMinimumResurrectTimeout {
			t.Errorf("Expected %s (minimum floor), got %s", defaultMinimumResurrectTimeout, timeout)
		}
	})

	t.Run("All dead large cluster: minimum floor dominates", func(t *testing.T) {
		// 0 ready, 150 dead -> healthTimeout = 0, rateLimit = (0*8)/32 = 0, min = 500ms
		pool := makePool(0, 150)
		conn := makeConn(1)

		timeout := pool.calculateResurrectTimeout(conn)
		// All dead = most aggressive (minimum floor)
		if timeout != defaultMinimumResurrectTimeout {
			t.Errorf("Expected %s (minimum floor), got %s", defaultMinimumResurrectTimeout, timeout)
		}
	})

	t.Run("All dead: rate limit scales with cluster size", func(t *testing.T) {
		// At 30 checks/sec max rate, interval = deadNodes / 30
		tests := []struct {
			dead    int
			want    time.Duration
			explain string
		}{
			{3, 500 * time.Millisecond, "all dead, floor dominates"},
			{15, 500 * time.Millisecond, "all dead, floor dominates"},
			{150, 500 * time.Millisecond, "all dead, floor dominates"},
		}
		for _, tt := range tests {
			pool := makePool(0, tt.dead)
			conn := makeConn(1)
			timeout := pool.calculateResurrectTimeout(conn)
			if timeout != tt.want {
				t.Errorf("dead=%d (%s): expected %s, got %s",
					tt.dead, tt.explain, tt.want, timeout)
			}
		}
	})

	t.Run("Mostly healthy large cluster: rate limit from TLS pressure", func(t *testing.T) {
		// 149 ready, 1 dead -> healthRatio = 149/150, rateLimit = (149*8)/32 = 37.25s -> capped 30s
		pool := makePool(149, 1)
		conn := makeConn(6) // Past factor cutoff: base capped at 30s

		timeout := pool.calculateResurrectTimeout(conn)
		// healthTimeout = 30s * 149/150 = ~29.8s
		// rateLimited = (149*8)/32 = 37.25s -> capped at 30s
		// max(29.8s, 30s, 500ms) = 30s (rate limit dominates due to TLS pressure)
		if timeout != defaultResurrectTimeoutMax {
			t.Errorf("Expected %s (rate limit capped at max), got %s", defaultResurrectTimeoutMax, timeout)
		}
	})

	t.Run("Recovering cluster: rate limit increases with ready count", func(t *testing.T) {
		// As servers recover, rate limit grows (more TLS handshake pressure)
		// rateLimit = (liveNodes * 8) / 32 = liveNodes / 4
		tests := []struct {
			ready   int
			dead    int
			minWant time.Duration
			explain string
		}{
			{0, 150, 500 * time.Millisecond, "all dead: minimum floor, aggressive"},
			{10, 140, 2500 * time.Millisecond, "10 ready: (10*8)/32 = 2.5s, backing off"},
			{50, 100, 12500 * time.Millisecond, "50 ready: (50*8)/32 = 12.5s, conservative"},
			{100, 50, 25 * time.Second, "100 ready: (100*8)/32 = 25s"},
		}
		for _, tt := range tests {
			pool := makePool(tt.ready, tt.dead)
			conn := makeConn(6) // base capped at 30s
			timeout := pool.calculateResurrectTimeout(conn)
			if timeout != tt.minWant {
				t.Errorf("ready=%d, dead=%d (%s): expected %s, got %s",
					tt.ready, tt.dead, tt.explain, tt.minWant, timeout)
			}
		}
	})

	t.Run("Exponential backoff progression", func(t *testing.T) {
		// 1 ready, 1 dead -> healthRatio = 0.5, rateLimit = (1*8)/32 = 250ms
		pool := makePool(1, 1)

		var prev time.Duration
		for failures := int64(1); failures <= 6; failures++ {
			conn := makeConn(failures)
			timeout := pool.calculateResurrectTimeout(conn)
			if timeout < prev {
				t.Errorf("Expected non-decreasing timeout: failures=%d got %s, previous was %s",
					failures, timeout, prev)
			}
			if failures > 1 && failures <= 4 && timeout <= prev {
				// Before hitting the cap, timeouts must strictly increase
				t.Errorf("Expected strictly increasing timeout before cap: failures=%d got %s, previous was %s",
					failures, timeout, prev)
			}
			prev = timeout
		}
	})

	t.Run("Backoff caps at resurrectTimeoutMax", func(t *testing.T) {
		pool := makePool(1, 0) // healthRatio = 1.0
		conn := makeConn(100)  // Way past cutoff

		timeout := pool.calculateResurrectTimeout(conn)
		if timeout > defaultResurrectTimeoutMax {
			t.Errorf("Expected timeout <= %s (max), got %s", defaultResurrectTimeoutMax, timeout)
		}
	})

	t.Run("Minimum timeout enforced", func(t *testing.T) {
		pool := makePool(100, 1)
		pool.resurrectTimeoutInitial = 1 * time.Nanosecond
		conn := makeConn(1)

		timeout := pool.calculateResurrectTimeout(conn)
		if timeout < defaultMinimumResurrectTimeout {
			t.Errorf("Expected timeout >= %s (minimum), got %s", defaultMinimumResurrectTimeout, timeout)
		}
	})

	t.Run("Jitter adds randomness without exceeding bounds", func(t *testing.T) {
		pool := makePool(1, 1) // healthRatio = 0.5
		pool.jitterScale = defaultJitterScale
		conn := makeConn(1)

		// base=5s, ratio=0.5 -> healthTimeout=2.5s, rateLimit=1/30=33ms, min=500ms
		// finalBase = max(2.5s, 33ms, 500ms) = 2.5s
		// jitter up to 0.5*2.5s=1.25s -> range [2.5s, 3.75s]
		minExpected := 2500 * time.Millisecond
		maxExpected := 3750 * time.Millisecond

		for range 50 {
			timeout := pool.calculateResurrectTimeout(conn)
			if timeout < minExpected || timeout > maxExpected {
				t.Errorf("Timeout %s outside expected range [%s, %s]",
					timeout, minExpected, maxExpected)
			}
		}
	})
}
