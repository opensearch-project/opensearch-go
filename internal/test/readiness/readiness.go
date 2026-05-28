// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package readiness implements a layered readiness FSM for integration
// tests. Each node tracked by the cluster aggregator owns a per-node FSM
// whose state is a single atomic uint32 partitioned into two halves:
//
//   - Lower 16 bits: ordinal layers, cumulative. Each layer is one bit;
//     reaching a higher layer requires every lower layer bit to be set.
//     Layers gate each other (no LayerHTTP without LayerTCP).
//   - Upper 16 bits: independent client-state flags. Each bit is owned by
//     a separate background poller in the transport client; ordering
//     between bits is not meaningful and bits may flip set/clear over the
//     lifetime of a connection.
//
// Because the two halves share a single uint32, satisfaction of a Target
// collapses to a single bitwise op:
//
//	func (s State) Satisfies(t State) bool { return s&t == t }
//
// This works because layer bits are cumulative (so "reached LayerHTTP"
// implies the LayerTCP bit is also set) and state bits are independent.
package readiness

import (
	"math/bits"
	"sync"
	"sync/atomic"
	"time"
)

// State is the packed readiness state for a node. The lower 16 bits hold
// cumulative layer bits; the upper 16 bits hold independent client-state
// flags. Combine layer and state values with OR to construct a Target.
type State uint32

const (
	layerMask State = 0x0000_FFFF
	stateMask State = 0xFFFF_0000
)

// Layer bits (lower 16). Each layer is cumulative: LayerHTTP includes
// LayerTCP, LayerClusterJoin includes LayerHTTP, etc. A node "is at"
// LayerX when every layer bit up to and including X is set.
const (
	LayerTCP         State = 1 << 0
	LayerHTTP        State = LayerTCP | 1<<1
	LayerClusterJoin State = LayerHTTP | 1<<2
	LayerStatsReady  State = LayerClusterJoin | 1<<3
	LayerConnReady   State = LayerStatsReady | 1<<4
)

// Client-state bits (upper 16). Independent: any subset can be set/cleared
// in any order. Only meaningful for nodes at LayerConnReady or above; the
// harness ignores these bits for nodes below LayerConnReady.
const (
	StateHardwareKnown       State = 1 << 16 // lcNeedsHardware cleared
	StateCatUpdateFresh      State = 1 << 17 // lcNeedsCatUpdate cleared
	StateClusterHealthProbed State = 1 << 18 // lcClusterHealthAvailable set
)

// Common targets. Compose with OR for custom requirements.
const (
	TargetClusterReady State = LayerStatsReady
	TargetConnUsable   State = LayerConnReady
	// TargetFullyReady currently requires only StateCatUpdateFresh in the
	// client-state half. StateHardwareKnown and StateClusterHealthProbed
	// remain defined for future use but are not observable until accessors
	// are added to opensearchtransport.ConnState.
	TargetFullyReady State = LayerConnReady | StateCatUpdateFresh
)

// Satisfies reports whether s meets every requirement in target.
// Single bitwise op: works for layer ordering (because layer bits are
// cumulative) and for independent client-state bits.
func (s State) Satisfies(target State) bool {
	return s&target == target
}

// Missing returns the bits in target that are not set in s.
func (s State) Missing(target State) State {
	return target &^ s
}

// LayerBits returns just the layer half of s.
func (s State) LayerBits() State { return s & layerMask }

// ClientStateBits returns just the client-state half of s.
func (s State) ClientStateBits() State { return s & stateMask }

// HighestLayer returns the largest cumulative layer constant whose bit is
// set in s. Returns 0 if no layer bits are set.
func (s State) HighestLayer() State {
	lb := uint32(s & layerMask)
	if lb == 0 {
		return 0
	}
	// Cumulative layers fill from bit 0 upward; the highest set bit
	// determines the highest reached layer.
	return State((uint32(1) << bits.Len32(lb)) - 1)
}

// String returns a human-readable representation of s, e.g.
// "LayerHTTP|StateHardwareKnown" or "LayerNone".
func (s State) String() string {
	if s == 0 {
		return "LayerNone"
	}
	out := layerName(s.HighestLayer())
	for _, sb := range stateNames {
		if s&sb.bit != 0 {
			if out != "" {
				out += "|"
			}
			out += sb.name
		}
	}
	if out == "" {
		return "Unknown"
	}
	return out
}

func layerName(l State) string {
	//exhaustive:ignore -- only layer constants are handled here; client-state bits and compound targets are not layers.
	switch l {
	case LayerTCP:
		return "LayerTCP"
	case LayerHTTP:
		return "LayerHTTP"
	case LayerClusterJoin:
		return "LayerClusterJoin"
	case LayerStatsReady:
		return "LayerStatsReady"
	case LayerConnReady:
		return "LayerConnReady"
	default:
		return ""
	}
}

var stateNames = []struct {
	bit  State
	name string
}{
	{StateHardwareKnown, "StateHardwareKnown"},
	{StateCatUpdateFresh, "StateCatUpdateFresh"},
	{StateClusterHealthProbed, "StateClusterHealthProbed"},
}

// Outcome is the result of a single LayerCheck probe.
type Outcome int

const (
	// OutcomePending means the check did not produce a state transition
	// this poll. The harness keeps polling until the layer's deadline.
	OutcomePending Outcome = iota
	// OutcomeAdvance means the harness should advance the node to the
	// state returned by the probe. Advancing to a smaller layer value is
	// a regression and is counted; advancing to a larger value is forward
	// progress.
	OutcomeAdvance
)

// Transition records one State change in the node's history. A
// transition is a regression when To.LayerBits() < From.LayerBits().
type Transition struct {
	At         time.Time
	From       State
	To         State
	Regression bool // true when the layer half decreased
	Note       string
}

// NodeFSM tracks the readiness of a single node. The state field packs
// the layer half and the client-state half into one atomic uint32; reads
// and writes therefore see a coherent snapshot.
type NodeFSM struct {
	NodeID string
	Name   string
	Addr   string

	state       atomic.Uint32 // packed State value
	enteredAtNs atomic.Int64  // unix nanos when the highest layer was first reached
	regressions atomic.Uint32 // count of OutcomeRegress events on layer bits

	mu struct {
		sync.Mutex
		history []Transition
	}
}

// State returns the node's current packed state.
func (n *NodeFSM) State() State { return State(n.state.Load()) }

// cumulativeLayer rounds layer up to the nearest valid cumulative
// layer constant by setting every bit at-or-below the highest set bit.
// Layer constants in this package are cumulative (LayerHTTP includes
// LayerTCP, etc.); a caller passing a single-bit value (e.g. 1<<2
// instead of LayerClusterJoin) would otherwise corrupt the FSM by
// leaving HighestLayer() in disagreement with Has().
//
// 0 -> 0
// 1<<0 (LayerTCP)         -> 0b00001 (LayerTCP, idempotent)
// 1<<1 (single bit)       -> 0b00011 (LayerHTTP, normalized)
// 1<<2 (single bit)       -> 0b00111 (LayerClusterJoin, normalized)
// 0b00111 (LayerClusterJoin) -> 0b00111 (idempotent)
func cumulativeLayer(layer State) State {
	layer &= layerMask
	if layer == 0 {
		return 0
	}
	return State((uint32(1)<<bits.Len32(uint32(layer)))-1) & layerMask
}

// Advance atomically transitions the node's layer half to next (the
// cumulative layer state, i.e. one of the LayerXxx constants). The
// upper client-state half is preserved.
//
// If next < current layer bits at the first observation, the call is
// treated as a deliberate regression: the regression counter is
// incremented and the layer is moved backward. Otherwise the call is
// forward progress, and a concurrent CAS loss that reveals the layer
// has been pushed even further forward by another goroutine is treated
// as a silent no-op (return false, false). This prevents two concurrent
// forward Advance calls from mis-counting the loser's retry as a
// regression.
func (n *NodeFSM) Advance(next State, note string) (bool, bool) {
	next = cumulativeLayer(next)
	now := time.Now()

	// Capture the caller's intent from the first observation: are they
	// trying to move forward (next > initialLayer) or deliberately
	// regress (next < initialLayer)? The CAS loop preserves this
	// classification so a race-induced retry doesn't silently flip it.
	initialLayer := State(n.state.Load()) & layerMask
	regressionIntent := next < initialLayer

	for {
		old := n.state.Load()
		oldState := State(old)
		oldLayer := oldState & layerMask
		if next == oldLayer {
			return false, false
		}
		// Forward-intent caller observing a concurrent winner that has
		// pushed the layer past next: silent no-op. The peer already
		// satisfied (and surpassed) the forward goal.
		if !regressionIntent && next < oldLayer {
			return false, false
		}
		newState := (oldState & stateMask) | next
		if !n.state.CompareAndSwap(old, uint32(newState)) {
			continue
		}
		regressed := next < oldLayer
		if regressed {
			n.regressions.Add(1)
		}
		n.enteredAtNs.Store(now.UnixNano())
		n.recordTransition(now, oldState, newState, regressed, note)
		return true, regressed
	}
}

// UpdateClientState atomically sets the bits in `set` and clears the
// bits in `clr` in the node's client-state half. Both arguments are
// masked to stateMask. Layer bits are preserved.
//
// Client-state bits flap freely (a poller can clear a bit it previously
// set when the underlying client state changes). UpdateClientState does
// not record a regression for any flip.
func (n *NodeFSM) UpdateClientState(set, clr State, note string) bool {
	set &= stateMask
	clr &= stateMask
	if set == 0 && clr == 0 {
		return false
	}
	now := time.Now()
	for {
		old := n.state.Load()
		oldState := State(old)
		newState := (oldState | set) &^ clr
		if newState == oldState {
			return false
		}
		if !n.state.CompareAndSwap(old, uint32(newState)) {
			continue
		}
		n.recordTransition(now, oldState, newState, false, note)
		return true
	}
}

func (n *NodeFSM) recordTransition(at time.Time, from, to State, regression bool, note string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	const maxHistory = 64
	if len(n.mu.history) >= maxHistory {
		n.mu.history = append(n.mu.history[:0], n.mu.history[len(n.mu.history)-maxHistory+1:]...)
	}
	n.mu.history = append(n.mu.history, Transition{
		At:         at,
		From:       from,
		To:         to,
		Regression: regression,
		Note:       note,
	})
}

// History returns a copy of the node's transition history (most recent last).
func (n *NodeFSM) History() []Transition {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Transition, len(n.mu.history))
	copy(out, n.mu.history)
	return out
}

// Has reports whether the node satisfies the given target.
// Bits requiring LayerConnReady or above are only honored once the node
// has reached LayerConnReady; before that, any required client-state
// bits are treated as not-yet-satisfied.
func (n *NodeFSM) Has(target State) bool {
	cur := n.State()
	// Client-state bits are undefined below LayerConnReady; if the target
	// requires any but the node hasn't reached LayerConnReady, fail.
	if target&stateMask != 0 && cur&LayerConnReady != LayerConnReady {
		return false
	}
	return cur.Satisfies(target)
}

// EnteredAt returns the wall-clock time at which the node reached its
// current highest layer.
func (n *NodeFSM) EnteredAt() time.Time {
	ns := n.enteredAtNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// Regressions returns the number of OutcomeRegress layer demotions
// observed on this node.
func (n *NodeFSM) Regressions() uint32 { return n.regressions.Load() }

// NodeSnapshot is a consistent point-in-time view of a NodeFSM,
// returned by [NodeFSM.Snapshot] for diagnostic output. Reading the
// individual fields via separate accessor calls would race against
// concurrent transitions and produce a mismatch (e.g. State already
// shows the new layer but EnteredAt still reports the old one).
type NodeSnapshot struct {
	State       State
	EnteredAt   time.Time
	Regressions uint32
	History     []Transition
}

// Snapshot returns a consistent point-in-time view of the node's
// state, transition history, regression count, and time the current
// layer was entered.
func (n *NodeFSM) Snapshot() NodeSnapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	hist := make([]Transition, len(n.mu.history))
	copy(hist, n.mu.history)
	var entered time.Time
	if ns := n.enteredAtNs.Load(); ns != 0 {
		entered = time.Unix(0, ns)
	}
	return NodeSnapshot{
		State:       State(n.state.Load()),
		EnteredAt:   entered,
		Regressions: n.regressions.Load(),
		History:     hist,
	}
}
