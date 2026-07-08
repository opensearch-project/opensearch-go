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
	"math/bits"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

const defaultFlushInterval = 30 * time.Second

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

// BulkIndexer represents a parallel, asynchronous, efficient indexer for OpenSearch.
type BulkIndexer interface {
	// Add adds an item to the indexer. It returns an error when the item cannot be added.
	// Use the OnSuccess and OnFailure callbacks to get the operation result for the item.
	//
	// You must call the Close() method after you're done adding items.
	//
	// It is safe for concurrent use. When it's called from goroutines,
	// they must finish before the call to Close, eg. using sync.WaitGroup.
	Add(context.Context, BulkIndexerItem) error

	// Flush drains all submitted items without closing the indexer, so the
	// indexer can be reused indefinitely.
	Flush(context.Context) error

	// Close waits until all added items are flushed and closes the indexer.
	Close(context.Context) error

	// Stats returns indexer statistics.
	Stats() BulkIndexerStats
}

// BulkIndexerConfig represents configuration of the indexer.
type BulkIndexerConfig struct {
	NumWorkers    int           // The number of workers. Defaults to runtime.NumCPU().
	FlushBytes    int           // The flush threshold in bytes. Defaults to 5MB.
	FlushInterval time.Duration // The flush threshold as duration. Defaults to 30sec.
	QueueSize     int           // Ring buffer capacity; rounded up to next power-of-two. Default: NumWorkers * 16.

	Client      *opensearchapi.Client  // The OpenSearch client.
	DebugLogger BulkIndexerDebugLogger // An optional logger for debugging.

	// Context for worker lifecycle. If nil, context.Background() will be used.
	//nolint:containedctx // Config struct is short-lived, context extracted during New()
	Context context.Context

	OnError      func(context.Context, error)          // Called for indexer errors.
	OnFlushStart func(context.Context) context.Context // Called when the flush starts.
	OnFlushEnd   func(context.Context)                 // Called when the flush ends.

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

// BulkIndexerDebugLogger defines the interface for a debugging logger.
type BulkIndexerDebugLogger interface {
	Printf(string, ...any)
}

type bulkIndexer struct {
	wg      sync.WaitGroup
	workers []*worker
	ticker  *time.Ticker
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

	// implicitClient is true when NewBulkIndexer implicitly created the client
	// (cfg.Client was nil). Close then closes it to release the shared cache
	// refcount; a caller-supplied client is left for its owner to close.
	implicitClient bool

	// Ring buffer fields.
	ring       []BulkIndexerItem
	ringMask   uint64
	claimSeq   atomic.Uint64 // next slot to claim
	publishSeq atomic.Uint64 // next slot ready to read (publish barrier)
	commitSeq  atomic.Uint64 // slots fully processed + HTTP-flushed
	commitMu   sync.Mutex
	commitCv   *sync.Cond
	closing    atomic.Bool

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
}

// nextPowerOfTwo rounds n up to the next power of two.
func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

// spinWait blocks until cond() is true, spinning briefly before yielding
// the CPU via a short sleep to avoid starving other goroutines under -race.
func spinWait(cond func() bool) {
	//nolint:mnd // 10 iterations is a tuning constant for the spin-to-sleep backoff threshold.
	for i := 0; !cond(); i++ {
		if i < 10 {
			runtime.Gosched()
		} else {
			time.Sleep(time.Millisecond)
		}
	}
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

	if cfg.NumWorkers == 0 {
		cfg.NumWorkers = runtime.NumCPU()
	}

	if cfg.FlushBytes == 0 {
		cfg.FlushBytes = 5e+6
	}

	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = defaultFlushInterval
	}

	if cfg.MetaBufferPoolMaxBytes == 0 {
		cfg.MetaBufferPoolMaxBytes = defaultMetaBufferPoolMaxBytes
	}

	//nolint:mnd // 16 is a tuning constant for the default ring buffer size multiplier.
	if cfg.QueueSize == 0 {
		cfg.QueueSize = cfg.NumWorkers * 16
	}

	ringSize := nextPowerOfTwo(cfg.QueueSize)

	bi := bulkIndexer{
		config:           cfg,
		stats:            &bulkIndexerStats{},
		metaPoolMaxBytes: cfg.MetaBufferPoolMaxBytes,
		implicitClient:   implicitClient,
		metaPool: sync.Pool{
			New: func() any {
				//nolint:mnd // 512B matches the original per-worker aux preallocation.
				return bytes.NewBuffer(make([]byte, 0, 512))
			},
		},
		ring:     make([]BulkIndexerItem, ringSize),
		ringMask: uint64(ringSize - 1), //nolint:gosec // ringSize is strictly positive
	}

	bi.commitCv = sync.NewCond(&bi.commitMu)

	bi.init(cfg.Context)

	return &bi, nil
}

// Add adds an item to the indexer.
//
// Adding an item after a call to Close() will return an error.
func (bi *bulkIndexer) Add(ctx context.Context, item BulkIndexerItem) error {
	if bi.closing.Load() {
		bi.stats.bulkAddFailCount.Add(1)
		err := errors.New("bulk indexer is closed")
		if bi.config.OnError != nil {
			bi.config.OnError(ctx, err)
		}
		return err
	}

	select {
	case <-ctx.Done():
		bi.stats.bulkAddFailCount.Add(1)
		if bi.config.OnError != nil {
			bi.config.OnError(ctx, ctx.Err())
		}
		return ctx.Err()
	default:
	}

	// Claim a slot.
	seq := bi.claimSeq.Add(1) - 1

	// Spin (with backoff) if ring is full (sequencer hasn't freed this slot yet).
	spinWait(func() bool { return seq-bi.commitSeq.Load() < uint64(len(bi.ring)) })

	bi.ring[seq&bi.ringMask] = item

	// Publish barrier: wait until all prior slots are published, then publish ours.
	spinWait(func() bool { return bi.publishSeq.Load() == seq })
	bi.publishSeq.Store(seq + 1)

	bi.stats.numAdded.Add(1)

	return nil
}

// Flush drains all submitted items without closing the indexer.
func (bi *bulkIndexer) Flush(ctx context.Context) error {
	// Snapshot: wait for everything claimed before this call.
	w := bi.claimSeq.Load()

	// Spawn a goroutine to broadcast commitCv when ctx is cancelled,
	// so the Wait() below does not deadlock if no further commits arrive.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			bi.commitCv.Broadcast()
		case <-done:
		}
	}()
	defer close(done)

	bi.commitMu.Lock()
	for bi.commitSeq.Load() < w {
		if ctx.Err() != nil {
			bi.commitMu.Unlock()
			return ctx.Err()
		}
		bi.commitCv.Wait()
	}
	bi.commitMu.Unlock()

	var firstErr error
	// Force-flush worker buffers that haven't hit FlushBytes yet.
	for _, worker := range bi.workers {
		worker.mu.Lock()
		if worker.buf.Len() == 0 {
			worker.mu.Unlock()
			continue
		}
		err := worker.flush(ctx)
		worker.mu.Unlock()

		if err != nil {
			if bi.config.OnError != nil {
				bi.config.OnError(ctx, err)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Close stops the periodic flush, sets the closing flag, flushes remaining
// items, and waits for all goroutines to finish.
func (bi *bulkIndexer) Close(ctx context.Context) error {
	bi.ticker.Stop()
	bi.closing.Store(true)

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

	_ = bi.Flush(ctx)

	select {
	case <-ctx.Done():
		if bi.config.OnError != nil {
			bi.config.OnError(ctx, ctx.Err())
		}
		return ctx.Err()
	default:
		bi.wg.Wait()
	}

	// Drain any remaining worker buffer data not yet HTTP-flushed.
	for _, w := range bi.workers {
		w.mu.Lock()
		if w.buf.Len() == 0 {
			w.mu.Unlock()
			continue
		}
		err := w.flush(ctx)
		w.mu.Unlock()

		if err != nil && bi.config.OnError != nil {
			bi.config.OnError(ctx, err)
		}
	}

	return nil
}

// Stats returns indexer statistics.
func (bi *bulkIndexer) Stats() BulkIndexerStats {
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
	}
}

// init initializes the bulk indexer.
func (bi *bulkIndexer) init(ctx context.Context) {
	for i := 1; i <= bi.config.NumWorkers; i++ {
		w := worker{
			id:  i,
			bi:  bi,
			buf: bytes.NewBuffer(make([]byte, 0, bi.config.FlushBytes)),
		}
		bi.workers = append(bi.workers, &w)
	}

	// Launch a single sequencer goroutine that drains the ring buffer
	// and dispatches items to workers.
	bi.wg.Add(1)
	//nolint:modernize // sync.WaitGroup does not have a Go() method in the standard library
	go func() {
		defer bi.wg.Done()
		var seq uint64
		workerIdx := 0
		for {
			if bi.closing.Load() && seq >= bi.claimSeq.Load() {
				return
			}

			spinWait(func() bool {
				return bi.publishSeq.Load() > seq || (bi.closing.Load() && seq >= bi.claimSeq.Load())
			})

			if bi.closing.Load() && seq >= bi.claimSeq.Load() {
				return
			}

			item := bi.ring[seq&bi.ringMask]
			w := bi.workers[workerIdx%len(bi.workers)]

			w.mu.Lock()
			w.processItem(ctx, item, bi)
			w.mu.Unlock()

			seq++
			bi.commitSeq.Store(seq)
			bi.commitMu.Lock()
			bi.commitCv.Broadcast()
			bi.commitMu.Unlock()

			workerIdx++
		}
	}()

	bi.ticker = time.NewTicker(bi.config.FlushInterval)

	// The flusher stops on either the caller's construction context or Close's
	// stopFlush cancel, whichever fires first. Deriving flushCtx from ctx folds
	// both signals into one channel. Workers keep the original ctx so Close can
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
				if bi.config.DebugLogger != nil {
					bi.config.DebugLogger.Printf("[indexer] Auto-flushing workers after %s\n", bi.config.FlushInterval)
				}

				for _, w := range bi.workers {
					w.mu.Lock()
					if w.buf.Len() == 0 {
						w.mu.Unlock()
						continue
					}
					err := w.flush(ctx)
					w.mu.Unlock()

					if err != nil && bi.config.OnError != nil {
						bi.config.OnError(ctx, err)
					}
				}
			}
		}
	}()
}

// worker represents an indexer worker.
type worker struct {
	id int
	mu sync.Mutex
	bi *bulkIndexer
	//nolint:mnd // Preallocated buffer size matches FlushBytes.
	buf   *bytes.Buffer
	items []BulkIndexerItem
}

// processItem writes meta+body for item and flushes the worker buffer if
// FlushBytes has been reached. Caller must hold w.mu.
func (w *worker) processItem(ctx context.Context, item BulkIndexerItem, bi *bulkIndexer) {
	if bi.config.DebugLogger != nil {
		bi.config.DebugLogger.Printf("[worker-%03d] Received item [%s:%s]\n", w.id, item.Action, item.DocumentID)
	}

	err := w.writeMeta(item)
	if err == nil {
		err = w.writeBody(ctx, &item)
	}

	if err != nil {
		if item.OnFailure != nil {
			item.OnFailure(ctx, item, bulkRespItemForOnFailure(opensearchapi.BulkRespItem{}), err)
		}
		bi.stats.numFailed.Add(1)

		return
	}

	w.items = append(w.items, item)
	if w.buf.Len() >= bi.config.FlushBytes {
		if err := w.flush(ctx); err != nil && bi.config.OnError != nil {
			bi.config.OnError(ctx, err)
		}
	}
}

// writeMeta formats and writes the item metadata to the buffer; it must be called under a lock.
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

// writeBody writes the item body to the buffer; it must be called under a lock.
func (w *worker) writeBody(ctx context.Context, item *BulkIndexerItem) error {
	if item.Body == nil {
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

	w.buf.WriteRune('\n')
	return nil
}

// flush writes out the worker buffer; it must be called under a lock.
func (w *worker) flush(ctx context.Context) error {
	if w.bi.config.OnFlushStart != nil {
		ctx = w.bi.config.OnFlushStart(ctx)
	}

	if w.bi.config.OnFlushEnd != nil {
		defer func() { w.bi.config.OnFlushEnd(ctx) }()
	}

	if w.buf.Len() < 1 {
		if w.bi.config.DebugLogger != nil {
			w.bi.config.DebugLogger.Printf("[worker-%03d] Flush: Buffer empty\n", w.id)
		}
		return nil
	}

	var (
		err error
		blk *opensearchapi.BulkResp
	)

	defer func() {
		w.items = w.items[:0]
		w.buf.Reset()
	}()

	if w.bi.config.DebugLogger != nil {
		w.bi.config.DebugLogger.Printf("[worker-%03d] Flush: %s\n", w.id, w.buf.String())
	}

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

	blk, err = w.bi.config.Client.Doc.Bulk(ctx, req)
	// Treat opensearchapi.PartialBulkError as success-with-failed-items:
	// the indexer's whole job is per-item dispatch, so the per-item loop
	// below already handles `info.Error != nil`. A real flush failure
	// (transport error, HTTP error, JSON parse error) flows through
	// handleBulkError as before.
	var partial *opensearchapi.PartialBulkError
	if err != nil {
		if !errors.As(err, &partial) {
			return w.handleBulkError(ctx, fmt.Errorf("flush: %w", err))
		}
		err = nil
	}

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
			if item.OnFailure != nil {
				item.OnFailure(ctx, item, bulkRespItemForOnFailure(info), nil)
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

	return err
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
