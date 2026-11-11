// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"sync"
	"sync/atomic"
	"time"
)

// These match gRPC weighted_round_robin's blackout_period and
// weight_expiration_period defaults.
const (
	// adaptiveSelectionBlackout is how long stats must have been reported
	// before they are usable.
	adaptiveSelectionBlackout = 10 * time.Second
	// adaptiveSelectionExpiry is how long stats stay usable without a fresh report.
	adaptiveSelectionExpiry = 3 * time.Minute
)

// adaptiveSelectionReading is the stored stats with the size of the report
// they came from.
type adaptiveSelectionReading struct {
	AdaptiveSelectionStats
	// clients is how many nodes the reporter held stats for, the server's own
	// clientNum: the C3 weight for the other clients that send to the node.
	clients int
}

// adaptiveSelectionSnapshot is an immutable view of the stored stats.
type adaptiveSelectionSnapshot struct {
	reading   adaptiveSelectionReading
	firstSeen time.Time
	lastSeen  time.Time
}

// reporterValues is the last values one reporter sent about the node.
type reporterValues struct {
	stats     AdaptiveSelectionStats
	changedAt time.Time // when stats last differed from the values before
}

// adaptiveSelection holds a node's adaptive_selection stats as reported by
// any polled node, the node itself included. The poller writes it under mu;
// readers load p without locking.
//
// Any reporter will do because a reporter learns the queue size and service
// time from the target's own shard responses, so neither carries the
// reporter's network time; only the response time does, and scoring does not
// read it. A node also reports on itself only while it coordinates searches,
// so its self-report freezes when clients stop sending to it, while the other
// coordinators that fan out to its shards keep reporting on it.
type adaptiveSelection struct {
	p atomic.Pointer[adaptiveSelectionSnapshot]

	mu struct {
		sync.Mutex
		// nodeID is the node every entry in byReporter is about.
		nodeID string
		// byReporter is the last values each reporter sent, so a reporter
		// repeating frozen values is told apart from one whose values move.
		// Entries unchanged for longer than adaptiveSelectionExpiry are
		// pruned, at most once per expiry period, so a node that left the
		// cluster drops out; a pruned reporter's next report is a new baseline.
		byReporter map[string]reporterValues
		prunedAt   time.Time
	}
}

// record stores reporter's report about nodeID. The server keeps an entry
// forever and its EWMAs freeze without traffic, so only a change from what the
// same reporter sent before is a fresh report: it becomes the stored stats and
// refreshes lastSeen. A reporter's first report, which may have been frozen
// for hours, only sets the baseline, so stats that every reporter has frozen
// never become usable or expire. A different nodeID (another node now
// answering at this URL) drops everything recorded about the old one. clients
// is the number of entries in the reporter's report.
func (a *adaptiveSelection) record(now time.Time, reporter, nodeID string, clients int, s AdaptiveSelectionStats) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.mu.nodeID != nodeID {
		a.mu.nodeID, a.mu.byReporter = nodeID, nil
		a.p.Store(nil)
	}
	if now.Sub(a.mu.prunedAt) > adaptiveSelectionExpiry {
		for r, v := range a.mu.byReporter {
			if now.Sub(v.changedAt) > adaptiveSelectionExpiry {
				delete(a.mu.byReporter, r)
			}
		}
		a.mu.prunedAt = now
	}

	prev, seen := a.mu.byReporter[reporter]
	if seen && prev.stats == s {
		return
	}
	if a.mu.byReporter == nil {
		a.mu.byReporter = make(map[string]reporterValues)
	}
	a.mu.byReporter[reporter] = reporterValues{stats: s, changedAt: now}
	if !seen {
		return
	}

	snap := &adaptiveSelectionSnapshot{
		reading:   adaptiveSelectionReading{AdaptiveSelectionStats: s, clients: clients},
		firstSeen: now,
		lastSeen:  now,
	}
	if cur := a.p.Load(); cur != nil && now.Sub(cur.lastSeen) <= adaptiveSelectionExpiry {
		snap.firstSeen = cur.firstSeen
	}
	a.p.Store(snap)
}

// recordAdaptiveSelection records the report polled from conn (node ID
// reporter) on every target this client also connects to; see
// [adaptiveSelection] for why any reporter will do. pool is nil for a
// single-node pool, which knows only conn. A self-report is recorded only while
// conn's ID is unset or matches the reporter: another node answering at conn's
// URL reports about itself, not about the node conn's ID names, which the
// peers' reports are about.
func recordAdaptiveSelection(conn *Connection, pool *multiServerPool, reporter string, report map[string]AdaptiveSelectionStats) {
	if len(report) == 0 {
		return
	}
	now := time.Now()
	for targetID, s := range report {
		var target *Connection
		switch {
		case targetID == reporter && (conn.ID == "" || conn.ID == reporter):
			target = conn
		case targetID != reporter && pool != nil:
			target = pool.connectionByID(targetID)
		}
		if target != nil {
			target.ars.record(now, reporter, targetID, len(report), s)
		}
	}
}

// reported reports whether any snapshot was ever recorded, without reading the clock.
func (a *adaptiveSelection) reported() bool { return a.p.Load() != nil }

// load returns the stats once past the blackout and before expiry.
func (a *adaptiveSelection) load(now time.Time) (adaptiveSelectionReading, bool) {
	cur := a.p.Load()
	if cur == nil || now.Sub(cur.firstSeen) < adaptiveSelectionBlackout || now.Sub(cur.lastSeen) > adaptiveSelectionExpiry {
		return adaptiveSelectionReading{}, false
	}
	return cur.reading, true
}
