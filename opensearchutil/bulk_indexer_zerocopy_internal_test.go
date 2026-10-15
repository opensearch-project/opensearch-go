// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// lentLog records what happened to items, in order, from any goroutine.
type lentLog struct {
	mu     sync.Mutex
	events []string
}

func (l *lentLog) addf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, fmt.Sprintf(format, args...))
}

func (l *lentLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.events...)
}

// reply answers every action of a request with the status.
func reply(status int) func(sentRequest, string) (int, string) {
	return func(r sentRequest, _ string) (int, string) {
		items := make([]string, len(r.actions))
		for i := range items {
			items[i] = fmt.Sprintf(`{"index":{"_id":"x","status":%d}}`, status)
		}

		return http.StatusOK, fmt.Sprintf(`{"took":1,"errors":%t,"items":[%s]}`, status >= 400, strings.Join(items, ","))
	}
}

func lentDoc(seq int) []byte { return fmt.Appendf(nil, `{"seq":%d}`, seq) }

// TestBulkIndexerZeroCopySendsTheSameRequest: a document lent to the indexer
// arrives as the same request body a copied one does.
func TestBulkIndexerZeroCopySendsTheSameRequest(t *testing.T) {
	t.Parallel()

	const docs = 40

	send := func(t *testing.T, lend bool) []recordedAction {
		t.Helper()

		cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(3)}, "n0")
		server := newFakeServer(cluster)
		bi := newClusterIndexer(t, server, BulkIndexerConfig{FlushInterval: time.Hour})

		for i := range docs {
			item := BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: fmt.Sprint("doc-", i)}
			if lend {
				require.NoError(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{BulkIndexerItem: item, Data: lentDoc(i)}))

				continue
			}

			item.Body = bytes.NewReader(lentDoc(i))
			require.NoError(t, bi.Add(t.Context(), item))
		}
		require.NoError(t, bi.Close(t.Context()))

		var actions []recordedAction
		for _, r := range server.requests() {
			actions = append(actions, r.actions...)
		}

		return actions
	}

	copied, lent := send(t, false), send(t, true)
	require.Len(t, copied, docs)
	require.ElementsMatch(t, copied, lent)
}

// TestBulkIndexerZeroCopyReleasesEachAcceptedItemOnce: whatever the cluster
// answers, every item the indexer took is given back once, and only after the
// request that carried it has ended and the callbacks have run.
func TestBulkIndexerZeroCopyReleasesEachAcceptedItemOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		// failed is whether the items end in OnFailure.
		failed bool
	}{
		{name: "created", status: http.StatusCreated},
		{name: "rejected for their own sake", status: http.StatusConflict, failed: true},
		{name: "the request failed", status: http.StatusBadGateway, failed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const docs = 12

			server := newFakeServer(newFakeCluster(nil))
			server.respond = reply(tt.status)
			if tt.status == http.StatusBadGateway {
				server.respond = func(sentRequest, string) (int, string) { return tt.status, `{"error":"bad gateway"}` }
			}

			bi := newClusterIndexer(t, server, BulkIndexerConfig{FlushInterval: time.Hour, ShardRouter: NoShardRouter})

			var (
				log      lentLog
				released = map[int]int{}
				mu       sync.Mutex
			)
			done := func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
				got, err := io.ReadAll(item.Body)
				require.NoError(t, err)
				log.addf("callback %s", got)
			}

			for i := range docs {
				require.NoError(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
					BulkIndexerItem: BulkIndexerItem{
						Index: "idx", Action: actionIndex, DocumentID: fmt.Sprint("doc-", i),
						OnSuccess: done,
						OnFailure: func(ctx context.Context, item BulkIndexerItem, resp opensearchapi.BulkRespItem, _ error) {
							done(ctx, item, resp)
						},
					},
					Data: lentDoc(i),
					Release: func(data []byte) {
						mu.Lock()
						defer mu.Unlock()
						released[i]++
						log.addf("release %s", data)
					},
				}))
			}
			require.NoError(t, bi.Close(t.Context()))

			mu.Lock()
			defer mu.Unlock()
			for i := range docs {
				require.Equal(t, 1, released[i], "item %d", i)
			}

			events := log.snapshot()
			require.Len(t, events, 2*docs, "a callback and a release for each item")
			for i, event := range events {
				if i < docs {
					require.True(t, strings.HasPrefix(event, "callback"), "the callbacks come first: %v", events)
				} else {
					require.True(t, strings.HasPrefix(event, "release"), "then the items are given back: %v", events)
				}
			}
		})
	}
}

// TestBulkIndexerZeroCopyRefusedItemsAreNotReleased: an item the indexer did not
// take is still the caller's.
func TestBulkIndexerZeroCopyRefusedItemsAreNotReleased(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // The case's context.
		item func(release func([]byte)) BulkIndexerZeroCopyItem
		// closed closes the indexer first.
		closed  bool
		wantErr error
	}{
		{
			name: "the indexer is closed",
			ctx:  t.Context(),
			item: func(release func([]byte)) BulkIndexerZeroCopyItem {
				return BulkIndexerZeroCopyItem{BulkIndexerItem: BulkIndexerItem{Index: "idx", Action: actionIndex}, Data: lentDoc(1), Release: release}
			},
			closed:  true,
			wantErr: ErrBulkIndexerClosed,
		},
		{
			name: "the context has ended",
			ctx:  cancelled,
			item: func(release func([]byte)) BulkIndexerZeroCopyItem {
				return BulkIndexerZeroCopyItem{BulkIndexerItem: BulkIndexerItem{Index: "idx", Action: actionIndex}, Data: lentDoc(1), Release: release}
			},
			wantErr: context.Canceled,
		},
		{
			name: "the item also sets Body",
			ctx:  t.Context(),
			item: func(release func([]byte)) BulkIndexerZeroCopyItem {
				return BulkIndexerZeroCopyItem{
					BulkIndexerItem: BulkIndexerItem{Index: "idx", Action: actionIndex, Body: strings.NewReader("{}")},
					Data:            lentDoc(1),
					Release:         release,
				}
			},
			wantErr: ErrZeroCopyBodySet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bi := newClusterIndexer(t, newFakeServer(newFakeCluster(nil)), BulkIndexerConfig{ShardRouter: NoShardRouter})
			if tt.closed {
				require.NoError(t, bi.Close(t.Context()))
			}

			var released atomic.Int64
			err := AddZeroCopy(tt.ctx, bi, tt.item(func([]byte) { released.Add(1) }))
			require.ErrorIs(t, err, tt.wantErr)

			if !tt.closed {
				require.NoError(t, bi.Close(t.Context()))
			}
			require.Zero(t, released.Load())
		})
	}
}

// TestBulkIndexerZeroCopyDoesNotCopyTheDocument: the serialized request holds the
// caller's slice itself, and the pools are asked for chunks only for the action
// lines, so a document much larger than a chunk takes none of its own.
func TestBulkIndexerZeroCopyDoesNotCopyTheDocument(t *testing.T) {
	t.Parallel()

	const docs = 5

	big := fmt.Appendf(nil, `{"v":"%s"}`, strings.Repeat("x", 10*defaultChunkSize))
	chunks, pieces := newChunkPool(defaultChunkSize), newChunkPool(lentPieceBytes)

	w := &worker{
		bi: &bulkIndexer{
			metaPoolMaxBytes: defaultMetaBufferPoolMaxBytes,
			metaPool:         sync.Pool{New: func() any { return new(bytes.Buffer) }},
		},
		buf: newChunkBuffer(chunks),
	}
	w.buf.small = pieces

	for i := range docs {
		require.NoError(t, w.writeItem(t.Context(), BulkIndexerItem{
			Index: "idx", Action: actionIndex, DocumentID: fmt.Sprint("d", i),
			Body: newLentBody(big, nil),
		}))
	}

	lent := 0
	for _, s := range w.buf.segs {
		if s.chunk == nil {
			lent++
			require.Same(t, &big[0], &s.data[0], "the buffer holds the caller's slice, not a copy of it")
		}
	}
	require.Equal(t, docs, lent)
	require.EqualValues(t, 1, chunks.served.Load(), "only the first action line, before any lent document, takes a chunk")
	require.EqualValues(t, docs, pieces.served.Load(), "and each lent document is followed by one small piece")
	require.Greater(t, w.buf.Len(), docs*len(big))

	w.buf.Reset()
	require.Equal(t, strings.Repeat("x", 10*defaultChunkSize), string(big[6:6+10*defaultChunkSize]), "the document is untouched")
}

// TestBulkIndexerZeroCopyCountsWhatItHolds: a lent document counts toward the
// job's memory limit by its length and the piece that follows it, so the limit
// holds for lent documents as it does for copied ones.
func TestBulkIndexerZeroCopyCountsWhatItHolds(t *testing.T) {
	t.Parallel()

	data := lentDoc(1)
	plain := BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "d", Body: bytes.NewReader(data)}
	lent := BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "d", Body: newLentBody(data, nil)}

	size := estimateItemBytes(plain)
	require.Equal(t, size, estimateItemBytes(lent), "the request is the same size")
	require.Equal(t, size+itemStructBytes, itemHeldBytes(plain, size))
	require.Equal(t, size+itemStructBytes+lentPieceBytes, itemHeldBytes(lent, size))
}

// onlyAdd hides everything an indexer offers beyond [BulkIndexer].
type onlyAdd struct{ BulkIndexer }

// TestAddZeroCopyCopiesForAnIndexerThatCannotLend: a custom BulkIndexer gets a copy
// of the document and its item is released at once, so the caller's contract holds.
func TestAddZeroCopyCopiesForAnIndexerThatCannotLend(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	inner := newClusterIndexer(t, server, BulkIndexerConfig{ShardRouter: NoShardRouter})
	bi := onlyAdd{inner}

	data := lentDoc(7)
	var released atomic.Int64
	require.NoError(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
		BulkIndexerItem: BulkIndexerItem{Index: "idx", Action: actionIndex, DocumentID: "d"},
		Data:            data,
		Release:         func([]byte) { released.Add(1) },
	}))
	require.EqualValues(t, 1, released.Load(), "released as soon as the copy was made")

	copy(data, strings.Repeat("z", len(data))) // The caller reuses its memory.
	require.NoError(t, inner.Close(t.Context()))

	requests := server.requests()
	require.Len(t, requests, 1)
	require.Equal(t, 7, requests[0].actions[0].seq, "and the indexer sent what was lent")

	require.ErrorIs(t, AddZeroCopy(t.Context(), bi, BulkIndexerZeroCopyItem{
		BulkIndexerItem: BulkIndexerItem{Body: strings.NewReader("{}")},
	}), ErrZeroCopyBodySet)
}

// TestLentBodyReadsLikeAReader: callbacks are handed the item, and may read its
// body and rewind it as they would a copied one.
func TestLentBodyReadsLikeAReader(t *testing.T) {
	t.Parallel()

	const doc = "0123456789"

	tests := []struct {
		name   string
		offset int64
		whence int
		want   string
		pos    int64
		errMsg string
	}{
		{name: "from the start", offset: 3, whence: io.SeekStart, want: "3456789", pos: 3},
		{name: "from the end", offset: -2, whence: io.SeekEnd, want: "89", pos: 8},
		{name: "past the end reads nothing", offset: 50, whence: io.SeekStart, want: "", pos: 50},
		{name: "before the start", offset: -1, whence: io.SeekStart, errMsg: "negative position"},
		{name: "an unknown origin", offset: 0, whence: 9, errMsg: "invalid whence"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := newLentBody([]byte(doc), nil)
			require.Equal(t, len(doc), body.Len())

			// Read some first, so a relative seek has somewhere to start from.
			_, err := io.CopyN(io.Discard, body, 2)
			require.NoError(t, err)
			require.Equal(t, len(doc)-2, body.Len())

			pos, err := body.Seek(tt.offset, tt.whence)
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.pos, pos)

			got, err := io.ReadAll(body)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))

			_, err = body.Seek(0, io.SeekStart)
			require.NoError(t, err)
			require.Equal(t, len(doc), body.Len(), "rewound")
		})
	}
}
