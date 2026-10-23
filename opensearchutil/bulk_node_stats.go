// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"math"
	"slices"
	"sync/atomic"
)

// BulkTargetStats is the bulk client's reading of one node it writes to. It is what
// [BulkIndexerStats.TargetWindow], [BulkIndexerStats.BufferSize] and
// [BulkIndexerStats.Slowdown] summarize across the nodes, with the node's own
// load beside it, so a caller can name the node an outlier is.
type BulkTargetStats struct {
	// Name is the node's name. It is empty for the one node that stands for the
	// cluster when the bulk client has no shard router.
	Name string

	// InFlightRequests is the requests out to the node (GAUGE, requests), and
	// InFlightBytes their size (GAUGE, bytes).
	InFlightRequests uint64
	InFlightBytes    uint64
	// QueuedBuffers is the node's sealed buffers not yet sent (GAUGE, buffers).
	QueuedBuffers uint64
	// HeldBytes is everything the bulk client holds for the node: open buffers, queued
	// buffers and requests out (GAUGE, bytes).
	HeldBytes uint64
	// NumRejections counts the node's requests answered with a 429 (COUNTER,
	// requests).
	NumRejections uint64

	// Window is the node's byte window as a fraction of its ceiling, where 1 is
	// wide open; BufferSize is the size of the node's shard buffers as a fraction
	// of the most they get; Slowdown is how much longer than expected the node
	// takes for its requests, where 0 is as expected (GAUGE, ratio each).
	Window     float64
	BufferSize float64
	Slowdown   float64
}

// nodeAtomicStats holds a node's statistics as atomics: a copy of each of the
// node's fields that GetTargetStats reports and the scheduler's lock guards, and
// the count of the node's rejections. A copy is stored with the lock held, where
// the scheduler changes the field, and loaded without it.
type nodeAtomicStats struct {
	inflight, queued, flightBytes atomic.Int64
	rejections                    atomic.Uint64
	window, bufferSize, slowdown  atomic.Uint64
}

// storeStats copies the node's fields into its atomic statistics. It must be
// called with the scheduler's lock held, after any of the fields changes.
func (ns *nodeState) storeStats() {
	ns.stats.inflight.Store(int64(ns.inflight))
	ns.stats.queued.Store(int64(ns.queued))
	ns.stats.flightBytes.Store(ns.flightBytes)
	ns.stats.window.Store(math.Float64bits(ns.windowBytes.SizeRatio()))
	ns.stats.bufferSize.Store(math.Float64bits(ns.bufferFrac))
	ns.stats.slowdown.Store(math.Float64bits(ns.slowdown))
}

// loadStats returns the node's statistics, each loaded from an atomic.
func (ns *nodeState) loadStats() BulkTargetStats {
	return BulkTargetStats{
		Name:             ns.name,
		InFlightRequests: uint64(max(ns.stats.inflight.Load(), 0)),
		InFlightBytes:    uint64(max(ns.stats.flightBytes.Load(), 0)),
		QueuedBuffers:    uint64(max(ns.stats.queued.Load(), 0)),
		HeldBytes:        uint64(max(ns.held.Load(), 0)),
		NumRejections:    ns.stats.rejections.Load(),
		Window:           math.Float64frombits(ns.stats.window.Load()),
		BufferSize:       math.Float64frombits(ns.stats.bufferSize.Load()),
		Slowdown:         math.Float64frombits(ns.stats.slowdown.Load()),
	}
}

// addNode adds a node the scheduler has just started using to the list
// GetTargetStats reads. The list is replaced, never changed, so a reader holds a
// list no writer touches. It must be called with the scheduler's lock held.
func (s *scheduler) addNode(ns *nodeState) {
	var list []*nodeState
	if old := s.nodeList.Load(); old != nil {
		list = *old
	}
	list = append(slices.Clip(list), ns)
	s.nodeList.Store(&list)
}

// GetTargetStats loads the statistics of each node into dst; see [BulkIndexer].
func (bi *bulkClient) GetTargetStats(dst []BulkTargetStats) []BulkTargetStats {
	dst = dst[:0]
	list := bi.sched.nodeList.Load()
	if list == nil {
		return dst
	}
	for _, ns := range *list {
		dst = append(dst, ns.loadStats())
	}

	return dst
}
