// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
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

// adaptiveSelectionSnapshot is an immutable view of the last reported stats.
type adaptiveSelectionSnapshot struct {
	nodeID    string
	stats     AdaptiveSelectionStats
	firstSeen time.Time
	lastSeen  time.Time
}

// adaptiveSelection holds a node's adaptive_selection stats as seen by the
// polled node. The poller is the only writer; readers are lock-free.
type adaptiveSelection struct {
	p atomic.Pointer[adaptiveSelectionSnapshot]
}

// record stores a report from nodeID; see [adaptiveSelection] for who may call it.
// The server keeps an entry forever and its EWMAs freeze without traffic, so
// values identical to the stored ones are not a new report and leave lastSeen
// unchanged; frozen values therefore expire. A different nodeID (another node
// now answering at this URL) restarts the blackout.
func (a *adaptiveSelection) record(now time.Time, nodeID string, s AdaptiveSelectionStats) {
	cur := a.p.Load()
	if cur != nil && cur.nodeID != nodeID {
		cur = nil
	}
	if cur != nil && cur.stats == s {
		return
	}
	snap := &adaptiveSelectionSnapshot{nodeID: nodeID, stats: s, firstSeen: now, lastSeen: now}
	if cur != nil && now.Sub(cur.lastSeen) <= adaptiveSelectionExpiry {
		snap.firstSeen = cur.firstSeen
	}
	a.p.Store(snap)
}

// load returns the stats once past the blackout and before expiry.
func (a *adaptiveSelection) load(now time.Time) (AdaptiveSelectionStats, bool) {
	cur := a.p.Load()
	if cur == nil || now.Sub(cur.firstSeen) < adaptiveSelectionBlackout || now.Sub(cur.lastSeen) > adaptiveSelectionExpiry {
		return AdaptiveSelectionStats{}, false
	}
	return cur.stats, true
}
