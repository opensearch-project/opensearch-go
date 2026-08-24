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

func TestAppendToDeadWithLock_Invariants(t *testing.T) {
	makePool := func() *multiServerPool {
		return &multiServerPool{
			name: "test",
		}
	}

	t.Run("sets deadSince when zero", func(t *testing.T) {
		pool := makePool()
		c := &Connection{
			URL: &url.URL{Scheme: "http", Host: "node1:9200"},
		}

		c.mu.RLock()
		require.True(t, c.loadDeadSince().IsZero())
		c.mu.RUnlock()

		pool.appendToDeadWithLock(c)

		c.mu.RLock()
		require.False(t, c.loadDeadSince().IsZero(), "appendToDeadWithLock must set deadSince")
		c.mu.RUnlock()
	})

	t.Run("preserves existing deadSince", func(t *testing.T) {
		pool := makePool()
		c := &Connection{
			URL: &url.URL{Scheme: "http", Host: "node1:9200"},
		}
		original := time.Now().Add(-5 * time.Minute).UTC()
		c.storeDeadSince(original)

		pool.appendToDeadWithLock(c)

		c.mu.RLock()
		require.Equal(t, original, c.loadDeadSince(), "appendToDeadWithLock must not overwrite existing deadSince")
		c.mu.RUnlock()
	})

	t.Run("sets lcUnknown when not set", func(t *testing.T) {
		pool := makePool()
		c := &Connection{
			URL: &url.URL{Scheme: "http", Host: "node1:9200"},
		}
		// Start with lcActive -- no lcUnknown
		c.setLifecycleBit(lcActive)

		pool.appendToDeadWithLock(c)

		lc := c.loadConnState().lifecycle()
		require.True(t, lc.has(lcUnknown), "appendToDeadWithLock must set lcUnknown, got %s", lc)
	})

	t.Run("preserves lcUnknown when already set", func(t *testing.T) {
		pool := makePool()
		c := &Connection{
			URL: &url.URL{Scheme: "http", Host: "node1:9200"},
		}
		c.setLifecycleBit(lcDead | lcNeedsWarmup | lcNeedsHardware)

		pool.appendToDeadWithLock(c)

		lc := c.loadConnState().lifecycle()
		require.True(t, lc.has(lcUnknown), "lcUnknown must remain set")
		require.True(t, lc.has(lcNeedsWarmup), "lcNeedsWarmup must be preserved")
		require.True(t, lc.has(lcNeedsHardware), "lcNeedsHardware must be preserved")
	})
}
