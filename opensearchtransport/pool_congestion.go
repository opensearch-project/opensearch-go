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
)

const (
	// waitThresholdNanos is the default congestion threshold for RESIZABLE
	// thread pools. When wait_per_completed >= this value, the pool is
	// considered congested and cwnd is halved (multiplicative decrease).
	// 1 ms = 1_000_000 ns.
	waitThresholdNanos = 1_000_000

	// defaultSyntheticCwndMultiplier sizes the synthetic default pool for
	// unmapped routes. The default pool has cwnd = multiplier * allocatedProcessors.
	defaultSyntheticCwndMultiplier = 4
)

// poolCongestion tracks AIMD congestion state for a single thread pool
// on a single node.
//
// Hot-path reads (calcConnScore, Perform) use atomic loads for lock-free
// access. The stats poller is the sole writer for AIMD state, protected
// by mu. The 429 handler may also write under TryLock to mark overload.
type poolCongestion struct {
	// Lock-free reads (hot path).
	cwnd       atomic.Int32 // current congestion window (>= 1)
	inFlight   atomic.Int32 // client-tracked in-flight requests
	overloaded atomic.Bool  // set by stats poller or 429 response

	// Mutable AIMD state --written only by stats poller under mu.
	mu struct {
		sync.Mutex
		maxCwnd          int32 // ceiling from thread pool config (0 = unknown)
		ssthresh         int32 // slow-start threshold
		prevCompleted    int64
		prevRejected     int64
		prevWaitTimeNano int64
		hasWaitTime      bool // pool reports total_wait_time_in_nanos (RESIZABLE)
	}
}

// poolRegistry holds per-pool congestion state for a Connection.
// Keyed by thread pool name string (e.g., "search", "write", "get").
// Pools are added on discovery and removed when no longer reported by
// the node (e.g., node replacement or in-place software upgrade).
type poolRegistry struct {
	pools sync.Map // map[string]*poolCongestion

	// defaultPool is a synthetic pool for unmapped routes, sized at
	// defaultSyntheticCwndMultiplier * allocatedProcessors. Provides
	// RTT-based scoring for requests that don't match any known thread
	// pool pattern.
	defaultPool poolCongestion
}

// get returns the poolCongestion for the named pool, or nil if not found.
// Empty poolName returns the default pool.
func (r *poolRegistry) get(name string) *poolCongestion {
	if name == "" {
		return &r.defaultPool
	}
	v, ok := r.pools.Load(name)
	if !ok {
		return nil
	}
	return v.(*poolCongestion)
}

// getOrCreate returns the poolCongestion for the named pool, creating it
// if it doesn't exist. Empty poolName returns the default pool.
func (r *poolRegistry) getOrCreate(name string) *poolCongestion {
	if name == "" {
		return &r.defaultPool
	}
	v, loaded := r.pools.LoadOrStore(name, &poolCongestion{})
	pc := v.(*poolCongestion)
	if !loaded {
		// Initialize cwnd to 1 for new pools (slow start).
		pc.cwnd.Store(1)
	}
	return pc
}

// getForScoring returns the poolCongestion for the named pool. Falls back
// to the default pool when the named pool doesn't exist. Empty poolName
// also returns the default pool.
func (r *poolRegistry) getForScoring(name string) *poolCongestion {
	if name == "" {
		return &r.defaultPool
	}
	v, ok := r.pools.Load(name)
	if !ok {
		return &r.defaultPool
	}
	return v.(*poolCongestion)
}

// remove deletes the named pool from the registry.
func (r *poolRegistry) remove(name string) {
	r.pools.Delete(name)
}

// setMaxCwnd stores the thread pool's configured size as the cwnd ceiling.
// Called by discovery when pool sizes are received from /_nodes/_local/thread_pool.
//
// If the current cwnd exceeds the new ceiling, it is clamped down immediately.
// This handles the race where the stats poller runs AIMD with maxCwnd=0
// (synthetic ceiling of 32) before the hardware health check delivers the
// real pool size.
func (r *poolRegistry) setMaxCwnd(name string, size int32) {
	pc := r.getOrCreate(name)
	pc.mu.Lock()
	oldMaxCwnd := pc.mu.maxCwnd
	oldCwnd := pc.cwnd.Load()
	pc.mu.maxCwnd = size
	// Clamp cwnd if it already exceeds the real ceiling.
	if oldCwnd > size {
		pc.cwnd.Store(size)
	}
	// Reset ssthresh to the real ceiling so slow start targets the correct value.
	pc.mu.ssthresh = size
	pc.mu.Unlock()
	if debugLogger != nil {
		debugLogger.Logf("setMaxCwnd: pool=%q size=%d oldMaxCwnd=%d oldCwnd=%d clamped=%v\n",
			name, size, oldMaxCwnd, oldCwnd, oldCwnd > size)
	}
}

// maxCwndOrDefault returns maxCwnd if positive, otherwise a default ceiling
// of defaultSyntheticCwndMultiplier * defaultServerCoreCount.
func maxCwndOrDefault(maxCwnd int32) int32 {
	if maxCwnd > 0 {
		return maxCwnd
	}
	return int32(defaultSyntheticCwndMultiplier * defaultServerCoreCount)
}

// ThreadPoolStats represents per-pool runtime statistics from
// GET /_nodes/_local/stats/thread_pool. Used by the stats poller to
// feed AIMD congestion control.
type ThreadPoolStats struct {
	Threads              int    `json:"threads"`
	Queue                int    `json:"queue"`
	Active               int    `json:"active"`
	Rejected             int64  `json:"rejected"`
	Largest              int    `json:"largest"`
	Completed            int64  `json:"completed"`
	TotalWaitTimeInNanos *int64 `json:"total_wait_time_in_nanos,omitempty"` // RESIZABLE pools only
}

// applyPoolAIMD updates the congestion window for a single pool based on
// the latest stats snapshot. Called by the stats poller under the pool's
// mutex.
//
// AIMD transitions:
//   - delta(rejected) > 0 -> pool-overloaded: set overloaded flag, halve cwnd
//   - No rejected, delta(completed) > 0:
//   - RESIZABLE pool with wait-time: use wait_per_completed as signal
//   - Other pools: use queue saturation as fallback signal
//   - Congested -> multiplicative decrease (cwnd /= 2)
//   - Not congested, cwnd < ssthresh -> slow start (cwnd *= 2)
//   - Not congested, cwnd >= ssthresh -> congestion avoidance (cwnd += 1)
func applyPoolAIMD(pc *poolCongestion, stats ThreadPoolStats) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	deltaCompleted := stats.Completed - pc.mu.prevCompleted
	deltaRejected := stats.Rejected - pc.mu.prevRejected
	pc.mu.prevCompleted = stats.Completed
	pc.mu.prevRejected = stats.Rejected

	cwnd := max(pc.cwnd.Load(), 1)
	maxCwnd := pc.mu.maxCwnd
	ssthresh := pc.mu.ssthresh
	if ssthresh < 1 {
		ssthresh = maxCwndOrDefault(maxCwnd)
	}

	// Rejected: hard overload signal.
	if deltaRejected > 0 {
		pc.overloaded.Store(true)
		newCwnd := max(cwnd/2, 1)
		pc.cwnd.Store(newCwnd)
		pc.mu.ssthresh = newCwnd
		return
	}
	// Clear overloaded only via stats poller when delta(rejected) == 0.
	pc.overloaded.Store(false)

	if deltaCompleted <= 0 {
		return
	}

	// Determine congestion signal.
	congested := false
	if stats.TotalWaitTimeInNanos != nil {
		pc.mu.hasWaitTime = true
		deltaWait := *stats.TotalWaitTimeInNanos - pc.mu.prevWaitTimeNano
		pc.mu.prevWaitTimeNano = *stats.TotalWaitTimeInNanos
		if deltaCompleted > 0 {
			waitPerCompleted := float64(deltaWait) / float64(deltaCompleted)
			congested = waitPerCompleted >= waitThresholdNanos
		}
	} else if !pc.mu.hasWaitTime {
		// Fallback: queue saturation for pools without wait-time data.
		active := int32(min(stats.Active, math.MaxInt32)) //nolint:gosec // thread count; overflow impossible
		congested = stats.Queue > 0 && active >= maxCwndOrDefault(maxCwnd)
	}

	switch {
	case congested:
		// Multiplicative decrease.
		newCwnd := max(cwnd/2, 1)
		pc.cwnd.Store(newCwnd)
		pc.mu.ssthresh = newCwnd
	case cwnd < ssthresh:
		// Slow start: double cwnd (capped at ceiling).
		pc.cwnd.Store(min(cwnd*2, maxCwndOrDefault(maxCwnd)))
	default:
		// Congestion avoidance: additive increase.
		pc.cwnd.Store(min(cwnd+1, maxCwndOrDefault(maxCwnd)))
	}
}

// updatePoolCongestion updates all pool congestion states for a connection
// based on the latest node stats poll. Adds newly discovered pools and
// removes pools no longer reported (e.g., node replacement or software upgrade).
func updatePoolCongestion(conn *Connection, threadPools map[string]ThreadPoolStats) {
	if threadPools == nil {
		return
	}

	// Update existing pools and add newly discovered ones.
	for name, tps := range threadPools {
		pc := conn.pools.getOrCreate(name)
		oldCwnd := pc.cwnd.Load()
		applyPoolAIMD(pc, tps)
		newCwnd := pc.cwnd.Load()
		if debugLogger != nil && oldCwnd != newCwnd {
			pc.mu.Lock()
			debugLogger.Logf("AIMD: conn=%s pool=%q cwnd=%d->%d maxCwnd=%d ssthresh=%d\n",
				conn.Name, name, oldCwnd, newCwnd, pc.mu.maxCwnd, pc.mu.ssthresh)
			pc.mu.Unlock()
		}
	}

	// Remove pools no longer reported by the node.
	conn.pools.pools.Range(func(key, _ any) bool {
		name := key.(string)
		if _, exists := threadPools[name]; !exists {
			conn.pools.pools.Delete(name)
		}
		return true
	})
}
