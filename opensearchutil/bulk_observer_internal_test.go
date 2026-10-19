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
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// eventLog is a BulkIndexerObserver that records each event as one line, in
// arrival order, so a case can state the whole sequence it expects.
type eventLog struct {
	BaseBulkIndexerObserver

	mu       sync.Mutex
	lines    []string
	requests []BulkRequestEvent
	failures []BulkErrorEvent
}

func (l *eventLog) add(line string) {
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

func (l *eventLog) OnRequestEnd(_ context.Context, e BulkRequestEvent) {
	l.mu.Lock()
	l.requests = append(l.requests, e)
	l.mu.Unlock()
	l.add(fmt.Sprintf("request-end a=%d items=%d status=%d err=%t", e.Attempt, e.Items, e.StatusCode, e.Err != nil))
}

func (l *eventLog) OnRequestError(_ context.Context, e BulkErrorEvent) {
	l.mu.Lock()
	l.failures = append(l.failures, e)
	l.mu.Unlock()
	l.add(fmt.Sprintf("request-failure scope=%s a=%d kind=%s items=%d status=%d retry=%t delay=%s err=%t",
		e.Err.Layer, e.Attempt, e.Err.Kind, e.Items, e.Err.Status, e.Retrying, e.Delay, e.Err.Err != nil))
}

func (l *eventLog) OnItemError(_ context.Context, e BulkErrorEvent) {
	l.mu.Lock()
	l.failures = append(l.failures, e)
	l.mu.Unlock()
	l.add(fmt.Sprintf("item-failure scope=%s a=%d kind=%s id=%s status=%d retry=%t delay=%s err=%t",
		e.Err.Layer, e.Attempt, e.Err.Kind, e.DocumentID, e.Err.Status, e.Retrying, e.Delay, e.Err.Err != nil))
}

func (l *eventLog) OnItemDone(_ context.Context, e BulkItemEvent) {
	l.add(fmt.Sprintf("done id=%s attempts=%d failed=%t status=%d err=%t meta=%v",
		e.DocumentID, e.Attempts, e.Failed, e.Resp.Status, e.Err != nil && e.Err.Err != nil, e.Metadata))
}

func TestBulkIndexerObserver(t *testing.T) {
	t.Parallel()

	const nanosecond = "1ns"

	// retryOnce retries the first failure after one nanosecond, and no other.
	retryOnce := RetryPolicyFunc(func(
		attempt int, _ *BulkError,
	) (time.Duration, bool) {
		return time.Nanosecond, attempt < 2
	})

	tests := []struct {
		name     string
		outcomes []bulkOutcome
		policy   RetryPolicy
		items    []BulkIndexerItem
		want     []string
		wantStat [numErrorKinds]uint64
	}{
		{
			name:     "a request failure retried, then accepted",
			outcomes: []bulkOutcome{outcomeTooManyRequests, outcomeOK},
			policy:   retryOnce,
			items:    []BulkIndexerItem{{DocumentID: "a"}, {DocumentID: "b"}},
			want: []string{
				"request-end a=1 items=2 status=429 err=true",
				"request-failure scope=request a=1 kind=rejected items=2 status=429 retry=true delay=1ns err=true",
				"request-end a=2 items=2 status=200 err=false",
				"done id=a attempts=2 failed=false status=201 err=false meta=<nil>",
				"done id=b attempts=2 failed=false status=201 err=false meta=<nil>",
			},
			wantStat: [numErrorKinds]uint64{KindRejected: 2},
		},
		{
			name:     "an item rejection retried, then accepted",
			outcomes: []bulkOutcome{outcomeItems(map[string]int{"b": 429}), outcomeOK},
			policy:   retryOnce,
			items:    []BulkIndexerItem{{DocumentID: "a"}, {DocumentID: "b", Metadata: 7}},
			want: []string{
				"request-end a=1 items=2 status=200 err=false",
				"done id=a attempts=1 failed=false status=201 err=false meta=<nil>",
				"item-failure scope=item a=1 kind=rejected id=b status=429 retry=true delay=" + nanosecond + " err=false",
				"request-end a=2 items=1 status=200 err=false",
				"done id=b attempts=2 failed=false status=201 err=false meta=7",
			},
			wantStat: [numErrorKinds]uint64{KindRejected: 1},
		},
		{
			name:     "a request failure with no policy fails every item",
			outcomes: []bulkOutcome{outcomeConnectionError},
			items:    []BulkIndexerItem{{DocumentID: "a"}, {DocumentID: "b"}},
			want: []string{
				"request-end a=1 items=2 status=0 err=true",
				"request-failure scope=request a=1 kind=transport items=2 status=0 retry=false delay=0s err=true",
				"done id=a attempts=1 failed=true status=0 err=true meta=<nil>",
				"done id=b attempts=1 failed=true status=0 err=true meta=<nil>",
			},
		},
		{
			name:     "a permanent item failure is not retried",
			outcomes: []bulkOutcome{outcomeItems(map[string]int{"a": 409})},
			items:    []BulkIndexerItem{{DocumentID: "a"}},
			want: []string{
				"request-end a=1 items=1 status=200 err=false",
				"item-failure scope=item a=1 kind=permanent id=a status=409 retry=false delay=0s err=false",
				"done id=a attempts=1 failed=true status=409 err=false meta=<nil>",
			},
		},
		{
			name:     "an item that cannot be serialized is never sent",
			outcomes: []bulkOutcome{outcomeOK},
			items:    []BulkIndexerItem{{DocumentID: "a", WaitForActiveShards: math.NaN()}},
			want: []string{
				"item-failure scope=item a=0 kind=permanent id=a status=0 retry=false delay=0s err=true",
				"done id=a attempts=0 failed=true status=0 err=true meta=<nil>",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: tt.outcomes}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
			log := &eventLog{}
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers:  1,
				Client:      client,
				Observer:    log,
				RetryPolicy: tt.policy,
			})

			for _, item := range tt.items {
				item.Action = "index"
				if item.Body == nil {
					item.Body = strings.NewReader(`{"a":1}`)
				}
				require.NoError(t, bi.Add(t.Context(), item))
			}
			_ = bi.Flush(t.Context())
			require.NoError(t, bi.Close(t.Context()))

			log.mu.Lock()
			defer log.mu.Unlock()
			require.Equal(t, tt.want, log.lines)
			require.Equal(t, tt.wantStat, bi.Stats().NumRetries)

			// Each request event reports the size of the body the transport saw.
			bodies := seq.bodies()
			require.Len(t, log.requests, len(bodies))
			for i, e := range log.requests {
				require.Equal(t, len(bodies[i]), e.Bytes, "request %d bytes", i)
				require.GreaterOrEqual(t, e.Duration, time.Duration(0))
			}
		})
	}
}

// TestBulkIndexerObserverScopes pins that the two failure levels reach separate
// observer methods with their own scope and counts.
func TestErrorLayerString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		layer ErrorLayer
		want  string
	}{
		{LayerUnknown, "unknown"},
		{LayerItem, "item"},
		{LayerRequest, "request"},
		{LayerIndex, "index"},
		{LayerIndexer, "indexer"},
		{ErrorLayer(42), "ErrorLayer(42)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.layer.String())
		})
	}
}

// doneCounter overrides one observer method and inherits the rest.
type doneCounter struct {
	BaseBulkIndexerObserver

	done atomic.Int64
}

func (o *doneCounter) OnItemDone(context.Context, BulkItemEvent) { o.done.Add(1) }

// TestBaseBulkIndexerObserver: an observer that embeds the base type and overrides
// one method is a complete observer; the methods it inherits do nothing.
func TestBaseBulkIndexerObserver(t *testing.T) {
	t.Parallel()

	t.Run("the inherited methods are safe to call", func(t *testing.T) {
		t.Parallel()

		o := &doneCounter{}
		var obs BulkIndexerObserver = o

		require.NotPanics(t, func() {
			obs.OnRequestEnd(t.Context(), BulkRequestEvent{})
			obs.OnRequestError(t.Context(), BulkErrorEvent{})
			obs.OnItemError(t.Context(), BulkErrorEvent{})
		})
		require.Zero(t, o.done.Load(), "only the overridden method counts")

		obs.OnItemDone(t.Context(), BulkItemEvent{})
		require.Equal(t, int64(1), o.done.Load())
	})

	t.Run("an indexer drives every method of it", func(t *testing.T) {
		t.Parallel()

		seq := &bulkSequence{outcomes: []bulkOutcome{outcomeTooManyRequests, outcomeOK}}
		client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
		o := &doneCounter{}
		bi := newBulkIndexer(t, BulkIndexerConfig{
			NumWorkers: 1,
			Client:     client,
			Observer:   o,
			RetryPolicy: RetryPolicyFunc(func(
				attempt int, _ *BulkError,
			) (time.Duration, bool) {
				return time.Nanosecond, attempt < 2
			}),
		})

		f := newCallbackCounts(1)
		require.NoError(t, bi.Add(t.Context(), f.item("a")))
		require.NoError(t, bi.Close(t.Context()))

		require.Equal(t, int64(1), f.succeeded.Load())
		require.Equal(t, int64(1), o.done.Load(), "the item is done once, after its retry")
		require.Len(t, seq.requests(), 2)
	})
}

func TestBulkIndexerObserverScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   bulkOutcome
		wantScope ErrorLayer
		wantItems int
		wantID    string
	}{
		{name: "whole request", outcome: outcomeTooManyRequests, wantScope: LayerRequest, wantItems: 3},
		{name: "one item", outcome: outcomeItems(map[string]int{"b": 429}), wantScope: LayerItem, wantItems: 1, wantID: "b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.outcome}}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
			log := &eventLog{}
			bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client, Observer: log})

			for _, id := range []string{"a", "b", "c"} {
				require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
					Action: "index", DocumentID: id, Body: strings.NewReader(`{"a":1}`),
				}))
			}
			_ = bi.Flush(t.Context())
			require.NoError(t, bi.Close(t.Context()))

			log.mu.Lock()
			defer log.mu.Unlock()
			require.Len(t, log.failures, 1)
			require.Equal(t, tt.wantScope, log.failures[0].Err.Layer)
			require.Equal(t, tt.wantItems, log.failures[0].Items)
			require.Equal(t, tt.wantID, log.failures[0].DocumentID)
			// The method matches the scope: a request failure never arrives as an item failure.
			if tt.wantScope == LayerRequest {
				require.Contains(t, log.lines[1], "request-failure")
			} else {
				require.Contains(t, strings.Join(log.lines, "\n"), "item-failure")
				require.NotContains(t, strings.Join(log.lines, "\n"), "request-failure")
			}
		})
	}
}

// TestBulkIndexerRetryWaitUsesContextAndTimer pins that a retry waits on a
// timer for the delay the policy asked for and on the context together: the wait
// ends when the context does, long before the delay, and the delay is not
// skipped while the context is alive.
func TestBulkIndexerRetryWaitUsesContextAndTimer(t *testing.T) {
	t.Parallel()

	t.Run("ends with the context, not the delay", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- waitForRetry(ctx, time.Hour) }()
		cancel()

		require.ErrorIs(t, <-done, context.Canceled)
	})

	t.Run("a timer ends it when the context lives", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, waitForRetry(t.Context(), time.Millisecond))
	})

	t.Run("a dead context stops a zero delay", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.ErrorIs(t, waitForRetry(ctx, 0), context.Canceled)
		require.NoError(t, waitForRetry(t.Context(), 0))
	})
}

// ctxObserver records the flush-context value each event arrived with.
type ctxObserver struct {
	BaseBulkIndexerObserver

	mu     sync.Mutex
	values []any
}

func (o *ctxObserver) record(ctx context.Context) {
	o.mu.Lock()
	o.values = append(o.values, ctx.Value(flushContextKey{}))
	o.mu.Unlock()
}

func (o *ctxObserver) OnRequestEnd(ctx context.Context, _ BulkRequestEvent) { o.record(ctx) }
func (o *ctxObserver) OnItemDone(ctx context.Context, _ BulkItemEvent)      { o.record(ctx) }

// TestBulkIndexerObserverReceivesFlushContext pins that observer calls run on
// the context OnFlushStart returned, so a tracing observer can join the flush's
// span.
func TestBulkIndexerObserverReceivesFlushContext(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	obs := &ctxObserver{}
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		Observer:   obs,
		OnFlushStart: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, flushContextKey{}, "span")
		},
	})

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: "index", DocumentID: "a", Body: strings.NewReader(`{"a":1}`),
	}))
	require.NoError(t, bi.Close(t.Context()))

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Equal(t, []any{"span", "span"}, obs.values, "request end and item done")
}
