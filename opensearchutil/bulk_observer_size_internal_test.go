// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// doneSizes records the Bytes of each item the bulk client is done with, by document id.
type doneSizes struct {
	BaseBulkIndexerObserver

	mu     sync.Mutex
	bytes  map[string]int
	failed map[string]bool
}

func (s *doneSizes) OnItemDone(_ context.Context, e BulkItemEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.bytes == nil {
		s.bytes, s.failed = map[string]int{}, map[string]bool{}
	}
	s.bytes[e.DocumentID], s.failed[e.DocumentID] = e.Bytes, e.Failed
}

// TestBulkItemEventBytes: an item's OnItemDone carries the length of its document,
// whether it was accepted, rejected by the cluster, or refused before it was
// buffered, and whatever position its reader was handed in at.
func TestBulkItemEventBytes(t *testing.T) {
	t.Parallel()

	const doc = `{"title":"a document of known size"}`

	server := newFakeServer(newFakeCluster(nil))
	server.respond = func(r sentRequest, body string) (int, string) {
		// The item whose id is "rejected" draws a 400 from the cluster; the rest are created.
		if strings.Contains(body, `"_id":"rejected"`) {
			return reply(http.StatusBadRequest)(r, body)
		}

		return reply(http.StatusCreated)(r, body)
	}

	sizes := &doneSizes{}
	bi := newBulkClient(t, server, BulkIndexerConfig{ShardRouter: NoShardRouter, Observer: sizes})

	add := func(item BulkIndexerItem) {
		t.Helper()
		item.Index = "idx"
		require.NoError(t, bi.Add(t.Context(), item))
	}

	// The rejected item goes out in a request of its own, so the others are accepted.
	add(BulkIndexerItem{
		Action: actionIndex, DocumentID: "rejected", Body: strings.NewReader(doc),
		OnError: func(context.Context, BulkIndexerItem, *BulkError) {}, // So Flush reports no error.
	})
	require.NoError(t, bi.Flush(t.Context()))

	add(BulkIndexerItem{Action: actionIndex, DocumentID: "accepted", Body: strings.NewReader(doc)})
	add(BulkIndexerItem{Action: actionDelete, DocumentID: "no-body"})

	// A reader handed in part way through is still measured from its start.
	partway := strings.NewReader(doc)
	_, err := partway.Seek(5, io.SeekStart)
	require.NoError(t, err)
	add(BulkIndexerItem{Action: actionIndex, DocumentID: "partway", Body: partway})

	require.NoError(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
		BulkIndexerItem: BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "lent"},
		Data:            []byte(doc),
		Release:         func([]byte) {},
	}))

	require.NoError(t, bi.Close(t.Context()))

	sizes.mu.Lock()
	defer sizes.mu.Unlock()

	want := map[string]int{"accepted": len(doc), "rejected": len(doc), "no-body": 0, "partway": len(doc), "lent": len(doc)}
	require.Equal(t, want, sizes.bytes)
	require.False(t, sizes.failed["accepted"])
	require.True(t, sizes.failed["rejected"], "the size is the same on the failure path")
}

// TestBulkItemEventBytesOfARefusedItem: an item refused before it is buffered reports
// its size too.
func TestBulkItemEventBytesOfARefusedItem(t *testing.T) {
	t.Parallel()

	const doc = `{"title":"never sent"}`

	sizes := &doneSizes{}
	bi := newBulkClient(t, newFakeServer(newFakeCluster(nil)), BulkIndexerConfig{
		ShardRouter: NoShardRouter,
		Observer:    sizes,
		RetryPolicy: retryOnce, // A retry needs an id, and this item has none.
	})

	var failure error
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: "idx", Action: actionIndex, Body: strings.NewReader(doc),
		OnError: func(_ context.Context, _ BulkIndexerItem, err *BulkError) { failure = err },
	}))
	require.NoError(t, bi.Close(t.Context()))

	require.ErrorIs(t, failure, ErrMissingDocumentID)
	sizes.mu.Lock()
	defer sizes.mu.Unlock()
	require.Equal(t, map[string]int{"": len(doc)}, sizes.bytes)
	require.True(t, sizes.failed[""])
}
