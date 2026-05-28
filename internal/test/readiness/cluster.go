// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness

import (
	"context"
	"sync"
	"time"
)

// Cluster aggregates per-node FSMs and the most recent observation
// metadata used by the diagnostic dump. It is safe for concurrent use:
// each NodeFSM is internally synchronized, and the Nodes map is guarded
// by a mutex for additions.
type Cluster struct {
	Expected int   // expected node count (e.g. OPENSEARCH_NODE_COUNT)
	MinNodes int   // minimum nodes that must satisfy the target (defaults to Expected)
	Target   State // packed layer+state target each counted node must satisfy

	Started time.Time

	mu struct {
		sync.Mutex
		nodes        map[string]*NodeFSM // keyed by node id (or addr fallback)
		lastErr      error               // most recent transient error from any check
		lastResp     []byte              // truncated bytes of last cat-nodes response
		polls        int
		lastPolledAt time.Time // updated once per poll tick by the harness
	}
}

// SetLastPolledAt records the current poll timestamp under c.mu.
func (c *Cluster) SetLastPolledAt(t time.Time) {
	c.mu.Lock()
	c.mu.lastPolledAt = t
	c.mu.Unlock()
}

// LastPolledAt returns the most recent poll timestamp, or the zero
// value if no poll has been recorded.
func (c *Cluster) LastPolledAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mu.lastPolledAt
}

// NewCluster constructs a Cluster aggregator. expected is the total number
// of nodes the cluster is expected to host; if minNodes is 0, it defaults
// to expected (i.e. all nodes must satisfy the target).
func NewCluster(expected, minNodes int, target State) *Cluster {
	if minNodes <= 0 || minNodes > expected {
		minNodes = expected
	}
	c := &Cluster{
		Expected: expected,
		MinNodes: minNodes,
		Target:   target,
		Started:  time.Now(),
	}
	c.mu.nodes = make(map[string]*NodeFSM)
	return c
}

// Node returns the FSM for the given node id, creating it on first
// observation. The returned pointer is stable for the lifetime of c.
func (c *Cluster) Node(id, name, addr string) *NodeFSM {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.mu.nodes[id]; ok {
		// Refresh display fields if a check learned them later.
		if n.Name == "" && name != "" {
			n.Name = name
		}
		if n.Addr == "" && addr != "" {
			n.Addr = addr
		}
		return n
	}
	n := &NodeFSM{NodeID: id, Name: name, Addr: addr}
	c.mu.nodes[id] = n
	return n
}

// Nodes returns a snapshot of the per-node FSMs (slice is freshly allocated;
// the FSM pointers themselves are shared with the aggregator).
func (c *Cluster) Nodes() []*NodeFSM {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*NodeFSM, 0, len(c.mu.nodes))
	for _, n := range c.mu.nodes {
		out = append(out, n)
	}
	return out
}

// Satisfied reports whether at least MinNodes have reached Target.
func (c *Cluster) Satisfied() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.mu.nodes) < c.MinNodes {
		return false
	}
	ready := 0
	for _, n := range c.mu.nodes {
		if n.Has(c.Target) {
			ready++
		}
	}
	return ready >= c.MinNodes
}

// RecordError stashes the most recent transient error from a check.
// Terminal errors are not recorded here; they abort the harness directly.
func (c *Cluster) RecordError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.lastErr = err
}

// RecordResponse stashes a copy of the last raw response body for
// inclusion in the diagnostic dump.
func (c *Cluster) RecordResponse(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dst := make([]byte, len(body))
	copy(dst, body)
	c.mu.lastResp = dst
}

// IncPolls bumps the poll counter; called once per harness tick.
func (c *Cluster) IncPolls() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.polls++
}

// Stats returns a snapshot of aggregator-level counters.
func (c *Cluster) Stats() (int, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mu.polls, append([]byte(nil), c.mu.lastResp...), c.mu.lastErr
}

// LayerCheck advances a node toward a specific layer. Each tick the
// harness invokes Probe on every node that hasn't yet reached the
// check's layer; the check decides whether to call Advance.
//
// Layer reports the layer this check promotes the node to (one of the
// LayerXxx constants). Implementations should only call NodeFSM.Advance
// when they have evidence the node is at that layer.
//
// A terminal error returned from Probe (e.g. authentication failure)
// aborts the harness. Transient errors (request timeouts, parse errors)
// should be recorded via Cluster.RecordError and the function should
// return nil to keep polling.
type LayerCheck interface {
	Layer() State
	Probe(ctx context.Context, c *Cluster) error
}

// ClientStateCheck mutates the upper half of a node's State, representing
// transport-client observations that flap independently. Each check owns
// a non-overlapping subset of the StateXxx bits returned by Bits.
//
// A check is only invoked for nodes that have reached LayerConnReady.
type ClientStateCheck interface {
	Bits() State
	Probe(ctx context.Context, c *Cluster) error
}
