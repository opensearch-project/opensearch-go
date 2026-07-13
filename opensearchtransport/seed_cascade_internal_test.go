// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAvailableForRouting covers the per-connection predicate that decides
// whether a connection may satisfy a policy's "has anything to serve" check.
// Seeds are always available; discovered connections are available only once
// they have been verified (lcNeedsHardware cleared).
func TestAvailableForRouting(t *testing.T) {
	newConn := func(seed bool, lc connLifecycle) *Connection {
		c := &Connection{URL: &url.URL{Scheme: "http", Host: "n:9200"}, seed: seed}
		c.state.Store(int64(newConnState(lc)))
		return c
	}

	tests := []struct {
		name string
		conn *Connection
		want bool
	}{
		{
			name: "seed dead and never verified is still available",
			conn: newConn(true, lcDead|lcNeedsWarmup|lcNeedsHardware),
			want: true,
		},
		{
			name: "seed active is available",
			conn: newConn(true, lcActive),
			want: true,
		},
		{
			name: "discovered never verified is not available",
			conn: newConn(false, lcDead|lcNeedsWarmup|lcNeedsHardware),
			want: false,
		},
		{
			name: "discovered verified (hardware cleared) is available",
			conn: newConn(false, lcDead|lcNeedsWarmup),
			want: true,
		},
		{
			name: "zero-value (non-seed, no hardware bit) defaults to available",
			conn: &Connection{URL: &url.URL{Scheme: "http", Host: "n:9200"}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.conn.availableForRouting())
		})
	}
}

// TestHasAvailableConnsWithLock covers the pool-level predicate that policies
// use to set their enabled bit. A pool whose only dead connections are
// never-verified discovered nodes must report "no available connections" so the
// request cascades to the seed fallback.
func TestHasAvailableConnsWithLock(t *testing.T) {
	seedURL := &url.URL{Scheme: "http", Host: "seed:9200"}
	discURL := &url.URL{Scheme: "http", Host: "10.0.0.1:9200"}

	deadDiscovered := func() *Connection {
		c := &Connection{URL: discURL, URLString: discURL.String()}
		c.state.Store(int64(newConnState(lcDead | lcNeedsWarmup | lcNeedsHardware)))
		return c
	}
	deadSeed := func() *Connection {
		c := &Connection{URL: seedURL, URLString: seedURL.String(), seed: true}
		c.state.Store(int64(newConnState(lcDead | lcNeedsWarmup | lcNeedsHardware)))
		return c
	}
	verifiedDiscovered := func() *Connection {
		c := &Connection{URL: discURL, URLString: discURL.String()}
		c.state.Store(int64(newConnState(lcDead | lcNeedsWarmup))) // hardware cleared
		return c
	}

	t.Run("only never-verified discovered dead conns -> not available", func(t *testing.T) {
		cp := &multiServerPool{name: "test"}
		cp.mu.dead = []*Connection{deadDiscovered(), deadDiscovered()}
		require.False(t, cp.hasAvailableConnsWithLock())
	})

	t.Run("a dead seed keeps the pool available", func(t *testing.T) {
		cp := &multiServerPool{name: "test"}
		cp.mu.dead = []*Connection{deadDiscovered(), deadSeed()}
		require.True(t, cp.hasAvailableConnsWithLock())
	})

	t.Run("a verified discovered dead conn keeps the pool available", func(t *testing.T) {
		cp := &multiServerPool{name: "test"}
		cp.mu.dead = []*Connection{deadDiscovered(), verifiedDiscovered()}
		require.True(t, cp.hasAvailableConnsWithLock())
	})

	t.Run("any ready conn keeps the pool available", func(t *testing.T) {
		cp := &multiServerPool{name: "test"}
		ready := &Connection{URL: discURL, URLString: discURL.String()}
		ready.state.Store(int64(newConnState(lcActive)))
		cp.mu.ready = []*Connection{ready}
		require.True(t, cp.hasAvailableConnsWithLock())
	})

	t.Run("empty pool is not available", func(t *testing.T) {
		cp := &multiServerPool{name: "test"}
		require.False(t, cp.hasAvailableConnsWithLock())
	})
}
