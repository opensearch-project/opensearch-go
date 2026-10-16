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

// FailureScope says what a failure applies to: a whole bulk request, or one
// item in it. The two are different events with different causes. A request
// fails as one unit, every item in it drawing the same error, and says nothing
// about whether the batch landed; an item fails alone, in a request that the
// cluster answered.
type FailureScope uint8

const (
	// ScopeRequest is the failure of a whole bulk request: no response, an
	// error status for the request, or a response that cannot be paired with
	// the items sent. Every item in the request is affected.
	ScopeRequest FailureScope = iota

	// ScopeItem is the failure of one item in a bulk request the cluster
	// answered, or of an item that was never sent because it could not be
	// serialized or had no document ID.
	ScopeItem
)

// String returns the name of the scope without its Scope prefix.
func (s FailureScope) String() string {
	switch s {
	case ScopeRequest:
		return "request"
	case ScopeItem:
		return "item"
	default:
		return "unknown"
	}
}

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

	// OnRequestFailure is called when a whole bulk request fails, once per
	// failed attempt, before any retry. The event's Scope is [ScopeRequest].
	OnRequestFailure(ctx context.Context, event BulkFailureEvent)

	// OnItemFailure is called when one item fails, once per failed attempt,
	// before any retry. It is called for an item the cluster rejected and for
	// an item that was never sent, but not for one failed by [BulkIndexer.Cancel]
	// or a Close that escalated: a cancellation is no failure of the work, and
	// the item's [BulkIndexerObserver.OnItemSettled] carries [ErrIndexerCanceled].
	// The event's Scope is [ScopeItem].
	OnItemFailure(ctx context.Context, event BulkFailureEvent)

	// OnItemSettled is called once per item, when it reaches its final state:
	// accepted, or failed with no retry left. It fires at the same moment as
	// the item's OnSuccess or OnFailure callback.
	OnItemSettled(ctx context.Context, event BulkItemEvent)
}

// BaseBulkIndexerObserver is an embeddable no-op [BulkIndexerObserver]. Embed it
// in your own struct and override only the methods you care about.
type BaseBulkIndexerObserver struct{}

// OnRequestEnd implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnRequestEnd(context.Context, BulkRequestEvent) {}

// OnRequestFailure implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnRequestFailure(context.Context, BulkFailureEvent) {}

// OnItemFailure implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnItemFailure(context.Context, BulkFailureEvent) {}

// OnItemSettled implements BulkIndexerObserver (no-op).
func (BaseBulkIndexerObserver) OnItemSettled(context.Context, BulkItemEvent) {}

// BulkRequestEvent describes one bulk request that has returned.
type BulkRequestEvent struct {
	// Index is the default index of the indexer, empty when items carry their own.
	Index string

	// Worker is the id of the worker that sent the request.
	Worker int

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

// BulkFailureEvent describes one failure of a request or of an item, as the
// retry policy was shown it.
type BulkFailureEvent struct {
	// Scope says whether Items counts the whole request or a single item.
	Scope FailureScope

	// Kind is the classification the retry policy was given.
	Kind FailureKind

	// Index is the default index of the indexer, empty when items carry their own.
	Index string

	// Worker is the id of the worker handling the failure.
	Worker int

	// Attempt is the number of requests that have carried the failed work,
	// counting the one that just failed: 1 after the first request. It is 0
	// for an item that was never sent.
	Attempt int

	// Items is the number of items affected: every item in the request for
	// [ScopeRequest], and 1 for [ScopeItem].
	Items int

	// DocumentID is the failed item's id, for [ScopeItem]. It is empty for
	// [ScopeRequest].
	DocumentID string

	// StatusCode is the HTTP status of the response: of the whole request for
	// [ScopeRequest] (0 when none arrived), of the item for [ScopeItem].
	StatusCode int

	// Resp is the response item for [ScopeItem] when the cluster rejected it.
	// It is the zero value otherwise.
	Resp opensearchapi.BulkRespItem

	// Err is the request's error for [ScopeRequest], and nil for an item the
	// cluster rejected. It is non-nil for an item that was never sent.
	Err error

	// Retrying reports that the retry policy asked for the work to be sent
	// again. When false the failure is final for the affected items.
	Retrying bool

	// Delay is how long the retry policy asked to wait first. It is 0 unless
	// Retrying is true.
	Delay time.Duration
}

// BulkItemEvent describes an item that reached its final state.
type BulkItemEvent struct {
	// Index is the index the item targeted, falling back to the default index
	// of the indexer.
	Index string

	// Action is the bulk action: index, create, update or delete.
	Action string

	// DocumentID is the item's id.
	DocumentID string

	// Metadata is the item's [BulkIndexerItem].Metadata, unchanged.
	Metadata any

	// Worker is the id of the worker that settled the item.
	Worker int

	// Attempts is the number of requests that carried the item: 1 when it
	// settled on its first, more after retries, and 0 when it was never sent.
	Attempts int

	// Failed is true when the item failed, and false when it was accepted.
	Failed bool

	// Resp is the response item the cluster returned, when it returned one.
	Resp opensearchapi.BulkRespItem

	// Err is the error the item failed with when the whole request failed, or
	// when the item was never sent. It is nil for an accepted item and for an
	// item the cluster rejected.
	Err error
}
