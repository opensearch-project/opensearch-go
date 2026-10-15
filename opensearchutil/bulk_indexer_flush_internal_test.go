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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestBulkIndexerFlushCompletesCallbacksBeforeReturning pins the guarantee a
// caller commits an external position on: when Flush returns, every item it
// covered has already had its OnSuccess or OnFailure called. Nothing closes the
// indexer or waits between Flush and the check, so a callback that ran after
// the drain was reported done would be counted short.
func TestBulkIndexerFlushCompletesCallbacksBeforeReturning(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 4
		numItems   = 24
	)

	tests := []struct {
		name          string
		outcome       bulkOutcome
		wantSucceeded int64
		wantFailed    int64
		wantFlushErr  bool
	}{
		{name: "every item accepted", outcome: outcomeOK, wantSucceeded: numItems},
		{name: "every item rejected", outcome: outcomeEveryItemRejected, wantFailed: numItems},
		{name: "request failed", outcome: outcomeConnectionError, wantFailed: numItems, wantFlushErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.outcome}}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
			bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: numWorkers, Client: client})

			// Workers run the callbacks concurrently, so the counts are atomic.
			var succeeded, failed atomic.Int64
			for i := range numItems {
				require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
					Action:     "index",
					DocumentID: fmt.Sprintf("doc_%d", i),
					Body:       strings.NewReader(`{"a":1}`),
					OnSuccess: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) {
						succeeded.Add(1)
					},
					OnFailure: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem, error) {
						failed.Add(1)
					},
				}))
			}

			flushErr := bi.Flush(t.Context())

			// Read before Close, which would join the workers and hide a
			// callback that had not run by the time Flush returned.
			require.Equal(t, tt.wantSucceeded, succeeded.Load(), "OnSuccess calls at Flush return")
			require.Equal(t, tt.wantFailed, failed.Load(), "OnFailure calls at Flush return")
			if tt.wantFlushErr {
				require.Error(t, flushErr)
			} else {
				require.NoError(t, flushErr)
			}

			require.NoError(t, bi.Close(t.Context()))
		})
	}
}

// TestBulkIndexerFlushCallbacksAfterContextEnds pins what a Flush whose context
// ends mid-drain leaves behind: it returns the context's error, and the covered
// item still reaches its callback exactly once, as a failure carrying the
// cancellation, because the bulk request ran on that context.
func TestBulkIndexerFlushCallbacksAfterContextEnds(t *testing.T) {
	t.Parallel()

	inFlight := make(chan struct{}, 1)
	client := newBulkTestClient(t, withInfo(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return defaultRoundTripFunc(req)
		}

		inFlight <- struct{}{}
		// Hold the request until its context ends, as a real transport does.
		<-req.Context().Done()

		return nil, req.Context().Err()
	}), withoutTransportRetry)

	var (
		successes atomic.Int64
		failures  = make(chan error, 2)
	)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client})
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     "index",
		DocumentID: "doc",
		Body:       strings.NewReader(`{"a":1}`),
		OnSuccess:  func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { successes.Add(1) },
		OnFailure: func(_ context.Context, _ BulkIndexerItem, _ opensearchapi.BulkRespItem, err error) {
			failures <- err
		},
	}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	flushed := make(chan error, 1)
	go func() { flushed <- bi.Flush(ctx) }()

	<-inFlight
	cancel()

	require.ErrorIs(t, <-flushed, context.Canceled)
	require.NoError(t, bi.Close(t.Context()))

	// Close has joined the workers, so the callback has run by now, but whether
	// it ran before or after Flush returned is not promised.
	require.Len(t, failures, 1, "OnFailure fires exactly once")
	require.ErrorIs(t, <-failures, context.Canceled)
	require.Zero(t, successes.Load())
	require.Equal(t, uint64(1), bi.Stats().NumFailed)
}
