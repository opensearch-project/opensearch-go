// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetNextActiveConnWithLock_Legacy(t *testing.T) {
	t.Parallel()

	t.Run("round-robin without selector", func(t *testing.T) {
		t.Parallel()
		c1 := createTestConnection("http://node1:9200", "data")
		c2 := createTestConnection("http://node2:9200", "data")

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{c1, c2}
		pool.mu.activeCount = 2

		pool.mu.RLock()
		first := pool.getNextActiveConnWithLock()
		second := pool.getNextActiveConnWithLock()
		pool.mu.RUnlock()

		// With round-robin, two calls should return different connections
		require.NotNil(t, first)
		require.NotNil(t, second)
		require.NotSame(t, first, second)
	})
}

func TestShouldSkipDraining(t *testing.T) {
	t.Parallel()

	t.Run("non-draining connection returns false", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		pool := &multiServerPool{name: "test"}

		require.False(t, pool.shouldSkipDraining(conn))
	})

	t.Run("draining connection returns true", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		conn.drainingQuiescingRemaining.Store(3)
		pool := &multiServerPool{name: "test"}

		require.True(t, pool.shouldSkipDraining(conn))
	})
}

func TestShouldSkipOverloaded(t *testing.T) {
	t.Parallel()

	t.Run("non-overloaded connection returns false", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		pool := &multiServerPool{name: "test"}

		require.False(t, pool.shouldSkipOverloaded(conn))
	})

	t.Run("overloaded connection returns true", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		// Set overloaded bit
		conn.state.Store(int64(newConnState(lcActive | lcOverloaded)))
		pool := &multiServerPool{name: "test"}

		require.True(t, pool.shouldSkipOverloaded(conn))
	})
}

func TestNewConnectionPool(t *testing.T) {
	t.Parallel()

	t.Run("single connection returns singleServerPool", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://node1:9200")
		conn := &Connection{URL: u}

		pool := NewConnectionPool([]*Connection{conn}, nil)
		_, ok := pool.(*singleServerPool)
		require.True(t, ok, "expected singleServerPool for single connection")
	})

	t.Run("multiple connections returns multiServerPool", func(t *testing.T) {
		t.Parallel()
		u1, _ := url.Parse("http://node1:9200")
		u2, _ := url.Parse("http://node2:9200")
		c1 := &Connection{URL: u1}
		c2 := &Connection{URL: u2}

		pool := NewConnectionPool([]*Connection{c1, c2}, nil)
		mp, ok := pool.(*multiServerPool)
		require.True(t, ok, "expected multiServerPool for multiple connections")
		require.Equal(t, 2, mp.mu.activeCount)
		require.Empty(t, mp.mu.dead)
	})

	t.Run("nil selector gets default round-robin", func(t *testing.T) {
		t.Parallel()
		u1, _ := url.Parse("http://node1:9200")
		u2, _ := url.Parse("http://node2:9200")
		c1 := &Connection{URL: u1}
		c2 := &Connection{URL: u2}

		pool := NewConnectionPool([]*Connection{c1, c2}, nil)
		mp := pool.(*multiServerPool)

		// Should be able to get connections (round-robin works)
		conn, err := mp.Next()
		require.NoError(t, err)
		require.NotNil(t, conn)
	})
}

func TestSingleServerPool_OnSuccess(t *testing.T) {
	t.Parallel()

	u, _ := url.Parse("http://node1:9200")
	conn := &Connection{URL: u}
	pool := &singleServerPool{connection: conn}

	// OnSuccess is a no-op; should not panic
	pool.OnSuccess(conn)
}

func TestSingleServerPool_OnFailure(t *testing.T) {
	t.Parallel()

	u, _ := url.Parse("http://node1:9200")
	conn := &Connection{URL: u}
	pool := &singleServerPool{connection: conn}

	err := pool.OnFailure(conn)
	require.NoError(t, err)
}

func TestDecrementDrainingQuiescing(t *testing.T) {
	t.Parallel()

	t.Run("zero returns zero", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		require.Equal(t, int64(0), conn.decrementDrainingQuiescing())
	})

	t.Run("positive decrements", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		conn.drainingQuiescingRemaining.Store(3)

		require.Equal(t, int64(2), conn.decrementDrainingQuiescing())
		require.Equal(t, int64(1), conn.decrementDrainingQuiescing())
		require.Equal(t, int64(0), conn.decrementDrainingQuiescing())
		require.Equal(t, int64(0), conn.decrementDrainingQuiescing()) // stays at 0
	})
}

func TestConnectionString(t *testing.T) {
	t.Parallel()

	t.Run("alive connection", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://node1:9200")
		conn := &Connection{URL: u}

		s := conn.String()
		require.Contains(t, s, "http://node1:9200")
		require.Contains(t, s, "dead=false")
	})

	t.Run("dead connection", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://node1:9200")
		conn := &Connection{URL: u}
		conn.mu.Lock()
		conn.mu.deadSince = time.Now().Add(-5 * time.Second)
		conn.failures.Store(3)
		conn.mu.Unlock()

		s := conn.String()
		require.Contains(t, s, "dead=true")
		require.Contains(t, s, "failures=3")
		require.Contains(t, s, "age=")
	})
}

func TestMultiServerPool_OnSuccess_SkipsDead(t *testing.T) {
	t.Parallel()

	t.Run("skips alive connection", func(t *testing.T) {
		t.Parallel()
		conn := createTestConnection("http://node1:9200", "data")
		pool := &multiServerPool{name: "test"}
		pool.mu.ready = []*Connection{conn}
		pool.mu.activeCount = 1
		pool.mu.dead = []*Connection{}
		pool.mu.members = map[*Connection]struct{}{conn: {}}

		// Connection is alive (deadSince is zero), OnSuccess should be a no-op
		pool.OnSuccess(conn)
		require.Equal(t, 1, pool.mu.activeCount)
	})
}

func TestCountByLifecycleWithLock(t *testing.T) {
	t.Parallel()

	t.Run("empty pool", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{}
		pool.mu.RLock()
		counts := pool.countByLifecycleWithLock()
		pool.mu.RUnlock()

		require.Zero(t, counts.active)
		require.Zero(t, counts.standby)
		require.Zero(t, counts.dead)
	})

	t.Run("mixed states", func(t *testing.T) {
		t.Parallel()
		active := createTestConnection("http://node1:9200", "data")
		standby := createTestConnection("http://node2:9200", "data")
		standby.state.Store(int64(newConnState(lcStandby)))
		dead := createTestConnection("http://node3:9200", "data")
		dead.state.Store(int64(newConnState(lcDead)))

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{active, standby}
		pool.mu.dead = []*Connection{dead}

		pool.mu.RLock()
		counts := pool.countByLifecycleWithLock()
		pool.mu.RUnlock()

		require.Equal(t, 1, counts.active)
		require.Equal(t, 1, counts.standby)
		require.Equal(t, 1, counts.dead)
	})
}

func TestRecalculateWarmupParams(t *testing.T) {
	t.Parallel()

	t.Run("auto-scales activeListCap", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{}
		pool.recalculateWarmupParams(5)

		require.Equal(t, 5, pool.activeListCap)
		require.Positive(t, pool.warmupRounds)
		require.Positive(t, pool.warmupSkipCount)
	})

	t.Run("respects explicit cap config", func(t *testing.T) {
		t.Parallel()
		explicitCap := 2
		pool := &multiServerPool{activeListCapConfig: &explicitCap, activeListCap: 2}
		pool.recalculateWarmupParams(5)

		// activeListCap should not change when explicit
		require.Equal(t, 2, pool.activeListCap)
	})
}
