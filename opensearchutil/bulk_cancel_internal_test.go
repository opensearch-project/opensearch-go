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

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// finals counts the callbacks each item received, so a case can assert that
// every item reached exactly one final state and see what it was.
type finals struct {
	succeeded atomic.Int64
	failed    atomic.Int64
	errs      chan error
}

func newFinals(capacity int) *finals { return &finals{errs: make(chan error, capacity)} }

func (f *finals) item(id string) BulkIndexerItem {
	return BulkIndexerItem{
		Action:     "index",
		DocumentID: id,
		Body:       strings.NewReader(`{"a":1}`),
		OnSuccess:  func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { f.succeeded.Add(1) },
		OnFailure: func(_ context.Context, _ BulkIndexerItem, _ opensearchapi.BulkRespItem, err error) {
			f.failed.Add(1)
			f.errs <- err
		},
	}
}

func TestBulkIndexerCloseWaitsForBackoff(t *testing.T) {
	t.Parallel()

	// A policy that backs off briefly and retries once: Close, with no deadline,
	// must not return until the retry has landed and the item is final.
	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeTooManyRequests, outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		RetryPolicy: RetryPolicyFunc(func(
			attempt int, _ FailureScope, _ FailureKind, _ opensearchapi.BulkRespItem, _ error,
		) (time.Duration, bool) {
			return 20 * time.Millisecond, attempt < 2
		}),
	})

	f := newFinals(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))
	require.NoError(t, bi.Close(context.Background()))

	require.Equal(t, int64(1), f.succeeded.Load(), "the retry landed before Close returned")
	require.Zero(t, f.failed.Load())
	require.Len(t, seq.requests(), 2)
}

func TestBulkIndexerCloseEscalatesWhenContextEnds(t *testing.T) {
	t.Parallel()

	// The policy asks for an hour of backoff. Without escalation Close would sit
	// out the hour; with it, ending Close's context cancels the backoff.
	backingOff := make(chan struct{}, 1)
	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeTooManyRequests, outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		RetryPolicy: RetryPolicyFunc(func(
			int, FailureScope, FailureKind, opensearchapi.BulkRespItem, error,
		) (time.Duration, bool) {
			backingOff <- struct{}{}
			return time.Hour, true
		}),
	})

	f := newFinals(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- bi.Close(ctx) }()

	<-backingOff
	cancel()

	require.ErrorIs(t, <-closed, context.Canceled)
	require.Equal(t, int64(1), f.failed.Load(), "the item fails exactly once")
	require.Zero(t, f.succeeded.Load())
	require.ErrorContains(t, <-f.errs, "es_rejected_execution_exception", "with the failure it last drew")
	require.Len(t, seq.requests(), 1, "no request after the cancel")
}

func TestBulkIndexerCancelInterruptsRequestInFlight(t *testing.T) {
	t.Parallel()

	inFlight := make(chan struct{}, 1)
	client := newBulkTestClient(t, withInfo(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return defaultRoundTripFunc(req)
		}
		inFlight <- struct{}{}
		<-req.Context().Done()

		return nil, req.Context().Err()
	}), withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client})

	f := newFinals(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))

	// The flush context never ends on its own, so only Cancel can free the request.
	flushed := make(chan error, 1)
	go func() { flushed <- bi.Flush(t.Context()) }()

	<-inFlight
	bi.Cancel()

	require.ErrorIs(t, <-flushed, context.Canceled)
	require.NoError(t, bi.Close(t.Context()))
	require.Equal(t, int64(1), f.failed.Load(), "exactly one OnFailure")
	require.ErrorIs(t, <-f.errs, context.Canceled)
	require.Zero(t, f.succeeded.Load())
}

func TestBulkIndexerCancelFailsUnsentItems(t *testing.T) {
	t.Parallel()

	const numItems = 12

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	// Neither the size nor the interval can flush, so every item is still waiting
	// when Cancel arrives.
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 4, Client: client})

	f := newFinals(numItems + 1)
	for i := range numItems {
		require.NoError(t, bi.Add(t.Context(), f.item(strings.Repeat("d", i+1))))
	}

	bi.Cancel()
	bi.Cancel() // idempotent

	// An item added after Cancel is refused the same way, and Add still returns nil.
	require.NoError(t, bi.Add(t.Context(), f.item("late")))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(numItems+1), f.failed.Load(), "every item fails exactly once")
	require.Zero(t, f.succeeded.Load())
	for range numItems + 1 {
		require.ErrorIs(t, <-f.errs, ErrIndexerCanceled)
	}
	require.Empty(t, seq.requests(), "nothing is sent after Cancel")
}

func TestBulkIndexerCancelFromCallback(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client})

	var settled atomic.Int64
	for _, id := range []string{"a", "b"} {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", DocumentID: id, Body: strings.NewReader(`{"a":1}`),
			OnSuccess: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) {
				settled.Add(1)
				bi.Cancel() // runs under the worker's lock; must not deadlock
			},
		}))
	}

	done := make(chan error, 1)
	go func() { done <- bi.Close(t.Context()) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung after Cancel was called from a callback")
	}
	require.Equal(t, int64(2), settled.Load(), "both items had already been answered")
}

func TestBulkIndexerCloseWithEndedContextFailsWaitingItems(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 2, Client: client})

	f := newFinals(4)
	for _, id := range []string{"a", "b", "c", "d"} {
		require.NoError(t, bi.Add(t.Context(), f.item(id)))
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, bi.Close(ctx), context.Canceled)
	require.Equal(t, int64(4), f.failed.Load(), "no item is left without a callback")
	require.Zero(t, f.succeeded.Load())
	require.Empty(t, seq.requests())
}

// TestBulkIndexerConstructionContextEndedLeavesNoItemBehind covers workers that
// exited because the construction context ended: whatever was still queued gets
// exactly one callback when Close runs, whether a worker took it first or not.
func TestBulkIndexerConstructionContextEndedLeavesNoItemBehind(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

	ctx, cancel := context.WithCancel(t.Context())
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client, Context: ctx})
	cancel()
	<-bi.(*bulkIndexer).flusherDone

	f := newFinals(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(1), f.succeeded.Load()+f.failed.Load(), "exactly one final callback")
}
