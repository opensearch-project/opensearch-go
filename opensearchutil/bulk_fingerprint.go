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
	"io"
)

// ErrMissingDocumentID is the error an item fails with when it has no
// DocumentID and the indexer must not send it without one: a [RetryPolicy] is
// set, so a retried item has to keep the id it was first sent under, and no
// [DocumentFingerprinter] supplied one.
var ErrMissingDocumentID = errors.New("opensearchutil: bulk item has no document ID")

// DocumentFingerprinter derives a client-side document id for an item that
// arrived without one, or returns an error that fails the item. Set it as
// [BulkIndexerConfig].Fingerprinter.
//
// It is called by [BulkIndexer.Add] on the caller's goroutine, for an item whose
// DocumentID is empty and only then, before the item is placed in a shard, so
// an id it generates routes the item exactly as the same id given explicitly
// would, and per-id ordering holds. Several Add calls may run it at once, so it
// must be safe for concurrent use, and it should be quick, since the caller
// waits for it.
//
// The id must be deterministic: the same document must always yield the same
// id. That is what makes a retry idempotent, and what a [RetryPolicy] requires.
// A hash of the body is the usual choice. The returned id must not be empty;
// an empty id is [ErrMissingDocumentID].
//
// item.Body may be read to compute the id. The indexer rewinds it afterwards,
// so the fingerprinter need not, but it must not close it.
//
// An error returned here fails only that item, through its OnFailure, without
// being queued: [BulkIndexer.Add] still returns nil. When the item has no
// OnFailure the error goes to OnError instead.
type DocumentFingerprinter func(ctx context.Context, item BulkIndexerItem) (string, error)

// ensureDocumentID gives item a DocumentID when it lacks one and the indexer's
// configuration says it needs one, returning the item to queue. It returns an
// error when the item cannot be given one and must not be sent.
//
// An item without an id is left alone when no fingerprinter and no retry policy
// is configured: the cluster then generates the id, as it always has.
func (bi *bulkIndexer) ensureDocumentID(ctx context.Context, item BulkIndexerItem) (BulkIndexerItem, error) {
	if item.DocumentID != "" {
		return item, nil
	}

	if bi.config.Fingerprinter == nil {
		if bi.config.RetryPolicy != nil {
			return item, ErrMissingDocumentID
		}

		return item, nil
	}

	id, err := bi.config.Fingerprinter(ctx, item)
	if err != nil {
		return item, fmt.Errorf("fingerprint document: %w", err)
	}
	if id == "" {
		return item, ErrMissingDocumentID
	}
	item.DocumentID = id

	// The fingerprinter may have read the body; the indexer reads it from the start.
	if item.Body != nil {
		if _, err := item.Body.Seek(0, io.SeekStart); err != nil {
			return item, fmt.Errorf("rewind body after fingerprinting: %w", err)
		}
	}

	return item, nil
}

// rejectItem fails an item that was refused before it was placed in a buffer. The
// item's OnFailure hears of it; an item without one has the error reported to
// OnError, since nothing else would. It runs on the caller's goroutine.
func (bi *bulkIndexer) rejectItem(ctx context.Context, item BulkIndexerItem, err error) {
	w := worker{bi: bi}
	if unhandled := w.failUnsent(ctx, item, err, 0); unhandled != nil && bi.config.OnError != nil {
		bi.config.OnError(ctx, unhandled)
	}
}
