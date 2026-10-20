// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerKeepsOrderOfDocumentsWithoutAShard: documents of one id are
// written in the order they were added whether or not the indexer can place them
// in a shard. With no shard router, a request is held and a later write of the
// same id waits for it, where a write of another id does not.
func TestBulkIndexerKeepsOrderOfDocumentsWithoutAShard(t *testing.T) {
	t.Parallel()

	newIndexer := func(t *testing.T) (*fakeServer, BulkIndexer) {
		t.Helper()

		server := newFakeServer(newFakeCluster(nil, "n0"))
		server.gate = make(chan struct{})
		server.entered = make(chan string, 8)

		// One byte sends every document as a request of its own.
		return server, newClusterIndexer(t, server, BulkIndexerConfig{
			ShardRouter: NoShardRouter, FlushBytes: 1, FlushInterval: time.Hour,
		})
	}

	t.Run("one id", func(t *testing.T) {
		t.Parallel()

		server, bi := newIndexer(t)

		addDoc(t, bi, "idx", "a", 1)
		<-server.entered
		addDoc(t, bi, "idx", "a", 2)

		require.Eventually(t, func() bool { return sealedWaiting(bi) == 1 },
			10*time.Second, 10*time.Millisecond, "the second write waits for the first, which is still held")
		require.Len(t, server.requests(), 1)

		close(server.gate)
		require.NoError(t, bi.Close(t.Context()))

		var seqs []int
		for _, req := range server.requests() {
			for _, a := range req.actions {
				seqs = append(seqs, a.seq)
			}
		}
		require.Equal(t, []int{1, 2}, seqs)
		require.Equal(t, 1, server.maxInflight())
	})

	t.Run("different ids", func(t *testing.T) {
		t.Parallel()

		server, bi := newIndexer(t)

		addDoc(t, bi, "idx", "a", 1)
		addDoc(t, bi, "idx", "b", 2)

		for range 2 {
			select {
			case <-server.entered:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "requests for different ids must be out together")
			}
		}

		close(server.gate)
		require.NoError(t, bi.Close(t.Context()))
		require.Equal(t, 2, server.maxInflight())
	})

	t.Run("a finished request releases what followed it", func(t *testing.T) {
		t.Parallel()

		server, bi := newIndexer(t)

		for seq := 1; seq <= 5; seq++ {
			addDoc(t, bi, "idx", "a", seq)
		}
		close(server.gate)
		require.NoError(t, bi.Close(t.Context()))

		var seqs []int
		for _, req := range server.requests() {
			for _, a := range req.actions {
				seqs = append(seqs, a.seq)
			}
		}
		require.Equal(t, []int{1, 2, 3, 4, 5}, seqs, "in the order added, none lost")
		require.Equal(t, 1, server.maxInflight())

		sched := bi.(*bulkIndexer).sched
		sched.Lock()
		defer sched.Unlock()
		require.Empty(t, sched.lastByID, "nothing is remembered once the buffers have finished")
	})
}

// sealedWaiting is how many sealed buffers the indexer holds back from sending.
func sealedWaiting(bi BulkIndexer) int {
	sched := bi.(*bulkIndexer).sched
	sched.Lock()
	defer sched.Unlock()

	return len(sched.sealed)
}

// TestBulkIndexerWithoutAShardMapKeepsOneRequestPerCPUOut: with no shard map the
// indexer has one node standing for the cluster, and the transport reports no window
// for it. When NumWorkers is not set the node's request window is GOMAXPROCS, the CPUs
// the program may use, which follows a container's CPU limit, and that window
// is then the only cap on requests out, so a cluster of any size is not held to what
// one or two requests carry. (With NumWorkers set, the window and the global cap are
// the same number and cannot be told apart.)
func TestBulkIndexerWithoutAShardMapKeepsOneRequestPerCPUOut(t *testing.T) {
	t.Parallel()

	want := runtime.GOMAXPROCS(0)

	server := newFakeServer(newFakeCluster(nil, "n0"))
	server.gate = make(chan struct{})
	server.entered = make(chan string, want+8)

	// One byte sends every document as a request of its own.
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		ShardRouter: NoShardRouter, FlushBytes: 1, FlushInterval: time.Hour,
	})

	const extra = 4
	for i := range want + extra {
		addDoc(t, bi, "idx", fmt.Sprint("d", i), i)
	}
	for i := range want {
		select {
		case <-server.entered:
		case <-time.After(10 * time.Second):
			require.FailNow(t, "requests must be out together", "only %d of %d", i, want)
		}
	}
	require.Eventually(t, func() bool { return sealedWaiting(bi) == extra },
		10*time.Second, 10*time.Millisecond, "the rest wait for the window")

	close(server.gate)
	require.NoError(t, bi.Close(t.Context()))
	require.Equal(t, want, server.maxInflight())
}
