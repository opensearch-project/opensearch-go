// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newDeadViableConn builds a discovered connection that was proven reachable
// (lcViable) and is currently dead, with deadSince set to now-dead.
func newDeadViableConn(host string, dead time.Duration) *Connection {
	c := &Connection{URL: &url.URL{Scheme: "http", Host: host}}
	c.setLifecycleBit(lcDead | lcViable)
	c.storeDeadSince(time.Now().Add(-dead))
	return c
}

func TestResetDeadConnViability(t *testing.T) {
	newTransport := func(verifyDeadAfter time.Duration, pool ConnectionPool) *Client {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		tp := &Client{verifyDeadAfter: verifyDeadAfter, ctx: ctx, cancelFunc: cancel}
		tp.mu.connectionPool = pool
		return tp
	}

	t.Run("clears lcViable on a connection dead longer than the window", func(t *testing.T) {
		conn := newDeadViableConn("stale:9200", 20*time.Minute)
		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{conn}

		newTransport(15*time.Minute, pool).resetDeadConnViability()

		require.False(t, conn.loadConnState().lifecycle().has(lcViable),
			"a connection dead longer than verifyDeadAfter must lose lcViable")
		require.False(t, conn.availableForRouting(),
			"and must no longer be a routing/zombie candidate")
	})

	t.Run("keeps lcViable on a connection dead less than the window", func(t *testing.T) {
		conn := newDeadViableConn("fresh:9200", 1*time.Minute)
		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{conn}

		newTransport(15*time.Minute, pool).resetDeadConnViability()

		require.True(t, conn.loadConnState().lifecycle().has(lcViable),
			"a connection not yet dead long enough must keep lcViable")
	})

	t.Run("never clears lcViable on a seed connection", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Scheme: "http", Host: "seed:9200"}, seed: true}
		conn.setLifecycleBit(lcDead | lcViable)
		conn.storeDeadSince(time.Now().Add(-1 * time.Hour))
		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{conn}

		newTransport(15*time.Minute, pool).resetDeadConnViability()

		require.True(t, conn.loadConnState().lifecycle().has(lcViable),
			"seed connections are exempt from viability expiry")
	})

	t.Run("no-op when disabled (verifyDeadAfter <= 0)", func(t *testing.T) {
		conn := newDeadViableConn("stale:9200", 1*time.Hour)
		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{conn}

		newTransport(0, pool).resetDeadConnViability()

		require.True(t, conn.loadConnState().lifecycle().has(lcViable),
			"disabled feature must not touch lcViable")
	})
}
