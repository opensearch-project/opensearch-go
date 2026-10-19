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
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func TestClassifyFailure(t *testing.T) {
	t.Parallel()

	t.Run("item", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			status int
			want   ErrorKind
		}{
			{name: "write queue full", status: http.StatusTooManyRequests, want: KindRejected},
			{name: "unavailable", status: http.StatusServiceUnavailable, want: KindUnavailable},
			{name: "version conflict", status: http.StatusConflict, want: KindPermanent},
			{name: "mapping error", status: http.StatusBadRequest, want: KindPermanent},
			{name: "not found", status: http.StatusNotFound, want: KindPermanent},
			{name: "server error", status: http.StatusInternalServerError, want: KindTransport},
			{name: "bad gateway", status: http.StatusBadGateway, want: KindTransport},
			{name: "gateway timeout", status: http.StatusGatewayTimeout, want: KindTransport},
			{name: "no status", status: 0, want: KindPermanent},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				require.Equal(t, tt.want, classifyItemFailure(opensearchapi.BulkRespItem{Status: tt.status}))
			})
		}
	})

	t.Run("request", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			status int
			err    error
			want   ErrorKind
		}{
			{name: "no response", err: errors.New("connection reset"), want: KindTransport},
			{name: "context deadline", err: context.DeadlineExceeded, want: KindTransport},
			{name: "write queue full", status: http.StatusTooManyRequests, err: errors.New("x"), want: KindRejected},
			{name: "unavailable", status: http.StatusServiceUnavailable, err: errors.New("x"), want: KindUnavailable},
			{name: "bad gateway", status: http.StatusBadGateway, err: errors.New("x"), want: KindTransport},
			{name: "gateway timeout", status: http.StatusGatewayTimeout, err: errors.New("x"), want: KindTransport},
			{name: "server error", status: http.StatusInternalServerError, err: errors.New("x"), want: KindTransport},
			{name: "unreadable success", status: http.StatusOK, err: errors.New("x"), want: KindTransport},
			{name: "bad request", status: http.StatusBadRequest, err: errors.New("x"), want: KindPermanent},
			{name: "request too large", status: http.StatusRequestEntityTooLarge, err: errors.New("x"), want: KindPermanent},
			{
				name: "unpairable response", status: http.StatusOK,
				err:  fmt.Errorf("flush: %w", &bulkResponseMismatchError{got: 2, want: 1}),
				want: KindPermanent,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				require.Equal(t, tt.want, classifyBatchFailure(tt.status, tt.err))
			})
		}
	})
}

// policyCall is what one Retry call was handed.
type policyCall struct {
	Scope   ErrorLayer
	Attempt int
	Kind    ErrorKind
	Status  int  // the response item's status; 0 for a failure of the whole request
	HasErr  bool // err had a cause, as a request's error
}

// recordingPolicy records every call and answers with decide.
type recordingPolicy struct {
	decide func(attempt int, kind ErrorKind) (time.Duration, bool)

	mu    sync.Mutex
	calls []policyCall
}

func (p *recordingPolicy) Retry(
	attempt int, err *BulkError,
) (time.Duration, bool) {
	p.mu.Lock()
	p.calls = append(p.calls, policyCall{
		Scope: err.Layer, Attempt: attempt, Kind: err.Kind, Status: err.Resp.Status, HasErr: err.Err != nil,
	})
	p.mu.Unlock()

	return p.decide(attempt, err.Kind)
}

func (p *recordingPolicy) recorded() []policyCall {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]policyCall(nil), p.calls...)
}

// retryThrice retries every failure until the third attempt has failed.
func retryThrice(attempt int, _ ErrorKind) (time.Duration, bool) { return 0, attempt < 3 }

// neverRetry declines every failure.
func neverRetry(int, ErrorKind) (time.Duration, bool) { return 0, false }

// outcomeItems accepts the request and gives each action the status listed for
// its document ID, or 201 when it is not listed.
func outcomeItems(statuses map[string]int) bulkOutcome {
	return func(actions []string) (*http.Response, error) {
		items := make([]string, len(actions))
		errored := false
		for i, action := range actions {
			op, id, _ := strings.Cut(action, ":")
			status, ok := statuses[id]
			if !ok {
				status = http.StatusCreated
			}

			errField := ""
			if status >= http.StatusMultipleChoices {
				errored = true
				errType := "simulated_exception"
				switch status {
				case http.StatusTooManyRequests:
					errType = "es_rejected_execution_exception"
				case http.StatusConflict:
					errType = "version_conflict_engine_exception"
				}
				errField = fmt.Sprintf(`,"error":{"type":%q,"reason":"simulated"}`, errType)
			}
			items[i] = fmt.Sprintf(`{%q:{"_id":%q,"status":%d%s}}`, op, id, status, errField)
		}

		return jsonResponse(http.StatusOK,
			fmt.Sprintf(`{"took":1,"errors":%t,"items":[%s]}`, errored, strings.Join(items, ","))), nil
	}
}

// outcomeStatus answers the whole request with an error body and the given status.
func outcomeStatus(status int) bulkOutcome {
	return func([]string) (*http.Response, error) {
		return jsonResponse(status,
			`{"error":{"type":"simulated_exception","reason":"simulated"},"status":`+fmt.Sprint(status)+`}`), nil
	}
}

// retryRun is what an indexer did with a set of items under a policy.
type retryRun struct {
	succeeded []string
	failed    []string
	failErrs  []*BulkError
	onErrors  []error
	flushErr  error
	seq       *bulkSequence
	policy    *recordingPolicy
	stats     BulkIndexerStats
}

// inherit is the flush context that is the test's own.
func inherit(ctx context.Context) context.Context { return ctx }

// runRetry adds one item per id, drains them with a single Flush on the context
// flushCtx derives from the test's, and closes the indexer.
func runRetry(
	t *testing.T, flushCtx func(context.Context) context.Context, outcomes []bulkOutcome, policy *recordingPolicy,
	ids ...string,
) *retryRun {
	t.Helper()

	run := &retryRun{seq: &bulkSequence{outcomes: outcomes}, policy: policy}
	client := newBulkTestClient(t, run.seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers:       1,
		Client:           client,
		RetryPolicy:      policy,
		OnUnhandledError: func(_ context.Context, err error) { run.onErrors = append(run.onErrors, err) },
	})

	for _, id := range ids {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action:     "index",
			DocumentID: id,
			Body:       strings.NewReader(`{"title":"` + id + `"}`),
			OnSuccess: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
				run.succeeded = append(run.succeeded, item.DocumentID)
			},
			OnError: func(_ context.Context, item BulkIndexerItem, err *BulkError) {
				run.failed = append(run.failed, item.DocumentID)
				run.failErrs = append(run.failErrs, err)
			},
		}))
	}

	run.flushErr = bi.Flush(flushCtx(t.Context()))
	require.NoError(t, bi.Close(t.Context()))
	run.stats = bi.Stats()

	return run
}

// TestBulkIndexerRetryPolicyKinds offers each failure kind to the policy with an
// injected failure and checks what the policy was told. The policy declines, so
// each item fails once, with the failure it was offered, and no request follows.
func TestBulkIndexerRetryPolicyKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outcome    bulkOutcome
		want       policyCall
		wantItemOK bool // the failure is an item's: OnError gets a nil err
	}{
		{
			name:    "request refused for a full write queue",
			outcome: outcomeTooManyRequests,
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindRejected, HasErr: true},
		},
		{
			name:    "request refused as unavailable",
			outcome: outcomeUnavailable,
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindUnavailable, HasErr: true},
		},
		{
			name:    "request failed before a response",
			outcome: outcomeConnectionError,
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindTransport, HasErr: true},
		},
		{
			name:    "request answered with a bad gateway",
			outcome: outcomeStatus(http.StatusBadGateway),
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindTransport, HasErr: true},
		},
		{
			name:    "request refused as malformed",
			outcome: outcomeStatus(http.StatusBadRequest),
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindPermanent, HasErr: true},
		},
		{
			name:    "response cannot be paired with the items",
			outcome: outcomeExtraItem,
			want:    policyCall{Scope: LayerRequest, Attempt: 1, Kind: KindPermanent, HasErr: true},
		},
		{
			name:       "item rejected for a full write queue",
			outcome:    outcomeItems(map[string]int{"a": http.StatusTooManyRequests}),
			want:       policyCall{Scope: LayerItem, Attempt: 1, Kind: KindRejected, Status: http.StatusTooManyRequests},
			wantItemOK: true,
		},
		{
			name:       "item unavailable",
			outcome:    outcomeItems(map[string]int{"a": http.StatusServiceUnavailable}),
			want:       policyCall{Scope: LayerItem, Attempt: 1, Kind: KindUnavailable, Status: http.StatusServiceUnavailable},
			wantItemOK: true,
		},
		{
			name:       "item hit a version conflict",
			outcome:    outcomeItems(map[string]int{"a": http.StatusConflict}),
			want:       policyCall{Scope: LayerItem, Attempt: 1, Kind: KindPermanent, Status: http.StatusConflict},
			wantItemOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := &recordingPolicy{decide: neverRetry}
			run := runRetry(t, inherit, []bulkOutcome{tt.outcome, outcomeOK}, policy, "a")

			require.Equal(t, []policyCall{tt.want}, policy.recorded(), "what the policy was told")
			require.Len(t, run.seq.requests(), 1, "a declined retry sends nothing more")
			require.Equal(t, []string{"a"}, run.failed)
			require.Empty(t, run.succeeded)
			require.Len(t, run.failErrs, 1)
			require.Equal(t, !tt.wantItemOK, run.failErrs[0].Err != nil, "OnError err has a cause beyond the item's response")
			require.Equal(t, uint64(1), run.stats.NumFailed)
		})
	}
}

func TestBulkIndexerRetryPolicy(t *testing.T) {
	t.Parallel()

	t.Run("a rejected request followed by success is one success", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: retryThrice}
		run := runRetry(t, inherit,
			[]bulkOutcome{outcomeTooManyRequests, outcomeOK},
			policy, "a", "b")

		require.NoError(t, run.flushErr)
		require.Empty(t, run.onErrors)
		require.Equal(t, []string{"a", "b"}, run.succeeded, "each item succeeds once")
		require.Empty(t, run.failed, "the 429 is never reported")
		require.Equal(t, []policyCall{{Scope: LayerRequest, Attempt: 1, Kind: KindRejected, HasErr: true}}, policy.recorded(),
			"one decision for the request, not one per item")
		require.Equal(t, [][]string{{"index:a", "index:b"}, {"index:a", "index:b"}}, run.seq.requests())
		bodies := run.seq.bodies()
		require.Equal(t, bodies[0], bodies[1], "the retry carries the same bytes")
		require.Equal(t, BulkIndexerStats{
			NumAdded: 2, NumUngroupedItems: 2, NumFlushed: 2, NumIndexed: 2, NumRequests: 2,
			NumRetries: [numErrorKinds]uint64{KindRejected: 2},
			Errors:     errorsAt(LayerRequest, KindRejected, 1),
		}, withoutLoad(run.stats))
	})

	t.Run("only the rejected item is sent again", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: retryThrice}
		run := runRetry(t, inherit, []bulkOutcome{
			outcomeItems(map[string]int{"b": http.StatusTooManyRequests}),
			outcomeOK,
		}, policy, "a", "b", "c")

		require.NoError(t, run.flushErr)
		require.Equal(t, []string{"a", "c", "b"}, run.succeeded, "a and c succeed on the first request, b on the second")
		require.Empty(t, run.failed)
		require.Equal(t, []policyCall{{Scope: LayerItem, Attempt: 1, Kind: KindRejected, Status: http.StatusTooManyRequests}},
			policy.recorded())
		require.Equal(t, [][]string{{"index:a", "index:b", "index:c"}, {"index:b"}}, run.seq.requests())
		require.Equal(t, []string{`{"index":{"_id":"b"}}`, `{"title":"b"}`},
			strings.Split(strings.TrimSuffix(run.seq.bodies()[1], "\n"), "\n"),
			"the retry carries b's metadata and body, rewound")
		require.Equal(t, BulkIndexerStats{
			NumAdded: 3, NumUngroupedItems: 3, NumFlushed: 3, NumIndexed: 3, NumRequests: 2,
			NumRetries: [numErrorKinds]uint64{KindRejected: 1},
			Errors:     errorsAt(LayerItem, KindRejected, 1),
		}, withoutLoad(run.stats))
	})

	t.Run("attempts count up until the policy gives up", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: retryThrice}
		run := runRetry(t, inherit, []bulkOutcome{outcomeTooManyRequests}, policy, "a")

		require.Equal(t, []policyCall{
			{Scope: LayerRequest, Attempt: 1, Kind: KindRejected, HasErr: true},
			{Scope: LayerRequest, Attempt: 2, Kind: KindRejected, HasErr: true},
			{Scope: LayerRequest, Attempt: 3, Kind: KindRejected, HasErr: true},
		}, policy.recorded())
		require.Len(t, run.seq.requests(), 3)
		require.Equal(t, []string{"a"}, run.failed, "one OnError, after the last attempt")
		require.ErrorContains(t, run.failErrs[0], "es_rejected_execution_exception")
		require.Error(t, run.flushErr)
		require.Equal(t, uint64(1), run.stats.NumFailed)
		require.Equal(t, uint64(3), run.stats.NumRequests)
	})

	t.Run("an item that is done drops out of later requests", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: func(attempt int, kind ErrorKind) (time.Duration, bool) {
			return 0, kind != KindPermanent && attempt < 5
		}}
		run := runRetry(t, inherit, []bulkOutcome{
			outcomeItems(map[string]int{"a": http.StatusTooManyRequests, "b": http.StatusConflict}),
			outcomeItems(map[string]int{"a": http.StatusServiceUnavailable}),
			outcomeOK,
		}, policy, "a", "b")

		require.Equal(t, []policyCall{
			{Scope: LayerItem, Attempt: 1, Kind: KindRejected, Status: http.StatusTooManyRequests},
			{Scope: LayerItem, Attempt: 1, Kind: KindPermanent, Status: http.StatusConflict},
			{Scope: LayerItem, Attempt: 2, Kind: KindUnavailable, Status: http.StatusServiceUnavailable},
		}, policy.recorded(), "b fails for good on the first request and is not offered again")
		require.Equal(t, [][]string{{"index:a", "index:b"}, {"index:a"}, {"index:a"}}, run.seq.requests())
		require.Equal(t, []string{"a"}, run.succeeded)
		require.Equal(t, []string{"b"}, run.failed)
		require.NoError(t, run.flushErr, "b has an OnError, so its rejection is not the flush's error")
	})

	t.Run("a cancelled context abandons the retry", func(t *testing.T) {
		t.Parallel()

		var cancel context.CancelFunc

		// Canceling inside Retry and then asking for a long delay is
		// deterministic: the wait sees a done context before it sleeps.
		policy := &recordingPolicy{decide: func(int, ErrorKind) (time.Duration, bool) {
			cancel()
			return time.Hour, true
		}}
		run := runRetry(t, func(ctx context.Context) context.Context {
			ctx, cancel = context.WithCancel(ctx) //nolint:gosec // cancel is registered with t.Cleanup below
			return ctx
		}, []bulkOutcome{outcomeTooManyRequests, outcomeOK}, policy, "a")
		t.Cleanup(cancel)

		require.ErrorIs(t, run.flushErr, context.Canceled)
		require.Len(t, run.seq.requests(), 1, "no request after the context is done")
		require.Equal(t, []string{"a"}, run.failed, "the item fails once, with the failure it drew")
		require.ErrorContains(t, run.failErrs[0], "es_rejected_execution_exception")
		require.Empty(t, run.succeeded)
		require.Equal(t, uint64(1), run.stats.NumFailed)
	})

	t.Run("an unserializable item is never offered to the policy", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: retryThrice}
		seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
		client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
		bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client, RetryPolicy: policy})

		var failures []error
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action:              "index",
			DocumentID:          "a",
			WaitForActiveShards: math.NaN(),
			OnError: func(_ context.Context, _ BulkIndexerItem, err *BulkError) {
				failures = append(failures, err)
			},
		}))
		require.NoError(t, bi.Close(t.Context()))

		require.Empty(t, policy.recorded())
		require.Len(t, failures, 1)
		require.ErrorContains(t, failures[0], "json: unsupported value: NaN")
		require.Empty(t, seq.requests())
	})

	t.Run("a retried item without an OnError is not reported once it succeeds", func(t *testing.T) {
		t.Parallel()

		policy := &recordingPolicy{decide: retryThrice}
		seq := &bulkSequence{outcomes: []bulkOutcome{
			outcomeItems(map[string]int{"a": http.StatusTooManyRequests}), outcomeOK,
		}}
		client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

		var onErrors []error
		bi := newBulkIndexer(t, BulkIndexerConfig{
			NumWorkers:       1,
			Client:           client,
			RetryPolicy:      policy,
			OnUnhandledError: func(_ context.Context, err error) { onErrors = append(onErrors, err) },
		})

		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", DocumentID: "a", Body: strings.NewReader(`{"title":"a"}`),
		}))
		require.NoError(t, bi.Flush(t.Context()))
		require.NoError(t, bi.Close(t.Context()))

		require.Empty(t, onErrors, "the rejection was retried, so there is nothing to report")
		require.Equal(t, uint64(0), bi.Stats().NumFailed)
	})
}

// TestWorkerFailPending: items whose retry was abandoned fail with the failure they
// last drew. Each item with an OnError hears of it. For the ones without, a
// request failure is reported once however many items it took down, and a
// rejection by the cluster once for each item, since each has its own cause.
func TestWorkerFailPending(t *testing.T) {
	t.Parallel()

	itemID := func(id string) *string { return &id }
	rejected := func(id, reason string) opensearchapi.BulkRespItem {
		return opensearchapi.BulkRespItem{
			ID:     itemID(id),
			Status: http.StatusBadRequest,
			Error:  &opensearchapi.ErrorCause{Type: "mapper_parsing_exception", Reason: &reason},
		}
	}
	errTimeout := errors.New("request timed out")
	timedOut := &BulkError{Layer: LayerRequest, Kind: KindTransport, Err: errTimeout}
	rejectedErr := func(id, reason string) *BulkError {
		resp := rejected(id, reason)

		return &BulkError{Layer: LayerItem, Kind: KindPermanent, Status: resp.Status, Resp: resp, action: "index"}
	}

	t.Run("request failure reaches every OnError and is reported once", func(t *testing.T) {
		t.Parallel()

		var calls []failureCall
		w := newTestWorker()
		noCallback := func(id string) BulkIndexerItem { return BulkIndexerItem{Action: "index", DocumentID: id} }

		err := w.failPending(t.Context(), []failedItem{
			{item: recordingItem("a", &calls), err: timedOut, attempt: 2},
			{item: noCallback("b"), err: timedOut, attempt: 2},
			{item: recordingItem("c", &calls), err: timedOut, attempt: 2},
			{item: noCallback("d"), err: timedOut, attempt: 2},
		})

		require.ErrorIs(t, err, errTimeout)
		require.Equal(t, 1, strings.Count(err.Error(), errTimeout.Error()), "one report for the request")

		require.Len(t, calls, 2)
		for i, id := range []string{"a", "c"} {
			require.Equal(t, id, calls[i].ID)
			require.Same(t, timedOut, calls[i].Err)
			require.ErrorIs(t, calls[i].Err, errTimeout)
			require.ErrorIs(t, calls[i].Err, ErrRequestLayer)
			require.ErrorIs(t, calls[i].Err, ErrTransport)
		}
		require.Equal(t, uint64(4), w.bi.stats.numFailed.Load())
	})

	t.Run("each rejected item without OnError is reported with its own cause", func(t *testing.T) {
		t.Parallel()

		var calls []failureCall
		w := newTestWorker()

		err := w.failPending(t.Context(), []failedItem{
			{item: BulkIndexerItem{Action: "index", DocumentID: "a"}, err: rejectedErr("a", "bad a"), attempt: 3},
			{item: recordingItem("b", &calls), err: rejectedErr("b", "bad b"), attempt: 3},
			{item: BulkIndexerItem{Action: "index", DocumentID: "c"}, err: rejectedErr("c", "bad c"), attempt: 3},
		})

		require.ErrorContains(t, err, `"index" "a" rejected: "mapper_parsing_exception": "bad a"`)
		require.ErrorContains(t, err, `"index" "c" rejected: "mapper_parsing_exception": "bad c"`)
		require.NotContains(t, err.Error(), "bad b", "b has an OnError of its own")

		require.Len(t, calls, 1)
		require.Equal(t, "b", calls[0].ID)
		require.NoError(t, calls[0].Err.Err, "a rejection by the cluster has no cause beyond its response")
		require.ErrorIs(t, calls[0].Err, ErrItemLayer)
		require.ErrorIs(t, calls[0].Err, ErrPermanent)
		require.Equal(t, http.StatusBadRequest, calls[0].Resp.Status)
		require.Equal(t, uint64(3), w.bi.stats.numFailed.Load())
	})

	t.Run("items that all have OnError leave nothing to report", func(t *testing.T) {
		t.Parallel()

		var calls []failureCall
		w := newTestWorker()

		err := w.failPending(t.Context(), []failedItem{
			{item: recordingItem("a", &calls), err: timedOut, attempt: 2},
			{item: recordingItem("b", &calls), err: rejectedErr("b", "bad b"), attempt: 2},
		})

		require.NoError(t, err)
		require.Len(t, calls, 2)
	})

	t.Run("the observer sees the attempts the item took", func(t *testing.T) {
		t.Parallel()

		obs := &attemptObserver{}
		w := newTestWorker()
		w.bi.config.Observer = obs

		require.NoError(t, w.failPending(t.Context(), []failedItem{
			{item: recordingItem("a", new([]failureCall)), err: timedOut, attempt: 4},
		}))

		require.Equal(t, []int{4}, obs.attempts)
	})

	t.Run("nothing pending fails nothing", func(t *testing.T) {
		t.Parallel()

		w := newTestWorker()

		require.NoError(t, w.failPending(t.Context(), nil))
		require.Zero(t, w.bi.stats.numFailed.Load())
	})
}

// attemptObserver records the attempts of the items it sees fail for good.
type attemptObserver struct {
	BaseBulkIndexerObserver

	attempts []int
}

func (o *attemptObserver) OnItemDone(_ context.Context, ev BulkItemEvent) {
	if ev.Failed {
		o.attempts = append(o.attempts, ev.Attempts)
	}
}

func TestRetryPolicyFunc(t *testing.T) {
	t.Parallel()

	var p RetryPolicy = RetryPolicyFunc(func(attempt int, err *BulkError) (time.Duration, bool) {
		return time.Duration(attempt) * time.Second, errors.Is(err, ErrRejected)
	})

	delay, retry := p.Retry(2, &BulkError{Layer: LayerItem, Kind: KindRejected})
	require.Equal(t, 2*time.Second, delay)
	require.True(t, retry)

	_, retry = p.Retry(1, &BulkError{Layer: LayerItem, Kind: KindPermanent})
	require.False(t, retry)
}

func TestErrorKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind ErrorKind
		want string
	}{
		{KindUnknown, "unknown"},
		{KindRejected, "rejected"},
		{KindUnavailable, "unavailable"},
		{KindTransport, "transport"},
		{KindPermanent, "permanent"},
		{KindCanceled, "canceled"},
		{ErrorKind(42), "ErrorKind(42)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.kind.String())
		})
	}
}
