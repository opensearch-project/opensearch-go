// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerBacksOffAsANodeSlowsDownInABubble is
// TestBulkIndexerBacksOffAsANodeSlowsDown with the clock of a synctest bubble in place
// of the hand-written manualClock: the fake server takes the time a request needs by
// sleeping, the indexer measures it with time.Now, and the bubble moves its clock on when
// every goroutine in it is waiting.
func TestBulkIndexerBacksOffAsANodeSlowsDownInABubble(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const ceilingDocs = 400

		h := newSizingHarness(t, ceilingDocs, 1, "n0")
		bi := h.bi.(*bulkIndexer)
		bi.now = time.Now // The bubble's fake clock, not the harness's.

		var rate atomic.Uint64
		setRate := func(bytesPerSecond float64) { rate.Store(uint64(bytesPerSecond)) }
		setRate(100e6)
		h.server.advance = func(r sentRequest) {
			// The bubble's clock is virtual: this Sleep advances it by the time the
			// request needs, and costs no real time.
			time.Sleep(time.Duration(float64(r.bytes) / float64(rate.Load()) * float64(time.Second)))
		}

		for range 80 {
			h.round("n0")
		}
		shardHealthy, windowHealthy := h.shardTarget("n0"), h.window("n0")
		require.Equal(t, int64(bi.sizing.max), shardHealthy, "a healthy shard ends up at the ceiling")

		setRate(20e6) // Five times slower. Nothing is rejected.
		for range 6 {
			h.round("n0")
		}
		require.Less(t, h.shardTarget("n0"), shardHealthy/2, "buffers must shrink well before the node rejects anything")
		require.GreaterOrEqual(t, h.window("n0"), windowHealthy, "a slow node is not a rejecting one, so its window is not cut")

		setRate(100e6)
		for range 120 {
			h.round("n0")
		}
		require.Equal(t, shardHealthy, h.shardTarget("n0"), "and grow back when it recovers")
		require.GreaterOrEqual(t, h.window("n0"), windowHealthy)

		// A bubble ends only when every goroutine in it has, so the indexer is closed
		// here and not left to a cleanup.
		require.NoError(t, h.bi.Close(t.Context()))
	})
}
