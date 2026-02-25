// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// selectorTestConn creates a minimal Connection for selector tests.
func selectorTestConn(t *testing.T, host, id string, rtt time.Duration, affinityLoad float64) *Connection {
	t.Helper()
	u, err := url.Parse("http://" + host)
	require.NoError(t, err)

	c := &Connection{
		URL:     u,
		ID:      id,
		rttRing: newRTTRing(4),
	}
	for range 4 {
		c.rttRing.add(rtt)
	}
	// Freeze the clock so load() returns exactly what store() wrote.
	c.affinityCounter.clock = newTestClock()
	c.affinityCounter.store(affinityLoad)
	return c
}

// --- poolRoundRobin tests ---

func TestPoolRoundRobinSelectNext(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		selectorTestConn(t, "node1:9200", "n1", 1*time.Millisecond, 0),
		selectorTestConn(t, "node2:9200", "n2", 1*time.Millisecond, 0),
		selectorTestConn(t, "node3:9200", "n3", 1*time.Millisecond, 0),
	}
	activeCount := 3

	t.Run("cycles through connections", func(t *testing.T) {
		t.Parallel()
		s := &poolRoundRobin{}

		seen := make(map[string]int)
		for range 9 {
			conn, activeCap, standbyCap, err := s.selectNext(conns, activeCount)
			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Equal(t, capRemain, activeCap, "round-robin should never signal cap changes")
			require.Equal(t, capRemain, standbyCap, "round-robin should never signal cap changes")
			seen[conn.ID]++
		}

		// Each connection should be selected exactly 3 times.
		for _, c := range conns {
			require.Equal(t, 3, seen[c.ID], "connection %s should be selected 3 times", c.ID)
		}
	})

	t.Run("wraps around correctly", func(t *testing.T) {
		t.Parallel()
		s := &poolRoundRobin{}

		var first, fourth *Connection
		for i := range 4 {
			conn, _, _, _ := s.selectNext(conns, activeCount)
			if i == 0 {
				first = conn
			}
			if i == 3 {
				fourth = conn
			}
		}
		require.Same(t, first, fourth, "4th selection should wrap to 1st connection")
	})
}

// --- poolLoadAffinity tests ---

func TestPoolLoadAffinitySelectNext(t *testing.T) {
	t.Parallel()

	t.Run("picks lowest score connection", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		conns := []*Connection{
			selectorTestConn(t, "hot:9200", "hot", 1*time.Millisecond, 500.0),
			selectorTestConn(t, "cold:9200", "cold", 1*time.Millisecond, 10.0),
			selectorTestConn(t, "warm:9200", "warm", 1*time.Millisecond, 200.0),
		}

		conn, _, _, err := s.selectNext(conns, 3)
		require.NoError(t, err)
		require.Equal(t, "cold", conn.ID, "should select the least loaded connection")
	})

	t.Run("factors RTT into score", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		// Same load but different RTT -- nearer node should win.
		conns := []*Connection{
			selectorTestConn(t, "far:9200", "far", 10*time.Millisecond, 10.0),
			selectorTestConn(t, "near:9200", "near", 200*time.Microsecond, 10.0),
		}

		conn, _, _, err := s.selectNext(conns, 2)
		require.NoError(t, err)
		require.Equal(t, "near", conn.ID, "should prefer nearer node at same load")
	})

	t.Run("does not modify counter on selection", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		conns := []*Connection{
			selectorTestConn(t, "node1:9200", "n1", 1*time.Millisecond, 100.0),
		}

		before := conns[0].affinityCounter.load()
		_, _, _, _ = s.selectNext(conns, 1)
		after := conns[0].affinityCounter.load()

		require.InDelta(t, before, after, 0.0, "selection should not modify the counter")
	})

	t.Run("signals capGrow when most connections busy", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		// 4 of 5 connections at high load, one cold. Mean ~= 724.
		// 4 connections (900, 900, 900, 900) are above mean -> busyRatio = 4/5 = 0.8 >= 0.6 -> grow.
		conns := []*Connection{
			selectorTestConn(t, "n1:9200", "n1", 1*time.Millisecond, 900),
			selectorTestConn(t, "n2:9200", "n2", 1*time.Millisecond, 900),
			selectorTestConn(t, "n3:9200", "n3", 1*time.Millisecond, 900),
			selectorTestConn(t, "n4:9200", "n4", 1*time.Millisecond, 900),
			selectorTestConn(t, "n5:9200", "n5", 1*time.Millisecond, 20),
		}

		_, activeCap, _, err := s.selectNext(conns, 5)
		require.NoError(t, err)
		require.Equal(t, capGrow, activeCap, "should signal grow when most connections are busy")
	})

	t.Run("signals capShrink when few connections busy", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		// One hot, rest cold. Mean ~= 334. Only n1 (1000) is above mean.
		// busyRatio = 1/3 ~= 0.33 > 0.3, so this is in the hold band.
		// Need busyRatio < 0.3 to shrink. Use 4 connections with 1 hot.
		// Mean ~= 252.5. Only n1 above mean. busyRatio = 1/4 = 0.25 < 0.3 -> shrink.
		conns := []*Connection{
			selectorTestConn(t, "n1:9200", "n1", 1*time.Millisecond, 1000),
			selectorTestConn(t, "n2:9200", "n2", 1*time.Millisecond, 3.0),
			selectorTestConn(t, "n3:9200", "n3", 1*time.Millisecond, 4.0),
			selectorTestConn(t, "n4:9200", "n4", 1*time.Millisecond, 3.0),
		}

		_, activeCap, standbyCap, err := s.selectNext(conns, 4)
		require.NoError(t, err)
		require.Equal(t, capShrink, activeCap, "should signal shrink when few connections are busy")
		require.Equal(t, capGrow, standbyCap, "should signal standby grow when shrinking active")
	})

	t.Run("remains when mixed load", func(t *testing.T) {
		t.Parallel()
		s := newPoolLoadAffinity()

		// Mix of hot and cold -- no cap change.
		conns := []*Connection{
			selectorTestConn(t, "n1:9200", "n1", 1*time.Millisecond, 900),
			selectorTestConn(t, "n2:9200", "n2", 1*time.Millisecond, 10.0),
		}

		_, activeCap, standbyCap, err := s.selectNext(conns, 2)
		require.NoError(t, err)
		require.Equal(t, capRemain, activeCap, "mixed load should not signal cap change")
		require.Equal(t, capRemain, standbyCap)
	})
}

// --- Interface compliance ---

func TestPoolSelectorInterfaceCompliance(t *testing.T) {
	t.Parallel()

	var _ poolSelector = (*poolRoundRobin)(nil)
	var _ poolSelector = (*poolLoadAffinity)(nil)
}

// selectorTestConnWithClock creates a Connection with a shared clock.
func selectorTestConnWithClock(t *testing.T, host, id string, rtt time.Duration, load float64, clk *testClock) *Connection {
	t.Helper()
	u, err := url.Parse("http://" + host)
	require.NoError(t, err)

	c := &Connection{
		URL:     u,
		ID:      id,
		rttRing: newRTTRing(4),
	}
	for range 4 {
		c.rttRing.add(rtt)
	}
	c.affinityCounter.clock = clk
	if load > 0 {
		c.affinityCounter.store(load)
	}
	return c
}

func TestPoolLoadAffinity_DecayReordersSelection(t *testing.T) {
	t.Parallel()

	clk := newTestClock()
	s := newPoolLoadAffinity()

	// Hot node (load 1000) vs cold node (load 5), same RTT.
	hot := selectorTestConnWithClock(t, "hot:9200", "hot", 1*time.Millisecond, 1000, clk)
	cold := selectorTestConnWithClock(t, "cold:9200", "cold", 1*time.Millisecond, 5, clk)
	conns := []*Connection{hot, cold}

	// At t=0: cold should be selected (lower score).
	conn, _, _, err := s.selectNext(conns, 2)
	require.NoError(t, err)
	require.Equal(t, "cold", conn.ID, "should select cold node initially")

	// After 15s (3 half-lives): hot decays to ~125, cold to ~0.625.
	// Cold still wins, but the gap has narrowed significantly.
	clk.Advance(15 * time.Second)

	hotLoad := hot.affinityCounter.load()
	coldLoad := cold.affinityCounter.load()
	expectedHot := 1000.0 * math.Exp(-affinityDecayLambda*15)
	expectedCold := 5.0 * math.Exp(-affinityDecayLambda*15)
	require.InDelta(t, expectedHot, hotLoad, 0.01, "hot load after 15s")
	require.InDelta(t, expectedCold, coldLoad, 0.01, "cold load after 15s")

	conn, _, _, err = s.selectNext(conns, 2)
	require.NoError(t, err)
	require.Equal(t, "cold", conn.ID, "cold should still win after 15s decay")

	// After another 15s (total 30s = 6 half-lives): hot ~15.6, cold ~0.078.
	// Selection should be stable — cold always wins with equal RTT.
	clk.Advance(15 * time.Second)

	conn, _, _, err = s.selectNext(conns, 2)
	require.NoError(t, err)
	require.Equal(t, "cold", conn.ID, "cold should still win after 30s total decay")
}
