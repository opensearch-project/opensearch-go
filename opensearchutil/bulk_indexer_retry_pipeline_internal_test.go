// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// retryOnce asks for each failed item to be sent once more, at once.
var retryOnce = RetryPolicyFunc(
	func(attempt int, _ FailureScope, _ FailureKind, _ opensearchapi.BulkRespItem, _ error) (time.Duration, bool) {
		return 0, attempt < 2
	})

// TestBulkIndexerReleasesLentDocumentsAfterTheFinalAttempt: a document lent to
// the indexer is still in use while a retry may send it again, so it is released
// once, after the attempt that settles it, and not after the first.
func TestBulkIndexerReleasesLentDocumentsAfterTheFinalAttempt(t *testing.T) {
	t.Parallel()

	const docs = 6

	server := newFakeServer(newFakeCluster(nil))

	var (
		mu               sync.Mutex
		releasedAtArrive []int
		released         int
	)
	server.respond = func(r sentRequest, _ string) (int, string) {
		mu.Lock()
		releasedAtArrive = append(releasedAtArrive, released)
		attempt := len(releasedAtArrive)
		mu.Unlock()

		if attempt == 1 {
			return reply(http.StatusTooManyRequests)(r, "")
		}

		return reply(http.StatusCreated)(r, "")
	}

	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushInterval: time.Hour,
		ShardRouter:   NoShardRouter,
		RetryPolicy:   retryOnce,
	})

	var succeeded atomic.Int64
	for i := range docs {
		require.NoError(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
			BulkIndexerItem: BulkIndexerItem{
				Index: "idx", Action: actionIndex, DocumentID: fmt.Sprint("doc-", i),
				OnSuccess: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { succeeded.Add(1) },
			},
			Data: lentDoc(i),
			Release: func([]byte) {
				mu.Lock()
				defer mu.Unlock()
				released++
			},
		}))
	}
	require.NoError(t, bi.Close(t.Context()))

	mu.Lock()
	defer mu.Unlock()

	require.Equal(t, []int{0, 0}, releasedAtArrive, "nothing is released while a retry may still send it")
	require.Equal(t, docs, released, "and each document is released once, when it is settled")
	require.EqualValues(t, docs, succeeded.Load())
	require.Len(t, server.requests(), 2)
}

// TestBulkIndexerRetriesKeepAShardsOrder: a retry happens in the request slot of
// the buffer it belongs to, so the next buffer of the shard is not sent until the
// retry has finished, and never overlaps it.
func TestBulkIndexerRetriesKeepAShardsOrder(t *testing.T) {
	t.Parallel()

	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(1)}, "n0")
	server := newFakeServer(cluster)
	server.yields = 50

	var rejected atomic.Bool
	server.respond = func(r sentRequest, _ string) (int, string) {
		// The first request, which carries doc-a, is rejected once.
		if rejected.CompareAndSwap(false, true) {
			return reply(http.StatusTooManyRequests)(r, "")
		}

		return reply(http.StatusCreated)(r, "")
	}

	one := docBytes("idx", "doc-a", 1)
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushBytes:    int(one), // One document fills a buffer.
		Sizing:        BulkSizing{Fixed: true},
		FlushInterval: time.Hour,
		RetryPolicy:   retryOnce,
	})

	addDoc(t, bi, "idx", "doc-a", 1)
	addDoc(t, bi, "idx", "doc-b", 2)
	require.NoError(t, bi.Close(t.Context()))

	var order []string
	for _, r := range server.requests() {
		for _, a := range r.actions {
			order = append(order, a.meta.DocumentID)
		}
	}
	require.Equal(t, []string{"doc-a", "doc-a", "doc-b"}, order, "doc-a is sent again before doc-b is sent at all")
	require.Zero(t, server.overlaps(), "the shard never has two requests out")
}

// TestBulkIndexerCancelEndsBlockedAdds: an Add waiting for room when the indexer is
// canceled does not wait for the cluster. It returns nil and its item fails with
// ErrIndexerCanceled, and a lent document is given back.
func TestBulkIndexerCancelEndsBlockedAdds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lend bool
	}{
		{name: "a copied document"},
		{name: "a lent document", lend: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := stalledServer()
			held := docBytes("idx", "doc-0", 1)

			bi := newClusterIndexer(t, server, BulkIndexerConfig{
				FlushBytes:     int(held),
				Sizing:         BulkSizing{Fixed: true},
				FlushInterval:  time.Hour,
				MaxMemoryBytes: 1, // Every item fills the job's memory alone.
				ShardRouter:    NoShardRouter,
			})
			indexer := bi.(*bulkIndexer)

			blocked := make(chan struct{}, 4)
			relieve := indexer.budget.onBlock
			indexer.budget.onBlock = func(key shardKey) {
				relieve(key)
				blocked <- struct{}{}
			}

			// The first item is taken and its request stalls at the server.
			addDoc(t, bi, "idx", "doc-0", 1)
			receive(t, server.entered)

			var (
				failure  = make(chan error, 1)
				released atomic.Int64
				addErr   = make(chan error, 1)
			)
			onFailure := func(_ context.Context, _ BulkIndexerItem, _ opensearchapi.BulkRespItem, err error) { failure <- err }
			go func() {
				item := BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "doc-1", OnFailure: onFailure}
				if tt.lend {
					addErr <- AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
						BulkIndexerItem: item, Data: lentDoc(2), Release: func([]byte) { released.Add(1) },
					})

					return
				}

				item.Body = seqBody(2)
				addErr <- bi.Add(t.Context(), item)
			}()

			receive(t, blocked) // The second Add is waiting for room.
			bi.Cancel()

			require.NoError(t, receive(t, addErr), "a canceled Add reports through the item, not its return")
			require.ErrorIs(t, receive(t, failure), ErrIndexerCanceled)
			if tt.lend {
				require.EqualValues(t, 1, released.Load())
			}

			close(server.gate)
			require.NoError(t, bi.Close(t.Context()))
		})
	}
}
