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

// Outcome is the result of a request to a node. [NodeWindows.Release] uses it to
// adjust the node's window.
type Outcome uint8

const (
	// OutcomeUnknown is the zero value: the result was never recorded. The window
	// does not change.
	OutcomeUnknown Outcome = iota
	// OutcomeAccepted means the node did the work. The window grows.
	OutcomeAccepted
	// OutcomeRejected means the node was overloaded and refused the work, with a
	// 429 or a rejected execution on its thread pool. The window shrinks.
	OutcomeRejected
	// OutcomeFailed means the request failed for a reason unrelated to the node's
	// load. The window does not change.
	OutcomeFailed
)

// NodeName is a node's name as the transport knows it: the node's
// [opensearchtransport.Connection].Name.
type NodeName string

// NodeWindows holds one [Window] per node and limits how much work is in flight
// to each node. A request waits in Acquire until its node's window has room. It
// is safe for concurrent use. Share one NodeWindows across every request a
// client sends, so that each window learns from all of them.
type NodeWindows struct {
	cfg WindowConfig

	mu struct {
		sync.Mutex
		// targets maps a node's name to its state. A node is added the first time
		// a request to it is admitted.
		targets map[NodeName]*targetNode
	}
}

// targetNode is the state of one node. [NodeWindows].mu guards every field.
type targetNode struct {
	window *Window
	// inFlight is the units admitted to the node and not yet released.
	inFlight int64
	// saturated is the saturation the last Release reported. A node's window is
	// pinned only when the node becomes saturated, not on every request while it
	// stays saturated.
	saturated bool
	// waiters is the number of Acquire calls waiting for room on the node.
	waiters int
	// wake is closed, and then replaced, each time a Release may have freed room.
	// Closing it wakes every waiter, as sync.Cond.Broadcast does. It is a channel
	// rather than a sync.Cond because a waiter must also stop when its context is
	// done, and sync.Cond.Wait cannot be interrupted.
	wake chan struct{}
}

// Reservation is the room that an admitted request holds in its node's window.
// Acquire returns it, and Release gives the room back. Releasing the zero
// Reservation does nothing.
type Reservation struct {
	target *targetNode
	units  int64
	// reductionsAtSend is the window's [Window.NumReductions] when the request was
	// admitted. Shrink uses it to ignore a 429 to a request that was sent before
	// the window's last reduction, since that reduction already responded to the
	// same overload.
	reductionsAtSend uint64
}

// NewNodeWindows returns a NodeWindows that gives each node a new [Window] built
// from cfg.
func NewNodeWindows(cfg WindowConfig) *NodeWindows {
	nw := &NodeWindows{cfg: cfg}
	nw.mu.targets = make(map[NodeName]*targetNode)

	return nw
}

// targetLocked returns the state of the named node, creating it if this is the
// node's first request. The caller holds nw.mu.
func (nw *NodeWindows) targetLocked(node NodeName) *targetNode {
	t, ok := nw.mu.targets[node]
	if !ok {
		t = &targetNode{window: NewWindow(nw.cfg), wake: make(chan struct{})}
		nw.mu.targets[node] = t
	}

	return t
}

// Acquire blocks until the named node's window has room for a request of units,
// adds the request's units to the node's in-flight count, and returns the
// request's reservation. A request larger than the whole window is admitted when
// nothing else is in flight to the node. If ctx is done first, Acquire returns
// ctx's error and admits nothing.
func (nw *NodeWindows) Acquire(ctx context.Context, node NodeName, units int64) (Reservation, error) {
	nw.mu.Lock()
	t := nw.targetLocked(node)
	for !t.window.CanWrite(t.inFlight, units) {
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

	t.inFlight += units
	r := Reservation{target: t, units: units, reductionsAtSend: t.window.NumReductions()}
	nw.mu.Unlock()

	return r, nil
}

// Release returns a reservation's room to its node's window once the request has
// finished, and adjusts the window according to outcome. saturated is whether
// the transport now reports the node as saturated. When a node becomes
// saturated, its window is pinned so that it stops growing for a while, but it
// does not shrink.
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
		// Shrink from the units in flight when the refusal arrived, which still
		// include the refused request's.
		t.window.Shrink(t.inFlight, r.reductionsAtSend)
	case OutcomeAccepted:
		t.window.Grow(r.units)
	case OutcomeUnknown, OutcomeFailed:
	}
	t.inFlight -= r.units

	close(t.wake)
	t.wake = make(chan struct{})
}

// NodeStats is a snapshot of what NodeWindows has inferred about one node.
type NodeStats struct {
	// Window is the size of the node's window, in units.
	Window float64
	// InFlight is the units admitted to the node and not yet released.
	InFlight int64
	// Waiters is the number of requests waiting in Acquire for room on the node.
	Waiters int
}

// Stats returns a snapshot of the named node. It returns false if no request to
// the node has been admitted yet.
func (nw *NodeWindows) Stats(node NodeName) (NodeStats, bool) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	t, ok := nw.mu.targets[node]
	if !ok {
		return NodeStats{}, false
	}

	return NodeStats{
		Window:   t.window.Size(),
		InFlight: t.inFlight,
		Waiters:  t.waiters,
	}, true
}
