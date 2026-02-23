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
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
		pool.mu.ready = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
		}
		for _, conn := range pool.mu.ready {
			conn.state.Store(int64(newConnState(lcActive)))
		}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = []*Connection{}

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
		pool.mu.ready = []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "foo1"}},
			{URL: &url.URL{Scheme: "http", Host: "foo2"}},
			{URL: &url.URL{Scheme: "http", Host: "foo3"}},
		}
		for _, conn := range pool.mu.ready {
			conn.state.Store(int64(newConnState(lcActive)))
		}
		pool.mu.activeCount = len(pool.mu.ready)
		pool.mu.dead = []*Connection{}

		var expected string
		for i := range 11 {
			c, err := pool.Next()
			if err != nil {
				t.Errorf("Unexpected error: %s", err)
			}

			switch i % len(pool.mu.ready) {
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

	t.Run("Resurrect dead connection when no ready connection is available", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = len(pool.mu.ready)
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

		if len(pool.mu.ready) != 0 {
			t.Errorf("Expected 0 connections in ready list, got: %v", pool.mu.ready)
		}

		if len(pool.mu.dead) != 2 {
			t.Errorf("Expected 2 connections in dead list, got: %v", pool.mu.dead)
		}
	})
}

func TestStatusConnectionPoolNextResurrectDead(t *testing.T) {
	t.Run("Resurrect dead connection when no ready connection is available", func(t *testing.T) {
		s := &roundRobinSelector{}
		s.curr.Store(-1)

		pool := &statusConnectionPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = len(pool.mu.ready)
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

		if len(pool.mu.ready) != 0 {
			t.Errorf("Expected 0 connections in ready list, got: %v", pool.mu.ready)
		}

		if len(pool.mu.dead) != 2 {
			t.Errorf("Expected 2 connections in dead list, got: %v", pool.mu.dead)
		}
	})

	t.Run("No connection available", func(t *testing.T) {
		pool := &statusConnectionPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = len(pool.mu.ready)
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

func TestNextWithEviction(t *testing.T) {
	t.Run("evicts externally demoted then falls to standby", func(t *testing.T) {
		demoted := newActiveConn("demoted")
		// Externally kill demoted (no position bits)
		demoted.state.Store(int64(newConnState(lcDead)))
		demoted.mu.Lock()
		demoted.mu.deadSince = time.Now()
		demoted.mu.Unlock()

		standby := newStandbyConn("s1")

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{demoted, standby}
		pool.mu.activeCount = 1 // only demoted is active
		pool.mu.dead = []*Connection{}

		// Next() hits demoted, detects no position bits, enters nextWithEviction.
		// Eviction removes demoted, falls through to standby.
		c, err := pool.Next()
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, "s1", c.URL.Host)

		// Demoted should be in dead
		require.Len(t, pool.mu.dead, 1)
		require.Equal(t, "demoted", pool.mu.dead[0].URL.Host)
	})

	t.Run("all externally demoted falls to standby", func(t *testing.T) {
		demoted1 := newActiveConn("d1")
		demoted1.state.Store(int64(newConnState(lcDead)))
		demoted2 := newActiveConn("d2")
		demoted2.state.Store(int64(newConnState(lcDead)))
		standby := newStandbyConn("s1")

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{demoted1, demoted2, standby}
		pool.mu.activeCount = 2
		pool.mu.dead = []*Connection{}

		c, err := pool.Next()
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, "s1", c.URL.Host)
	})
}

func TestEvictExternallyDemotedWithLock(t *testing.T) {
	t.Run("moves connection from ready to dead and notifies observer", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		// Mark a1 as externally dead
		a1.state.Store(int64(newConnState(lcDead)))

		obs := newRecordingObserver()
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{a1, a2}
		pool.mu.activeCount = 2
		pool.mu.dead = []*Connection{}
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		pool.mu.Lock()
		pool.evictExternallyDemotedWithLock(a1, a1.loadConnState())
		pool.mu.Unlock()

		require.Equal(t, 1, pool.mu.activeCount)
		require.Len(t, pool.mu.dead, 1)
		require.Equal(t, "a1", pool.mu.dead[0].URL.Host)

		// deadSince should be set
		a1.mu.RLock()
		require.False(t, a1.mu.deadSince.IsZero())
		a1.mu.RUnlock()

		// Observer should have received a demote event
		require.Equal(t, 1, obs.count("demote"))
		events := obs.get("demote")
		require.Equal(t, "http://a1", events[0].URL)
	})
}

func TestDeferredCapEnforcement(t *testing.T) {
	t.Run("reduces active to cap", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		a3 := newActiveConn("a3")
		pool := newStandbyPool([]*Connection{a1, a2, a3}, nil)
		pool.activeListCap = 2

		pool.deferredCapEnforcement()

		require.Equal(t, 2, pool.mu.activeCount)
		require.Len(t, pool.mu.ready, 3)
	})
}

func TestNextFallback(t *testing.T) {
	t.Run("active found after lock upgrade", func(t *testing.T) {
		a1 := newActiveConn("a1")
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{a1}
		pool.mu.activeCount = 1
		pool.mu.dead = []*Connection{}

		c, err := pool.nextFallback()
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, "a1", c.URL.Host)
	})

	t.Run("standby used when no active", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool(nil, []*Connection{s1})

		c, err := pool.nextFallback()
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, "s1", c.URL.Host)
	})

	t.Run("zombie from dead when no active or standby", func(t *testing.T) {
		d1 := &Connection{URL: &url.URL{Scheme: "http", Host: "dead1"}}
		d1.mu.deadSince = time.Now()

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = 0
		pool.mu.dead = []*Connection{d1}

		c, err := pool.nextFallback()
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, "dead1", c.URL.Host)
	})

	t.Run("error when nothing available", func(t *testing.T) {
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{}
		pool.mu.activeCount = 0
		pool.mu.dead = []*Connection{}

		c, err := pool.nextFallback()
		require.ErrorIs(t, err, ErrNoConnections)
		require.Nil(t, c)
	})

	t.Run("evicts externally demoted then finds healthy active", func(t *testing.T) {
		demoted := newActiveConn("d1")
		demoted.state.Store(int64(newConnState(lcDead)))
		healthy := newActiveConn("a1")

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{demoted, healthy}
		pool.mu.activeCount = 2
		pool.mu.dead = []*Connection{}
		// Set round-robin counter so nextFallback picks demoted first
		pool.nextReady.Store(0)

		c, err := pool.nextFallback()
		require.NoError(t, err)
		// After evicting demoted, should find healthy
		require.Equal(t, "a1", c.URL.Host)
		require.Len(t, pool.mu.dead, 1)
	})

	t.Run("evicts all demoted in fallback then uses zombie", func(t *testing.T) {
		d1 := newActiveConn("d1")
		d1.state.Store(int64(newConnState(lcDead)))
		d2 := newActiveConn("d2")
		d2.state.Store(int64(newConnState(lcDead)))
		zombie := &Connection{URL: &url.URL{Scheme: "http", Host: "zombie"}}
		zombie.mu.deadSince = time.Now()

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{d1, d2}
		pool.mu.activeCount = 2
		pool.mu.dead = []*Connection{zombie}

		c, err := pool.nextFallback()
		require.NoError(t, err)
		require.Equal(t, "zombie", c.URL.Host)
		// Both demoted should be in dead now (plus original zombie)
		require.Len(t, pool.mu.dead, 3)
	})
}

func TestNextWithWarmup(t *testing.T) {
	t.Run("warming connection eventually accepted", func(t *testing.T) {
		// Create a warming connection: lcActive|lcNeedsWarmup with warmup managers set
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "warming"}}
		conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
		conn.startWarmup(1, 1) // 1 round, 1 skip -> first call skips, second accepts

		obs := newRecordingObserver()
		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{conn}
		pool.mu.activeCount = 1
		pool.mu.dead = []*Connection{}
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		// Keep calling Next() until warmup completes (starvation prevention ensures it returns)
		for range 10 {
			c, err := pool.Next()
			require.NoError(t, err)
			require.Equal(t, "warming", c.URL.Host)
		}

		// Observer should have received warmup_request events
		require.Positive(t, obs.count("warmup_request"))
	})
}

func TestTryZombieWithLock(t *testing.T) {
	t.Run("returns nil when dead list empty", func(t *testing.T) {
		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{}

		pool.mu.Lock()
		c := pool.tryZombieWithLock()
		pool.mu.Unlock()

		require.Nil(t, c)
	})

	t.Run("rotates dead list", func(t *testing.T) {
		d1 := &Connection{URL: &url.URL{Scheme: "http", Host: "d1"}}
		d2 := &Connection{URL: &url.URL{Scheme: "http", Host: "d2"}}

		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{d1, d2}

		pool.mu.Lock()
		c := pool.tryZombieWithLock()
		pool.mu.Unlock()

		require.Equal(t, "d1", c.URL.Host)
		// After rotation: [d2, d1]
		require.Equal(t, "d2", pool.mu.dead[0].URL.Host)
		require.Equal(t, "d1", pool.mu.dead[1].URL.Host)
	})
}
