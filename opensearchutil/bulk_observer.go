// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// BulkIndexerObserver receives notifications about the requests a [BulkIndexer]
// sends and how each item fares, for statistics and metrics. Set it as
// [BulkIndexerConfig].Observer.
//
// The context is the one the flush runs on, carrying whatever OnFlushStart put
// on it, so a tracing observer can link its metrics to the flush's span.
//
// Implementations must be safe for concurrent use: with more than one request out,
// events arrive from several goroutines at once. Methods run on the goroutine
// that sends the buffer, as the item callbacks do, so they must return quickly
// and must not call back into the indexer ([BulkIndexer.Add], [BulkIndexer.Flush],
// [BulkIndexer.Close]), which would stall the buffer they are called for. Events
// are values and may be retained.
type BulkIndexerObserver interface {
	// OnRequestEnd is called after each bulk request returns, including a
	// retry, whether the request failed or not.
	OnRequestEnd(ctx context.Context, event BulkRequestEvent)

	// OnRequestError is called when a whole bulk request fails, once per
	// failed attempt, before any retry. The event's Err has the layer
	// [LayerRequest].
	OnRequestError(ctx context.Context, event BulkErrorEvent)

	// OnItemError is called when one item fails, once per failed attempt,
	// before any retry. It is called for an item the cluster rejected and for
	// an item that was never sent, but not for one failed by [BulkIndexer.Cancel]
	// or a Close that escalated: a cancellation is no failure of the work, and
	// the item's [BulkIndexerObserver.OnItemDone] carries [ErrIndexerCanceled].
	// The event's Err has the layer [LayerItem].
	OnItemError(ctx context.Context, event BulkErrorEvent)

	// OnItemDone is called once per item, when the indexer is done with it: it
	// was accepted, or it failed and will not be retried. It fires at the same
	// moment as the item's OnSuccess or OnError callback.
	OnItemDone(ctx context.Context, event BulkItemEvent)
}

// BaseBulkIndexerObserver is an embeddable no-op [BulkIndexerObserver]. Embed it
// in your own struct and override only the methods you care about.
type BaseBulkIndexerObserver struct{}

// OnRequestEnd implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnRequestEnd(context.Context, BulkRequestEvent) {}

// OnRequestError implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnRequestError(context.Context, BulkErrorEvent) {}

// OnItemError implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnItemError(context.Context, BulkErrorEvent) {}

// OnItemDone implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnItemDone(context.Context, BulkItemEvent) {}

// BulkRequestEvent describes one bulk request that has returned.
type BulkRequestEvent struct {
	// Index is the default index of the indexer, empty when items carry their own.
	Index string

	// Node is the name of the node the request was sent to, as [FlushNode] reports
	// it. It is empty for a request the transport routed as it saw fit.
	Node string

	// Attempt is 1 for the first request of a flush and counts up for each
	// retry of it.
	Attempt int

	// Items is the number of items the request carried.
	Items int

	// Bytes is the size of the request body.
	Bytes int

	// Duration is the time from sending the request until it returned.
	Duration time.Duration

	// StatusCode is the HTTP status of the response, or 0 when none arrived.
	StatusCode int

	// Err is non-nil when the request failed as a whole. It is nil when the
	// cluster answered, even if it rejected some or all of the items.
	Err error
}

// BulkErrorEvent describes one error of a request or of an item, as the retry
// policy was shown it.
type BulkErrorEvent struct {
	// Err is the error: its Layer says whether Items counts the whole request or a
	// single item, its Kind is the classification the retry policy was given, and
	// it holds the response status, the item's response and the cause.
	Err *BulkError

	// Index is the default index of the indexer, empty when items carry their own.
	Index string

	// Node is the name of the node the failed work was bound for, as [FlushNode]
	// reports it. It is empty for work with no node of its own: a request the
	// transport routed as it saw fit, or an item refused before it was buffered.
	Node string

	// Attempt is the number of requests that have carried the failed work,
	// counting the one that just failed: 1 after the first request. It is 0
	// for an item that was never sent.
	Attempt int

	// Items is the number of items affected: every item in the request for
	// [LayerRequest], and 1 for [LayerItem].
	Items int

	// DocumentID is the failed item's ID, for [LayerItem]. It is empty for
	// [LayerRequest].
	DocumentID string

	// Retrying reports that the retry policy asked for the work to be sent
	// again. When false the error is final for the affected items.
	Retrying bool

	// Delay is how long the retry policy asked to wait first. It is 0 unless
	// Retrying is true.
	Delay time.Duration
}

// BulkItemEvent describes an item the indexer is done with.
type BulkItemEvent struct {
	// Index is the index the item targeted, falling back to the default index
	// of the indexer.
	Index string

	// Action is the bulk action: index, create, update or delete.
	Action string

	// DocumentID is the item's id.
	DocumentID string

	// Bytes is the size of the item's document, as [BulkIndexerItem].Body supplies
	// it: the length of Body from its start, in bytes. It does not include the
	// action and metadata line, the newline that ends the document, or the
	// indexer's own bookkeeping for the item, so it is not the item's share of
	// [BulkRequestEvent].Bytes. It is 0 for an item with no Body, such as a delete.
	// For an update it is the whole Body, normally the {"doc": ...} wrapper.
	//
	// It is measured once, when Add accepts the item, so every item reports the same
	// figure whether it succeeded, was rejected, or was refused before it was
	// buffered.
	Bytes int

	// Metadata is the item's [BulkIndexerItem].Metadata, unchanged.
	Metadata any

	// Node is the name of the node the item's last request was bound for, as
	// [FlushNode] reports it. It is empty for an item with no node of its own: one
	// the transport routed as it saw fit, or one refused before it was buffered.
	Node string

	// Attempts is the number of requests that carried the item: 1 when it
	// was done after its first, more after retries, and 0 when it was never sent.
	Attempts int

	// Failed is true when the item failed, and false when it was accepted.
	Failed bool

	// Resp is the response item the cluster returned, when it returned one.
	Resp opensearchapi.BulkRespItem

	// Err is the error the item failed with, and nil for an accepted item.
	Err *BulkError
}
