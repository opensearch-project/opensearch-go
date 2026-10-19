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

// RetryPolicy decides whether the indexer sends a failed item again, and after
// how long. The indexer drives the loop; the policy supplies the backoff and
// the limit. Set it as [BulkIndexerConfig].RetryPolicy.
//
// Retrying requires idempotent writes: a failure of kind [KindTransport] is
// ambiguous, because the batch may already have reached the cluster, and
// sending it again would index an item without a DocumentID a second time under
// a new generated _id. So when a RetryPolicy is set, every item must carry a
// DocumentID, and the indexer enforces it: an item without one is not sent and
// fails through its own OnError with [ErrMissingDocumentID]. Set
// [BulkIndexerConfig].Fingerprinter to have the indexer generate the id.
//
// Retry is asked about two layers of error, and err.Layer says which. With
// [LayerRequest] the whole request failed: err.Err is the request's error, and one
// call stands for every item in the request, which all draw the same error. With
// [LayerItem] the cluster answered and rejected one item: err.Resp is that item's
// response, and there is one call per rejected item. Errors of the other layers
// are never retried, and a policy never sees them. err.Kind, or errors.Is with
// ErrRejected, ErrUnavailable, ErrTransport or ErrPermanent, says why. attempt
// counts the requests that have carried the work so far: 1 after the first
// request fails. [KindPermanent] is offered too, which no retry can fix; a policy
// should return false for it. The indexer applies no limit of its own, so a
// policy that always returns true retries a persistent error forever.
//
// When retry is true the item is sent again, in a request of its own together
// with the other items being retried, after the longest delay any of them was
// given. While an item is being retried neither OnSuccess nor OnError fires
// for it: each fires once, when the indexer is done with the item. When retry is
// false the item fails with the failure it was offered, and OnError fires.
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
// A retry happens above the transport's own: the transport has already resent,
// up to its MaxRetries, a request that failed on a network error or drew a status
// in its RetryOnStatus (502, 503 and 504 by default), before the indexer sees the
// error. It also resends a request a node answered with a 429 when the client
// has a router and the node's write thread pool is tracked, as with the default
// router, and it routes that resend afresh, so it can go to another node. A 429
// the transport resent successfully never reaches the indexer, and does not cut
// the node's window. Attempts multiply with those, and DisableRetry on the
// client turns the transport's side off.
//
// A document that cannot be serialized, or that has no id, fails at once,
// without a retry, and a policy never sees it.
type RetryPolicy interface {
	Retry(attempt int, err *BulkError) (delay time.Duration, retry bool)
}

// RetryPolicyFunc adapts a function to a [RetryPolicy].
type RetryPolicyFunc func(attempt int, err *BulkError) (time.Duration, bool)

// Retry calls f.
func (f RetryPolicyFunc) Retry(attempt int, err *BulkError) (time.Duration, bool) {
	return f(attempt, err)
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
func classifyItemFailure(info opensearchapi.BulkRespItem) ErrorKind {
	switch info.Status {
	case http.StatusTooManyRequests:
		return KindRejected
	case http.StatusServiceUnavailable:
		return KindUnavailable
	}

	if info.Status >= http.StatusInternalServerError {
		return KindTransport
	}

	return KindPermanent
}

// classifyBatchFailure classifies a bulk request that failed as a whole. status
// is the HTTP status of the response, or 0 when none arrived; it is taken from
// the response rather than from err, which carries no status for an error body
// the client could not parse.
func classifyBatchFailure(status int, err error) ErrorKind {
	if _, ok := errors.AsType[*bulkResponseMismatchError](err); ok { //nolint:errcheck // discarded result is the matched error
		return KindPermanent
	}

	switch {
	case status == http.StatusTooManyRequests:
		return KindRejected
	case status == http.StatusServiceUnavailable:
		return KindUnavailable
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return KindPermanent
	default:
		return KindTransport
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
// send again. err is the error it was offered, kept so the item can still be
// failed with it if the retry is abandoned. attempt is the number of requests
// that have carried it.
type failedItem struct {
	item    BulkIndexerItem
	err     *BulkError
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
// event arrives without its Index, Node, Retrying and Delay, which are filled
// in here.
func (w *worker) decideRetry(ctx context.Context, ev BulkErrorEvent) (time.Duration, bool) {
	var (
		delay time.Duration
		retry bool
	)
	// A canceled indexer or an ended context sends nothing more, so the policy is
	// not asked and the failure is not counted as a retry.
	if policy := w.bi.config.RetryPolicy; policy != nil && ctx.Err() == nil && !w.bi.canceled() {
		delay, retry = policy.Retry(ev.Attempt, ev.Err)
	}
	delay = max(delay, 0)

	w.bi.stats.countError(ev.Err)

	if retry {
		w.bi.stats.numRetries[ev.Err.Kind].Add(uint64(ev.Items)) //nolint:gosec // Items is a count, never negative
		ev.Retrying, ev.Delay = true, delay
	}

	if obs := w.bi.config.Observer; obs != nil {
		ev.Index, ev.Node = w.bi.config.Index, w.node.Name
		if ev.Err.Layer == LayerRequest {
			obs.OnRequestError(ctx, ev)
		} else {
			obs.OnItemError(ctx, ev)
		}
	}

	return delay, retry
}

// failItem fails an item for good with err: it counts it, tells the item's OnError
// and the observer. attempts is the number of requests that carried it.
//
// An item with neither OnError nor the deprecated OnFailure returns err to report
// instead, so the flush site can surface it through OnUnhandledError; whether to
// use it is up to the caller. An item with either returns nil.
func (w *worker) failItem(ctx context.Context, item BulkIndexerItem, err *BulkError, attempts int) error {
	w.bi.stats.numFailed.Add(1)
	l := w.bi.sinceEpoch() - item.addedAt
	w.bi.recordDone(l, l)
	w.observeDone(ctx, item, attempts, err.Resp, err)

	switch {
	case item.OnError != nil:
		item.OnError(ctx, item, err)

		return nil
	case item.OnFailure != nil:
		// The v5.0 callback, with the arguments v5.0 gave it.
		resp := err.Resp
		if resp.Error == nil {
			resp.Error = &opensearchapi.ErrorCause{}
		}
		item.OnFailure(ctx, item, resp, err.Err)

		return nil
	}

	return err
}

// observeDone tells the observer that the indexer is done with an item. err is nil
// for an item the cluster accepted.
func (w *worker) observeDone(
	ctx context.Context, item BulkIndexerItem, attempts int, resp opensearchapi.BulkRespItem, err *BulkError,
) {
	obs := w.bi.config.Observer
	if obs == nil {
		return
	}

	index := item.Index
	if index == "" {
		index = w.bi.config.Index
	}
	obs.OnItemDone(ctx, BulkItemEvent{
		Index:      index,
		Action:     item.Action,
		DocumentID: item.DocumentID,
		Bytes:      int(item.bodyBytes),
		Metadata:   item.Metadata,
		Node:       w.node.Name,
		Attempts:   attempts,
		Failed:     err != nil,
		Resp:       resp,
		Err:        err,
	})
}

// failUnsent fails an item that was never written to a request, for the reason
// cause: it does not serialize, has no document ID, or the indexer was canceled.
// A retry cannot help, so the policy is not asked.
// attempts is the number of requests that carried the item before, 0 for an item
// never sent. It returns what failItem does.
//
// A cancellation is not a failure of the work, so it is left out of the error
// events, whose kinds count what went wrong; the item still reaches OnItemDone,
// with an error of [KindCanceled].
func (w *worker) failUnsent(ctx context.Context, item BulkIndexerItem, cause error, attempts int) error {
	err := unsentError(item, cause)
	w.bi.stats.countError(err)
	if obs := w.bi.config.Observer; obs != nil && err.Kind != KindCanceled {
		obs.OnItemError(ctx, BulkErrorEvent{
			Err:        err,
			Index:      w.bi.config.Index,
			Node:       w.node.Name,
			Attempt:    attempts,
			Items:      1,
			DocumentID: item.DocumentID,
		})
	}

	return w.failItem(ctx, item, err, attempts)
}

// sendWithRetry sends the buffered items and, while the policy asks for it,
// sends the ones that failed again. It returns once it is done with every item;
// the error is the join of what each request reported.
func (w *worker) sendWithRetry(ctx context.Context) error {
	var errs []error

	retry, err := w.send(ctx, 1)
	errs = append(errs, err)

	for attempt := 2; len(retry.items) > 0; attempt++ {
		waiting := int64(len(retry.items))
		w.bi.stats.retryBackoffItems.Add(waiting)
		waitErr := waitForRetry(ctx, retry.delay)
		w.bi.stats.retryBackoffItems.Add(-waiting)
		if waitErr != nil {
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
// because its body cannot be read a second time, fails at once.
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
// last drew. An item without an OnError of its own is reported through the
// returned error instead, once per distinct request failure.
func (w *worker) failPending(ctx context.Context, items []failedItem) error {
	var (
		errs      []error
		batchSeen bool
	)
	for _, f := range items {
		err := w.failItem(ctx, f.item, f.err, f.attempt)
		switch {
		case err == nil:
		case f.err.Layer != LayerRequest:
			errs = append(errs, err)
		case !batchSeen:
			batchSeen = true
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
