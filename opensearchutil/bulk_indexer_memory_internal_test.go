// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerChunkPoolReturnsEveryChunk sends many batches through a bulk client and
// checks that no pooled chunk leaks: once the bulk client is idle, every chunk that was
// handed out has been given back, and the pool served some of them from what it already
// held. How many it can reuse depends on the garbage collector, which empties a
// sync.Pool, so the test does not ask for a ratio.
func TestBulkIndexerChunkPoolReturnsEveryChunk(t *testing.T) {
	t.Parallel()

	const (
		ceilingDocs = 400
		rounds      = 200
	)

	tests := []struct {
		name          string
		shardsPerNode int
		nodes         []string
	}{
		{name: "one node, one shard", shardsPerNode: 1, nodes: []string{"n0"}},
		{name: "one node, several shards", shardsPerNode: 4, nodes: []string{"n0"}},
		{name: "several nodes", shardsPerNode: 2, nodes: []string{"n0", "n1", "n2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newSizingHarness(t, ceilingDocs, tt.shardsPerNode, tt.nodes...)
			bi := h.bi.(*bulkClient)

			for range rounds {
				for _, node := range tt.nodes {
					h.round(node)
				}
			}
			waitIdle(t, h.bi)
			require.NoError(t, h.bi.Close(t.Context()))

			served, returned, allocated := bi.chunks.served.Load(), bi.chunks.returned.Load(), bi.chunks.allocated.Load()
			require.GreaterOrEqual(t, served, uint64(rounds), "each round used the pool")
			require.Equal(t, served, returned, "every chunk handed out is given back once the indexer is idle")
			require.Less(t, allocated, served, "the pool reused some chunks instead of allocating them all")
		})
	}
}
