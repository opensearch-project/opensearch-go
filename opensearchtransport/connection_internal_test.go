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
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"
)

func TestSingleConnectionPoolNext(t *testing.T) {
	t.Run("Single URL", func(t *testing.T) {
		pool := &singleConnectionPool{
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

func TestSingleConnectionPoolOnSuccess(t *testing.T) {
	t.Run("Noop", func(t *testing.T) {
		pool := &singleConnectionPool{
			connection: &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}},
		}

		// OnSuccess should be a no-op and not return an error
		pool.OnSuccess(&Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}})
		// Test passes if no panic or error occurs
	})
}

func TestSingleConnectionPoolURLs(t *testing.T) {
	t.Run("Return single URL", func(t *testing.T) {
		expectedURL := &url.URL{Scheme: "http", Host: "foo1"}
		pool := &singleConnectionPool{
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

func TestSingleConnectionPoolConnections(t *testing.T) {
	t.Run("Return single connection", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
		pool := &singleConnectionPool{
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

func TestSingleConnectionPoolOnFailure(t *testing.T) {
	t.Run("Noop", func(t *testing.T) {
		pool := &singleConnectionPool{
			connection: &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}},
		}

		if err := pool.OnFailure(&Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}); err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
	})
}

func TestStatusConnectionPoolNext(t *testing.T) {
	t.Run("No URL", func(t *testing.T) {
		pool := &statusConnectionPool{}

		c, err := pool.Next()
		if err == nil {
			t.Errorf("Expected error, but got: %s", c.URL)
		}
	})

	t.Run("Two URLs", func(t *testing.T) {
		var c *Connection

		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
		}

		c, _ = pool.Next()

		if c.URL.String() != "http://foo1" {
			t.Errorf("Unexpected URL, want=foo1, got=%s", c.URL)
		}

		c, _ = pool.Next()
		if c.URL.String() != "http://foo2" {
			t.Errorf("Unexpected URL, want=http://foo2, got=%s", c.URL)
		}

		c, _ = pool.Next()
		if c.URL.String() != "http://foo1" {
			t.Errorf("Unexpected URL, want=http://foo1, got=%s", c.URL)
		}
	})

	t.Run("Three URLs", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
			{URL: &url.URL{Scheme: "http", Host: "foo3"}},
		}

		var expected string
		for i := range 11 {
			c, err := pool.Next()
			if err != nil {
				t.Errorf("Unexpected error: %s", err)
			}

			switch i % len(pool.mu.live) {
			case 0:
				expected = "http://foo1"
			case 1:
				expected = "http://foo2"
			case 2:
				expected = "http://foo3"
			default:
				t.Fatalf("Unexpected i %% 3: %d", i%3)
			}

			if c.URL.String() != expected {
				t.Errorf("Unexpected URL, want=%s, got=%s", expected, c.URL)
			}
		}
	})

	t.Run("Resurrect dead connection when no live is available", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{}
		pool.mu.dead = func() []*Connection {
			conn1 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
			conn1.failures.Store(3)
			conn1.mu.deadSince = time.Now().UTC() // Mark as dead
			conn2 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo2"}}
			conn2.failures.Store(1)
			conn2.mu.deadSince = time.Now().UTC() // Mark as dead
			return []*Connection{conn1, conn2}
		}()

		c, err := pool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		if c == nil {
			t.Errorf("Expected connection, got nil: %v", c)
		}

		if c.URL.String() != "http://foo1" {
			t.Errorf("Expected <http://foo1>, got: %s", c.URL.String())
		}

		c.mu.Lock()
		isDead := !c.mu.deadSince.IsZero()
		c.mu.Unlock()
		if !isDead {
			t.Errorf("Expected connection to be dead (zombie), got: %v", c)
		}

		if len(pool.mu.live) != 0 {
			t.Errorf("Expected 0 connections in live list, got: %v", pool.mu.live)
		}

		if len(pool.mu.dead) != 2 {
			t.Errorf("Expected 2 connections in dead list, got: %v", pool.mu.dead)
		}
	})
}

func TestStatusConnectionPoolNextResurrectDead(t *testing.T) {
	t.Run("Resurrect dead connection when no live is available", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{}
		pool.mu.dead = func() []*Connection {
			conn1 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo1"}}
			conn1.failures.Store(3)
			conn1.mu.deadSince = time.Now().UTC() // Mark as dead
			conn2 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo2"}}
			conn2.failures.Store(1)
			conn2.mu.deadSince = time.Now().UTC() // Mark as dead
			return []*Connection{conn1, conn2}
		}()

		c, err := pool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		if c == nil {
			t.Errorf("Expected connection, got nil: %v", c)
		}

		if c.URL.String() != "http://foo1" {
			t.Errorf("Expected <http://foo1>, got: %s", c.URL.String())
		}

		c.mu.Lock()
		isDead := !c.mu.deadSince.IsZero()
		c.mu.Unlock()
		if !isDead {
			t.Errorf("Expected connection to be dead (zombie), got: %v", c)
		}

		if len(pool.mu.live) != 0 {
			t.Errorf("Expected 0 connections in live list, got: %v", pool.mu.live)
		}

		if len(pool.mu.dead) != 2 {
			t.Errorf("Expected 2 connections in dead list, got: %v", pool.mu.dead)
		}
	})

	t.Run("No connection available", func(t *testing.T) {
		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{}
		pool.mu.dead = []*Connection{}

		c, err := pool.Next()
		if err == nil {
			t.Errorf("Expected error, but got: %s", c.URL)
		}

		if err.Error() != ErrNoConnections.Error() {
			t.Errorf("Expected %q error, got: %s", ErrNoConnections.Error(), err.Error())
		}
	})
}

func TestStatusConnectionPoolOnSuccess(t *testing.T) {
	t.Run("Move connection to live list and mark it as healthy", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		// Initialize pool with proper timeout values for consistency
		pool := &statusConnectionPool{
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
			t.Errorf("Expected the connection to be live; %s", conn)
		}

		conn.mu.Lock()
		deadSince := conn.mu.deadSince
		conn.mu.Unlock()
		if !deadSince.IsZero() {
			t.Errorf("Unexpected value for DeadSince: %s", deadSince)
		}

		if len(pool.mu.live) != 1 {
			t.Errorf("Expected 1 live connection, got: %d", len(pool.mu.live))
		}

		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected 0 dead connections, got: %d", len(pool.mu.dead))
		}
	})
}

func TestStatusConnectionPoolOnFailure(t *testing.T) {
	t.Run("Remove connection, mark it, and sort dead connections", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		// Initialize pool with proper timeout values to prevent immediate resurrection
		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		}
		pool.mu.live = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
		}
		pool.mu.dead = func() []*Connection {
			conn3 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo3"}}
			// failures is 0 by default
			conn4 := &Connection{URL: &url.URL{Scheme: "http", Host: "foo4"}}
			conn4.failures.Store(99)
			return []*Connection{conn3, conn4}
		}()

		conn := pool.mu.live[0]

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
		if len(pool.mu.live) != 1 {
			t.Errorf("Expected 1 live connection, got: %d", len(pool.mu.live))
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

		// Initialize pool with proper timeout values to prevent immediate resurrection
		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		}
		pool.mu.live = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
			{URL: &url.URL{Scheme: "http", Host: "foo3"}},
		}

		conn := pool.mu.live[0]
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

func TestStatusConnectionPoolResurrect(t *testing.T) {
	t.Run("Mark the connection as dead and add/remove it to the lists", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.live = []*Connection{}
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
			t.Errorf("Expected connection to be live, got dead=true")
		}

		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected no dead connections, got: %v", pool.mu.dead)
		}

		if len(pool.mu.live) != 1 {
			t.Errorf("Expected 1 live connection, got: %v", pool.mu.live)
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

		if len(pool.mu.live) != 1 {
			t.Errorf("Expected 1 live connection, got: %v", pool.mu.live)
		}

		if len(pool.mu.dead) != 1 {
			t.Errorf("Expected 1 dead connection, got: %v", pool.mu.dead)
		}
	})

	t.Run("Schedule resurrect", func(t *testing.T) {
		// Channel to signal when resurrection is complete
		done := make(chan struct{})

		// Create round-robin selector
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      0,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      0, // Allow immediate resurrection for test
			jitterScale:                  defaultJitterScale,
			// Mock health check function that always succeeds for tests
			healthCheck: func(ctx context.Context, u *url.URL) (*http.Response, error) {
				t.Logf("Health check called for %s", u)
				// Signal completion after health check
				defer close(done)
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
		pool.mu.live = []*Connection{}
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
		t.Logf("Starting resurrection test - dead connections: %d", len(pool.mu.dead))

		conn.mu.RLock()
		deadSince := conn.mu.deadSince
		conn.mu.RUnlock()

		pool.scheduleResurrect(conn, deadSince)

		// Wait for the resurrection to actually complete
		<-done

		pool.mu.Lock()
		defer pool.mu.Unlock()

		t.Logf("After resurrection - live: %d, dead: %d", len(pool.mu.live), len(pool.mu.dead))

		if len(pool.mu.live) != 1 {
			t.Errorf("Expected 1 live connection, got: %d", len(pool.mu.live))
		}
		if len(pool.mu.dead) != 0 {
			t.Errorf("Expected no dead connections, got: %d", len(pool.mu.dead))
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
