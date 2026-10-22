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

// Outcome is what a node did with a request, as [Admission.Release] is told it.
type Outcome uint8

const (
	// Accepted means the node did the work. It grows the node's window.
	Accepted Outcome = iota
	// Rejected means the node refused the work because it was overloaded: a 429,
	// or a rejected execution on its thread pool. It shrinks the node's window.
	Rejected
	// Failed means the request failed for another reason, which says nothing
	// about the node's load. The window is left as it is.
	Failed
)

// Admission keeps a [Window] per node, keyed by node name, and admits a request
// to a node only while the node's window has room for it. It is safe for
// concurrent use, and is meant to be shared by every request a client sends, so
// that the windows learn from all of them.
type Admission struct {
	cfg WindowConfig

	mu    sync.Mutex
	nodes map[string]*admissionNode
}

type admissionNode struct {
	window      *Window
	outstanding int64
	// saturated is the last saturation Release was told of, so that a node that
	// becomes saturated pins its window once, not on every request.
	saturated bool
	// waiters counts the Acquire calls blocked on the node.
	waiters int
	// wake is closed, and replaced, whenever a Release may have made room.
	wake chan struct{}
}

// Ticket is an admitted request. Pass it to [Admission.Release] when the request
// returns. The zero Ticket releases nothing.
type Ticket struct {
	node   *admissionNode
	units  int64
	sentAt uint64
}

// NewAdmission returns an Admission whose nodes each start with a [Window] built
// from cfg.
func NewAdmission(cfg WindowConfig) *Admission {
	return &Admission{cfg: cfg, nodes: make(map[string]*admissionNode)}
}

// nodeLocked returns the named node's state, creating it on first use.
func (a *Admission) nodeLocked(name string) *admissionNode {
	n, ok := a.nodes[name]
	if !ok {
		n = &admissionNode{window: NewWindow(a.cfg), wake: make(chan struct{})}
		a.nodes[name] = n
	}

	return n
}

// Acquire waits until the named node's window has room for a request of units,
// counts the request as outstanding, and returns its ticket. A request larger
// than the window is admitted when nothing else is outstanding to the node. It
// returns ctx's error, admitting nothing, when ctx is done first.
func (a *Admission) Acquire(ctx context.Context, node string, units int64) (Ticket, error) {
	a.mu.Lock()
	n := a.nodeLocked(node)
	for !n.window.Allows(n.outstanding, units) {
		wake := n.wake
		n.waiters++
		a.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			a.mu.Lock()
			n.waiters--
			a.mu.Unlock()

			return Ticket{}, ctx.Err()
		}

		a.mu.Lock()
		n.waiters--
	}

	n.outstanding += units
	t := Ticket{node: n, units: units, sentAt: n.window.Shrinks()}
	a.mu.Unlock()

	return t, nil
}

// Release records that an admitted request has returned. outcome teaches the
// node's window what the request showed. saturated reports whether the
// transport now sees the node as saturated: a node that has just become so has
// its window pinned, as after a shrink, but not shrunk.
func (a *Admission) Release(t Ticket, outcome Outcome, saturated bool) {
	n := t.node
	if n == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if saturated && !n.saturated {
		n.window.Pin()
	}
	n.saturated = saturated

	switch outcome {
	case Rejected:
		// Shrink from what was out when the refusal arrived, the refused request's
		// units among them.
		n.window.Shrink(n.outstanding, t.sentAt)
	case Accepted:
		n.window.Grow(t.units)
	case Failed:
	}
	n.outstanding -= t.units

	close(n.wake)
	n.wake = make(chan struct{})
}

// NodeState is a snapshot of one node's admission state.
type NodeState struct {
	// Window is the node's window, in units.
	Window float64
	// Fraction is Window as a part of the most it grows to.
	Fraction float64
	// Outstanding is the units admitted and not yet released.
	Outstanding int64
	// Waiters is the number of requests waiting for room.
	Waiters int
}

// State returns a snapshot of the named node's admission state, and false when
// no request has been admitted to the node yet.
func (a *Admission) State(node string) (NodeState, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	n, ok := a.nodes[node]
	if !ok {
		return NodeState{}, false
	}

	return NodeState{
		Window:      n.window.Size(),
		Fraction:    n.window.Fraction(),
		Outstanding: n.outstanding,
		Waiters:     n.waiters,
	}, true
}
