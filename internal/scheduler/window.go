// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scheduler

import "math"

// PinCompletions is how many accepted completions a pin lasts: after
// [Window.Shrink] or [Window.Pin] the window does not grow for that many.
const PinCompletions = 3

// WindowConfig is what a [Window] starts from. Its units are the sender's: bytes
// for the bulk client, documents for mget.
type WindowConfig struct {
	// Start is the window a node begins with, and Ceiling the most it grows to.
	Start, Ceiling float64
	// Floor is the least it falls to: one full request, so that a node can always
	// be sent something.
	Floor float64
	// Unit is the additive step: the window grows by about this much for each
	// window's worth of units the node completes. It is the largest request.
	Unit float64
	// Backoff is the factor Shrink multiplies the window by.
	Backoff float64
	// Fixed holds the window where it started, whatever the node does.
	Fixed bool
}

// Window is how many units a sender may have outstanding to one node: in
// requests that have been sent and not yet answered. It answers a 429 the way
// TCP answers a lost packet.
//
//   - Shrink halves it on a 429, to half of the smaller of the window and the
//     units that were outstanding when the 429 arrived. Shrinking from the units in
//     flight, and not from the window, makes the first 429 count: a window that
//     starts far above what is ever outstanding would not change at half of
//     itself. A 429 to a request sent before the last shrink belongs to the
//     overload that shrink already answered, and does not shrink the window again.
//   - Grow raises it by one unit for each window's worth of units in requests the
//     node accepts, so a window that was shrunk recovers slowly, and one that was
//     not barely moves.
//   - A pin stops Grow from raising it for the next PinCompletions accepted
//     requests; a 429 still shrinks a pinned window. Shrink starts a pin, and Pin
//     starts one without shrinking, for a node that has become saturated.
//
// A request is sent only when it fits in what is left of the window, or when
// nothing is outstanding at all, so that a request larger than the window still
// goes. Not guarded: the caller's lock holds it.
type Window struct {
	cfg  WindowConfig
	size float64
	// shrinks counts the times Shrink has reduced the window. A request records
	// the count when it is sent.
	shrinks uint64
	// pinned is how many more accepted requests Grow must count before it raises
	// the window again.
	pinned int
}

// NewWindow returns a window that starts at cfg.Start, kept between cfg.Floor
// and cfg.Ceiling.
func NewWindow(cfg WindowConfig) *Window {
	cfg.Floor = math.Min(cfg.Floor, cfg.Ceiling)

	return &Window{cfg: cfg, size: math.Min(math.Max(cfg.Start, cfg.Floor), cfg.Ceiling)}
}

// Allows reports whether a request of size units may be sent to a node that has
// outstanding units in flight.
func (w *Window) Allows(outstanding, size int64) bool {
	return outstanding == 0 || float64(outstanding+size) <= w.size
}

// Shrink halves the window for a 429 that arrived with outstanding units in
// flight, the refused request's among them, and pins it. sentAt is [Window.Shrinks]
// when the refused request was sent; a request sent before the last shrink does
// not shrink the window again. It reports whether it reduced the window.
func (w *Window) Shrink(outstanding int64, sentAt uint64) bool {
	if w.cfg.Fixed {
		return false
	}

	w.pinned = PinCompletions

	if sentAt != w.shrinks {
		// Sent before the last shrink: the same overload, already answered.
		return false
	}

	w.size = math.Max(w.cfg.Floor, w.cfg.Backoff*math.Min(w.size, float64(outstanding)))
	w.shrinks++

	return true
}

// Pin stops Grow from raising the window for the next PinCompletions accepted
// requests, or leaves a longer pin in place. It does not shrink the window, and a
// 429 during the pin still does.
func (w *Window) Pin() {
	if w.cfg.Fixed {
		return
	}

	w.pinned = max(w.pinned, PinCompletions)
}

// Grow records a request of units that the node accepted, without a 429. While
// the window is pinned it counts the pin down by one; otherwise it raises the
// window by Unit × units / size, up to the ceiling.
func (w *Window) Grow(units int64) {
	if w.cfg.Fixed {
		return
	}

	switch {
	case w.pinned > 0:
		w.pinned--
	case w.size <= 0:
	default:
		w.size = math.Min(w.cfg.Ceiling, w.size+w.cfg.Unit*float64(units)/w.size)
	}
}

// Size is the window, in units.
func (w *Window) Size() float64 { return w.size }

// Shrinks is the number of times Shrink has reduced the window. A sender records
// it when it sends a request and passes it to Shrink if the request is refused.
func (w *Window) Shrinks() uint64 { return w.shrinks }

// Fraction is the window as a part of the most it grows to.
func (w *Window) Fraction() float64 {
	if w.cfg.Ceiling <= 0 {
		return 0
	}

	return w.size / w.cfg.Ceiling
}
