// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scheduler

import "math"

// PinCompletions is the number of accepted requests a pin lasts. After
// [Window.Shrink] or [Window.Pin], the window does not grow until that many
// requests have been accepted.
const PinCompletions = 3

// WindowConfig sets up a [Window]. Its units are whatever the sender counts:
// bytes for the bulk client, documents for mget.
type WindowConfig struct {
	// Start is the window's initial size, and Ceiling is the largest it grows to.
	Start, Ceiling float64
	// Floor is the smallest the window shrinks to. Set it to one full request, so
	// that a node can always be sent something. A Floor at or above Ceiling
	// fixes the window at Ceiling: it never grows or shrinks.
	Floor float64
	// Increment is how much the window grows each time the node accepts a full
	// window's worth of units. Set it to the size of the largest request.
	Increment float64
	// Backoff is the factor Shrink multiplies the window by, such as 0.5.
	Backoff float64
}

// Window limits how many units a sender may have in flight to one node: sent,
// and not yet answered. It reacts to a 429 the way TCP congestion control reacts
// to a lost packet.
//
//   - Shrink cuts the window on a 429, to Backoff times the smaller of the window
//     and the units that were in flight when the 429 arrived. Using the units in
//     flight makes the first 429 count even when the window starts far larger
//     than anything that is ever in flight. A 429 to a request that was sent
//     before the last reduction does not shrink the window again, because that
//     reduction already responded to the same overload.
//   - Grow enlarges the window by about one Increment for each window's worth of
//     units the node accepts, so a window recovers slowly after a reduction.
//   - A pin stops Grow from enlarging the window for the next PinCompletions
//     accepted requests. A 429 still shrinks a pinned window. Shrink starts a pin,
//     and Pin starts one without shrinking, for a node that has become saturated.
//
// A request may be sent when it fits in the room left in the window, or when
// nothing is in flight, so that a request larger than the window can still be
// sent. A Window is not safe for concurrent use; its owner's lock guards it.
type Window struct {
	cfg  WindowConfig
	size float64
	// reductions is the number of times Shrink has reduced the window. Each
	// request records it when the request is sent.
	reductions uint64
	// pinned is the number of accepted requests Grow must still count before it
	// enlarges the window again.
	pinned int
}

// NewWindow returns a window of size cfg.Start, kept between cfg.Floor and
// cfg.Ceiling.
func NewWindow(cfg WindowConfig) *Window {
	cfg.Floor = math.Min(cfg.Floor, cfg.Ceiling)

	return &Window{cfg: cfg, size: math.Min(math.Max(cfg.Start, cfg.Floor), cfg.Ceiling)}
}

// fixed reports whether the window can never change size: its floor is its
// ceiling.
func (w *Window) fixed() bool {
	return w.cfg.Floor >= w.cfg.Ceiling
}

// CanWrite reports whether a request of size units may be sent to a node that
// already has inFlight units in flight. It reserves nothing.
func (w *Window) CanWrite(inFlight, size int64) bool {
	return inFlight == 0 || float64(inFlight+size) <= w.size
}

// Shrink reduces the window for a 429 and pins it. inFlight is the units in
// flight when the 429 arrived, including the refused request's.
// reductionsAtSend is [Window.NumReductions] as it was when the refused request
// was sent; if the window has been reduced since, Shrink only pins it. Shrink
// reports whether it reduced the window.
func (w *Window) Shrink(inFlight int64, reductionsAtSend uint64) bool {
	if w.fixed() {
		return false
	}

	w.pinned = PinCompletions

	if reductionsAtSend != w.reductions {
		// Sent before the last reduction, which already responded to this overload.
		return false
	}

	w.size = math.Max(w.cfg.Floor, w.cfg.Backoff*math.Min(w.size, float64(inFlight)))
	w.reductions++

	return true
}

// Pin stops Grow from enlarging the window for the next PinCompletions accepted
// requests, unless a longer pin is already in place. It does not shrink the
// window, and a 429 during the pin still does.
func (w *Window) Pin() {
	if w.fixed() {
		return
	}

	w.pinned = max(w.pinned, PinCompletions)
}

// Grow records that the node accepted a request of units without a 429. While
// the window is pinned, Grow only counts down the pin. Otherwise it enlarges
// the window by Increment × units / size, up to the ceiling.
func (w *Window) Grow(units int64) {
	if w.fixed() {
		return
	}

	switch {
	case w.pinned > 0:
		w.pinned--
	case w.size <= 0:
	default:
		w.size = math.Min(w.cfg.Ceiling, w.size+w.cfg.Increment*float64(units)/w.size)
	}
}

// Size returns the size of the window, in units.
func (w *Window) Size() float64 { return w.size }

// NumReductions returns the number of times Shrink has reduced the window. A
// sender records it when it sends a request, and passes it to Shrink if the
// request is refused.
func (w *Window) NumReductions() uint64 { return w.reductions }

// SizeRatio returns the window's size divided by its ceiling: 1 when the window
// is fully open, and less after a reduction. It returns 0 for a window with no
// ceiling.
func (w *Window) SizeRatio() float64 {
	if w.cfg.Ceiling <= 0 {
		return 0
	}

	return w.size / w.cfg.Ceiling
}
