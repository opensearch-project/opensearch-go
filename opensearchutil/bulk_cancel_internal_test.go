// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// callbackCounts counts the callbacks each item received, so a case can assert that
// every item got exactly one, and see which.
type callbackCounts struct {
	succeeded atomic.Int64
	failed    atomic.Int64
	errs      chan error
}

func newCallbackCounts(capacity int) *callbackCounts {
	return &callbackCounts{errs: make(chan error, capacity)}
}

func (f *callbackCounts) item(id string) BulkIndexerItem {
	return BulkIndexerItem{
		Action:     "index",
		DocumentID: id,
		Body:       strings.NewReader(`{"a":1}`),
		OnSuccess:  func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { f.succeeded.Add(1) },
		OnError: func(_ context.Context, _ BulkIndexerItem, err *BulkError) {
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
			attempt int, _ *BulkError,
		) (time.Duration, bool) {
			return 20 * time.Millisecond, attempt < 2
		}),
	})

	f := newCallbackCounts(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))
	require.NoError(t, bi.Close(t.Context()))

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
			int, *BulkError,
		) (time.Duration, bool) {
			backingOff <- struct{}{}
			return time.Hour, true
		}),
	})

	f := newCallbackCounts(1)
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

	f := newCallbackCounts(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))

	// The flush context never ends on its own, so only Cancel can free the request.
	flushed := make(chan error, 1)
	go func() { flushed <- bi.Flush(t.Context()) }()

	<-inFlight
	bi.Cancel()

	require.ErrorIs(t, <-flushed, context.Canceled)
	require.NoError(t, bi.Close(t.Context()))
	require.Equal(t, int64(1), f.failed.Load(), "exactly one OnError")
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

	f := newCallbackCounts(numItems + 1)
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

	var succeeded atomic.Int64
	for _, id := range []string{"a", "b"} {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", DocumentID: id, Body: strings.NewReader(`{"a":1}`),
			OnSuccess: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) {
				succeeded.Add(1)
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
	require.Equal(t, int64(2), succeeded.Load(), "both items had already been answered")
}

func TestBulkIndexerCloseWithEndedContextFailsWaitingItems(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 2, Client: client})

	f := newCallbackCounts(4)
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

	f := newCallbackCounts(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(1), f.succeeded.Load()+f.failed.Load(), "exactly one callback")
}

func TestBulkIndexerCancelDoesNotCountARetry(t *testing.T) {
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

	var asked atomic.Int64
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		// A policy that always retries: only the canceled context can stop it.
		RetryPolicy: RetryPolicyFunc(func(
			int, *BulkError,
		) (time.Duration, bool) {
			asked.Add(1)
			return time.Hour, true
		}),
	})

	f := newCallbackCounts(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))

	go func() { _ = bi.Flush(t.Context()) }()

	<-inFlight
	bi.Cancel()
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(1), f.failed.Load())
	require.Zero(t, asked.Load(), "a canceled indexer sends nothing more, so the policy is not asked")
	require.Equal(t, [numErrorKinds]uint64{}, bi.Stats().NumRetries, "no retry was sent, so none is counted")
}

// failureCall is what one item's OnError was handed.
type failureCall struct {
	ID   string
	Resp opensearchapi.BulkRespItem
	Err  *BulkError
}

// recordingItem returns an item whose OnError appends to calls.
func recordingItem(id string, calls *[]failureCall) BulkIndexerItem {
	return BulkIndexerItem{
		Action:     "index",
		DocumentID: id,
		OnError: func(_ context.Context, item BulkIndexerItem, err *BulkError) {
			*calls = append(*calls, failureCall{ID: item.DocumentID, Resp: err.Resp, Err: err})
		},
	}
}

// TestWorkerFailCanceled: a canceled worker fails every buffered item with
// ErrIndexerCanceled and sends nothing. An item with an OnError hears of it
// there; one without is reported through the returned error. The observer sees
// OnItemDone for each item, but no failure event, since a cancel is not a failure.
func TestWorkerFailCanceled(t *testing.T) {
	t.Parallel()

	t.Run("items with and without OnError", func(t *testing.T) {
		t.Parallel()

		var calls []failureCall
		obs := &cancelObserver{}
		w := newTestWorker()
		w.bi.config.Observer = obs
		w.items = []BulkIndexerItem{
			recordingItem("a", &calls),
			{Action: "index", DocumentID: "b"},
			recordingItem("c", &calls),
			{Action: "index", DocumentID: "d"},
		}

		err := w.failCanceled(t.Context())

		require.ErrorIs(t, err, ErrIndexerCanceled)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 2, strings.Count(err.Error(), ErrIndexerCanceled.Error()), "once for each item without an OnError")

		require.Len(t, calls, 2)
		for i, id := range []string{"a", "c"} {
			require.Equal(t, id, calls[i].ID)
			require.ErrorIs(t, calls[i].Err, ErrIndexerCanceled)
			require.Equal(t, LayerIndexer, calls[i].Err.Layer)
			require.Equal(t, KindCanceled, calls[i].Err.Kind)
		}

		require.Equal(t, uint64(4), w.bi.stats.numFailed.Load())
		require.Equal(t, int64(4), obs.done.Load())
		require.Equal(t, int64(4), obs.canceled.Load())
		require.Zero(t, obs.failures.Load())
	})

	t.Run("items that all have OnError leave nothing to report", func(t *testing.T) {
		t.Parallel()

		var calls []failureCall
		w := newTestWorker()
		w.items = []BulkIndexerItem{recordingItem("a", &calls), recordingItem("b", &calls)}

		require.NoError(t, w.failCanceled(t.Context()))
		require.Len(t, calls, 2)
	})

	t.Run("an empty buffer has nothing to fail", func(t *testing.T) {
		t.Parallel()

		w := newTestWorker()

		require.NoError(t, w.failCanceled(t.Context()))
		require.Zero(t, w.bi.stats.numFailed.Load())
	})
}

// cancelObserver counts what the observer hears of canceled items.
type cancelObserver struct {
	BaseBulkIndexerObserver

	failures atomic.Int64
	done     atomic.Int64
	canceled atomic.Int64
}

func (o *cancelObserver) OnItemError(context.Context, BulkErrorEvent) { o.failures.Add(1) }

func (o *cancelObserver) OnItemDone(_ context.Context, ev BulkItemEvent) {
	o.done.Add(1)
	if ev.Failed && errors.Is(ev.Err, ErrIndexerCanceled) {
		o.canceled.Add(1)
	}
}

func TestBulkIndexerCancelIsNotReportedAsAnErrorKind(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	obs := &cancelObserver{}
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client, Observer: obs})

	f := newCallbackCounts(2)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))
	bi.Cancel()
	// Added after Cancel, so refused on the caller's goroutine.
	require.NoError(t, bi.Add(t.Context(), f.item("late")))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, int64(2), f.failed.Load())
	require.Equal(t, int64(2), obs.done.Load(), "each item is done")
	require.Equal(t, int64(2), obs.canceled.Load(), "with ErrIndexerCanceled")
	require.Zero(t, obs.failures.Load(), "and none is a failure of any kind")
}
