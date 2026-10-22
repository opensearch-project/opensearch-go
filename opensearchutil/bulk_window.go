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
	// backoff is the factor shrink multiplies the window by.
	backoff float64
	// fixed holds the window where it started, whatever the node does.
	fixed bool
}

// byteWindow is how many bytes the bulk client may have outstanding to one node: in
// requests that have been sent and not yet answered. It is the only part of the
// bulk client that answers a 429, and it does so the way TCP answers a lost packet.
//
//   - shrinkWindowSize halves it on a 429, to half of the smaller of the window and
//     the bytes that were outstanding when the 429 arrived. Shrinking from the bytes
//     in flight, and not from the window, makes the first 429 count: a window that
//     starts at the whole job's memory is far above what is ever outstanding, and half
//     of it would change nothing. A 429 to a request sent before the last shrink
//     belongs to the overload that shrink already answered, and does not shrink the
//     window again.
//   - growWindowSize raises it by one unit for each window's worth of bytes in flushes
//     the node accepts, so a window that was shrunk recovers slowly, and one that was
//     not barely moves.
//   - A pin stops growWindowSize from raising it for the next pinFlushes accepted
//     flushes; a 429 still shrinks a pinned window. shrinkWindowSize starts a pin, and
//     pinWindowSize starts one without shrinking, for a node that has become
//     write-saturated.
//
// A sealed buffer is sent only when it fits in what is left of the window, or when
// nothing is outstanding at all, so that a buffer larger than the window still goes.
// Not guarded: the scheduler's lock holds it.
type byteWindow struct {
	cfg  windowConfig
	size float64
	// shrinks counts the times shrinkWindowSize has reduced the window. A request
	// records the count when it is sent.
	shrinks uint64
	// pinnedFlushes is how many more accepted flushes growWindowSize must count before
	// it raises the window again.
	pinnedFlushes int
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

// shrinkWindowSize halves the window for a 429 that arrived with outstanding bytes in
// flight, the refused request's among them, and pins it. sentAt is the shrinks count
// when the refused request was sent; a request sent before the last shrink does not
// shrink the window again. It reports whether it reduced the window.
func (w *byteWindow) shrinkWindowSize(outstanding int64, sentAt uint64) bool {
	if w.cfg.fixed {
		return false
	}

	w.pinnedFlushes = pinFlushes

	if sentAt != w.shrinks {
		// Sent before the last shrink: the same overload, already answered.
		return false
	}

	w.size = math.Max(w.cfg.floor, w.cfg.backoff*math.Min(w.size, float64(outstanding)))
	w.shrinks++

	return true
}

// pinWindowSize stops growWindowSize from raising the window for the next pinFlushes
// accepted flushes, or leaves a longer pin in place. It does not shrink the window,
// and a 429 during the pin still does.
func (w *byteWindow) pinWindowSize() {
	if w.cfg.fixed {
		return
	}

	w.pinnedFlushes = max(w.pinnedFlushes, pinFlushes)
}

// growWindowSize records a flush of bytes that the node accepted, without a 429.
// While the window is pinned it counts the pin down by one flush; otherwise it raises
// the window by unit × bytes / size, up to the ceiling.
func (w *byteWindow) growWindowSize(bytes int64) {
	if w.cfg.fixed {
		return
	}

	switch {
	case w.pinnedFlushes > 0:
		w.pinnedFlushes--
	case w.size <= 0:
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
// on the node is measured against. The rate is smoothed before the best is taken
// from it, so that one lucky flush does not become a best no honest flush can match.
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
func (r *rateTracker) learn(f flushResult) {
	if !f.hasValidLatency() || float64(f.bytes) < r.floor {
		return
	}

	if rate, ok := f.throughput(); ok {
		r.rateS = smoothInto(r.rateS, rate)
		r.base = math.Max(r.rateS, r.base*sizingBaseDecay)
	}
}
