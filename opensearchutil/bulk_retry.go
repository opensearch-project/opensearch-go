// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// FailureKind classifies why a bulk request, or one item of it, failed. It is
// what a [RetryPolicy] is given to decide whether a retry can help.
type FailureKind int

const (
	// FailureRejected means the cluster refused the work because a write queue
	// was full (HTTP 429), either for the whole request or for one item. The
	// work was not applied.
	FailureRejected FailureKind = iota

	// FailureUnavailable means the cluster, or a proxy in front of it, could not
	// serve the work right now (HTTP 503), either for the whole request or for
	// one item. The work is treated as not applied.
	FailureUnavailable

	// FailureTransport means the outcome is unknown: the request failed before
	// a response arrived, or the response was a server error other than 503 (for
	// one item, a status of 500 or more other than 503) or could not be read. The batch may have been applied in full, in part, or
	// not at all, so retrying it can write a document twice unless the writes
	// are idempotent. See the precondition on [RetryPolicy].
	FailureTransport

	// FailurePermanent means the same request would fail the same way: a
	// mapping error, a version conflict, any other 4xx, or a response that
	// cannot be paired with the items sent. Retrying cannot succeed.
	FailurePermanent
)

// numFailureKinds is the number of FailureKind values, sizing the retry counters.
const numFailureKinds = 4

// String returns the name of the kind without its Failure prefix.
func (k FailureKind) String() string {
	switch k {
	case FailureRejected:
		return "rejected"
	case FailureUnavailable:
		return "unavailable"
	case FailureTransport:
		return "transport"
	case FailurePermanent:
		return "permanent"
	default:
		return fmt.Sprintf("FailureKind(%d)", int(k))
	}
}

// RetryPolicy decides whether the indexer sends a failed item again, and after
// how long. The indexer drives the loop; the policy supplies the backoff and
// the limit. Set it as [BulkIndexerConfig].RetryPolicy.
//
// Retrying requires idempotent writes: a failure of kind [FailureTransport] is
// ambiguous, because the batch may already have reached the cluster, and
// sending it again would index an item without a DocumentID a second time under
// a new generated _id. So when a RetryPolicy is set, every item must carry a
// DocumentID, and the indexer enforces it: an item without one is not sent and
// fails through its own OnFailure with [ErrMissingDocumentID]. Set
// [BulkIndexerConfig].Fingerprinter to have the indexer generate the id.
//
// A bulk request can fail in two ways, and Retry is told which by scope. With
// [ScopeRequest] the whole request failed: resp is empty, err is non-nil, and
// one call stands for every item in the request, which all draw the same
// failure. With [ScopeItem] the cluster answered and rejected one item: resp is
// that item's response, err is nil, and there is one call per rejected item.
// attempt counts the requests that have carried the work so far: 1 after the
// first request fails. kind is also offered for [FailurePermanent], which no
// retry can fix; a policy should return false for it. The indexer applies no
// limit of its own, so a policy that always returns true retries a persistent
// failure forever.
//
// When retry is true the item is sent again, in a request of its own together
// with the other items being retried, after the longest delay any of them was
// given. While an item is being retried neither OnSuccess nor OnFailure fires
// for it: each fires once, when the item reaches a final state. When retry is
// false the item fails with the failure it was offered, and OnFailure fires.
//
// Retry runs on the goroutine that sends the buffer, as the item callbacks do, so
// it must not block. That goroutine then waits for delay while the buffer keeps
// its request slot: the next buffer of the same shard is not sent until the
// retries have finished, which keeps the shard's documents in order, and the
// memory the buffer holds stays counted against MaxMemoryBytes. Other shards are
// not held up. The wait ends early, and the items still waiting fail with the
// failure they last drew, when the context of the flush is done: the context
// passed to Flush or Close, or the one the indexer was built with for a
// periodic or size-triggered flush. Keep the delay short against FlushInterval.
//
// A retry happens above the transport's own: the transport has already resent
// a request that drew a 502, 503 or 504 or failed on a network error, up to its
// MaxRetries, before the indexer sees the failure. Attempts multiply with
// those, and DisableRetry on the client turns the transport's side off.
//
// A document that cannot be serialized, or that has no id, fails at once,
// without a retry, and a policy never sees it.
type RetryPolicy interface {
	Retry(
		attempt int, scope FailureScope, kind FailureKind, resp opensearchapi.BulkRespItem, err error,
	) (delay time.Duration, retry bool)
}

// RetryPolicyFunc adapts a function to a [RetryPolicy].
type RetryPolicyFunc func(
	attempt int, scope FailureScope, kind FailureKind, resp opensearchapi.BulkRespItem, err error,
) (time.Duration, bool)

// Retry calls f.
func (f RetryPolicyFunc) Retry(
	attempt int, scope FailureScope, kind FailureKind, resp opensearchapi.BulkRespItem, err error,
) (time.Duration, bool) {
	return f(attempt, scope, kind, resp, err)
}

// bulkResponseMismatchError reports a bulk response that cannot be paired with
// the items sent, because it holds more results than the request had actions.
type bulkResponseMismatchError struct {
	got, want int
}

func (e *bulkResponseMismatchError) Error() string {
	return fmt.Sprintf("bulk response has %d items, indexer serialized %d", e.got, e.want)
}

// classifyItemFailure classifies the rejection of one item the cluster answered.
func classifyItemFailure(info opensearchapi.BulkRespItem) FailureKind {
	switch info.Status {
	case http.StatusTooManyRequests:
		return FailureRejected
	case http.StatusServiceUnavailable:
		return FailureUnavailable
	}

	if info.Status >= http.StatusInternalServerError {
		return FailureTransport
	}

	return FailurePermanent
}

// classifyBatchFailure classifies a bulk request that failed as a whole. status
// is the HTTP status of the response, or 0 when none arrived; it is taken from
// the response rather than from err, which carries no status for an error body
// the client could not parse.
func classifyBatchFailure(status int, err error) FailureKind {
	if _, ok := errors.AsType[*bulkResponseMismatchError](err); ok { //nolint:errcheck // discarded result is the matched error
		return FailurePermanent
	}

	switch {
	case status == http.StatusTooManyRequests:
		return FailureRejected
	case status == http.StatusServiceUnavailable:
		return FailureUnavailable
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return FailurePermanent
	default:
		return FailureTransport
	}
}

// batchStatus returns the HTTP status of the response behind blk, or 0 when the
// request produced none.
func batchStatus(blk *opensearchapi.BulkResp) int {
	if blk == nil {
		return 0
	}
	if resp := blk.Inspect().Response; resp != nil {
		return resp.StatusCode
	}

	return 0
}

// failedItem is an item whose last request failed and which the policy chose to
// send again. resp and err are the failure it was offered, kept so the item can
// still be failed with them if the retry is abandoned. attempt is the number of
// requests that have carried it.
type failedItem struct {
	item    BulkIndexerItem
	op      string
	resp    opensearchapi.BulkRespItem
	err     error
	attempt int
}

// retryBatch is the items to send again and how long to wait first.
type retryBatch struct {
	items []failedItem
	delay time.Duration
}

// add queues an item for another attempt after delay; the batch waits for the
// longest delay asked for.
func (r *retryBatch) add(f failedItem, delay time.Duration) {
	r.items = append(r.items, f)
	r.delay = max(r.delay, delay)
}

// decideRetry reports a failure to the observer and asks the configured policy
// whether to send the affected work again. With no policy it declines. The
// event arrives without its Index, Worker, Retrying and Delay, which are filled
// in here.
func (w *worker) decideRetry(ctx context.Context, ev BulkFailureEvent) (time.Duration, bool) {
	var (
		delay time.Duration
		retry bool
	)
	if policy := w.bi.config.RetryPolicy; policy != nil {
		delay, retry = policy.Retry(ev.Attempt, ev.Scope, ev.Kind, ev.Resp, ev.Err)
	}
	delay = max(delay, 0)

	if retry {
		w.bi.stats.numRetries[ev.Kind].Add(uint64(ev.Items)) //nolint:gosec // Items is a count, never negative
		ev.Retrying, ev.Delay = true, delay
	}

	if obs := w.bi.config.Observer; obs != nil {
		ev.Index, ev.Worker = w.bi.config.Index, w.id
		if ev.Scope == ScopeRequest {
			obs.OnRequestFailure(ctx, ev)
		} else {
			obs.OnItemFailure(ctx, ev)
		}
	}

	return delay, retry
}

// settleFailed gives an item its final failure: it counts it, tells the item's
// OnFailure and the observer. resp is the response item, and err the error the
// item failed with (nil for an item the cluster rejected). attempts is the
// number of requests that carried it.
//
// An item without an OnFailure of its own returns the error to report instead,
// so the flush site can surface it through OnError; whether to use it is up to
// the caller.
func (w *worker) settleFailed(
	ctx context.Context, item BulkIndexerItem, op string, resp opensearchapi.BulkRespItem, err error, attempts int,
) error {
	w.bi.stats.numFailed.Add(1)
	w.observeSettled(ctx, item, attempts, true, resp, err)

	if item.OnFailure != nil {
		item.OnFailure(ctx, item, bulkRespItemForOnFailure(resp), err)
		return nil
	}
	if err == nil {
		return bulkItemFailure(op, resp)
	}

	return err
}

// observeSettled tells the observer that an item reached its final state.
func (w *worker) observeSettled(
	ctx context.Context, item BulkIndexerItem, attempts int, failed bool, resp opensearchapi.BulkRespItem, err error,
) {
	obs := w.bi.config.Observer
	if obs == nil {
		return
	}

	index := item.Index
	if index == "" {
		index = w.bi.config.Index
	}
	obs.OnItemSettled(ctx, BulkItemEvent{
		Index:      index,
		Action:     item.Action,
		DocumentID: item.DocumentID,
		Metadata:   item.Metadata,
		Worker:     w.id,
		Attempts:   attempts,
		Failed:     failed,
		Resp:       resp,
		Err:        err,
	})
}

// failUnsent fails an item that could not be written to a request, because it
// does not serialize or has no document id. A retry cannot help, so the policy
// is not asked. attempts is the number of requests that carried the item
// before, 0 for an item never sent. It returns what settleFailed does. It must
func (w *worker) failUnsent(ctx context.Context, item BulkIndexerItem, err error, attempts int) error {
	if obs := w.bi.config.Observer; obs != nil {
		obs.OnItemFailure(ctx, BulkFailureEvent{
			Scope:      ScopeItem,
			Kind:       FailurePermanent,
			Index:      w.bi.config.Index,
			Worker:     w.id,
			Attempt:    attempts,
			Items:      1,
			DocumentID: item.DocumentID,
			Err:        err,
		})
	}

	return w.settleFailed(ctx, item, item.Action, opensearchapi.BulkRespItem{}, err, attempts)
}

// sendWithRetry sends the buffered items and, while the policy asks for it,
// sends the ones that failed again. It returns once every item has reached a
// final state; the error is the join of what each request reported. It must be
func (w *worker) sendWithRetry(ctx context.Context) error {
	var errs []error

	retry, err := w.send(ctx, 1)
	errs = append(errs, err)

	for attempt := 2; len(retry.items) > 0; attempt++ {
		if waitErr := waitForRetry(ctx, retry.delay); waitErr != nil {
			// Out of time: the items still waiting fail with the failure that
			// put them there, and the caller learns the drain was cut short.
			errs = append(errs, waitErr, w.failPending(ctx, retry.items))
			break
		}

		pending, rebuildErr := w.rebuild(ctx, retry.items)
		errs = append(errs, rebuildErr)
		if pending == 0 {
			break
		}

		retry, err = w.send(ctx, attempt)
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// waitForRetry sleeps for delay, or until ctx is done. A delay of zero or less
// does not sleep but still reports a context that is already done, so a
// cancelled flush stops retrying.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// rebuild replaces the worker's buffer with a fresh serialization of items, in
// order, and returns how many were written. An item that no longer serializes,
// because its body cannot be read a second time, fails at once. It must be
func (w *worker) rebuild(ctx context.Context, items []failedItem) (int, error) {
	clear(w.items)
	w.items = w.items[:0]
	w.recycleBuf()

	var errs []error
	for _, f := range items {
		if err := w.writeItem(ctx, f.item); err != nil {
			errs = append(errs, w.failUnsent(ctx, f.item, err, f.attempt))
			continue
		}

		w.items = append(w.items, f.item)
	}

	return len(w.items), errors.Join(errs...)
}

// failPending fails items whose retry was abandoned, each with the failure it
// last drew. An item without an OnFailure of its own is reported through the
// returned error instead, once per distinct request failure. It must be called
func (w *worker) failPending(ctx context.Context, items []failedItem) error {
	var (
		errs      []error
		batchSeen bool
	)
	for _, f := range items {
		err := w.settleFailed(ctx, f.item, f.op, f.resp, f.err, f.attempt)
		switch {
		case err == nil:
		case f.err == nil:
			errs = append(errs, err)
		case !batchSeen:
			batchSeen = true
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
