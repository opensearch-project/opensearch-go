// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"
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
// without sending them, and returns that error once for the flush site to
// report. It must be called under a lock.
func (w *worker) failCanceled(ctx context.Context) error {
	for _, item := range w.items {
		_ = w.failUnsent(ctx, item, ErrIndexerCanceled, 0) //nolint:errcheck // the flush reports ErrIndexerCanceled once
	}

	return ErrIndexerCanceled
}

// flushCanceled runs when the indexer is canceled while the worker is idle: it
// flushes the buffer, which fails the buffered items without sending them.
func (w *worker) flushCanceled(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.buf.Len() == 0 {
		return
	}
	if err := w.flush(ctx); err != nil && w.bi.config.OnError != nil {
		w.bi.config.OnError(ctx, err)
	}
}

// failQueued fails the entries a worker left on its queue because it exited
// early, which happens when the construction context ends. It runs after the
// workers have stopped and the queues are closed, so every item still reaches a
// final state and every Flush barrier is answered.
func (bi *bulkIndexer) failQueued(ctx context.Context) {
	w := worker{bi: bi}
	for _, queue := range bi.queues {
		for entry := range queue {
			if entry.flush != nil {
				entry.flush.done <- ErrIndexerCanceled
				continue
			}
			// An item without an OnFailure is not reported to OnError: the caller
			// canceled the indexer, so the failure is no surprise.
			_ = w.failUnsent(ctx, entry.item, ErrIndexerCanceled, 0) //nolint:errcheck // see above
		}
	}
}
