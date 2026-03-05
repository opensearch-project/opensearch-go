// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// selectorTestConn creates a minimal Connection for selector tests.
func selectorTestConn(t *testing.T, host, id string, rtt time.Duration, load float64) *Connection {
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
	c.estLoad.clock = newTestClock()
	c.estLoad.store(load)
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

// --- Interface compliance ---

func TestPoolSelectorInterfaceCompliance(t *testing.T) {
	t.Parallel()

	var _ poolSelector = (*poolRoundRobin)(nil)
}
