// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Lifecycle bit tests
// ---------------------------------------------------------------------------

func TestNeedsCatUpdate_LifecycleBit(t *testing.T) {
	t.Parallel()

	t.Run("set and query", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		require.False(t, c.needsCatUpdate())

		err := c.setNeedsCatUpdate()
		require.NoError(t, err, "first set should return nil")
		require.True(t, c.needsCatUpdate())
	})

	t.Run("idempotent set", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		require.NoError(t, c.setNeedsCatUpdate())
		require.Error(t, c.setNeedsCatUpdate(), "second set should return error (already set)")
		require.True(t, c.needsCatUpdate())
	})

	t.Run("clear", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		c.setNeedsCatUpdate()
		require.True(t, c.needsCatUpdate())

		err := c.clearNeedsCatUpdate()
		require.NoError(t, err)
		require.False(t, c.needsCatUpdate())
	})

	t.Run("clear when not set is no-op", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		err := c.clearNeedsCatUpdate()
		require.Error(t, err, "clear on unflagged connection should return error")
	})

	t.Run("survives resurrection to active", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		// Start as dead
		c.state.Store(int64(newConnState(lcDead)))
		c.setNeedsCatUpdate()

		// Simulate resurrection: dead -> ready+active
		c.casLifecycle(c.loadConnState(), 0, lcReady|lcActive, lcUnknown)

		// needsCatUpdate survives because it's an independent metadata bit
		require.True(t, c.needsCatUpdate(),
			"needsCatUpdate should survive resurrection")
		lc := c.loadConnState().lifecycle()
		require.True(t, lc.has(lcReady|lcActive),
			"connection should be ready+active after resurrection")
	})

	t.Run("combinable with other metadata", func(t *testing.T) {
		t.Parallel()
		c := newTestConn(t, "node1")
		c.state.Store(int64(newConnState(lcReady | lcActive)))
		c.setNeedsCatUpdate()
		c.setLifecycleBit(lcNeedsWarmup)

		lc := c.loadConnState().lifecycle()
		require.True(t, lc.has(lcNeedsCatUpdate))
		require.True(t, lc.has(lcNeedsWarmup))
		require.True(t, lc.has(lcReady|lcActive))
	})

	t.Run("String includes needsCatUpdate", func(t *testing.T) {
		t.Parallel()
		lc := lcReady | lcActive | lcNeedsCatUpdate
		s := lc.String()
		require.Contains(t, s, "needsCatUpdate")
		require.Contains(t, s, "ready")
		require.Contains(t, s, "active")
	})
}

// ---------------------------------------------------------------------------
// rendezvousTopK filtering tests
// ---------------------------------------------------------------------------

func TestRendezvousTopK_FilterNeedsCatUpdate(t *testing.T) {
	t.Parallel()

	t.Run("no flags set passes all connections", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConnRTT(t, "node1", 1*time.Millisecond),
			newTestConnRTT(t, "node2", 2*time.Millisecond),
			newTestConnRTT(t, "node3", 3*time.Millisecond),
		}
		var jitter atomic.Int64
		result := rendezvousTopK("my-index", "", conns, 3, &jitter, nil, nil)
		require.Len(t, result, 3)
	})

	t.Run("flagged connections excluded", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConnRTT(t, "node1", 1*time.Millisecond),
			newTestConnRTT(t, "node2", 2*time.Millisecond),
			newTestConnRTT(t, "node3", 3*time.Millisecond),
		}
		conns[1].setNeedsCatUpdate()

		var jitter atomic.Int64
		result := rendezvousTopK("my-index", "", conns, 3, &jitter, nil, nil)
		require.Len(t, result, 2)
		for _, c := range result {
			require.NotEqual(t, "node2", c.Name,
				"flagged connection should be excluded")
		}
	})

	t.Run("all flagged returns nil", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConnRTT(t, "node1", 1*time.Millisecond),
			newTestConnRTT(t, "node2", 2*time.Millisecond),
		}
		conns[0].setNeedsCatUpdate()
		conns[1].setNeedsCatUpdate()

		var jitter atomic.Int64
		result := rendezvousTopK("my-index", "", conns, 2, &jitter, nil, nil)
		require.Nil(t, result)
	})

	t.Run("k clamped after filtering", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConnRTT(t, "node1", 1*time.Millisecond),
			newTestConnRTT(t, "node2", 2*time.Millisecond),
			newTestConnRTT(t, "node3", 3*time.Millisecond),
		}
		conns[0].setNeedsCatUpdate()
		conns[2].setNeedsCatUpdate()
		// Only node2 remains; requesting k=3 should clamp to 1.

		var jitter atomic.Int64
		result := rendezvousTopK("my-index", "", conns, 3, &jitter, nil, nil)
		require.Len(t, result, 1)
		require.Equal(t, "node2", result[0].Name)
	})
}

// ---------------------------------------------------------------------------
// filterNeedsCatUpdate unit tests
// ---------------------------------------------------------------------------

func TestFilterNeedsCatUpdate(t *testing.T) {
	t.Parallel()

	t.Run("returns same slice when no flags", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConn(t, "a"),
			newTestConn(t, "b"),
		}
		result := filterNeedsCatUpdate(conns)
		// Should be the exact same slice (no allocation).
		require.Len(t, result, len(conns))
	})

	t.Run("filters flagged connections", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConn(t, "a"),
			newTestConn(t, "b"),
			newTestConn(t, "c"),
		}
		conns[1].setNeedsCatUpdate()
		result := filterNeedsCatUpdate(conns)
		require.Len(t, result, 2)
		require.Equal(t, "a", result[0].Name)
		require.Equal(t, "c", result[1].Name)
	})

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		result := filterNeedsCatUpdate(nil)
		require.Nil(t, result)
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		result := filterNeedsCatUpdate([]*Connection{})
		require.Empty(t, result)
	})
}

// ---------------------------------------------------------------------------
// clearAllNeedsCatUpdate tests
// ---------------------------------------------------------------------------

func TestClearAllNeedsCatUpdate(t *testing.T) {
	t.Parallel()

	t.Run("clears flags on all connections", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			newTestConn(t, "node1"),
			newTestConn(t, "node2"),
			newTestConn(t, "node3"),
		}
		for _, c := range conns {
			c.setNeedsCatUpdate()
		}

		// Build a minimal Client with a multiServerPool.
		pool := testPool(conns)
		client := &Client{}
		client.mu.connectionPool = pool

		client.clearAllNeedsCatUpdate()

		for _, c := range conns {
			require.False(t, c.needsCatUpdate(),
				"needsCatUpdate should be cleared on %s", c.Name)
		}
	})
}

// ---------------------------------------------------------------------------
// requestCatRefresh tests
// ---------------------------------------------------------------------------

func TestRequestCatRefresh(t *testing.T) {
	t.Parallel()

	t.Run("sets atomic flag", func(t *testing.T) {
		t.Parallel()
		client := &Client{}
		require.False(t, client.catRefreshNeeded.Load(), "flag should start false")

		client.requestCatRefresh()
		require.True(t, client.catRefreshNeeded.Load(), "flag should be set after request")
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()
		client := &Client{}

		client.requestCatRefresh()
		client.requestCatRefresh()
		require.True(t, client.catRefreshNeeded.Load(), "flag should still be set")
	})

	t.Run("swap clears flag", func(t *testing.T) {
		t.Parallel()
		client := &Client{}

		client.requestCatRefresh()
		require.True(t, client.catRefreshNeeded.Swap(false), "swap should return true")
		require.False(t, client.catRefreshNeeded.Load(), "flag should be cleared after swap")
	})
}

// ---------------------------------------------------------------------------
// requestDiscoveryNow tests
// ---------------------------------------------------------------------------

func TestRequestDiscoveryNow(t *testing.T) {
	t.Parallel()

	t.Run("sets atomic flag", func(t *testing.T) {
		t.Parallel()
		client := &Client{}
		require.False(t, client.discoveryNeeded.Load(), "flag should start false")

		client.requestDiscoveryNow()
		require.True(t, client.discoveryNeeded.Load(), "flag should be set after request")
	})
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestConn(t *testing.T, name string) *Connection {
	t.Helper()
	u, err := url.Parse("http://" + name + ":9200")
	require.NoError(t, err)
	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        name,
		Name:      name,
		rttRing:   newRTTRing(4),
	}
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	return c
}

func newTestConnRTT(t *testing.T, name string, rtt time.Duration) *Connection {
	t.Helper()
	c := newTestConn(t, name)
	for range 4 {
		c.rttRing.add(rtt)
	}
	return c
}

// testPool creates a minimal multiServerPool for testing.
func testPool(conns []*Connection) *multiServerPool {
	pool := &multiServerPool{}
	pool.mu.ready = conns
	pool.mu.activeCount = len(conns)
	pool.mu.dead = []*Connection{}
	return pool
}
