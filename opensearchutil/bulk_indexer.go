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
// were added.
type BulkIndexer interface {
	// Add adds an item to the indexer. It returns an error when the item cannot be added.
	// Use the OnSuccess and OnFailure callbacks to get the operation result for the item.
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
	// A non-nil error means the drain itself failed; documents the cluster
	// rejected individually go to their OnFailure callback. Safe alongside Add
	// and other concurrent Flush calls. After Close it returns ErrBulkIndexerClosed.
	Flush(context.Context) error

	// Close waits until all added items are flushed and closes the indexer.
	Close(context.Context) error

	// Stats returns indexer statistics.
	Stats() BulkIndexerStats
}

// BulkIndexerConfig represents configuration of the indexer.
type BulkIndexerConfig struct {
	// NumWorkers caps the number of bulk requests the indexer may have outstanding
	// at once, across all nodes. The default, 0, sets no cap beyond the ones that
	// are always there: a shard has one request out at a time, and a node has as many
	// as the window the transport allows its write pool, which follows the pool's
	// own statistics. A cap of 1 sends one request at a time, and runs callbacks on
	// one goroutine at a time.
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

	OnError      func(context.Context, error)          // Called for indexer errors.
	OnFlushStart func(context.Context) context.Context // Called when the flush starts.
	OnFlushEnd   func(context.Context)                 // Called when the flush ends.

	// OnWarning is called, at most once for each condition and before
	// NewBulkIndexer returns, when the indexer cannot do something it would do by
	// default and carries on without it. Today that is shard-aware buffering with
	// a transport that cannot say where shards live; the error wraps
	// [ErrShardRoutingUnavailable] and names the cause. It is not an error that
	// stops anything, and OnError is not called for it.
	OnWarning func(context.Context, error)

	// OnAddBlocked is called each time an Add had to wait for buffer space, with
	// how long it waited, once the wait is over, whether Add went on to succeed
	// or gave up. It runs on the goroutine that called Add. It is where a
	// caller records the distribution of waits, in a histogram for instance;
	// BulkIndexerStats carries only their count and total.
	OnAddBlocked func(context.Context, time.Duration)

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

	// Sizing tunes how the size of a shard's buffer follows how quickly the shard's
	// requests are served, and, for the same controller, how the node limit follows
	// the node. The byte fields (MinBytes, InitialBytes, StepBytes) apply to shard
	// buffers; a node's limit takes its own from MaxNodeBytes. The zero value selects
	// defaults derived from FlushBytes.
	Sizing BulkSizing

	// MaxMemoryBytes is the job's memory limit: the most the indexer holds from the
	// moment Add accepts an item until the request that sends it has finished. That
	// is the item's request body and the structure that carries it to its callbacks,
	// so a stream of small documents is not undercounted. When the limit is spent,
	// Add waits for requests to finish and give memory back, and the fullest shard
	// buffer is sent at once so that the wait is short, so a slow or stalled cluster
	// slows the caller down and does not grow the indexer's memory. An item larger
	// than the whole limit is let through alone.
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

	// NodeBytes is what a node starts with of the job's memory to buffer documents
	// bound for it, in open shard buffers, and the least it is ever left with once
	// others are using theirs. It is a starting point and a floor, and not a limit:
	// the indexer gives a node that is taking everything it is sent more, up to what
	// the other nodes leave of MaxMemoryBytes, so an indexer that fixes on a few
	// nodes uses the memory for them, and takes it back as more nodes are used. A
	// node that rejects a request, slows down, or is given more than it takes is
	// allowed less. When the open buffers for a node outgrow what it is allowed, its
	// fullest is sent; an item that crosses the line is still accepted. Defaults to
	// MaxMemoryBytes divided among the nodes the router knows, and at least
	// FlushBytes.
	NodeBytes int
}

// BulkIndexerStats represents the indexer statistics.
type BulkIndexerStats struct {
	NumAdded         uint64
	BulkAddFailCount uint64 // Items rejected by Add() because the caller's context was cancelled before the item could be enqueued.
	NumFlushed       uint64
	NumFailed        uint64
	NumIndexed       uint64
	NumCreated       uint64
	NumUpdated       uint64
	NumDeleted       uint64
	NumRequests      uint64

	// NumGroupedItems counts items placed in a shard, and so in that shard's
	// order, and NumUngroupedItems those that belong to no shard: the shard map of
	// the index was unknown, or the item had neither a routing value nor a
	// DocumentID. Without a shard router every item is ungrouped. A high ungrouped
	// share is efficiency lost, not data lost.
	NumGroupedItems   uint64
	NumUngroupedItems uint64

	// NumByteFlushes counts batches sent because they reached the size chosen for
	// their node, and NumTimerFlushes those sent by the flush interval. Batches
	// sent because Flush or Close asked, or because Add ran out of buffer space,
	// are in neither. See TimerFlushRatio.
	NumByteFlushes  uint64
	NumTimerFlushes uint64

	// BufferedBytes is the number of bytes counted against MaxMemoryBytes that
	// are not in a bulk request yet: waiting in a shard buffer. InFlightBytes is the
	// number in requests being sent.
	BufferedBytes uint64
	InFlightBytes uint64
	// NumBlockedAdds counts Add calls that had to wait for buffer space.
	NumBlockedAdds uint64
	// BlockedAddDuration is the total time Add calls spent waiting for buffer
	// space. A value that grows while the cluster is healthy says MaxMemoryBytes
	// is too small for the workload; one that grows while requests stall says the
	// limit is doing its job.
	BlockedAddDuration time.Duration
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
	Body                io.ReadSeeker
	RetryOnConflict     *int

	OnSuccess func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem)        // Per item
	OnFailure func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem, error) // Per item
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
	// after the indexer is finished with Data: the request carrying it has ended and
	// the callbacks have returned, whatever the outcome. It is not called for an item
	// that was not accepted, which the caller still owns. It runs on the goroutine
	// that sent the request and must not block or call Add.
	Release func(data []byte)
}

// lentBody is the Body of an item whose document the indexer was lent. It reads as
// the document, so callbacks given the item can read it, and it tells the
// serializer to take the slice and not copy it.
type lentBody struct {
	data    []byte
	off     int
	release func([]byte)
}

func newLentBody(data []byte, release func([]byte)) *lentBody {
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
	// sizing sizes a shard's buffers, and nodeSizing a node's limit.
	sizing, nodeSizing resolvedSizing
	// slots, when the config caps NumWorkers, holds a token for each request that
	// is out.
	slots chan struct{}
	// itemPool recycles the slices that carry a buffer's items to the request that
	// sends them, and workerPool the state a request is sent with.
	itemPool, workerPool sync.Pool
	workerSeq            atomic.Int64

	ticker *time.Ticker
	// stopFlush cancels the flusher goroutine; flusherDone is closed when that
	// goroutine returns. Close cancels via stopFlush (non-blocking and
	// idempotent, so Close never deadlocks even when the flusher already
	// returned via the construction context) and then waits on flusherDone, so
	// the periodic flush has fully stopped before Close runs its final drain and
	// the deferred implicit-client Close.
	stopFlush   context.CancelFunc
	flusherDone chan struct{}
	stats       *bulkIndexerStats

	metaPool         sync.Pool
	metaPoolMaxBytes int

	// placement tracks which indexes the router can place documents in. Reads are
	// lock-free; the mutex serializes the one-time switch of an index from
	// unplaced to placed, see firstPlacement.
	placement struct {
		sync.Mutex
		// known holds the indexes whose shard map the router has answered for
		// (string -> struct{}).
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
}

// NewBulkIndexer creates a new bulk indexer.
func NewBulkIndexer(cfg BulkIndexerConfig) (BulkIndexer, error) {
	implicitClient := false
	if cfg.Client == nil {
		var err error
		cfg.Client, err = opensearchapi.NewDefaultClient()
		if err != nil {
			return nil, err
		}
		implicitClient = true
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

	if cfg.NodeBytes == 0 {
		cfg.NodeBytes = defaultNodeBytes(cfg, router)
	}

	// A node's limit is steered by the same controller as a shard's buffer, over
	// the node's requests as a whole. It starts at the node's even share, never goes
	// below one full shard buffer, and is bounded above by the job's memory.
	nodeSizing := BulkSizing{
		Fixed:        cfg.Sizing.Fixed,
		Headroom:     cfg.Sizing.Headroom,
		Backoff:      cfg.Sizing.Backoff,
		Kp:           cfg.Sizing.Kp,
		Ki:           cfg.Sizing.Ki,
		Kd:           cfg.Sizing.Kd,
		MinBytes:     cfg.FlushBytes,
		InitialBytes: min(cfg.NodeBytes, cfg.MaxMemoryBytes),
	}

	nodeResolved := nodeSizing.resolve(cfg.MaxMemoryBytes)
	if cfg.Sizing.Fixed {
		// A fixed node limit is the even share, not the whole job.
		share := float64(min(cfg.NodeBytes, cfg.MaxMemoryBytes))
		nodeResolved.min, nodeResolved.initial, nodeResolved.max = share, share, share
	}
	// Any request that fills a quarter of a shard buffer says as much about a node
	// as it can.
	nodeResolved.sampleFloor = float64(cfg.FlushBytes) * sizingMinSampleFraction

	bi := bulkIndexer{
		config:           cfg,
		stats:            &bulkIndexerStats{},
		router:           router,
		now:              time.Now,
		sizing:           cfg.Sizing.resolve(cfg.FlushBytes),
		nodeSizing:       nodeResolved,
		metaPoolMaxBytes: cfg.MetaBufferPoolMaxBytes,
		implicitClient:   implicitClient,
		metaPool: sync.Pool{
			New: func() any {
				//nolint:mnd // 512B matches the original per-worker aux preallocation.
				return bytes.NewBuffer(make([]byte, 0, 512))
			},
		},
	}

	if warning != nil {
		err := fmt.Errorf("%w: %w", ErrShardRoutingUnavailable, warning)
		opensearchtransport.Debug().Err(err).Msg("BulkIndexer: starting without shard-aware buffering")
		if cfg.OnWarning != nil {
			cfg.OnWarning(cfg.Context, err)
		}
	}

	bi.init(cfg.Context)

	return &bi, nil
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

// defaultNodeBytes divides the job's memory among the nodes the router knows, so
// that one node cannot hold all of it. It is never less than a full shard buffer.
func defaultNodeBytes(cfg BulkIndexerConfig, router ShardRouter) int {
	nodes := max(len(router.WriteNodes()), 1)

	return max(cfg.MaxMemoryBytes/nodes, cfg.FlushBytes)
}

// Add adds an item to the indexer.
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

func (bi *bulkIndexer) add(ctx context.Context, item BulkIndexerItem) error {
	if err := ctx.Err(); err != nil {
		return bi.addFailed(ctx, err)
	}

	key, node, err := bi.place(ctx, item)
	if err != nil {
		return bi.addFailed(ctx, err)
	}

	// Take the bytes before the item is queued, so a stalled cluster stops the
	// caller here and not at an ever-growing set of batches.
	size := estimateItemBytes(item)
	held := itemHeldBytes(item, size)

	held, err = bi.budget.acquire(ctx, bi.workersDone(), held)
	if err != nil {
		return bi.addFailed(ctx, err)
	}

	bi.sched.Lock()
	if bi.sched.closed {
		bi.sched.Unlock()
		bi.budget.release(held)

		return ErrBulkIndexerClosed
	}
	bi.sched.add(item, size, held, key, node)
	bi.sched.Unlock()

	bi.stats.numAdded.Add(1)
	if key != nil {
		bi.stats.numGroupedItems.Add(1)
	} else {
		bi.stats.numUngroupedItems.Add(1)
	}

	return nil
}

// addFailed counts and reports an Add that did not take its item.
func (bi *bulkIndexer) addFailed(ctx context.Context, err error) error {
	bi.stats.bulkAddFailCount.Add(1)
	if bi.config.OnError != nil {
		bi.config.OnError(ctx, err)
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
		// answer will need the same drain.
		bi.placement.known.Delete(index)

		return nil, Node{}, nil
	}

	if _, known := bi.placement.known.Load(index); !known {
		if err := bi.firstPlacement(ctx, index); err != nil {
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
func (bi *bulkIndexer) firstPlacement(ctx context.Context, index string) error {
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

	bi.placement.known.Store(index, struct{}{})

	return nil
}

// flushAll sends everything the indexer holds and waits for it. It fails only
// when ctx does; a rejection the flush reports is not the caller's to handle.
// Items rejected by the drain reach their own OnFailure, and what is left had
// none and is reported the way a periodic flush reports it, through OnError.
func (bi *bulkIndexer) flushAll(ctx context.Context) error {
	return bi.sched.drain(ctx, false)
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

// estimateItemBytes approximates the bytes an item takes once serialized: its
// action line, which is counted field by field, and its body. It is what the item
// holds of the in-flight byte limit and what counts toward the size of its batch,
// so FlushBytes is close to the size of the request body. It ignores JSON
// escaping, and counts a version only roughly, so a batch can be a few percent
// off its size.
func estimateItemBytes(item BulkIndexerItem) int64 {
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
		total += bodyLen(item.Body) + 1 // The newline after the body.
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
// flush and Close there is nothing here for OnError to report.
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

	return bi.sched.drain(ctx, true)
}

// Close stops the periodic flush, sends everything that is buffered, and stops
// the workers.
func (bi *bulkIndexer) Close(ctx context.Context) error {
	bi.ticker.Stop()

	// Stop the periodic flusher and wait for it to return before the final
	// drain below, so no auto-flush races the drain. stopFlush is non-blocking
	// and idempotent; flusherDone is already closed if the flusher exited via
	// the construction context, so this never blocks Close indefinitely.
	bi.stopFlush()
	<-bi.flusherDone

	// Close the implicitly-created client on every exit path (including the
	// ctx-cancelled early return below), or the shared cache refcount -- and
	// thus the transport's goroutines and pool -- would leak.
	if bi.implicitClient {
		defer func() {
			if err := bi.config.Client.Close(); err != nil && bi.config.OnError != nil {
				bi.config.OnError(ctx, err)
			}
		}()
	}

	defer func() {
		bi.sched.close()
		bi.wg.Wait()
	}()

	if err := ctx.Err(); err != nil {
		if bi.config.OnError != nil {
			bi.config.OnError(ctx, err)
		}

		return err
	}

	// Each batch reports its own failure to OnError as it finishes.
	if err := bi.sched.drain(ctx, false); err != nil && bi.config.OnError != nil {
		bi.config.OnError(ctx, err)
	}

	return nil
}

// Stats returns indexer statistics.
func (bi *bulkIndexer) Stats() BulkIndexerStats {
	blocked, blockedFor := bi.budget.blockedStats()
	inFlight := max(bi.flushingBytes.Load(), 0)

	return BulkIndexerStats{
		NumAdded:         bi.stats.numAdded.Load(),
		BulkAddFailCount: bi.stats.bulkAddFailCount.Load(),
		NumFlushed:       bi.stats.numFlushed.Load(),
		NumFailed:        bi.stats.numFailed.Load(),
		NumIndexed:       bi.stats.numIndexed.Load(),
		NumCreated:       bi.stats.numCreated.Load(),
		NumUpdated:       bi.stats.numUpdated.Load(),
		NumDeleted:       bi.stats.numDeleted.Load(),
		NumRequests:      bi.stats.numRequests.Load(),

		NumGroupedItems:   bi.stats.numGroupedItems.Load(),
		NumUngroupedItems: bi.stats.numUngroupedItems.Load(),
		NumByteFlushes:    bi.stats.numByteFlushes.Load(),
		NumTimerFlushes:   bi.stats.numTimerFlushes.Load(),

		BufferedBytes:      uint64(max(bi.budget.inUse()-inFlight, 0)),
		InFlightBytes:      uint64(inFlight),
		NumBlockedAdds:     blocked,
		BlockedAddDuration: blockedFor,
	}
}

// flushTrigger says what made a batch be sent.
type flushTrigger uint8

const (
	// flushRequested is a batch sent because the caller asked: Flush, or Close.
	flushRequested flushTrigger = iota
	// flushSize is a batch that reached the size chosen for its node.
	flushSize
	// flushInterval is the periodic flush every FlushInterval.
	flushInterval
	// flushPressure is a batch sent early because Add ran out of buffer space.
	flushPressure
)

// init initializes the bulk indexer.
func (bi *bulkIndexer) init(ctx context.Context) {
	bi.chunks = newChunkPool(defaultChunkSize)
	bi.pieces = newChunkPool(lentPieceBytes)
	bi.sched = newScheduler(bi)
	bi.budget = newByteBudget(int64(bi.config.MaxMemoryBytes))
	bi.budget.onBlock = bi.sched.relieve
	if bi.config.OnAddBlocked != nil {
		bi.budget.onUnblock = bi.config.OnAddBlocked
	}

	if bi.config.NumWorkers > 0 {
		bi.slots = make(chan struct{}, bi.config.NumWorkers)
	}

	bi.itemPool.New = func() any { return new([]BulkIndexerItem) }
	bi.workerPool.New = func() any { return &worker{id: int(bi.workerSeq.Add(1)), bi: bi} }

	// Nothing is sent once the construction context has ended.
	context.AfterFunc(ctx, bi.sched.stop)

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
	w.send(b)
	bi.workerPool.Put(w)
}

// worker sends buffers. It serializes a buffer's items into a request body, sends
// it as one bulk request, and reports the outcome to the scheduler. buf and items
// are the buffer being sent, valid from the start of send to the end of flush.
type worker struct {
	id int
	bi *bulkIndexer

	buf   *chunkBuffer
	items []BulkIndexerItem

	// node is the node the buffer is sent to.
	node Node
	// throttled is set by flush when the node rejected the request or any item
	// with a 429, and failed when the request did not complete. Items the cluster
	// rejected for their own sake, a version conflict or a mapping error, are
	// neither: they say nothing about the node.
	throttled, failed bool
}

// observeResponse records what the response to a bulk request says about the
// node, before the items are dealt with.
func (w *worker) observeResponse(blk *opensearchapi.BulkResp, err error) {
	w.throttled = isThrottled(blk, err)
	w.failed = !w.throttled && isTransportFailure(err)
}

// send serializes and sends one buffer.
func (w *worker) send(b *buffer) {
	bi := w.bi
	ctx := b.ctx

	w.buf = newChunkBuffer(bi.chunks)
	w.buf.small = bi.pieces
	w.items = w.items[:0]
	w.node = b.dest
	w.throttled, w.failed = false, false

	for _, item := range b.items {
		if err := w.writeItem(ctx, item); err != nil {
			if item.OnFailure != nil {
				item.OnFailure(ctx, item, bulkRespItemForOnFailure(opensearchapi.BulkRespItem{}), err)
			}

			bi.stats.numFailed.Add(1)

			continue
		}

		w.items = append(w.items, item)
	}

	size := int64(w.buf.Len())
	start := bi.now()
	err := w.flush(withBatchNode(ctx, w.node))
	elapsed := bi.now().Sub(start)

	// flush resets the buffer; this hands its chunks back if it returned early.
	w.buf.Reset()
	w.buf = nil
	clear(w.items)
	w.items = w.items[:0]

	// The request has ended and the callbacks have run: the documents lent to the
	// indexer are the caller's again, before the memory they held is given back.
	releaseLent(b.items)

	bi.sched.complete(b, observeSample(w.node, size, elapsed, w.throttled, w.failed), err)
}

// releaseLent returns the documents lent to the indexer to their owners.
func releaseLent(items []BulkIndexerItem) {
	for i := range items {
		if lent, ok := items[i].Body.(*lentBody); ok && lent.release != nil {
			lent.release(lent.data)
		}
	}
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
	var err error

	meta := bulkActionMetadata{
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
	if meta.DocumentID == "" {
		meta.Version = nil
		meta.VersionType = nil
	}

	buf := w.bi.metaPool.Get().(*bytes.Buffer)
	buf.Reset()

	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)

	err = enc.Encode(map[string]bulkActionMetadata{
		item.Action: meta,
	})
	if err != nil {
		w.bi.putMetaBuffer(buf)
		return err
	}

	_, err = w.buf.Write(buf.Bytes())

	w.bi.putMetaBuffer(buf)

	return err
}

func (bi *bulkIndexer) putMetaBuffer(buf *bytes.Buffer) {
	if buf.Cap() <= bi.metaPoolMaxBytes {
		bi.metaPool.Put(buf)
	}
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

	if _, err := w.buf.ReadFrom(item.Body); err != nil {
		if w.bi.config.OnError != nil {
			w.bi.config.OnError(ctx, err)
		}
		return err
	}

	if _, err := item.Body.Seek(0, io.SeekStart); err != nil {
		if w.bi.config.OnError != nil {
			w.bi.config.OnError(ctx, err)
		}
		return err
	}

	w.buf.appendByte('\n')
	return nil
}

// flush writes out the batch the worker holds.
func (w *worker) flush(ctx context.Context) error {
	if w.bi.config.OnFlushStart != nil {
		ctx = w.bi.config.OnFlushStart(ctx)
	}

	if w.bi.config.OnFlushEnd != nil {
		defer func() { w.bi.config.OnFlushEnd(ctx) }()
	}

	if w.buf.Len() < 1 {
		opensearchtransport.Debug().Int("worker", w.id).Msg("BulkIndexer: flush skipped, buffer empty")
		return nil
	}

	defer func() {
		clear(w.items)
		w.items = w.items[:0]
		w.buf.Reset()
	}()

	// w.buf is passed as a Stringer so its contents are rendered only when a
	// debug logger is installed; the emit runs before the deferred Reset above,
	// so the buffer is still intact.
	opensearchtransport.Debug().Int("worker", w.id).Stringer("body", w.buf).Msg("BulkIndexer: flush")

	w.bi.stats.numRequests.Add(1)
	req := opensearchapi.BulkReq{
		Index: w.bi.config.Index,
		Body:  w.buf,
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

	blk, err := w.bi.config.Client.Doc.Bulk(w.toNode(ctx), req)
	w.observeResponse(blk, err)

	return w.processBulkResp(ctx, blk, err)
}

// processBulkResp reports the result of one bulk request: the per-item results
// when the cluster answered, or a whole-buffer failure when the request did not
// land. A *PartialBulkError is the former, so each rejected document reaches its
// OnFailure callback; a rejected document with no OnFailure has its error joined
// into the return so the flush sites (worker.run, auto-flush, Close) surface it
// through OnError. When every rejected document has an OnFailure, the return is
// nil and OnError does not fire.
func (w *worker) processBulkResp(ctx context.Context, blk *opensearchapi.BulkResp, err error) error {
	_, isPartial := errors.AsType[*opensearchapi.PartialBulkError](err) //nolint:errcheck // discarded result is the matched error
	if err != nil && !isPartial {
		return w.handleBulkError(ctx, fmt.Errorf("flush: %w", err))
	}

	// The bulk API returns one result per serialized action. More results than
	// w.items means the response cannot be paired positionally: the loop below
	// would read w.items[i] past the end and panic, and even the results that
	// line up may belong to other items. Fail every serialized item instead.
	// Fewer results needs no guard; the loop simply leaves the extras
	// undispatched.
	if got, want := len(blk.Items), len(w.items); got > want {
		return w.handleBulkError(ctx, fmt.Errorf(
			"flush: bulk response has %d items, indexer serialized %d", got, want,
		))
	}

	// Errors for rejected items that have no OnFailure callback of their own.
	// Left nil on the happy path (every item succeeds, or every rejected item
	// has an OnFailure), so errors.Join returns nil and no OnError fires.
	var errs []error
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
			w.bi.stats.numFailed.Add(1)
			// A rejected item goes to its own OnFailure; one without falls back
			// to OnError so the failure is not swallowed.
			if item.OnFailure != nil {
				item.OnFailure(ctx, item, bulkRespItemForOnFailure(info), nil)
			} else {
				errs = append(errs, bulkItemFailure(op, info))
			}
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

			if item.OnSuccess != nil {
				item.OnSuccess(ctx, item, info)
			}
		}
	}

	return errors.Join(errs...)
}

func (w *worker) handleBulkError(ctx context.Context, err error) error {
	w.bi.stats.numFailed.Add(uint64(len(w.items)))

	// info (the response item) will be empty since the bulk request failed
	info := bulkRespItemForOnFailure(opensearchapi.BulkRespItem{})
	for i := range w.items {
		if item := w.items[i]; item.OnFailure != nil {
			item.OnFailure(ctx, item, info, err)
		}
	}

	return err
}

// bulkRespItemForOnFailure ensures BulkRespItem.Error is non-nil so OnFailure
// callbacks can safely read Error.Type and Error.Reason without nil checks.
func bulkRespItemForOnFailure(item opensearchapi.BulkRespItem) opensearchapi.BulkRespItem {
	if item.Error == nil {
		item.Error = &opensearchapi.ErrorCause{}
	}
	return item
}

// bulkItemFailure builds the error handed to OnError for a rejected item that
// has no OnFailure callback of its own. A status-only rejection (e.g. a 404
// delete) carries no error object, so fall back to the status.
func bulkItemFailure(op string, info opensearchapi.BulkRespItem) error {
	var id string
	if info.ID != nil {
		id = *info.ID
	}
	if info.Error != nil {
		var reason string
		if info.Error.Reason != nil {
			reason = *info.Error.Reason
		}
		return fmt.Errorf("%q %q rejected: %q: %q", op, id, info.Error.Type, reason)
	}
	return fmt.Errorf("%q %q rejected: status %d", op, id, info.Status)
}
