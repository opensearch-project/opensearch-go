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

const offsetDoc = `{"seq":7}`

// seekOnly hides every method of a reader but Read and Seek, so the bulk client cannot
// ask it for its length and has to measure it by seeking.
type seekOnly struct{ io.ReadSeeker }

// readerAt returns a reader of offsetDoc positioned at offset, as one is that its owner
// has already read part of.
func readerAt(t *testing.T, offset int64, hideLen bool) io.ReadSeeker {
	t.Helper()

	r := strings.NewReader(offsetDoc)
	_, err := r.Seek(offset, io.SeekStart)
	require.NoError(t, err)

	if hideLen {
		return seekOnly{r}
	}

	return r
}

// TestBulkIndexerSendsABodyFromItsStart: an item's Body is read from offset 0 whatever
// the position its reader was handed in at, on the first attempt, on a retry, and
// when a fingerprinter read it to make the id.
func TestBulkIndexerSendsABodyFromItsStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		retry        bool
		fingerprint  bool
		wantAttempts int
	}{
		{name: "one attempt", wantAttempts: 1},
		{name: "a retry", retry: true, wantAttempts: 2},
		{name: "a fingerprinted id", retry: true, fingerprint: true, wantAttempts: 2},
	}
	for _, tt := range tests {
		for _, hideLen := range []bool{false, true} {
			name := tt.name
			if hideLen {
				name += ", measured by seeking"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var (
					mu      sync.Mutex
					attempt int
					bodies  []string
				)
				server := newFakeServer(newFakeCluster(nil))
				server.respond = func(r sentRequest, body string) (int, string) {
					mu.Lock()
					attempt++
					n := attempt
					bodies = append(bodies, body)
					mu.Unlock()

					if tt.retry && n == 1 {
						return reply(http.StatusTooManyRequests)(r, "")
					}

					return reply(http.StatusCreated)(r, "")
				}

				cfg := BulkIndexerConfig{FlushInterval: 0, ShardRouter: NoShardRouter}
				if tt.retry {
					cfg.RetryPolicy = retryOnce
				}
				if tt.fingerprint {
					cfg.Fingerprinter = func(_ context.Context, item BulkIndexerItem) (string, error) {
						_, err := io.Copy(io.Discard, item.Body) // Reads the body to its end.

						return "fingerprint", err
					}
				}
				bi := newBulkClient(t, server, cfg)

				item := BulkIndexerItem{Index: "idx", Action: actionIndex, Body: readerAt(t, 3, hideLen)}
				if !tt.fingerprint {
					item.DocumentID = "doc"
				}
				require.NoError(t, bi.Add(t.Context(), item))
				require.NoError(t, bi.Close(t.Context()))

				mu.Lock()
				defer mu.Unlock()
				require.Len(t, bodies, tt.wantAttempts)
				for i, body := range bodies {
					lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
					require.Len(t, lines, 2, "attempt %d: an action line and a document", i+1)
					require.Equal(t, offsetDoc, lines[1], "attempt %d sends the whole document", i+1)
				}
			})
		}
	}
}

// TestBulkIndexerSizesABodyFromItsStart: the size the bulk client reserves for an item is
// that of the whole document, not of what is left of it after the position its reader
// was handed in at.
func TestBulkIndexerSizesABodyFromItsStart(t *testing.T) {
	t.Parallel()

	buffered := func(offset int64, hideLen bool) uint64 {
		bi := newBulkClient(t, newFakeServer(newFakeCluster(nil)), BulkIndexerConfig{ShardRouter: NoShardRouter})
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Index: "idx", Action: actionIndex, DocumentID: "doc", Body: readerAt(t, offset, hideLen),
		}))
		stats := bi.Stats()
		require.NoError(t, bi.Close(t.Context()))

		return stats.BufferedBytes
	}

	for _, hideLen := range []bool{false, true} {
		require.Equal(t, buffered(0, hideLen), buffered(3, hideLen), "hideLen=%v", hideLen)
	}
}
