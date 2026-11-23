// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// nodeStatsPath is the node statistics the stats poller reads from each node.
// indexing_pressure is in the node stats API from OpenSearch 1.0.0.
const nodeStatsPath = "/_nodes/_local/stats/jvm,breaker,thread_pool,indexing_pressure"

// IndexingPressureStats is the indexing_pressure section of a node's
// statistics. Only the cumulative counters the router reads are decoded.
type IndexingPressureStats struct {
	Memory IndexingPressureMemory `json:"memory"`
}

// IndexingPressureMemory holds the indexing pressure byte counters.
type IndexingPressureMemory struct {
	Total IndexingPressureTotal `json:"total"`
}

// IndexingPressureTotal holds the cumulative indexing pressure counters since
// the node started.
type IndexingPressureTotal struct {
	// CoordinatingInBytes counts the bytes of every write this node received
	// from a client and coordinated, whatever node holds the shards.
	CoordinatingInBytes int64 `json:"coordinating_in_bytes"`
}

// coordLoad tracks how much write coordination a node is doing, from the
// indexing_pressure counters the stats poller reads. Coordinating a bulk
// request (parsing it, splitting it by shard and gathering the replies) runs
// on the thread that received it, outside the node's write thread pool, so the
// write pool's congestion window does not see it.
type coordLoad struct {
	// rate is the node's coordinating bytes per second per allocated core
	// over the last poll interval, as math.Float64bits; 0 until two polls.
	rate atomic.Uint64
	// measured is true once rate holds a measurement.
	measured atomic.Bool

	mu struct {
		sync.Mutex
		prevBytes int64
		prevAt    time.Time
	}
}

// update records a poll of the node's cumulative coordinating byte count at
// time at. procs is the node's allocated core count, 0 when unknown. A counter
// that went backwards means the node restarted: the poll becomes the new
// baseline.
func (l *coordLoad) update(bytes int64, at time.Time, procs int32) {
	l.mu.Lock()
	defer l.mu.Unlock()

	prevBytes, prevAt := l.mu.prevBytes, l.mu.prevAt
	l.mu.prevBytes, l.mu.prevAt = bytes, at
	if prevAt.IsZero() || bytes < prevBytes {
		return
	}
	elapsed := at.Sub(prevAt).Seconds()
	if elapsed <= 0 {
		return
	}
	if procs <= 0 {
		procs = int32(defaultServerCoreCount)
	}
	rate := float64(bytes-prevBytes) / elapsed / float64(procs)
	l.rate.Store(math.Float64bits(rate))
	l.measured.Store(true)
}

// load returns the node's last measured coordinating rate per core.
func (l *coordLoad) load() float64 {
	return math.Float64frombits(l.rate.Load())
}

// clusterCoordLoad is the mean coordinating rate per core across the nodes the
// last poll cycle measured, the yardstick [coordLoadFactor] measures a node
// against.
type clusterCoordLoad struct {
	mean atomic.Uint64 // math.Float64bits; 0 until a cycle measures a rate
}

// update sets the mean from the rates of the nodes polled in one cycle. Nodes
// with no rate yet are left out.
func (c *clusterCoordLoad) update(conns []*Connection) {
	var sum float64
	var n int
	for _, conn := range conns {
		if !conn.coordLoad.measured.Load() {
			continue
		}
		sum += conn.coordLoad.load()
		n++
	}
	if n == 0 {
		return
	}
	c.mean.Store(math.Float64bits(sum / float64(n)))
}

// load returns the cluster mean, 0 when no cycle has measured one.
func (c *clusterCoordLoad) load() float64 {
	if c == nil {
		return 0
	}
	return math.Float64frombits(c.mean.Load())
}

// coordLoadFactor is the multiplier a node's coordinating load adds to its
// score for a request with no index ([selectNoIndex]): 1 for a node doing no
// coordination, 2 for one doing the cluster's mean, and so on in proportion.
// It is 1 for every node until the cluster mean is known, which takes two
// polls.
func coordLoadFactor(conn *Connection, cluster *clusterCoordLoad) float64 {
	mean := cluster.load()
	if mean <= 0 {
		return 1
	}
	return 1 + conn.coordLoad.load()/mean
}
