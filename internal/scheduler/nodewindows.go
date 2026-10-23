// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scheduler

import (
	"context"
	"sync"
)

// Outcome is what a node did with a request, as [NodeWindows.Release] is told it.
type Outcome uint8

const (
	// OutcomeUnknown is the zero Outcome, for a request whose result was never
	// recorded. It leaves the window as it is.
	OutcomeUnknown Outcome = iota
	// OutcomeAccepted means the node did the work. It grows the node's window.
	OutcomeAccepted
	// OutcomeRejected means the node refused the work because it was overloaded:
	// a 429, or a rejected execution on its thread pool. It shrinks the node's
	// window.
	OutcomeRejected
	// OutcomeFailed means the request failed for another reason, which says
	// nothing about the node's load. The window is left as it is.
	OutcomeFailed
)

// NodeName names a node as the transport knows it: its
// [opensearchtransport.Connection].Name.
type NodeName string

// NodeWindows keeps a [Window] per node, and admits a request to a node only
// while the node's window has room for it. It is safe for concurrent use, and is
// meant to be shared by every request a client sends, so that the windows learn
// from all of them.
type NodeWindows struct {
	cfg WindowConfig

	mu struct {
		sync.Mutex
		// targets holds each node's window and what is outstanding to it, by the
		// node's name. A node is added the first time a request is admitted to it.
		targets map[NodeName]*targetNode
	}
}

// targetNode is one node's state. Every field is guarded by [NodeWindows].mu.
type targetNode struct {
	window      *Window
	outstanding int64
	// saturated is the last saturation Release was told of, so that a node that
	// becomes saturated pins its window once, not on every request.
	saturated bool
	// waiters counts the Acquire calls blocked on the node.
	waiters int
	// wake is closed, and replaced, whenever a Release may have made room. It is a
	// broadcast, as sync.Cond's is, made of a channel so that a waiter can also
	// select on its context: sync.Cond.Wait cannot be interrupted.
	wake chan struct{}
}

// Reservation is the room a request holds in its node's window, from the
// Acquire that admitted it until it is passed to [NodeWindows.Release]. The zero
// Reservation releases nothing.
type Reservation struct {
	target *targetNode
	units  int64
	// reductionsAtSend is the window's [Window.NumReductions] when the request
	// was admitted. A 429 to a request admitted before the window's last reduction
	// belongs to the overload that reduction already answered.
	reductionsAtSend uint64
}

// NewNodeWindows returns a NodeWindows whose nodes each start with a [Window]
// built from cfg.
func NewNodeWindows(cfg WindowConfig) *NodeWindows {
	nw := &NodeWindows{cfg: cfg}
	nw.mu.targets = make(map[NodeName]*targetNode)

	return nw
}

// targetLocked returns the named node's state, creating it on first use.
func (nw *NodeWindows) targetLocked(node NodeName) *targetNode {
	t, ok := nw.mu.targets[node]
	if !ok {
		t = &targetNode{window: NewWindow(nw.cfg), wake: make(chan struct{})}
		nw.mu.targets[node] = t
	}

	return t
}

// Acquire waits until the named node's window has room for a request of units,
// counts the request as outstanding, and returns its reservation. A request
// larger than the window is admitted when nothing else is outstanding to the
// node. It returns ctx's error, admitting nothing, when ctx is done first.
func (nw *NodeWindows) Acquire(ctx context.Context, node NodeName, units int64) (Reservation, error) {
	nw.mu.Lock()
	t := nw.targetLocked(node)
	for !t.window.CanWrite(t.outstanding, units) {
		wake := t.wake
		t.waiters++
		nw.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			nw.mu.Lock()
			t.waiters--
			nw.mu.Unlock()

			return Reservation{}, ctx.Err()
		}

		nw.mu.Lock()
		t.waiters--
	}

	t.outstanding += units
	r := Reservation{target: t, units: units, reductionsAtSend: t.window.NumReductions()}
	nw.mu.Unlock()

	return r, nil
}

// Release records that an admitted request has returned. outcome teaches the
// node's window what the request showed. saturated reports whether the
// transport now sees the node as saturated: a node that has just become so has
// its window pinned, as after a reduction, but not reduced.
func (nw *NodeWindows) Release(r Reservation, outcome Outcome, saturated bool) {
	t := r.target
	if t == nil {
		return
	}

	nw.mu.Lock()
	defer nw.mu.Unlock()

	if saturated && !t.saturated {
		t.window.Pin()
	}
	t.saturated = saturated

	switch outcome {
	case OutcomeRejected:
		// Shrink from what was out when the refusal arrived, the refused request's
		// units among them.
		t.window.Shrink(t.outstanding, r.reductionsAtSend)
	case OutcomeAccepted:
		t.window.Grow(r.units)
	case OutcomeUnknown, OutcomeFailed:
	}
	t.outstanding -= r.units

	close(t.wake)
	t.wake = make(chan struct{})
}

// NodeStats is what NodeWindows has inferred about one node: the room it allows
// the node and how much of it is in use. It is a snapshot.
type NodeStats struct {
	// Window is the node's window, in units.
	Window float64
	// SizeRatio is Window divided by the most it grows to; see [Window.SizeRatio].
	SizeRatio float64
	// Outstanding is the units admitted and not yet released.
	Outstanding int64
	// Waiters is the number of requests waiting for room.
	Waiters int
}

// Stats returns a snapshot of what is known of the named node, and false when no
// request has been admitted to the node yet.
func (nw *NodeWindows) Stats(node NodeName) (NodeStats, bool) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	t, ok := nw.mu.targets[node]
	if !ok {
		return NodeStats{}, false
	}

	return NodeStats{
		Window:      t.window.Size(),
		SizeRatio:   t.window.SizeRatio(),
		Outstanding: t.outstanding,
		Waiters:     t.waiters,
	}, true
}
