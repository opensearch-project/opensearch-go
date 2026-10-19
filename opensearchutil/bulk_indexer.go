// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

const defaultFlushInterval = 30 * time.Second

// What DefaultMaxMemoryBytes is sized for: keeping one default AWS gp3 volume
// busy, which takes about 150 MB/s, from a single client.
//
// The bytes that must be held to sustain a rate are the rate times the time a
// request is out, and a bulk request spends most of that time on the server, in the
// translog fsync and indexing, and not on the wire: the wire alone, at 1 Gbps and
// 5 ms, is a bandwidth-delay product of only 625 kB. A pessimistic second of server
// time is assumed. Filling one buffer while the last is out doubles it.
const (
	assumedBytesPerSecond  = 150_000_000
	assumedRoundTrip       = 5 * time.Millisecond
	assumedServiceTime     = time.Second
	memoryBufferingFactor  = 2
	assumedMemoryBytesNeed = memoryBufferingFactor * assumedBytesPerSecond *
		int64(assumedRoundTrip+assumedServiceTime) / int64(time.Second) // 301.5 MB.
)

// DefaultMaxMemoryBytes is the default for BulkIndexerConfig.MaxMemoryBytes when
// the process has no Go memory limit (GOMEMLIMIT) to take a share of. It is what a
// single default gp3 volume's 150 MB/s needs held (301.5 MB, see above) rounded up
// to the next power of two, which also leaves room for the structure that carries
// each small document to its callbacks.
const DefaultMaxMemoryBytes = 512 << 20 // 512 MiB.

// DefaultMaxMemoryBytes must hold what the assumptions above need; this fails to
// compile when it does not.
const _ = uint(DefaultMaxMemoryBytes - assumedMemoryBytesNeed)

// memoryLimitShare is the part of the Go memory limit, when there is one, that the
// default MaxMemoryBytes takes: a quarter, leaving the rest to the application.
const memoryLimitShare = 4

// minLimitedMemoryBytes is the least the default MaxMemoryBytes is when the process
// has a Go memory limit: a small limit does not shrink the default below it, so a
// process that sets one for its own reasons still gets a bulk pipeline that keeps
// several volumes busy.
const minLimitedMemoryBytes = 4 << 30 // 4 GiB.

//nolint:mnd // Well-known power-of-two buffer cap.
const defaultMetaBufferPoolMaxBytes = 32 << 10 // 32 KiB

// Bulk action names as they appear in the action/metadata line of a bulk
// request (e.g. `{ "index": { ... } }`).
const (
	actionIndex  = "index"
	actionCreate = "create"
	actionDelete = "delete"
	actionUpdate = "update"
)

// ErrBulkIndexerClosed is returned by Add and Flush on an indexer that has been
// closed.
var ErrBulkIndexerClosed = errors.New("opensearchutil: bulk indexer is closed")

// ErrNilStats is returned by GetStats when it is given no struct to fill.
var ErrNilStats = errors.New("opensearchutil: GetStats needs a non-nil BulkIndexerStats")

// ErrInvalidConfig is wrapped by the error [NewBulkIndexer] returns for a
// configuration it cannot run with, such as a negative byte limit.
var ErrInvalidConfig = errors.New("opensearchutil: invalid bulk indexer configuration")

// ErrShardMapChanged is wrapped by the error an item fails with when the shard
// map it was buffered under was lost or replaced before the item was sent: the
// router stopped answering for the index, or reported a different shard geometry
// (see [ShardTarget.Generation]), as when the index is recreated, split or shrunk.
// The item's shard is then no longer where later actions on the same document will
// go, and sending it could let one of them reach the server first. It fails
// through its own OnError, or OnUnhandledError if it has none, and was never sent.
//
// Only items not yet sent fail. A request already out cannot be recalled; the Add
// that found the change waits for it to be answered before it proceeds, so that
// no later action overtakes it.
var ErrShardMapChanged = errors.New("opensearchutil: shard map changed before the item was sent")

// ErrShardRoutingUnavailable is wrapped by the error passed to OnWarning when
// the indexer starts without shard-aware buffering because it has no way to
// learn where shards live.
var ErrShardRoutingUnavailable = errors.New("opensearchutil: shard routing unavailable")

// BulkIndexer represents a parallel, asynchronous, efficient indexer for OpenSearch.
//
// It keeps a buffer per shard and sends a shard's buffer, as one bulk request, to
// the node hosting the shard, so a request carries many documents for the one shard
// it touches. Three limits keep its memory bounded: a limit on the job, a limit on
// what is buffered for each node, and a size for each shard's buffer. The last two
// follow what the cluster can take, so that bulk traffic gives way to the rest of
// what a node is serving. Documents of one shard are written in the order they
// were added, also when the shard moves to another node. When the router reports
// that an index's shard geometry changed or stops answering for it, the items still
// buffered for its shards fail with [ErrShardMapChanged] rather than risk a later
// action reaching the server first.
//
// The limits delay work and never stop it. A shard's buffer is never asked to be
// smaller than 1 byte (BulkSizing.MinBytes is held to at least 1, also with
// [ShardFixed]) and is sealed once it holds FlushBytes, so no buffer waits for
// a size it cannot reach; a shard that holds nothing may always take one item, past
// MaxMemoryBytes if need be; and when nothing is out to a node, one sealed buffer
// is sent whatever its size, past NodeBytes if need be.
type BulkIndexer interface {
	// Add adds an item to the indexer. It returns an error when the item cannot be added.
	// Use the OnSuccess and OnError callbacks to get the operation result for the item.
	//
	// You must call the Close() method after you're done adding items.
	//
	// It is safe for concurrent use. When it's called from goroutines,
	// they must finish before the call to Close, eg. using sync.WaitGroup.
	//
	// Add blocks while the memory the indexer holds, from Add to the end of the
	// request that sends the item, is at MaxMemoryBytes, until requests finish and
	// give it back or ctx ends.
	//
	// Items that carry a DocumentID or a Routing are written in the order they
	// were added: the indexer sends no batch until every earlier batch with a
	// document of one of its shards has finished, whichever node either goes to.
	//
	// Callbacks run on the goroutine that sends the request and must not block.
	// In particular they must not call Add or Flush.
	Add(context.Context, BulkIndexerItem) error

	// Flush sends every item in BulkIndexer's queue before the call was made
	// and leaves the indexer open, so one indexer can drain on demand and keep
	// going. Items added concurrently may land in this drain or the next, and
	// the bulk requests run on ctx.
	//
	// When Flush returns without a context error, every item it covered has
	// had its OnSuccess or OnError called: the
	// callbacks run before the drain is reported done, so a caller may commit
	// an external position, such as a stream offset, from what the callbacks
	// recorded the moment Flush returns. Callers rely on this ordering.
	//
	// When ctx ends first, Flush returns the context's error and stops waiting.
	// It does not undo the drain: the requests it started finish on their own. The bulk
	// requests run on ctx, so a request still in flight is cancelled and the
	// items it carried fail with that error. Their callbacks, like those of
	// items the drain had not reached, fire exactly once, but may fire after
	// Flush has returned. So when Flush returns a context error, the items it
	// covered are not known to be final: record completions as the callbacks
	// report them rather than inferring them from the return.
	//
	// A non-nil error that is not a context error means the drain itself
	// failed; documents the cluster rejected individually go to their
	// OnError callback. Safe alongside Add and other concurrent Flush calls.
	// After Close it returns ErrBulkIndexerClosed.
	Flush(context.Context) error

	// Close waits until the indexer is done with every added item, which
	// includes waiting out requests in flight and retry backoff, and closes the
	// indexer. It blocks for as long as that takes, bounded only by the client's
	// own request timeouts and by when the retry policy gives up.
	//
	// When ctx ends first, Close escalates to Cancel: the items not yet final
	// fail, the workers unwind promptly, and Close returns ctx's error once
	// they have. Pass a context with a deadline to bound Close, or run it in a
	// goroutine to avoid waiting.
	Close(context.Context) error

	// Cancel stops the indexer immediately instead of waiting: requests in
	// flight are canceled, retry backoff ends, and every item that is not yet
	// final fails through its OnError exactly once, with ErrIndexerCanceled
	// when it was never sent. Items added afterwards fail the same way. Cancel
	// does not wait and does not replace Close, which still must be called. It
	// is idempotent and safe for concurrent use, including from a callback.
	Cancel()

	// Stats returns indexer statistics.
	Stats() BulkIndexerStats

	// GetStats loads the indexer's statistics into dst, which replaces everything
	// in it. It is for a caller that reads them often, for example one that steers
	// its own input by how loaded the indexer is: the caller owns dst and reuses
	// it, so nothing is allocated, and the indexer's state can only be read
	// through it, never written. Every value is loaded from an atomic, so GetStats
	// takes no lock and is safe to call at any time and from any goroutine, but the
	// values are loaded one after another and are not a single instant: a counter
	// can have moved between two of them. It returns [ErrNilStats] when dst is nil,
	// and works on a closed indexer, whose statistics no longer change.
	GetStats(dst *BulkIndexerStats) error
}

// BulkIndexerConfig represents configuration of the indexer.
type BulkIndexerConfig struct {
	// NumWorkers caps the number of bulk requests the indexer may have outstanding
	// at once, across all nodes. The default, 0, sets no cap beyond the ones that
	// are always there: a shard has one request out at a time, and a node has as many
	// as the window the transport allows its write pool, which follows the pool's
	// own statistics. A node the transport reports no window for, such as the one
	// that stands for the cluster when the indexer knows no shard map, has as many
	// as NumWorkers, or GOMAXPROCS when NumWorkers is 0. GOMAXPROCS follows a
	// container's CPU limit since Go 1.25, where runtime.NumCPU does not. A cap of
	// 1 sends one request at a time, and runs callbacks on one goroutine at a time.
	NumWorkers int

	// FlushBytes is the largest buffer the indexer keeps for a shard, and so the
	// largest bulk request body it sends. How large a shard's buffer is, up to this,
	// follows how quickly the shard's requests are served: see Sizing. Defaults to
	// 5MB.
	FlushBytes int

	// FlushInterval is the longest a document waits in a batch that has not
	// filled. Defaults to 30sec.
	//
	// Batches are per node and not per worker, but a sparse stream still fills
	// them slowly, and for a sparse stream the interval is the latency of a
	// document. An indexer that wants documents out sooner than 30 seconds, a
	// streaming one for instance, should lower it.
	FlushInterval time.Duration

	Client *opensearchapi.Client // The OpenSearch client.

	// Context for worker lifecycle. If nil, context.Background() will be used.
	//nolint:containedctx // Config struct is short-lived, context extracted during New()
	Context context.Context

	// OnUnhandledError is called for an error no item callback took: a failed item
	// that has no OnError of its own, as a *BulkError, and the indexer's own errors,
	// such as a flush or a Close cut short by its context.
	OnUnhandledError func(context.Context, error)
	// OnError is the v5.0 name of OnUnhandledError, kept so that code written for
	// v5.0 and earlier still builds. It is called in OnUnhandledError's place when
	// OnUnhandledError is nil.
	//
	// Deprecated: Use OnUnhandledError.
	OnError      func(context.Context, error)
	OnFlushStart func(context.Context) context.Context // Called when the flush starts.
	OnFlushEnd   func(context.Context)                 // Called when the flush ends.

	// OnWarning is called, at most once for each condition and before
	// NewBulkIndexer returns, when the indexer cannot do something it would do by
	// default and carries on without it. Today that is shard-aware buffering with
	// a transport that cannot say where shards live; the error wraps
	// [ErrShardRoutingUnavailable] and names the cause. It is not an error that
	// stops anything, and OnUnhandledError is not called for it.
	OnWarning WarningFunc

	// OnAddBlocked is called each time an Add had to wait for buffer space, with
	// how long it waited, once the wait is over, whether Add went on to succeed
	// or gave up. It runs on the goroutine that called Add. It is where a
	// caller records the distribution of waits, in a histogram for instance;
	// BulkIndexerStats carries only their count and total.
	OnAddBlocked AddBlockedFunc

	// RetryPolicy, when set, decides whether an item that failed is sent again.
	// With it set, OnSuccess and OnError fire once per item, when the indexer is
	// done with it, never for an attempt that is going to be retried.
	// When nil, an item that fails is not retried. Retrying requires every
	// item to carry a DocumentID, which the indexer enforces; see [RetryPolicy].
	RetryPolicy RetryPolicy

	// Fingerprinter, when set, derives the DocumentID of an item that has none.
	// See [DocumentFingerprinter].
	Fingerprinter DocumentFingerprinter

	// Observer, when set, receives notifications about the requests the indexer
	// sends and how each item fares, for statistics and metrics. See
	// [BulkIndexerObserver] for what it may do.
	Observer BulkIndexerObserver

	// Parameters of the Bulk API.
	Index               string
	ErrorTrace          bool
	Header              http.Header
	Human               bool
	Pipeline            string
	Pretty              bool
	Refresh             string
	Routing             string
	Source              []string
	SourceExcludes      []string
	SourceIncludes      []string
	Timeout             time.Duration
	WaitForActiveShards string

	// MetaBufferPoolMaxBytes is the upper bound for buffers retained in
	// the metadata serialization pool. Buffers that grow beyond this
	// cap are discarded instead of returned. Defaults to 32 KiB.
	MetaBufferPoolMaxBytes int

	// ShardRouter maps documents to shards and shards to nodes. The indexer sends
	// the documents of a shard to the node that hosts it, buffers them with the
	// other documents bound for that node, and writes a shard's documents in the
	// order they were added. The server regroups every bulk by shard whatever the
	// client does, so a router that is missing or wrong costs efficiency and not
	// correctness.
	//
	// When nil, the indexer asks the client's transport, see [ClientShardRouter];
	// a transport that cannot answer leaves documents unplaced, which is reported
	// through OnWarning. Set it to override that, for example with a
	// [ChainShardRouter] that falls back to a [StaticShardRouter] when the cluster
	// does not know an index, or to [NoShardRouter] to turn shard-aware buffering
	// off.
	ShardRouter ShardRouter

	// Sizing tunes how a shard's buffer follows how quickly the shard's requests
	// are served, and how a node's window of bytes out at once answers a 429. The
	// byte fields (MinBytes, InitialBytes, StepBytes) apply to shard buffers; the
	// window is bounded by NodeBytes. The zero value selects defaults derived from
	// FlushBytes.
	//
	// The OPENSEARCH_GO_BULK_SHARD, OPENSEARCH_GO_BULK_NODE and
	// OPENSEARCH_GO_BULK_PID_KP, _KI and _KD environment variables override the
	// matching fields when they are set and valid, so an operator can fix the shard
	// buffers or the node windows, each on its own, or retune the PID gains, without
	// a code change. See [BulkSizing].
	Sizing BulkSizing

	// MaxMemoryBytes is the job's memory limit: the most the indexer holds from the
	// moment Add accepts an item until the request that sends it has finished. That
	// is the item's request body and the structure that carries it to its callbacks,
	// so a stream of small documents is not undercounted. When the limit is spent,
	// Add waits for requests to finish and give memory back, and the fullest shard
	// buffer that can go out is sent at once so that the wait is short, so a slow or
	// stalled cluster slows the caller down and does not grow the indexer's memory.
	// An item larger than the whole limit is let through alone.
	//
	// The limit is shared among shards. While other shards are waiting for room, a
	// shard holds no more than an even share of it, so a shard whose requests are
	// slow makes only the callers adding to it wait; while nobody else is waiting, a
	// shard may use all of it. A shard that holds nothing may always take one item,
	// so that no shard is ever left with nothing in progress: the limit can be passed
	// by at most one item for each such shard.
	//
	// Defaults to max(GOMEMLIMIT/4, 4 GiB, [DefaultMaxMemoryBytes]) when the process
	// has a Go memory limit (GOMEMLIMIT), and to [DefaultMaxMemoryBytes], enough for
	// one default AWS gp3 volume's 150 MB/s, when it does not. The 4 GiB floor can
	// exceed a Go memory limit under 16 GiB; a process that sets a smaller one, and
	// wants the indexer held inside it, sets MaxMemoryBytes.
	//
	// Wait time is reported by BulkIndexerStats.NumBlockedAdds and
	// BlockedAddDuration, and by OnAddBlocked.
	MaxMemoryBytes int

	// NodeBytes is the most bytes the indexer will have out to one node at once, in
	// requests that have been sent and not yet answered, and the window it starts at.
	// Left unset it is MaxMemoryBytes: wide open, so that it is a 429 that teaches the
	// indexer where the node's limit is. A 429 cuts the window below it, to half of
	// what was out when the 429 arrived, and it grows back slowly; set NodeBytes to
	// keep a node under a figure of your own whatever it accepts. A sealed buffer that
	// would take the node past its window waits, still counting against
	// MaxMemoryBytes, until the requests out are answered; when nothing is out, one
	// is sent whatever its size. With [NodeFixed] the window stays at NodeBytes.
	// It does not change the size of any buffer, and it is separate from the number of
	// requests out at once, which the client's transport limits.
	NodeBytes int
}

// BulkIndexerStats represents the indexer statistics.
type BulkIndexerStats struct {
	// Each field is a COUNTER, which only grows, so that two readings give a rate,
	// or a GAUGE, a reading of the moment. Durations are in nanoseconds and sizes in
	// bytes unless the field says otherwise.

	// NumAdded counts the items Add queued (COUNTER, items).
	NumAdded uint64
	// BulkAddFailCount counts the items Add refused because the caller's context
	// ended before the item could be queued (COUNTER, items).
	BulkAddFailCount uint64
	// NumFlushed counts the items the cluster accepted (COUNTER, items).
	NumFlushed uint64
	// NumFailed counts the items that failed and will not be retried (COUNTER,
	// items). Every queued item ends in NumFlushed or NumFailed, once.
	NumFailed uint64
	// NumIndexed, NumCreated, NumUpdated and NumDeleted count the accepted items by
	// action (COUNTER, items).
	NumIndexed uint64
	NumCreated uint64
	NumUpdated uint64
	NumDeleted uint64
	// NumRequests counts the bulk requests sent, retries included (COUNTER,
	// requests).
	NumRequests uint64

	// NumGroupedItems counts items placed in a shard, and so in that shard's
	// order, and NumUngroupedItems those that belong to no shard: the shard map of
	// the index was unknown, or the item had neither a routing value nor a
	// DocumentID (COUNTER, items). Without a shard router every item is ungrouped.
	// A high ungrouped share is efficiency lost, not data lost.
	NumGroupedItems   uint64
	NumUngroupedItems uint64

	// NumByteFlushes counts batches sent because they reached the size chosen for
	// their node, NumTimerFlushes those sent by the flush interval, and
	// NumPressureFlushes those sent early because Add ran out of memory under
	// MaxMemoryBytes, and NumCloseFlushes those sent by the final drain in Close
	// (COUNTER, batches). Batches sent because Flush asked are in none of them.
	// See TimerFlushRatio. A growing NumPressureFlushes says the indexer is short
	// of memory for the cluster's pace.
	NumByteFlushes     uint64
	NumTimerFlushes    uint64
	NumPressureFlushes uint64
	NumCloseFlushes    uint64

	// BufferedBytes is the number of bytes counted against MaxMemoryBytes that
	// are not in a bulk request yet: waiting in a shard buffer. InFlightBytes is the
	// number in requests being sent (GAUGE, bytes).
	BufferedBytes uint64
	InFlightBytes uint64
	// NumBlockedAdds counts Add calls that had to wait for buffer space (COUNTER,
	// calls).
	NumBlockedAdds uint64
	// BlockedAddDuration is the total time Add calls spent waiting for buffer
	// space (COUNTER, as a time.Duration). A value that grows while the cluster is
	// healthy says MaxMemoryBytes is too small for the workload; one that grows while
	// requests stall says the limit is doing its job.
	BlockedAddDuration time.Duration

	// NumRetries counts the items sent again after an error, indexed by the
	// ErrorKind of the error that caused it (COUNTER, items). An item retried twice
	// counts twice. Only kinds a RetryPolicy chose to retry appear, so KindUnknown
	// and KindCanceled stay 0.
	NumRetries [numErrorKinds]uint64

	// Errors counts errors as they happen, indexed by ErrorLayer and then ErrorKind,
	// as Errors[LayerRequest][KindRejected] (COUNTER, occurrences). It counts one
	// for each failed attempt of a whole request, and one for each item for the
	// item, index and indexer layers, whether or not the RetryPolicy then retried
	// it: the same occurrences an Observer's OnRequestError and OnItemError
	// receive, and osprom's bulk_errors_total counts. Unlike those it also counts
	// items failed by Cancel (LayerIndexer, KindCanceled), which send no event.
	// LayerUnknown and KindUnknown are never counted. The three relate as follows:
	// an item the cluster rejects twice and then accepts counts 2 in
	// Errors[LayerItem], 2 in NumRetries and 0 in NumFailed; one it rejects for good
	// counts each attempt in Errors[LayerItem] and 1 in NumFailed. A request that
	// fails as a whole counts 1 in Errors[LayerRequest] however many items it
	// carried, and each of them in NumRetries or NumFailed.
	Errors [numErrorLayers][numErrorKinds]uint64

	// ItemBytes is the total length of the documents of the items Add queued, the
	// length of each item's Body, without its action line (COUNTER, bytes). Divided
	// by NumAdded it is the mean document size. MaxItemBytes is the largest single
	// document seen (bytes, never decreases: it is the largest since the indexer was
	// built, not lately). For how often documents are large, use an Observer, such
	// as osprom's bulk_item_size_bytes histogram.
	ItemBytes    uint64
	MaxItemBytes uint64

	// ItemLatencyNanos is the total time items took from Add to the moment the
	// indexer was done with them, accepted or failed for good, waiting for their
	// buffer to fill, for room on their node, and for retries included (COUNTER,
	// nanoseconds). Divided by NumFlushed + NumFailed it is the mean latency.
	// MaxItemLatencyNanos is the longest (nanoseconds, never decreases).
	ItemLatencyNanos    uint64
	MaxItemLatencyNanos uint64

	// RequestDurationNanos is the total time bulk requests took, from sending to the
	// answer, retries included (COUNTER, nanoseconds). Divided by NumRequests it is
	// the mean. MaxRequestDurationNanos is the longest (nanoseconds, never
	// decreases). RequestBytes is the total size of the request bodies sent (COUNTER,
	// bytes), before any compression by the transport.
	RequestDurationNanos    uint64
	MaxRequestDurationNanos uint64
	RequestBytes            uint64

	// InFlightRequests is the number of bulk requests sent and not yet answered
	// (GAUGE, requests). QueuedBuffers is the number of sealed buffers waiting to be
	// sent, for room on their node or for an earlier request of their shard (GAUGE,
	// buffers). BlockedShards is the number of shards with a request out and another
	// buffer, open or sealed, waiting behind it (GAUGE, shards); a hot shard shows
	// here first.
	InFlightRequests uint64
	QueuedBuffers    uint64
	BlockedShards    uint64

	// MaxMemoryBytes is the limit BufferedBytes and InFlightBytes count against
	// (GAUGE, bytes; it does not change).
	MaxMemoryBytes uint64
	// NumRejections counts the requests a node answered with a 429, for the whole
	// request or for any item in it, whether or not a RetryPolicy sent them again
	// (COUNTER, requests). A caller that samples it sees that the cluster pushed
	// back since it last looked, however briefly, and can compare two readings for a
	// rate.
	NumRejections uint64
	// NumNodes is the number of nodes the indexer has written to (GAUGE, nodes). The
	// three readings below are taken across them.
	NumNodes uint64
	// NodeWindow is how many bytes the indexer will have out to a node at once, as a
	// fraction of the most it would allow (NodeBytes): 1 while the node takes what it
	// is sent, cut to half of what was out each time it answers 429, and rising slowly
	// as it takes writes again (GAUGE, fraction). A node that is merely slow does not
	// move it; Slowdown and BufferSize show that. Before any node has been used every
	// field is 1.
	NodeWindow JobNodeStats
	// BufferSize is how large the indexer's buffers for a node are, as a fraction of
	// FlushBytes, averaged over the node's shards and smoothed over its last few
	// requests (GAUGE, fraction). The indexer makes them smaller as a node serves its
	// requests more slowly and larger while it keeps up. Before any node has been
	// used every field is the size buffers start at.
	BufferSize JobNodeStats
	// Slowdown is how much longer a node takes for its requests than it needs to,
	// smoothed over its last few requests: 0 when it takes the expected time, 1 when
	// it takes twice as long (GAUGE, ratio). The expected time is the one the buffer
	// sizing judges a request by: the node's fixed cost per request plus the
	// request's bytes at the best rate the node has shown. It is 0 before any node has
	// been used.
	Slowdown JobNodeStats
}

// BudgetFill returns how full the indexer's memory is, from 0 to 1: the bytes it
// holds, buffered and in requests, over MaxMemoryBytes. Add starts to wait when it
// reaches 1. It is 0 when MaxMemoryBytes is not known.
func (s BulkIndexerStats) BudgetFill() float64 {
	if s.MaxMemoryBytes == 0 {
		return 0
	}

	return min(float64(s.BufferedBytes+s.InFlightBytes)/float64(s.MaxMemoryBytes), 1)
}

// TimerFlushRatio returns the share of the batches sent because they filled or
// because the flush interval ran out that the interval sent, from 0 to 1, or 0
// when there were none. A high ratio means batches rarely fill before the
// interval flushes them: the stream is too sparse for the batch size, and
// requests carry fewer documents per shard than they could. Lower FlushBytes or
// FlushInterval, or accept the efficiency.
func (s BulkIndexerStats) TimerFlushRatio() float64 {
	total := s.NumTimerFlushes + s.NumByteFlushes
	if total == 0 {
		return 0
	}

	return float64(s.NumTimerFlushes) / float64(total)
}

// BulkIndexerItem represents an indexer item.
type BulkIndexerItem struct {
	Index               string
	Action              string
	DocumentID          string
	Routing             *string
	Version             *int64
	VersionType         *string
	IfSeqNum            *int64
	IfPrimaryTerm       *int64
	WaitForActiveShards any
	Refresh             *string
	RequireAlias        *bool
	// Body is the document. The indexer reads it from offset 0, whatever the
	// position it is handed in at, once to size it, once more if a Fingerprinter
	// needs it, and again for each attempt to send it, and it leaves the reader at 0.
	// A caller that keeps its own position in a reader must pass a reader of just the
	// document, such as an [io.SectionReader].
	Body            io.ReadSeeker
	RetryOnConflict *int

	// Metadata is opaque data the caller attaches to the item, such as the
	// stream offset the document was read from. It is never serialized into the
	// request and the indexer never reads it. It comes back unchanged in the
	// BulkIndexerItem handed to OnSuccess and OnError, so a callback can
	// recover it without a closure per item. The indexer keeps the value
	// referenced until its OnSuccess or OnError has run.
	Metadata any

	// bodyBytes is the length of Body, for BulkItemEvent.Bytes and the ItemBytes
	// statistic, and addedAt the indexer's clock when Add took the item, for the
	// ItemLatencyNanos statistic. Add sets both.
	bodyBytes int64
	addedAt   time.Duration

	// OnSuccess is called once for an item the cluster accepted, with its response.
	OnSuccess func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem)
	// OnError is called once for an item that failed and will not be retried. The
	// error says which layer failed and why, and holds the item's response in
	// Resp when the cluster rejected it; see [BulkError]. An item without
	// OnError is reported to [BulkIndexerConfig].OnUnhandledError instead.
	OnError func(context.Context, BulkIndexerItem, *BulkError)

	// OnFailure is the v5.0 name and signature of OnError, kept so that code written
	// for v5.0 and earlier still builds and behaves as it did. It is called only when
	// OnError is nil, with the arguments v5.0 gave it: the item's response, whose
	// Error is never nil, and the cause of the error, which is nil for an item the
	// cluster rejected.
	//
	// Deprecated: Use OnError, which receives the error as a [BulkError].
	OnFailure func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem, error)
}

// ErrZeroCopyBodySet is returned by [AddZeroCopy] for an item that sets Body as well
// as Data.
var ErrZeroCopyBodySet = errors.New("opensearchutil: zero-copy item sets Body; use Data")

// BulkIndexerZeroCopyItem is a [BulkIndexerItem] whose document the indexer sends
// from the caller's own memory instead of copying it. It is added with
// [AddZeroCopy], so that code which lends memory says so where it adds it.
//
// Lending trades a copy for ownership. The indexer reads Data where it is, from
// the moment the item is added until the request that carries it has finished and
// the item's callbacks have returned, and the caller must not change it, or reuse
// it, before Release is called. A caller that pools its buffers returns the slice
// to the pool in Release.
//
// What a lent slice holds counts toward MaxMemoryBytes by its length. A slice cut
// from a larger buffer keeps the whole buffer alive until Release, though, and the
// limit does not see that: a record cut from a large read buffer belongs in a
// plain [BulkIndexerItem], which copies the document and lets the buffer go.
type BulkIndexerZeroCopyItem struct {
	// BulkIndexerItem carries the action, the document ID, and the callbacks. Its
	// Body must be nil: the document is Data.
	BulkIndexerItem

	// Data is the document, sent as it is. It may be empty for an action that has
	// no document, such as a delete.
	Data []byte

	// Release, when set, is called once for each item that [AddZeroCopy] accepted,
	// after the indexer is finished with Data: the request carrying it has ended,
	// retries included, and the callbacks have returned, whatever the outcome, and
	// the transport has finished reading it. net/http can go on reading a request
	// body after it has answered, when the server replies early or the context ends,
	// so in that rare case Release runs when the transport is done, on its
	// goroutine, and possibly after Close has returned. A transport that never
	// finishes with the body never has Release called; the slice is then left to
	// the garbage collector and the caller's pool simply makes another. Release is
	// not called for an item that was not accepted, which the caller still owns. It
	// must not block or call Add.
	Release ZeroCopyReleaseFunc
}

// ZeroCopyReleaseFunc hands a lent document back to its owner; see
// [BulkIndexerZeroCopyItem.Release].
type ZeroCopyReleaseFunc func(data []byte)

// WarningFunc is the type of [BulkIndexerConfig.OnWarning].
type WarningFunc func(ctx context.Context, err error)

// AddBlockedFunc is the type of [BulkIndexerConfig.OnAddBlocked]; waited is how long
// the Add waited for buffer space.
type AddBlockedFunc func(ctx context.Context, waited time.Duration)

// lentBody is the Body of an item whose document the indexer was lent. It reads as
// the document, so callbacks given the item can read it, and it tells the
// serializer to take the slice and not copy it.
type lentBody struct {
	data    []byte
	off     int
	release ZeroCopyReleaseFunc
}

func newLentBody(data []byte, release ZeroCopyReleaseFunc) *lentBody {
	return &lentBody{data: data, release: release}
}

// Len returns the number of unread bytes.
func (l *lentBody) Len() int { return len(l.data) - l.off }

// Read implements [io.Reader].
func (l *lentBody) Read(p []byte) (int, error) {
	if l.off >= len(l.data) {
		return 0, io.EOF
	}

	n := copy(p, l.data[l.off:])
	l.off += n

	return n, nil
}

// Seek implements [io.Seeker].
func (l *lentBody) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = int64(l.off)
	case io.SeekEnd:
		base = int64(len(l.data))
	default:
		return 0, errors.New("opensearchutil: lent body: invalid whence")
	}

	pos := base + offset
	if pos < 0 {
		return 0, errors.New("opensearchutil: lent body: negative position")
	}

	l.off = int(min(pos, int64(len(l.data))))

	return pos, nil
}

// zeroCopyAdder is implemented by the indexers [NewBulkIndexer] returns.
type zeroCopyAdder interface {
	AddZeroCopy(context.Context, BulkIndexerZeroCopyItem) error
}

// AddZeroCopy adds a document the indexer sends from the caller's memory. See
// [BulkIndexerZeroCopyItem] for what the caller gives up in exchange for the copy
// it saves.
//
// It behaves as bi.Add does, blocking while the indexer's memory is spent and
// returning ErrBulkIndexerClosed after Close; an error means the item was not
// taken and Release will not be called. For an indexer that cannot lend, a custom
// implementation of [BulkIndexer], it copies Data, calls Release at once, and adds
// the copy.
func AddZeroCopy(ctx context.Context, bi BulkIndexer, item BulkIndexerZeroCopyItem) error {
	if adder, ok := bi.(zeroCopyAdder); ok {
		return adder.AddZeroCopy(ctx, item)
	}

	if item.Body != nil {
		return ErrZeroCopyBodySet
	}

	plain := item.BulkIndexerItem
	if len(item.Data) > 0 {
		plain.Body = bytes.NewReader(bytes.Clone(item.Data))
	}

	if err := bi.Add(ctx, plain); err != nil {
		return err
	}

	if item.Release != nil {
		item.Release(item.Data)
	}

	return nil
}

type bulkActionMetadata struct {
	Index               string  `json:"_index,omitempty"`
	DocumentID          string  `json:"_id,omitempty"`
	Routing             *string `json:"routing,omitempty"`
	Version             *int64  `json:"version,omitempty"`
	VersionType         *string `json:"version_type,omitempty"`
	IfSeqNum            *int64  `json:"if_seq_no,omitempty"`
	IfPrimaryTerm       *int64  `json:"if_primary_term,omitempty"`
	WaitForActiveShards any     `json:"wait_for_active_shards,omitempty"`
	Refresh             *string `json:"refresh,omitempty"`
	RequireAlias        *bool   `json:"require_alias,omitempty"`
	RetryOnConflict     *int    `json:"retry_on_conflict,omitempty"`
}

type bulkIndexer struct {
	wg sync.WaitGroup

	sched *scheduler
	// router maps documents to shards and nodes; never nil.
	router ShardRouter
	// sizing sizes a shard's buffers, and window is what each node's byte window
	// starts from.
	sizing resolvedSizing
	window windowConfig
	// slots, when the config caps NumWorkers, holds a token for each request that
	// is out.
	slots chan struct{}
	// itemPool recycles the slices that carry a buffer's items to the request that
	// sends them, and workerPool the state a request is sent with.
	itemPool, workerPool sync.Pool

	ticker *time.Ticker
	// stopFlush cancels the flusher goroutine; flusherDone is closed when that
	// goroutine returns. Close cancels via stopFlush (non-blocking and
	// idempotent, so Close never deadlocks even when the flusher already
	// returned via the construction context) and then waits on flusherDone, so
	// the periodic flush has fully stopped before Close runs its final drain and
	// the deferred implicit-client Close.
	stopFlush   context.CancelFunc
	flusherDone chan struct{}

	// cancelCtx ends when Cancel is called, or Close escalates. It is joined
	// into the context of every flush, so requests and retry backoff end with it.
	//nolint:containedctx // Carries the cancellation of Cancel to the workers.
	cancelCtx context.Context
	cancel    context.CancelFunc
	stats     *bulkIndexerStats

	metaPool         sync.Pool
	metaPoolMaxBytes int

	// placement tracks which indexes the router can place documents in. Reads are
	// lock-free; the mutex serializes the one-time switch of an index from
	// unplaced to placed, see firstPlacement.
	placement struct {
		sync.Mutex
		// known holds the indexes whose shard map the router has answered for, with
		// the geometry generation it placed them with (string -> uint64; 0 when the
		// router does not version its answers).
		known sync.Map
		// unplaced holds the indexes that had an item buffered without a shard for
		// want of a shard map (string -> struct{}). Ordering between those items
		// and the shard-placed ones that follow needs a drain, see firstPlacement.
		unplaced sync.Map
	}

	// chunks supplies the slab chunks every batch buffer is made of.
	chunks *chunkPool
	// pieces supplies the small chunks that follow a document sent without a copy.
	pieces *chunkPool
	// budget bounds the bytes held from Add to the end of the request; nil when
	// the indexer was built without init, as some tests do.
	budget *byteBudget
	// flushingBytes is the part of the budget in requests being sent.
	flushingBytes atomic.Int64

	// now is the clock request latency is read from.
	now func() time.Time
	// epoch is now when the indexer was built; item latency is measured from it.
	epoch time.Time

	// implicitClient is true when NewBulkIndexer implicitly created the client
	// (cfg.Client was nil). Close then closes it to release the shared cache
	// refcount; a caller-supplied client is left for its owner to close.
	implicitClient bool

	config BulkIndexerConfig
}

type bulkIndexerStats struct {
	numAdded         atomic.Uint64
	bulkAddFailCount atomic.Uint64
	numFlushed       atomic.Uint64
	numFailed        atomic.Uint64
	numIndexed       atomic.Uint64
	numCreated       atomic.Uint64
	numUpdated       atomic.Uint64
	numDeleted       atomic.Uint64
	numRequests      atomic.Uint64

	numGroupedItems   atomic.Uint64
	numUngroupedItems atomic.Uint64
	numByteFlushes    atomic.Uint64
	numTimerFlushes   atomic.Uint64
	numRetries        [numErrorKinds]atomic.Uint64

	numRejections atomic.Uint64
	numNodes      atomic.Uint64
	// nodeWindow, bufferSize and slowdown are the spreads across the nodes that the
	// scheduler publishes whenever a request completes.
	nodeWindow, bufferSize, slowdown spreadBits

	itemBytes, maxItemBytes                       atomic.Uint64
	itemLatencyNanos, maxItemLatencyNanos         atomic.Uint64
	requestDurationNanos, maxRequestDurationNanos atomic.Uint64
	requestBytes                                  atomic.Uint64
	numPressureFlushes                            atomic.Uint64
	numCloseFlushes                               atomic.Uint64
	// inFlightRequests, queuedBuffers and blockedShards are gauges the scheduler
	// keeps under its lock.
	inFlightRequests, queuedBuffers, blockedShards atomic.Int64
	errorCounts                                    [numErrorLayers][numErrorKinds]atomic.Uint64
}

// storeMax raises m to v when v is larger. Once m has stopped rising it costs a load.
func storeMax(m *atomic.Uint64, v uint64) {
	for {
		cur := m.Load()
		if v <= cur || m.CompareAndSwap(cur, v) {
			return
		}
	}
}

// countError counts one occurrence of err at its layer and kind.
func (st *bulkIndexerStats) countError(err *BulkError) {
	if int(err.Layer) < numErrorLayers && int(err.Kind) < numErrorKinds {
		st.errorCounts[err.Layer][err.Kind].Add(1)
	}
}

// sinceEpoch is the indexer's clock as a duration since it was built, the form an
// item's addedAt and the time it is done are compared in.
func (bi *bulkIndexer) sinceEpoch() time.Duration {
	return bi.now().Sub(bi.epoch)
}

// recordDone adds the latency of n items, totaling sum and the longest max, from
// Add to the moment the indexer was done with them.
func (bi *bulkIndexer) recordDone(sum, maxLatency time.Duration) {
	bi.stats.itemLatencyNanos.Add(uint64(max(sum, 0)))
	storeMax(&bi.stats.maxItemLatencyNanos, uint64(max(maxLatency, 0)))
}

// NewBulkIndexer creates a new bulk indexer. It returns an error wrapping
// [ErrInvalidConfig] for a negative byte limit (FlushBytes, MaxMemoryBytes,
// NodeBytes, or BulkSizing's MinBytes, InitialBytes or StepBytes; zero selects the
// default of each) and for a BulkSizing.Shard or Node that names no setting.
func NewBulkIndexer(cfg BulkIndexerConfig) (BulkIndexer, error) {
	// Before the client is made, so that a refusal has nothing to release.
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	implicitClient := false
	if cfg.Client == nil {
		var err error
		cfg.Client, err = opensearchapi.NewDefaultClient()
		if err != nil {
			return nil, err
		}
		implicitClient = true
	}

	// The deprecated OnError stands in for OnUnhandledError.
	if cfg.OnUnhandledError == nil {
		cfg.OnUnhandledError = cfg.OnError
	}

	// Initialize context if not provided
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}

	router, warning := chooseRouter(cfg)

	if cfg.FlushBytes == 0 {
		cfg.FlushBytes = 5e+6
	}

	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = defaultFlushInterval
	}

	if cfg.MetaBufferPoolMaxBytes == 0 {
		cfg.MetaBufferPoolMaxBytes = defaultMetaBufferPoolMaxBytes
	}

	if cfg.MaxMemoryBytes == 0 {
		cfg.MaxMemoryBytes = int(min(defaultMemoryLimit(), math.MaxInt))
	}

	// A node's byte window starts wide open, at the whole job's memory, unless the
	// caller capped it with NodeBytes. Rejections teach it where the server's limit is.
	sizing := cfg.Sizing.resolve(cfg.FlushBytes)
	window := windowConfig{
		start:   float64(cfg.MaxMemoryBytes),
		ceiling: float64(cfg.MaxMemoryBytes),
		floor:   float64(cfg.FlushBytes),
		unit:    float64(cfg.FlushBytes),
		backoff: sizing.backoff,
		fixed:   sizing.nodeFixed,
	}
	if cfg.NodeBytes > 0 {
		window.ceiling = float64(min(cfg.NodeBytes, cfg.MaxMemoryBytes))
		window.start = window.ceiling
	}

	bi := bulkIndexer{
		config:           cfg,
		stats:            &bulkIndexerStats{},
		router:           router,
		now:              time.Now,
		epoch:            time.Now(),
		sizing:           sizing,
		window:           window,
		metaPoolMaxBytes: cfg.MetaBufferPoolMaxBytes,
		implicitClient:   implicitClient,
		metaPool:         sync.Pool{New: newMetaEncoder},
	}

	if warning != nil {
		err := fmt.Errorf("%w: %w", ErrShardRoutingUnavailable, warning)
		opensearchtransport.Debug().Err(err).Msg("BulkIndexer: starting without shard-aware buffering")
		if cfg.OnWarning != nil {
			cfg.OnWarning(cfg.Context, err)
		}
	}

	bi.cancelCtx, bi.cancel = context.WithCancel(context.Background())

	bi.init(cfg.Context)

	return &bi, nil
}

// validate returns an error wrapping [ErrInvalidConfig] for a byte limit below
// zero, or a sizing setting that does not exist. Each is a size or a limit; a negative one would not be the default the
// zero value selects but a limit that undoes itself, as a memory limit under which
// the indexer's accounting goes negative.
func (c BulkIndexerConfig) validate() error {
	limits := []struct {
		name  string
		bytes int
	}{
		{"FlushBytes", c.FlushBytes},
		{"MaxMemoryBytes", c.MaxMemoryBytes},
		{"NodeBytes", c.NodeBytes},
		{"Sizing.MinBytes", c.Sizing.MinBytes},
		{"Sizing.InitialBytes", c.Sizing.InitialBytes},
		{"Sizing.StepBytes", c.Sizing.StepBytes},
	}
	for _, l := range limits {
		if l.bytes < 0 {
			return fmt.Errorf("%w: %s is %d, and must not be negative", ErrInvalidConfig, l.name, l.bytes)
		}
	}

	if c.Sizing.Shard >= numShardSizings {
		return fmt.Errorf("%w: Sizing.Shard is %v, which is not a ShardSizing", ErrInvalidConfig, c.Sizing.Shard)
	}
	if c.Sizing.Node >= numNodeSizings {
		return fmt.Errorf("%w: Sizing.Node is %v, which is not a NodeSizing", ErrInvalidConfig, c.Sizing.Node)
	}

	return nil
}

// errNoTransportRouter is the reason given when the client's transport offers no
// shard router at all.
var errNoTransportRouter = errors.New("the client's transport does not report where shards live")

// chooseRouter returns the router the indexer uses, and a reason when it had to
// fall back to none. A router the caller supplied is theirs and is not second
// guessed: it may be a StaticShardRouter on a client that has no router.
func chooseRouter(cfg BulkIndexerConfig) (ShardRouter, error) {
	if cfg.ShardRouter != nil {
		return cfg.ShardRouter, nil
	}

	cr := clientRouter(cfg.Client)
	if cr == nil {
		return NoShardRouter, errNoTransportRouter
	}

	if err := cr.unavailable(); err != nil {
		// A router that will never answer is dropped, not asked about every item.
		return NoShardRouter, err
	}

	return cr, nil
}

// defaultMemoryLimit is the default job memory limit: when the process has a Go
// memory limit, the greatest of a quarter of it, 4 GiB and [DefaultMaxMemoryBytes];
// when it does not, [DefaultMaxMemoryBytes].
func defaultMemoryLimit() int64 {
	if limit := debug.SetMemoryLimit(-1); limit > 0 && limit < math.MaxInt64 {
		return max(limit/memoryLimitShare, minLimitedMemoryBytes, DefaultMaxMemoryBytes)
	}

	return DefaultMaxMemoryBytes
}

// Add adds an item to the indexer.
//
// An item that has no DocumentID is given one by [BulkIndexerConfig].Fingerprinter
// when it is set. When a RetryPolicy is set and no id can be had, the item is
// not queued and fails through its OnError with [ErrMissingDocumentID]; Add
// returns nil, and that OnError runs on the caller's goroutine.
//
// After Cancel an item is not queued either: it fails through its OnError with
// [ErrIndexerCanceled], and Add returns nil. After Close, Add returns
// [ErrBulkIndexerClosed].
func (bi *bulkIndexer) Add(ctx context.Context, item BulkIndexerItem) error {
	return bi.add(ctx, item)
}

// AddZeroCopy adds an item whose document the indexer sends from the caller's
// memory; see [AddZeroCopy].
func (bi *bulkIndexer) AddZeroCopy(ctx context.Context, item BulkIndexerZeroCopyItem) error {
	if item.Body != nil {
		return bi.addFailed(ctx, ErrZeroCopyBodySet)
	}

	// Always a lentBody, even with no data, so that Release is called for the item.
	item.Body = newLentBody(item.Data, item.Release)

	return bi.add(ctx, item.BulkIndexerItem)
}

// rewindBody moves a caller's body to its start. A body the indexer was lent is made
// to start there.
func rewindBody(body io.ReadSeeker) error {
	if body == nil {
		return nil
	}
	if _, lent := body.(*lentBody); lent {
		return nil
	}

	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind body: %w", err)
	}

	return nil
}

// orderKey returns the value whose items are written in the order they were
// added: the document id, or failing that the routing value. It is empty for an
// item with neither, which has no order to keep.
func (bi *bulkIndexer) orderKey(item BulkIndexerItem) string {
	switch {
	case item.DocumentID != "":
		return item.DocumentID
	case item.Routing != nil && *item.Routing != "":
		return *item.Routing
	default:
		return bi.config.Routing
	}
}

func (bi *bulkIndexer) add(ctx context.Context, item BulkIndexerItem) error {
	if err := ctx.Err(); err != nil {
		return bi.addFailed(ctx, err)
	}

	// Every read the indexer makes of the body starts at 0: its size, the
	// fingerprint, and each attempt to send it.
	if err := rewindBody(item.Body); err != nil {
		return bi.refuse(ctx, item, err)
	}

	// Measured once, here, before any path that finishes the item, so that all of
	// them report the same size; the estimate of the item's request bytes reuses it.
	// Taken here too is the moment its latency is measured from.
	item.bodyBytes = bodyLen(item.Body)
	item.addedAt = bi.sinceEpoch()

	if bi.canceled() {
		return bi.refuse(ctx, item, ErrIndexerCanceled)
	}

	item, err := bi.ensureDocumentID(ctx, item)
	if err != nil {
		return bi.refuse(ctx, item, err)
	}

	key, node, err := bi.place(ctx, item)
	if err != nil {
		return bi.addFailed(ctx, err)
	}

	// Take the bytes before the item is queued, so a stalled cluster stops the
	// caller here and not at an ever-growing set of batches.
	size := estimateItemBytesWithBody(item, item.bodyBytes)
	held := itemHeldBytes(item, size)

	budgetKey := unplacedKey()
	if key != nil {
		budgetKey = *key
	}

	held, err = bi.budget.acquire(ctx, bi.workersDone(), budgetKey, held)
	if err != nil {
		if bi.canceled() && errors.Is(err, errBudgetStopped) {
			return bi.refuse(ctx, item, ErrIndexerCanceled)
		}

		return bi.addFailed(ctx, err)
	}

	bi.sched.Lock()
	if bi.sched.closed {
		bi.sched.Unlock()
		bi.budget.release(budgetKey, held)

		return ErrBulkIndexerClosed
	}
	bi.sched.add(item, size, held, key, node, bi.orderKey(item))
	bi.sched.Unlock()

	bi.stats.numAdded.Add(1)
	if key != nil {
		bi.stats.numGroupedItems.Add(1)
	} else {
		bi.stats.numUngroupedItems.Add(1)
	}

	return nil
}

// refuse fails an item that was taken from the caller but will not be queued:
// Add returns nil and the item's OnError, or OnUnhandledError when it has none, hears
// of err. A lent document is the caller's again, since the indexer is done with
// it. After Close the item is not taken at all, and Add reports that instead.
func (bi *bulkIndexer) refuse(ctx context.Context, item BulkIndexerItem, err error) error {
	if bi.isClosed() {
		return ErrBulkIndexerClosed
	}

	bi.rejectItem(ctx, item, err)
	releaseLent([]BulkIndexerItem{item})

	return nil
}

// isClosed reports whether Close has begun.
func (bi *bulkIndexer) isClosed() bool {
	bi.sched.Lock()
	defer bi.sched.Unlock()

	return bi.sched.closed
}

// addFailed counts and reports an Add that did not take its item.
func (bi *bulkIndexer) addFailed(ctx context.Context, err error) error {
	bi.stats.bulkAddFailCount.Add(1)
	if bi.config.OnUnhandledError != nil {
		bi.config.OnUnhandledError(ctx, err)
	}

	return err
}

// place finds the shard an item belongs to and the node that hosts it. The chain
// is nil, and the item is unplaced, when the indexer cannot say: no index, no
// routing value, or no shard map for the index.
//
// The server's own order for the routing value: a routing value on the action
// line, else the ?routing= of the request, which is only a default for actions
// that carry none, else the document ID. An action with none of them gets a
// generated ID and lands on a random shard.
func (bi *bulkIndexer) place(ctx context.Context, item BulkIndexerItem) (*shardKey, Node, error) {
	index := item.Index
	if index == "" {
		index = bi.config.Index
	}

	var routing string
	switch {
	case item.Routing != nil && *item.Routing != "":
		routing = *item.Routing
	case bi.config.Routing != "":
		routing = bi.config.Routing
	default:
		routing = item.DocumentID
	}

	if index == "" || routing == "" {
		return nil, Node{}, nil
	}

	target, ok := bi.router.LookupShard(index, routing)
	if !ok {
		// Load first: this runs for every item of an index still waiting for its
		// shard map, and a sync.Map store is a write.
		if _, marked := bi.placement.unplaced.Load(index); !marked {
			bi.placement.unplaced.Store(index, struct{}{})
		}
		// A shard map that was known and is gone is unknown again, and the next
		// answer will need the same drain. The documents of its shards, which the
		// unordered buffers this item now joins do not wait for, are dealt with first.
		if _, known := bi.placedGeneration(index); known {
			if err := bi.placementChanged(ctx, index, 0, true); err != nil {
				return nil, Node{}, err
			}
		}

		return nil, Node{}, nil
	}

	placed, known := bi.placedGeneration(index)
	switch {
	case !known:
		if err := bi.firstPlacement(ctx, index, target.Generation); err != nil {
			return nil, Node{}, err
		}
	case geometryChanged(placed, target.Generation):
		if err := bi.placementChanged(ctx, index, target.Generation, false); err != nil {
			return nil, Node{}, err
		}
	}

	return &shardKey{index: index, shard: target.Shard}, target.Node, nil
}

// firstPlacement records that the router knows index. Before that an index may
// have been unknown, and items already buffered for it belong to no shard, so
// they are in no shard's order. The first placed item for the same document would
// be sent without waiting for them, and the two could reach the server out of
// order. To keep the order of a document's actions, the switch drains what the
// indexer holds, as Flush does, before the first placed item for the index is
// queued. That is one drain per index, and only for an index that had items
// waiting for its shard map.
//
// Add must not be called from a callback, as ever, and least of all here: the
// drain waits for every request, including the one running the callback.
func (bi *bulkIndexer) firstPlacement(ctx context.Context, index string, generation uint64) error {
	bi.placement.Lock()
	defer bi.placement.Unlock()

	// Another Add may have completed the switch while this one waited.
	if _, known := bi.placement.known.Load(index); known {
		return nil
	}

	if _, waiting := bi.placement.unplaced.Load(index); waiting {
		if err := bi.flushAll(ctx); err != nil {
			return err
		}
		bi.placement.unplaced.Delete(index)
	}

	bi.placement.known.Store(index, generation)

	return nil
}

// placedGeneration returns the generation the router placed index with, and whether
// it has placed it at all.
func (bi *bulkIndexer) placedGeneration(index string) (uint64, bool) {
	v, known := bi.placement.known.Load(index)
	generation, _ := v.(uint64)

	return generation, known
}

// geometryChanged reports whether a router that placed an index at generation
// from now places it at to, which is a different shard geometry. A router that does
// not version its answers reports 0, and a change cannot be told from it.
func geometryChanged(from, to uint64) bool {
	return from != 0 && to != 0 && from != to
}

// placementChanged runs when the router's answer for an index no longer agrees with
// the one its documents were placed with: its shard geometry changed to generation,
// or, with lost set, it stopped answering at all. A document may now belong to a
// different shard than its earlier actions went to, and a shard's buffers wait
// only for each other, so a later action could reach the server before an earlier
// one. The buffers of the index's shards that have not been sent fail their items
// with [ErrShardMapChanged], and the Add that found the change waits for the
// requests already out to be answered, so that nothing it places overtakes them.
//
// Like firstPlacement it holds the placement lock for the wait, and Add must not be
// called from a callback.
func (bi *bulkIndexer) placementChanged(ctx context.Context, index string, generation uint64, lost bool) error {
	bi.placement.Lock()
	defer bi.placement.Unlock()

	// Another Add may have dealt with the change while this one waited.
	placed, known := bi.placedGeneration(index)
	if !known || (!lost && !geometryChanged(placed, generation)) {
		return nil
	}

	err := fmt.Errorf("%w: index %q", ErrShardMapChanged, index)
	for _, b := range bi.sched.abortIndex(index, err) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-bi.workersDone():
			return bi.config.Context.Err()
		case <-b.done:
		}
	}

	if lost {
		bi.placement.known.Delete(index)
	} else {
		bi.placement.known.Store(index, generation)
	}

	return nil
}

// flushAll sends everything the indexer holds and waits for it. It fails only
// when ctx does; a rejection the flush reports is not the caller's to handle.
// Items rejected by the drain reach their own OnError, and what is left had
// none and is reported the way a periodic flush reports it, through OnUnhandledError.
//
// What is held is mostly other callers' items, so it is sent on the indexer's
// context, as a periodic flush sends it, and ctx only bounds the wait: a caller
// that gives up must not fail documents whose own callers are still waiting.
func (bi *bulkIndexer) flushAll(ctx context.Context) error {
	return bi.sched.drain(flushRequested, bi.config.Context, ctx, false)
}

// workersDone returns a channel that closes when the workers stop for good
// because the construction context ended; nil, which never closes, when the
// indexer has none.
func (bi *bulkIndexer) workersDone() <-chan struct{} {
	if bi.config.Context == nil {
		return nil
	}

	return bi.config.Context.Done()
}

// itemStructBytes is the memory a buffered item takes beyond its request body: the
// BulkIndexerItem that carries it to its callbacks. For a small document it is
// several times the body, which is why the job's memory limit counts it.
var itemStructBytes = int64(reflect.TypeFor[BulkIndexerItem]().Size()) //nolint:gochecknoglobals // A constant of the type.

// lentPieceBytes is the size of the small chunk that follows a document lent to the
// indexer: room for its newline and the next action line. It is what a lent
// document costs the indexer to send beyond its length, and is counted with it
// toward MaxMemoryBytes.
const lentPieceBytes = 256

// itemHeldBytes is what an item holds of the job's memory: the estimate of its
// request body, the structure that carries it, and for a lent document the small
// chunk that follows it in the request.
func itemHeldBytes(item BulkIndexerItem, size int64) int64 {
	held := size + itemStructBytes
	if _, lent := item.Body.(*lentBody); lent {
		held += lentPieceBytes
	}

	return held
}

// Sizes of the optional action-line fields, name and punctuation included, for
// the values whose own length varies little.
const (
	sizeVersionField         = 14
	sizeVersionTypeField     = 24
	sizeIfSeqNoField         = 16
	sizeIfPrimaryTermField   = 21
	sizeRefreshField         = 20
	sizeRequireAliasField    = 21
	sizeRetryOnConflictField = 22
	sizeWaitForActiveField   = 32
)

// estimateItemBytesWithBody approximates the bytes an item takes once serialized:
// its action line, which is counted field by field, and its body, which is body
// bytes long, measured already. It is what the item holds of the in-flight byte
// limit and what counts toward the size of its batch, so FlushBytes is close to
// the size of the request body. It ignores JSON escaping, and counts a version
// only roughly, so a batch can be a few percent off its size.
func estimateItemBytesWithBody(item BulkIndexerItem, body int64) int64 {
	// {"ACTION":{ ... }}

	const framing = len(`{"":{}}`) + len("\n")

	n := framing + len(item.Action)
	fields := 0
	field := func(size int) {
		if fields > 0 {
			n++ // The comma.
		}
		n += size
		fields++
	}

	if item.Index != "" {
		field(len(`"_index":""`) + len(item.Index))
	}
	if item.DocumentID != "" {
		field(len(`"_id":""`) + len(item.DocumentID))
	}
	if item.Routing != nil {
		field(len(`"routing":""`) + len(*item.Routing))
	}

	for _, size := range []struct {
		set  bool
		size int
	}{
		{item.Version != nil && item.DocumentID != "", sizeVersionField},
		{item.VersionType != nil && item.DocumentID != "", sizeVersionTypeField},
		{item.IfSeqNum != nil, sizeIfSeqNoField},
		{item.IfPrimaryTerm != nil, sizeIfPrimaryTermField},
		{item.Refresh != nil, sizeRefreshField},
		{item.RequireAlias != nil, sizeRequireAliasField},
		{item.RetryOnConflict != nil, sizeRetryOnConflictField},
		{item.WaitForActiveShards != nil, sizeWaitForActiveField},
	} {
		if size.set {
			field(size.size)
		}
	}

	total := int64(n)
	if item.Body != nil {
		total += body + 1 // The newline after the body.
	}

	return total
}

// bodyLen returns the number of unread bytes in body, or 0 when it cannot tell.
// The readers callers usually pass answer without touching their position; any
// other reader is measured by seeking to its end and back.
func bodyLen(body io.ReadSeeker) int64 {
	switch r := body.(type) {
	case nil:
		return 0
	case interface{ Len() int }:
		return int64(r.Len())
	}

	cur, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0
	}

	end, err := body.Seek(0, io.SeekEnd)
	if err != nil {
		return 0
	}

	if _, err := body.Seek(cur, io.SeekStart); err != nil {
		return 0
	}

	return end - cur
}

// Flush drains every item added before the call, leaving the indexer open.
//
// Errors reach the caller through the return value, so unlike the periodic
// flush and Close there is nothing here for OnUnhandledError to report.
func (bi *bulkIndexer) Flush(ctx context.Context) error {
	// Reject a dead context up front rather than letting the waits below
	// choose randomly between a ready ctx.Done and a finished batch, so the
	// caller either gets a drain or an error, never a coin flip.
	if err := ctx.Err(); err != nil {
		return err
	}

	bi.sched.Lock()
	closed := bi.sched.closed
	bi.sched.Unlock()

	if closed {
		return ErrBulkIndexerClosed
	}

	return bi.sched.drain(flushRequested, ctx, ctx, true)
}

// Close stops the periodic flush, sends everything that is buffered, waits until
// it is done with every added item, which includes the requests in
// flight and the retry backoff, and stops the indexer. If ctx ends first, Close
// cancels the indexer, as Cancel does, waits for the requests to unwind, and
// returns ctx's error.
func (bi *bulkIndexer) Close(ctx context.Context) error {
	bi.ticker.Stop()

	// Stop the periodic flusher and wait for it to return before the final
	// drain below, so no auto-flush races the drain. stopFlush is non-blocking
	// and idempotent; flusherDone is already closed if the flusher exited via
	// the construction context, so this never blocks Close indefinitely.
	bi.stopFlush()
	<-bi.flusherDone

	// Close the implicitly-created client on every exit path, or the shared
	// cache refcount -- and thus the transport's goroutines and pool -- would
	// leak.
	if bi.implicitClient {
		defer func() {
			if err := bi.config.Client.Close(); err != nil && bi.config.OnUnhandledError != nil {
				bi.config.OnUnhandledError(ctx, err)
			}
		}()
	}
	// Release the cancellation context once Close is done with it.
	defer bi.cancel()

	// A context that ends while Close waits cancels the indexer, so the wait
	// below is bounded. A context that has already ended does so at once and
	// synchronously, since AfterFunc would run the cancel on its own goroutine.
	if ctx.Err() != nil {
		bi.Cancel()
	}
	stop := context.AfterFunc(ctx, bi.Cancel)
	defer stop()

	// Each batch reports its own failure to OnUnhandledError as it finishes.
	drainErr := bi.sched.drain(flushClose, ctx, ctx, false)

	bi.sched.close()
	bi.wg.Wait()
	bi.failStranded(ctx)

	if err := ctx.Err(); err != nil {
		if bi.config.OnUnhandledError != nil {
			bi.config.OnUnhandledError(ctx, err)
		}

		return err
	}

	if drainErr != nil && bi.config.OnUnhandledError != nil {
		bi.config.OnUnhandledError(ctx, drainErr)
	}

	return nil
}

// Stats returns indexer statistics.
func (bi *bulkIndexer) Stats() BulkIndexerStats {
	var stats BulkIndexerStats
	bi.loadStats(&stats)

	return stats
}

// GetStats loads the indexer statistics into dst; see [BulkIndexer].
func (bi *bulkIndexer) GetStats(dst *BulkIndexerStats) error {
	if dst == nil {
		return ErrNilStats
	}
	bi.loadStats(dst)

	return nil
}

// loadStats overwrites dst with the statistics, each loaded from an atomic.
func (bi *bulkIndexer) loadStats(dst *BulkIndexerStats) {
	blocked, blockedFor := bi.budget.blockedStats()
	inFlight := max(bi.flushingBytes.Load(), 0)

	*dst = BulkIndexerStats{
		NumAdded:         bi.stats.numAdded.Load(),
		BulkAddFailCount: bi.stats.bulkAddFailCount.Load(),
		NumFlushed:       bi.stats.numFlushed.Load(),
		NumFailed:        bi.stats.numFailed.Load(),
		NumIndexed:       bi.stats.numIndexed.Load(),
		NumCreated:       bi.stats.numCreated.Load(),
		NumUpdated:       bi.stats.numUpdated.Load(),
		NumDeleted:       bi.stats.numDeleted.Load(),
		NumRequests:      bi.stats.numRequests.Load(),

		NumGroupedItems:    bi.stats.numGroupedItems.Load(),
		NumUngroupedItems:  bi.stats.numUngroupedItems.Load(),
		NumByteFlushes:     bi.stats.numByteFlushes.Load(),
		NumTimerFlushes:    bi.stats.numTimerFlushes.Load(),
		NumPressureFlushes: bi.stats.numPressureFlushes.Load(),
		NumCloseFlushes:    bi.stats.numCloseFlushes.Load(),

		ItemBytes:               bi.stats.itemBytes.Load(),
		MaxItemBytes:            bi.stats.maxItemBytes.Load(),
		ItemLatencyNanos:        bi.stats.itemLatencyNanos.Load(),
		MaxItemLatencyNanos:     bi.stats.maxItemLatencyNanos.Load(),
		RequestDurationNanos:    bi.stats.requestDurationNanos.Load(),
		MaxRequestDurationNanos: bi.stats.maxRequestDurationNanos.Load(),
		RequestBytes:            bi.stats.requestBytes.Load(),
		InFlightRequests:        uint64(max(bi.stats.inFlightRequests.Load(), 0)),
		QueuedBuffers:           uint64(max(bi.stats.queuedBuffers.Load(), 0)),
		BlockedShards:           uint64(max(bi.stats.blockedShards.Load(), 0)),

		BufferedBytes:      uint64(max(bi.budget.inUse()-inFlight, 0)),
		InFlightBytes:      uint64(inFlight),
		NumBlockedAdds:     blocked,
		BlockedAddDuration: blockedFor,

		MaxMemoryBytes: uint64(max(bi.config.MaxMemoryBytes, 0)),
		NumRejections:  bi.stats.numRejections.Load(),
		NumNodes:       bi.stats.numNodes.Load(),
		NodeWindow:     bi.stats.nodeWindow.load(),
		BufferSize:     bi.stats.bufferSize.load(),
		Slowdown:       bi.stats.slowdown.load(),
	}
	for i := range bi.stats.numRetries {
		dst.NumRetries[i] = bi.stats.numRetries[i].Load()
	}
	for l := range bi.stats.errorCounts {
		for k := range bi.stats.errorCounts[l] {
			dst.Errors[l][k] = bi.stats.errorCounts[l][k].Load()
		}
	}
}

// flushTrigger says what made a batch be sent.
type flushTrigger uint8

const (
	// flushRequested is a batch sent because the caller asked: Flush, a cancel, or
	// the drain before an index's first placed item.
	flushRequested flushTrigger = iota
	// flushSize is a batch that reached the size chosen for its node.
	flushSize
	// flushInterval is the periodic flush every FlushInterval.
	flushInterval
	// flushPressure is a batch sent early because Add ran out of buffer space.
	flushPressure
	// flushClose is a batch sent by the final drain in Close.
	flushClose
)

// init initializes the bulk indexer.
func (bi *bulkIndexer) init(ctx context.Context) {
	bi.chunks = newChunkPool(defaultChunkSize)
	bi.pieces = newChunkPool(lentPieceBytes)
	bi.sched = newScheduler(bi)
	bi.budget = newByteBudget(int64(bi.config.MaxMemoryBytes))
	bi.budget.onBlock = bi.sched.relieve
	bi.budget.halt = bi.cancelCtx.Done()
	if bi.config.OnAddBlocked != nil {
		bi.budget.onUnblock = bi.config.OnAddBlocked
	}

	if bi.config.NumWorkers > 0 {
		bi.slots = make(chan struct{}, bi.config.NumWorkers)
	}

	wideOpen := JobNodeStats{Min: 1, Median: 1, Mean: 1, Max: 1}
	bi.stats.nodeWindow.store(wideOpen)
	initial := bi.sizing.start() / bi.sizing.max
	bi.stats.bufferSize.store(JobNodeStats{Min: initial, Median: initial, Mean: initial, Max: initial})

	bi.itemPool.New = func() any { return new([]BulkIndexerItem) }
	bi.workerPool.New = func() any { return &worker{bi: bi} }

	// Nothing is sent once the construction context has ended.
	context.AfterFunc(ctx, bi.sched.stop)

	// Cancel sends what is buffered at once, so that it fails now and not at the
	// next flush.
	//nolint:contextcheck // Cancel acts on its own context, not the construction context.
	context.AfterFunc(bi.cancelCtx, func() { bi.sched.flushCanceled(bi.cancelCtx) })

	bi.ticker = time.NewTicker(bi.config.FlushInterval)

	// The flusher stops on either the caller's construction context or Close's
	// stopFlush cancel, whichever fires first. Deriving flushCtx from ctx folds
	// both signals into one channel. Requests keep the original ctx so Close can
	// still drive its final drain flush after stopping the periodic flusher.
	flushCtx, stopFlush := context.WithCancel(ctx)
	bi.stopFlush = stopFlush
	bi.flusherDone = make(chan struct{})

	go func() {
		defer close(bi.flusherDone)
		for {
			select {
			case <-flushCtx.Done():
				return
			case <-bi.ticker.C:
				opensearchtransport.Debug().
					Dur("flush_interval", bi.config.FlushInterval).
					Msg("BulkIndexer: auto-flushing shard buffers")

				bi.sched.Lock()
				bi.sched.sealAllOpen(flushInterval, ctx)
				bi.sched.Unlock()

				bi.sched.evictIdle()
			}
		}
	}()
}

// getItems returns an empty slice to collect a buffer's items in.
func (bi *bulkIndexer) getItems() []BulkIndexerItem {
	return (*bi.itemPool.Get().(*[]BulkIndexerItem))[:0]
}

// putItems recycles a buffer's items, dropping the references they hold so a
// finished document is not kept alive by the pool.
func (bi *bulkIndexer) putItems(items []BulkIndexerItem) {
	// A slice that grew for a very large buffer is left to the collector.
	const retainLimit = 1 << 16

	if cap(items) > retainLimit {
		return
	}

	clear(items)
	items = items[:0]
	bi.itemPool.Put(&items)
}

// run sends one buffer, as a goroutine of its own: how many are out at once is
// bounded by the scheduler, one per shard and a window per node, and optionally by
// NumWorkers.
func (bi *bulkIndexer) run(b *buffer) {
	defer bi.wg.Done()

	if bi.slots != nil {
		bi.slots <- struct{}{}
		defer func() { <-bi.slots }()
	}

	w := bi.workerPool.Get().(*worker)
	w.sendBuffer(b)
	bi.workerPool.Put(w)
}

// worker sends buffers. It serializes a buffer's items into a request body, sends
// it as one bulk request, and reports the outcome to the scheduler. buf and items
// are the buffer being sent, valid from the start of send to the end of flush.
type worker struct {
	bi *bulkIndexer

	buf   *chunkBuffer
	items []BulkIndexerItem
	// retired are the buffers of earlier attempts that a transport was still
	// reading when the next attempt began; see recycleBuf.
	retired []*chunkBuffer

	// node is the node the buffer is sent to.
	node Node
	// throttled is set by flush when the node rejected the request or any item
	// with a 429, and failed when the request did not complete. Items the cluster
	// rejected for their own sake, a version conflict or a mapping error, are
	// neither: they say nothing about the node. Either is set when any attempt at
	// the buffer drew it.
	throttled, failed bool
	// served is set when any attempt had an item the node indexed, and tooLarge is
	// the size of the last request the node refused for being too large, or 0.
	served   bool
	tooLarge int64
	// sent and busy total the bytes of the requests made for the buffer, retries
	// included, and the time they took, which is what the controllers read the
	// node's rate from.
	sent int64
	busy time.Duration
}

// observeResponse records what the response to a bulk request says about the
// node, before the items are dealt with.
func (w *worker) observeResponse(blk *opensearchapi.BulkResp, err error, size int) {
	throttled := isThrottled(blk, err)
	w.throttled = w.throttled || throttled
	w.failed = w.failed || (!throttled && isTransportFailure(err))
	w.served = w.served || isServed(blk)

	if isTooLarge(blk, err) {
		w.tooLarge = int64(size)
	}
}

// sendBuffer serializes and sends one buffer, retrying what the policy asks to,
// and reports the outcome to the scheduler once it is done with every item.
func (w *worker) sendBuffer(b *buffer) {
	bi := w.bi
	ctx := b.ctx

	w.buf = newChunkBuffer(bi.chunks)
	w.buf.small = bi.pieces
	w.items = w.items[:0]
	w.node = b.dest
	w.throttled, w.failed, w.served, w.tooLarge = false, false, false, 0
	w.sent, w.busy = 0, 0
	w.retired = nil

	var aborted []error
	for _, item := range b.items {
		if bi.canceled() {
			_ = w.failUnsent(ctx, item, ErrIndexerCanceled, 0) //nolint:errcheck // a canceled item is not an OnUnhandledError matter

			continue
		}

		if b.abort != nil {
			// An item without an OnError is reported to OnUnhandledError with the buffer's.
			aborted = append(aborted, w.failUnsent(ctx, item, b.abort, 0))

			continue
		}

		if err := w.writeItem(ctx, item); err != nil {
			// Reported to the item's OnError alone, not to OnUnhandledError: the return is
			// what OnUnhandledError would get.
			_ = w.failUnsent(ctx, item, err, 0) //nolint:errcheck // see above

			continue
		}

		w.items = append(w.items, item)
	}

	err := errors.Join(append(aborted, w.flush(withBatchNode(ctx, w.node)))...)

	clear(w.items)
	w.items = w.items[:0]

	// Every attempt has ended and the callbacks have run: the chunks go back to the
	// pool, and the documents lent to the indexer are the caller's again, before the
	// memory they held is given back. With a retry policy that is the final attempt,
	// not the first. If the transport is still reading the body, as net/http can
	// after it has returned, both wait for it to finish.
	buf, lent := w.buf, lentBodies(b.items)
	bufs := append(w.retired, buf) //nolint:gocritic // The retired slice is handed on whole.
	w.buf, w.retired = nil, nil
	buf.whenIdle(buf.Reset)
	whenAllIdle(bufs, func() { releaseLentBodies(lent) })

	bi.sched.complete(b, observeSample(w.node, w.sent, w.busy, w), err)
}

// releaseLent returns the documents lent to the indexer to their owners.
func releaseLent(items []BulkIndexerItem) {
	releaseLentBodies(lentBodies(items))
}

// lentBodies returns the documents lent to the indexer among items, or nil.
func lentBodies(items []BulkIndexerItem) []*lentBody {
	var lent []*lentBody
	for i := range items {
		if l, ok := items[i].Body.(*lentBody); ok && l.release != nil {
			lent = append(lent, l)
		}
	}

	return lent
}

// releaseLentBodies calls the Release function of each.
func releaseLentBodies(lent []*lentBody) {
	for _, l := range lent {
		l.release(l.data)
	}
}

// recycleBuf empties the worker's buffer for the next attempt. A transport that is
// still reading the last attempt's body, which net/http can after it has returned,
// is left the buffer, and the worker takes a new one; the old buffer gives its
// chunks back when the transport is done with them.
func (w *worker) recycleBuf() {
	if w.buf.idle() {
		w.buf.Reset()

		return
	}

	old := w.buf
	w.buf = newChunkBuffer(w.bi.chunks)
	w.buf.small = w.bi.pieces
	w.retired = append(w.retired, old)
	old.whenIdle(old.Reset)
}

// batchNodeKey is the context key for [withBatchNode].
type batchNodeKey struct{}

// withBatchNode returns a context that carries the name of the node a batch is
// being sent to, which [FlushNode] reads.
func withBatchNode(ctx context.Context, node Node) context.Context {
	return context.WithValue(ctx, batchNodeKey{}, node.Name)
}

// FlushNode returns the name of the node the batch being flushed is sent to, for
// use in BulkIndexerConfig.OnFlushStart, where it can name the node on a trace
// span or a metric. It returns the empty string for a context that does not come
// from a flush, and for a batch with no node of its own, which the transport
// routes as it sees fit.
func FlushNode(ctx context.Context) string {
	name, _ := ctx.Value(batchNodeKey{}).(string)

	return name
}

// toNode makes the request go to the batch's node when that node is known.
func (w *worker) toNode(ctx context.Context) context.Context {
	if w.node.Conn == nil {
		return ctx
	}

	return opensearchtransport.WithWriteNode(ctx, w.node.Conn)
}

// isThrottled reports whether the node rejected a bulk request, or any item in
// it, for want of room in its write queue.
func isThrottled(blk *opensearchapi.BulkResp, err error) bool {
	// The status of the response itself: the error the client builds takes its
	// status from the body, which a refusal does not always have.
	if batchStatus(blk) == http.StatusTooManyRequests {
		return true
	}

	if se, ok := errors.AsType[*opensearch.StructError](err); ok && se.Status == http.StatusTooManyRequests {
		return true
	}

	if se, ok := errors.AsType[*opensearch.StringError](err); ok && se.Status == http.StatusTooManyRequests {
		return true
	}

	if blk == nil {
		return false
	}

	for i := range blk.Items {
		item := &blk.Items[i]

		for _, r := range []*opensearchapi.BulkRespItem{item.Index, item.Create, item.Update, item.Delete} {
			if r != nil && r.Status == http.StatusTooManyRequests {
				return true
			}
		}
	}

	return false
}

// isServed reports whether the node indexed at least one item of the response, so that
// how long the request took measures how fast it indexes.
func isServed(blk *opensearchapi.BulkResp) bool {
	if blk == nil {
		return false
	}

	for i := range blk.Items {
		item := &blk.Items[i]

		for _, r := range []*opensearchapi.BulkRespItem{item.Index, item.Create, item.Update, item.Delete} {
			if r != nil && r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices {
				return true
			}
		}
	}

	return false
}

// isTooLarge reports whether the node refused the request for its size: a 413,
// which is the node's http.max_content_length. The status of the response is read as
// well as the error, since the error takes its status from the body.
func isTooLarge(blk *opensearchapi.BulkResp, err error) bool {
	if batchStatus(blk) == http.StatusRequestEntityTooLarge {
		return true
	}

	if se, ok := errors.AsType[*opensearch.StructError](err); ok && se.Status == http.StatusRequestEntityTooLarge {
		return true
	}

	se, ok := errors.AsType[*opensearch.StringError](err)

	return ok && se.Status == http.StatusRequestEntityTooLarge
}

// isTransportFailure reports whether err, the error of the bulk call itself,
// says the request did not complete, as opposed to the cluster having rejected
// some of the items.
func isTransportFailure(err error) bool {
	if err == nil {
		return false
	}

	_, partial := errors.AsType[*opensearchapi.PartialBulkError](err) //nolint:errcheck // discarded result is the matched error

	return !partial
}

// writeItem serializes item's action line and body into the worker buffer. On any error the buffer is restored to its
// previous length so a failed item cannot leave an unpaired NDJSON action that
// would desynchronize later items from the bulk response.
func (w *worker) writeItem(ctx context.Context, item BulkIndexerItem) error {
	bufLen := w.buf.Len()

	err := w.writeMeta(item)
	if err == nil {
		err = w.writeBody(ctx, &item)
	}
	if err != nil {
		w.buf.Truncate(bufLen)
	}

	return err
}

// writeMeta formats and writes the item metadata to the buffer.
func (w *worker) writeMeta(item BulkIndexerItem) error {
	enc := w.bi.metaPool.Get().(*metaEncoder)

	line, err := enc.encode(&item)
	if err != nil {
		// An encoder that failed is not trusted with another item.
		return err
	}

	_, err = w.buf.Write(line)

	w.bi.putMetaEncoder(enc)

	return err
}

func (bi *bulkIndexer) putMetaEncoder(enc *metaEncoder) {
	if enc.buf.Cap() <= bi.metaPoolMaxBytes {
		bi.metaPool.Put(enc)
	}
}

// metaEncoder writes the action line of an item, such as {"index":{"_id":"1"}}
// and a newline. It is pooled whole, so that an item costs no allocation for it: the
// buffer it writes into, the JSON encoder bound to that buffer, and the metadata
// and the line the encoder is given are made once and reused for every item.
//
// The line is a struct with a field for each of the four actions the bulk API has,
// of which exactly one is set. Encoding a struct allocates nothing, where encoding
// the map[string]metadata that an arbitrary action name needs allocates for every
// entry, so an action with another name takes the map, with the same output.
type metaEncoder struct {
	buf  bytes.Buffer
	enc  *json.Encoder
	meta bulkActionMetadata
	line actionLine
	// entry is the one-entry map for an action that is not one of the four.
	entry map[string]*bulkActionMetadata
}

// actionLine is an action line with a field for each action the bulk API knows. Only
// the one that is set is written.
type actionLine struct {
	Index  *bulkActionMetadata `json:"index,omitempty"`
	Create *bulkActionMetadata `json:"create,omitempty"`
	Update *bulkActionMetadata `json:"update,omitempty"`
	Delete *bulkActionMetadata `json:"delete,omitempty"`
}

// newMetaEncoder makes a metaEncoder, as a sync.Pool wants it.
func newMetaEncoder() any {
	e := &metaEncoder{entry: make(map[string]*bulkActionMetadata, 1)}
	e.buf.Grow(initialMetaBytes)
	e.enc = json.NewEncoder(&e.buf)
	e.enc.SetEscapeHTML(false)

	return e
}

// initialMetaBytes is the size a metaEncoder's buffer starts at.
const initialMetaBytes = 512

// encode returns the action line for item. The line is the encoder's buffer: it is
// good until the next call, and the caller copies it. The encoder keeps no reference
// to the item once it returns.
func (e *metaEncoder) encode(item *BulkIndexerItem) ([]byte, error) {
	e.meta = bulkActionMetadata{
		Index:               item.Index,
		DocumentID:          item.DocumentID,
		Version:             item.Version,
		VersionType:         item.VersionType,
		Routing:             item.Routing,
		IfPrimaryTerm:       item.IfPrimaryTerm,
		IfSeqNum:            item.IfSeqNum,
		WaitForActiveShards: item.WaitForActiveShards,
		Refresh:             item.Refresh,
		RequireAlias:        item.RequireAlias,
		RetryOnConflict:     item.RetryOnConflict,
	}

	// Can not specify version or seq num if no document ID is passed
	if e.meta.DocumentID == "" {
		e.meta.Version = nil
		e.meta.VersionType = nil
	}

	e.buf.Reset()

	var err error
	switch item.Action {
	case "index":
		e.line.Index = &e.meta
		err = e.enc.Encode(&e.line)
	case "create":
		e.line.Create = &e.meta
		err = e.enc.Encode(&e.line)
	case "update":
		e.line.Update = &e.meta
		err = e.enc.Encode(&e.line)
	case "delete":
		e.line.Delete = &e.meta
		err = e.enc.Encode(&e.line)
	default:
		e.entry[item.Action] = &e.meta
		err = e.enc.Encode(e.entry)
		clear(e.entry)
	}

	e.line = actionLine{}
	e.meta = bulkActionMetadata{}

	return e.buf.Bytes(), err
}

// writeBody writes the item body to the buffer.
func (w *worker) writeBody(ctx context.Context, item *BulkIndexerItem) error {
	if item.Body == nil {
		return nil
	}

	if lent, ok := item.Body.(*lentBody); ok {
		w.buf.appendRef(lent.data)
		w.buf.appendByte('\n')

		return nil
	}

	// The document is the whole of Body, from offset 0, on every attempt.
	rewind := func() error {
		if _, err := item.Body.Seek(0, io.SeekStart); err != nil {
			if w.bi.config.OnUnhandledError != nil {
				w.bi.config.OnUnhandledError(ctx, err)
			}
			return err
		}

		return nil
	}

	if err := rewind(); err != nil {
		return err
	}

	if _, err := w.buf.ReadFrom(item.Body); err != nil {
		if w.bi.config.OnUnhandledError != nil {
			w.bi.config.OnUnhandledError(ctx, err)
		}
		return err
	}

	if err := rewind(); err != nil {
		return err
	}

	w.buf.appendByte('\n')
	return nil
}

// flush writes out the batch the worker holds.
func (w *worker) flush(ctx context.Context) error {
	// The flush ends with Cancel as well as with the caller's context.
	ctx, release := w.bi.joinCancel(ctx)
	defer release()

	if w.bi.config.OnFlushStart != nil {
		ctx = w.bi.config.OnFlushStart(ctx)
	}

	if w.bi.config.OnFlushEnd != nil {
		defer func() { w.bi.config.OnFlushEnd(ctx) }()
	}

	if w.buf.Len() < 1 {
		opensearchtransport.Debug().Str("node", w.node.Name).Msg("BulkIndexer: flush skipped, buffer empty")
		return nil
	}

	defer func() {
		clear(w.items)
		w.items = w.items[:0]
		w.recycleBuf()
	}()

	// Nothing is sent once the indexer is canceled: the buffered items fail.
	if w.bi.canceled() {
		return w.failCanceled(ctx)
	}

	// w.buf is passed as a Stringer so its contents are rendered only when a
	// debug logger is installed; the emit runs before the deferred Reset above,
	// so the buffer is still intact.
	opensearchtransport.Debug().Str("node", w.node.Name).Stringer("body", w.buf).Msg("BulkIndexer: flush")

	return w.sendWithRetry(ctx)
}

// send makes one bulk request carrying the buffered items and reports its
// result. attempt is 1 for the first request of a flush and counts up for each
// retry.
func (w *worker) send(ctx context.Context, attempt int) (retryBatch, error) {
	w.bi.stats.numRequests.Add(1)

	// The body is read out of the buffer by the request, so size it first.
	size := w.buf.Len()

	req := opensearchapi.BulkReq{
		Index: w.bi.config.Index,
		Body:  w.buf.lease(),
		Params: &opensearchapi.BulkParams{
			Pipeline:            w.bi.config.Pipeline,
			Refresh:             w.bi.config.Refresh,
			Routing:             w.bi.config.Routing,
			Source:              strings.Join(w.bi.config.Source, ","),
			SourceExcludes:      w.bi.config.SourceExcludes,
			SourceIncludes:      w.bi.config.SourceIncludes,
			WaitForActiveShards: w.bi.config.WaitForActiveShards,

			TimeoutParams: opensearchapi.TimeoutParams{
				Timeout: w.bi.config.Timeout,
			},
			DebugParams: opensearchapi.DebugParams{
				Pretty:     w.bi.config.Pretty,
				Human:      w.bi.config.Human,
				ErrorTrace: w.bi.config.ErrorTrace,
			},
		},
		Header: w.bi.config.Header,
	}

	start := w.bi.now()
	blk, err := w.bi.config.Client.Doc.Bulk(w.toNode(ctx), req)
	elapsed := w.bi.now().Sub(start)

	// The controllers read a rate, bytes over the time the node spent on them, so
	// the time spent waiting between attempts is left out.
	w.sent += int64(size)
	w.busy += elapsed
	w.bi.stats.requestDurationNanos.Add(uint64(max(elapsed, 0)))
	storeMax(&w.bi.stats.maxRequestDurationNanos, uint64(max(elapsed, 0)))
	w.bi.stats.requestBytes.Add(uint64(max(size, 0)))
	w.observeResponse(blk, err, size)

	if obs := w.bi.config.Observer; obs != nil {
		ev := BulkRequestEvent{
			Index:      w.bi.config.Index,
			Node:       w.node.Name,
			Attempt:    attempt,
			Items:      len(w.items),
			Bytes:      size,
			Duration:   elapsed,
			StatusCode: batchStatus(blk),
		}
		// A *PartialBulkError means the cluster answered, so the request did not fail.
		_, isPartial := errors.AsType[*opensearchapi.PartialBulkError](err) //nolint:errcheck // discarded result is the matched error
		if err != nil && !isPartial {
			ev.Err = err
		}
		obs.OnRequestEnd(ctx, ev)
	}

	return w.processBulkResp(ctx, blk, err, attempt)
}

// processBulkResp reports the result of one bulk request: the per-item results
// when the cluster answered, or a whole-buffer failure when the request did not
// land. A *PartialBulkError is the former, so each rejected document reaches its
// OnError callback; a rejected document with no OnError has its error joined
// into the return so the flush sites (a buffer's send, auto-flush, Close) surface it
// through OnUnhandledError. When every rejected document has an OnError, the return is
// nil and OnUnhandledError does not fire.
//
// With a RetryPolicy set, a failure the policy asks to retry is not reported:
// the item comes back in the returned retryBatch instead, and neither its
// callback nor the failure count sees it until a later attempt finishes it.
// attempt is the number of requests the items in w.items have been through,
// counting this one.
func (w *worker) processBulkResp(
	ctx context.Context, blk *opensearchapi.BulkResp, err error, attempt int,
) (retryBatch, error) {
	_, isPartial := errors.AsType[*opensearchapi.PartialBulkError](err) //nolint:errcheck // discarded result is the matched error
	if err != nil && !isPartial {
		wrapped := fmt.Errorf("flush: %w", err)
		status := batchStatus(blk)
		return w.handleBulkError(ctx, wrapped, classifyBatchFailure(status, err), status, attempt)
	}

	// The bulk API returns one result per serialized action. A different number
	// of results than w.items means the response cannot be paired positionally:
	// more would make the loop below read w.items[i] past the end and panic, and
	// fewer would leave the extra items without a callback, so Flush would
	// return nil with items unfinished. Even the results that line up may belong
	// to other items. Fail every serialized item instead.
	if got, want := len(blk.Items), len(w.items); got != want {
		mismatch := fmt.Errorf("flush: %w", &bulkResponseMismatchError{got: got, want: want})
		return w.handleBulkError(ctx, mismatch, KindPermanent, batchStatus(blk), attempt)
	}

	// Errors for rejected items that have no OnError callback of their own.
	// Left nil on the happy path (every item succeeds, or every rejected item
	// has an OnError), so errors.Join returns nil and no OnUnhandledError fires.
	var (
		errs  []error
		retry retryBatch

		// The latency of the items accepted, summed for the whole response.
		doneAt              = w.bi.sinceEpoch()
		latency, latencyMax time.Duration
	)
	defer func() { w.bi.recordDone(latency, latencyMax) }()

	for i, blkItem := range blk.Items {
		var (
			item BulkIndexerItem
			info opensearchapi.BulkRespItem
			op   string
		)

		item = w.items[i]
		// Each BulkItem carries exactly one non-nil operation result keyed by
		// the action that produced it. Select that action as "op" and its
		// result as "info".
		switch {
		case blkItem.Index != nil:
			op, info = actionIndex, *blkItem.Index
		case blkItem.Create != nil:
			op, info = actionCreate, *blkItem.Create
		case blkItem.Delete != nil:
			op, info = actionDelete, *blkItem.Delete
		case blkItem.Update != nil:
			op, info = actionUpdate, *blkItem.Update
		}
		if info.Error != nil || info.Status >= http.StatusMultipleChoices {
			itemErr := &BulkError{
				Layer: LayerItem, Kind: classifyItemFailure(info), Status: info.Status, Resp: info, action: op,
			}
			delay, ok := w.decideRetry(ctx, BulkErrorEvent{
				Err:        itemErr,
				Attempt:    attempt,
				Items:      1,
				DocumentID: item.DocumentID,
			})
			if ok {
				retry.add(failedItem{item: item, err: itemErr, attempt: attempt}, delay)
				continue
			}

			// A rejected item goes to its own OnError; one without falls back
			// to OnUnhandledError so the error is not swallowed. A nil result is
			// dropped by errors.Join below.
			errs = append(errs, w.failItem(ctx, item, itemErr, attempt))
		} else {
			w.bi.stats.numFlushed.Add(1)

			switch op {
			case actionIndex:
				w.bi.stats.numIndexed.Add(1)
			case actionCreate:
				w.bi.stats.numCreated.Add(1)
			case actionDelete:
				w.bi.stats.numDeleted.Add(1)
			case actionUpdate:
				w.bi.stats.numUpdated.Add(1)
			}

			l := doneAt - item.addedAt
			latency += l
			latencyMax = max(latencyMax, l)

			w.observeDone(ctx, item, attempt, info, nil)
			if item.OnSuccess != nil {
				item.OnSuccess(ctx, item, info)
			}
		}
	}

	return retry, errors.Join(errs...)
}

// handleBulkError deals with a request that failed as a whole. The policy is asked
// once, for the request: every item in it drew the same failure. When it asks
// for a retry the items come back in the retryBatch and nothing is reported;
// otherwise each item fails with err, and so does the flush. status is the HTTP
// status of the response, or 0 when none arrived.
func (w *worker) handleBulkError(
	ctx context.Context, err error, kind ErrorKind, status, attempt int,
) (retryBatch, error) {
	reqErr := &BulkError{Layer: LayerRequest, Kind: kind, Status: status, Err: err}
	delay, ok := w.decideRetry(ctx, BulkErrorEvent{
		Err:     reqErr,
		Attempt: attempt,
		Items:   len(w.items),
	})
	if ok {
		retry := retryBatch{items: make([]failedItem, 0, len(w.items)), delay: delay}
		for _, item := range w.items {
			retry.items = append(retry.items, failedItem{item: item, err: reqErr, attempt: attempt})
		}

		return retry, nil
	}

	// Each item's own OnError hears of it; the flush reports the error once, below.
	for _, item := range w.items {
		_ = w.failItem(ctx, item, reqErr, attempt) //nolint:errcheck // reqErr is returned below
	}

	return retryBatch{}, reqErr
}
