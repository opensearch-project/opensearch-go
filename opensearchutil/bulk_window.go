// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import "math"

// windowConfig is what a byteWindow starts from.
type windowConfig struct {
	// start is the window a node begins with, and ceiling the most it grows to.
	start, ceiling float64
	// floor is the least it falls to: one full buffer, so that a node can always be
	// sent something.
	floor float64
	// unit is the additive step: the window grows by about this much for each window's
	// worth of bytes the node completes. It is FlushBytes, the largest request.
	unit float64
	// backoff is the factor a rejection cuts the window by.
	backoff float64
	// fixed holds the window where it started, whatever the node does.
	fixed bool
}

// byteWindow is how many bytes the indexer may have outstanding to one node: in
// requests that have been sent and not yet answered. It is the only part of the
// indexer that answers a 429, and it does so the way TCP answers a lost packet.
//
//   - A rejection cuts it to half of the smaller of the window and the bytes that were
//     outstanding when the rejection arrived. Cutting from the bytes in flight, and not
//     from the window, makes the first rejection count: a window that starts at the whole
//     job's memory is far above what is ever outstanding, and half of it would change
//     nothing. Rejections that come from requests sent before the cut are the same
//     event and do not cut again.
//   - It grows by one unit for each window's worth of bytes the node completes, so a
//     window that was cut recovers slowly, and one that has not been cut barely moves.
//   - A node whose write pool is overloaded, and the few completions after a rejection,
//     do not grow it.
//
// A sealed buffer is sent only when it fits in what is left of the window, or when
// nothing is outstanding at all, so that a buffer larger than the window still goes.
// Not guarded: the scheduler's lock holds it.
type byteWindow struct {
	cfg  windowConfig
	size float64
	// cuts counts the cuts made so far. A request remembers the count when it is sent.
	cuts uint64
	// hold is how many completions must pass before the window may grow again.
	hold int
}

func newByteWindow(cfg windowConfig) *byteWindow {
	cfg.floor = math.Min(cfg.floor, cfg.ceiling)

	return &byteWindow{cfg: cfg, size: math.Min(math.Max(cfg.start, cfg.floor), cfg.ceiling)}
}

// allows reports whether a request of size bytes may be sent to a node that has
// outstanding bytes in flight.
func (w *byteWindow) allows(outstanding, size int64) bool {
	return outstanding == 0 || float64(outstanding+size) <= w.size
}

// reject cuts the window for a rejection that arrived with outstanding bytes in flight,
// the rejected request's among them, which was sent when the window had been cut cuts
// times. It reports whether it cut.
func (w *byteWindow) reject(outstanding int64, sentAt uint64) bool {
	if w.cfg.fixed {
		return false
	}

	w.hold = sizingHoldFlushes

	if sentAt != w.cuts {
		// Sent before the last cut: the same overload, already answered.
		return false
	}

	w.size = math.Max(w.cfg.floor, w.cfg.backoff*math.Min(w.size, float64(outstanding)))
	w.cuts++

	return true
}

// complete grows the window for a request of bytes that the node answered without
// rejecting it, unless the node's write pool is overloaded.
func (w *byteWindow) complete(bytes int64, overloaded bool) {
	if w.cfg.fixed {
		return
	}

	switch {
	case w.hold > 0:
		w.hold--
	case overloaded || w.size <= 0:
	default:
		w.size = math.Min(w.cfg.ceiling, w.size+w.cfg.unit*float64(bytes)/w.size)
	}
}

// fraction is the window as a part of the most it grows to.
func (w *byteWindow) fraction() float64 {
	if w.cfg.ceiling <= 0 {
		return 0
	}

	return w.size / w.cfg.ceiling
}

// rateTracker keeps the best service rate a node has shown, which a shard that starts
// on the node is measured against, and says how far a flush is from it. The rate is
// smoothed before the best is taken from it, so that one lucky flush does not become
// a best no honest flush can match.
type rateTracker struct {
	// base is the best recent smoothed rate, in bytes per nanosecond, fading slowly.
	base float64
	// rateS is the smoothed rate; 0 until the first flush.
	rateS float64
	// floor is the smallest request whose rate means anything.
	floor float64
}

// learn folds a flush into the best rate. A flush that was rejected, failed or indexed
// nothing, or is too small to tell anything, says nothing about how fast the node serves.
func (r *rateTracker) learn(f flushSample) {
	if !f.timed() || float64(f.bytes) < r.floor {
		return
	}

	if rate, ok := f.rate(); ok {
		r.rateS = smoothInto(r.rateS, rate)
		r.base = math.Max(r.rateS, r.base*sizingBaseDecay)
	}
}

// slowdown is how much slower than its best rate the flush shows the node to be: 0 at
// the best, 1 when it took twice as long for the same bytes. It is false for a flush that
// says nothing about the rate, or when no best rate is known yet.
func (r *rateTracker) slowdown(f flushSample) (float64, bool) {
	if !f.timed() || float64(f.bytes) < r.floor || r.base <= 0 {
		return 0, false
	}

	rate, ok := f.rate()
	if !ok || rate <= 0 {
		return 0, false
	}

	return math.Max(r.base/rate-1, 0), true
}
