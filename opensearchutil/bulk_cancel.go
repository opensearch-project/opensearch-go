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
)

// ErrIndexerCanceled is the error an item fails with when the indexer was
// canceled before the item was sent: [BulkIndexer.Cancel] was called, or the
// context passed to [BulkIndexer.Close] ended while the item was still waiting.
// It wraps [context.Canceled], so errors.Is matches either.
//
// An item that was already in a request when the indexer was canceled fails
// with that request's error instead, which wraps [context.Canceled] too and is
// ambiguous about whether the item landed.
var ErrIndexerCanceled = fmt.Errorf("opensearchutil: bulk indexer canceled: %w", context.Canceled)

// Cancel stops the indexer immediately. Requests in flight are canceled, retry
// backoff ends, and every item that has not reached a final state fails through
// its own OnFailure exactly once: with ErrIndexerCanceled for an item that was
// never sent, and with the request's error for one that was in flight. Items
// added after Cancel fail the same way, with Add returning nil.
//
// Cancel does not wait for any of that to finish; [BulkIndexer.Close] does, and
// is still required to release the indexer. It is idempotent, safe for
// concurrent use, and safe to call from an item callback.
func (bi *bulkIndexer) Cancel() {
	bi.cancel()
}

// canceled reports whether Cancel has been called, or Close escalated.
func (bi *bulkIndexer) canceled() bool {
	return bi.cancelCtx != nil && bi.cancelCtx.Err() != nil
}

// joinCancel returns ctx joined to the indexer's cancellation: the result ends
// when ctx does or when the indexer is canceled. The returned func releases it.
func (bi *bulkIndexer) joinCancel(ctx context.Context) (context.Context, func()) {
	if bi.cancelCtx == nil {
		return ctx, func() {}
	}

	joined, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(bi.cancelCtx, func() { cancel(ErrIndexerCanceled) })

	return joined, func() {
		stop()
		cancel(nil)
	}
}

// failCanceled fails every item in the worker's buffer with ErrIndexerCanceled,
// without sending them. An item without an OnFailure of its own is reported
// through the returned error, as for any other unsent item, so a buffer whose
// items all have one does not reach OnError.
func (w *worker) failCanceled(ctx context.Context) error {
	errs := make([]error, 0, len(w.items))
	for _, item := range w.items {
		errs = append(errs, w.failUnsent(ctx, item, ErrIndexerCanceled, 0))
	}

	return errors.Join(errs...)
}

// flushCanceled runs when the indexer is canceled: it seals every open buffer, so
// that the items they hold fail now instead of at the next flush. A sealed buffer
// that is dispatched finds the indexer canceled and fails its items without
// sending them.
func (s *scheduler) flushCanceled(canceled context.Context) {
	s.Lock()
	defer s.Unlock()

	s.sealAllOpen(flushRequested, context.WithoutCancel(canceled))
}

// failStranded fails the items of buffers that were never dispatched, which
// happens when the construction context ends and the scheduler stops sending. It
// runs after the requests in flight have finished, so every item still reaches a
// final state and every Flush waiting on a buffer is answered.
func (bi *bulkIndexer) failStranded(ctx context.Context) {
	s := bi.sched

	s.Lock()
	var stranded []*buffer
	for _, ns := range s.nodes {
		for b := range ns.opens {
			stranded = append(stranded, b)
		}
	}
	stranded = append(stranded, s.sealed...)
	for _, b := range stranded {
		if b.sealed {
			s.sealedPending--
		}
		delete(s.live, b)
		delete(b.node.opens, b)
		if b.shard.open == b {
			b.shard.open = nil
		}
		b.err = ErrIndexerCanceled
		bi.budget.release(b.held)
		s.release(b.node, b.held)
	}
	s.sealed = nil
	s.Unlock()

	w := worker{bi: bi}
	for _, b := range stranded {
		for _, item := range b.items {
			// An item without an OnFailure is not reported to OnError: the caller
			// canceled the indexer, so the failure is no surprise.
			_ = w.failUnsent(ctx, item, ErrIndexerCanceled, 0) //nolint:errcheck // see above
		}
		releaseLent(b.items)
		close(b.done)
		bi.putItems(b.items)
		b.items = nil
	}
}
