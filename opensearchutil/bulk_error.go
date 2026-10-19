// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// ErrorLayer is the part of a [BulkIndexer] an error applies to. Each layer holds
// the ones below it: an item is in a request, a request carries the items of one
// index's shard, and the indexer holds every index's items.
type ErrorLayer uint8

const (
	// LayerUnknown is the zero value. No [BulkError] the indexer reports has it.
	LayerUnknown ErrorLayer = iota

	// LayerItem is an error of one item: the cluster answered its request and
	// rejected the item, or the item was never sent because it could not be
	// serialized or had no document ID.
	LayerItem

	// LayerRequest is an error of a whole bulk request: no response, an error
	// status for the request, or a response that cannot be paired with the items
	// sent. Every item in the request draws the same error, and nothing says
	// whether the batch landed.
	LayerRequest

	// LayerIndex is an error of every unsent item of one index: its shard map
	// changed or was lost before they were sent; see [ErrShardMapChanged].
	LayerIndex

	// LayerIndexer is an error of every item the indexer still held: it was
	// canceled, by [BulkIndexer.Cancel] or by a Close whose context ended; see
	// [ErrIndexerCanceled].
	LayerIndexer

	numErrorLayers = iota
)

// String returns the name of the layer without its Layer prefix, in lower case.
func (l ErrorLayer) String() string {
	switch l {
	case LayerUnknown:
		return "unknown"
	case LayerItem:
		return "item"
	case LayerRequest:
		return "request"
	case LayerIndex:
		return "index"
	case LayerIndexer:
		return "indexer"
	default:
		return "ErrorLayer(" + strconv.Itoa(int(l)) + ")"
	}
}

// ErrorKind is why work failed, which decides whether sending it again can help.
// It is what a [RetryPolicy] is given to decide.
type ErrorKind uint8

const (
	// KindUnknown is the zero value. No [BulkError] the indexer reports has it.
	KindUnknown ErrorKind = iota

	// KindRejected means the cluster refused the work because a write queue was
	// full (HTTP 429), either for the whole request or for one item. The work was
	// not applied.
	KindRejected

	// KindUnavailable means the cluster, or a proxy in front of it, could not serve
	// the work right now (HTTP 503), either for the whole request or for one item.
	// The work is treated as not applied.
	KindUnavailable

	// KindTransport means the outcome is unknown: the request failed before a
	// response arrived, or the response was a server error other than 503 (for one
	// item, a status of 500 or more other than 503) or could not be read. The batch
	// may have been applied in full, in part, or not at all, so retrying it can
	// write a document twice unless the writes are idempotent. See the
	// precondition on [RetryPolicy].
	KindTransport

	// KindPermanent means the same work would fail the same way: a mapping error,
	// a version conflict, any other 4xx, a response that cannot be paired with the
	// items sent, an item that cannot be serialized or has no document ID, or an
	// item whose index's shard map changed. Retrying cannot succeed.
	KindPermanent

	// KindCanceled means the indexer was canceled before the work was sent; see
	// [ErrIndexerCanceled]. It is not a failure of the work, and is never retried.
	KindCanceled

	numErrorKinds = iota
)

// String returns the name of the kind without its Kind prefix, in lower case.
func (k ErrorKind) String() string {
	switch k {
	case KindUnknown:
		return "unknown"
	case KindRejected:
		return "rejected"
	case KindUnavailable:
		return "unavailable"
	case KindTransport:
		return "transport"
	case KindPermanent:
		return "permanent"
	case KindCanceled:
		return "canceled"
	default:
		return "ErrorKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// The layer sentinels: errors.Is(err, ErrRequestLayer) reports whether err is a
// [BulkError] of that layer.
var (
	ErrItemLayer    = errors.New("opensearchutil: bulk item error")
	ErrRequestLayer = errors.New("opensearchutil: bulk request error")
	ErrIndexLayer   = errors.New("opensearchutil: bulk index error")
	ErrIndexerLayer = errors.New("opensearchutil: bulk indexer error")
)

// The kind sentinels: errors.Is(err, ErrRejected) reports whether err is a
// [BulkError] of that kind. [KindCanceled] matches [ErrIndexerCanceled].
var (
	ErrRejected    = errors.New("opensearchutil: rejected by the cluster")
	ErrUnavailable = errors.New("opensearchutil: cluster unavailable")
	ErrTransport   = errors.New("opensearchutil: transport error, outcome unknown")
	ErrPermanent   = errors.New("opensearchutil: permanent error")
)

//nolint:gochecknoglobals // Lookup tables for BulkError.Is.
var (
	layerSentinels = [numErrorLayers]error{
		LayerItem: ErrItemLayer, LayerRequest: ErrRequestLayer, LayerIndex: ErrIndexLayer, LayerIndexer: ErrIndexerLayer,
	}
	kindSentinels = [numErrorKinds]error{
		KindRejected: ErrRejected, KindUnavailable: ErrUnavailable, KindTransport: ErrTransport,
		KindPermanent: ErrPermanent, KindCanceled: ErrIndexerCanceled,
	}
)

// BulkError is the error a [BulkIndexer] reports for work that failed: to an
// item's OnError, to a [RetryPolicy], to [BulkIndexerObserver], and to
// OnUnhandledError for an item that has no OnError. It says which layer failed and
// why, and keeps the cause.
//
// errors.Is matches it against the sentinel of its layer (ErrItemLayer,
// ErrRequestLayer, ErrIndexLayer, ErrIndexerLayer) and of its kind (ErrRejected,
// ErrUnavailable, ErrTransport, ErrPermanent, ErrIndexerCanceled), and against
// anything its cause matches.
type BulkError struct {
	// Layer is the part of the indexer that failed.
	Layer ErrorLayer
	// Kind is why, and decides whether a retry can help.
	Kind ErrorKind
	// Status is the HTTP status: of the item for LayerItem, of the request for
	// LayerRequest, and 0 when none arrived or for the other layers.
	Status int
	// Resp is the item's response when the cluster answered and rejected it, and
	// the zero value otherwise.
	Resp opensearchapi.BulkRespItem
	// Err is the cause: the request's error, the reason an item was never sent, or
	// ErrShardMapChanged or ErrIndexerCanceled. It is nil for an item the cluster
	// rejected, whose reason is in Resp.
	Err error

	// action is the item's bulk action, for the message of a rejected item.
	action string
}

// Error returns the cause's message, or for an item the cluster rejected, the
// action, the document ID and the cluster's reason.
func (e *BulkError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}

	var id string
	if e.Resp.ID != nil {
		id = *e.Resp.ID
	}
	if e.Resp.Error != nil {
		var reason string
		if e.Resp.Error.Reason != nil {
			reason = *e.Resp.Error.Reason
		}

		return fmt.Sprintf("%q %q rejected: %q: %q", e.action, id, e.Resp.Error.Type, reason)
	}

	return fmt.Sprintf("%q %q rejected: status %d", e.action, id, e.Status)
}

// Unwrap returns the cause.
func (e *BulkError) Unwrap() error { return e.Err }

// Is reports whether target is the sentinel of e's layer or of its kind.
func (e *BulkError) Is(target error) bool {
	if target == nil {
		return false
	}
	if int(e.Layer) < len(layerSentinels) && layerSentinels[e.Layer] == target {
		return true
	}

	return int(e.Kind) < len(kindSentinels) && kindSentinels[e.Kind] == target
}

// unsentError classifies the reason err an item was never sent, and wraps it.
func unsentError(item BulkIndexerItem, err error) *BulkError {
	switch {
	case errors.Is(err, ErrIndexerCanceled):
		return &BulkError{Layer: LayerIndexer, Kind: KindCanceled, Err: err, action: item.Action}
	case errors.Is(err, ErrShardMapChanged):
		return &BulkError{Layer: LayerIndex, Kind: KindPermanent, Err: err, action: item.Action}
	default:
		return &BulkError{Layer: LayerItem, Kind: KindPermanent, Err: err, action: item.Action}
	}
}
