// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBulkIndexerFlushThatEndedStillLetsBufferReportItsError: a Flush whose
// context ends returns the context's error, so the failure of the buffer it was
// waiting for must reach OnError, since nobody else is waiting for it.
func TestBulkIndexerFlushThatEndedStillLetsBufferReportItsError(t *testing.T) {
	t.Parallel()

	var (
		held    = make(chan struct{}, 1)
		release = make(chan struct{})
	)
	client := newBulkTestClient(t, withInfo(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return defaultRoundTripFunc(req)
		}
		held <- struct{}{}
		<-release

		return jsonResponse(http.StatusInternalServerError, `{"error":"boom"}`), nil
	}), withoutTransportRetry)

	onErrors := make(chan error, 4)
	var reported atomic.Int64
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		// One byte sends the buffer on size, with no Flush involved.
		FlushBytes: 1,
		OnError: func(_ context.Context, err error) {
			reported.Add(1)
			onErrors <- err
		},
	})

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: "index", DocumentID: "a", Body: strings.NewReader(`{"a":1}`),
	}))
	<-held

	flushCtx, cancel := context.WithCancel(t.Context())
	flushed := make(chan error, 1)
	go func() { flushed <- bi.Flush(flushCtx) }()

	// Flush has joined the buffer once the cancel can end it; the request is held,
	// so the buffer is still in flight.
	require.Eventually(t, func() bool {
		s := bi.(*bulkIndexer).sched
		s.Lock()
		defer s.Unlock()
		for b := range s.live {
			if b.waiters > 0 {
				return true
			}
		}

		return false
	}, 10*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-flushed, context.Canceled)

	close(release)
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(1), reported.Load(), "the failure reaches OnError once, as it would without the Flush")
	require.ErrorContains(t, <-onErrors, "boom")
}
