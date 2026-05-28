// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLayerBitsCumulative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		layer     State
		bitCount  int
		highBit   uint32 // expected highest bit index (0-based)
		impliesAt []State
	}{
		{
			name:      "LayerTCP is bit 0 only",
			layer:     LayerTCP,
			bitCount:  1,
			highBit:   0,
			impliesAt: []State{LayerTCP},
		},
		{
			name:      "LayerHTTP includes LayerTCP",
			layer:     LayerHTTP,
			bitCount:  2,
			highBit:   1,
			impliesAt: []State{LayerTCP, LayerHTTP},
		},
		{
			name:      "LayerClusterJoin includes LayerTCP and LayerHTTP",
			layer:     LayerClusterJoin,
			bitCount:  3,
			highBit:   2,
			impliesAt: []State{LayerTCP, LayerHTTP, LayerClusterJoin},
		},
		{
			name:      "LayerStatsReady includes all lower layers",
			layer:     LayerStatsReady,
			bitCount:  4,
			highBit:   3,
			impliesAt: []State{LayerTCP, LayerHTTP, LayerClusterJoin, LayerStatsReady},
		},
		{
			name:      "LayerConnReady includes all five layers",
			layer:     LayerConnReady,
			bitCount:  5,
			highBit:   4,
			impliesAt: []State{LayerTCP, LayerHTTP, LayerClusterJoin, LayerStatsReady, LayerConnReady},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lb := uint32(tt.layer & layerMask)
			require.NotZero(t, lb)
			require.Equal(t, tt.bitCount, popcount(lb), "expected %d cumulative bits", tt.bitCount)

			for _, lower := range tt.impliesAt {
				require.True(t, tt.layer.Satisfies(lower),
					"%v should satisfy %v", tt.layer, lower)
			}
		})
	}
}

func TestSatisfies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		state  State
		target State
		want   bool
	}{
		{"empty state never satisfies a layer", 0, LayerTCP, false},
		{"layer is reflexive", LayerHTTP, LayerHTTP, true},
		{"higher layer satisfies lower target", LayerStatsReady, LayerHTTP, true},
		{"lower layer does not satisfy higher target", LayerHTTP, LayerStatsReady, false},
		{
			name:   "layer plus required state bit",
			state:  LayerConnReady | StateHardwareKnown,
			target: LayerConnReady | StateHardwareKnown,
			want:   true,
		},
		{
			name:   "missing one client-state bit fails",
			state:  LayerConnReady | StateHardwareKnown,
			target: LayerConnReady | StateHardwareKnown | StateCatUpdateFresh,
			want:   false,
		},
		{
			name:   "extra client-state bits are fine",
			state:  LayerConnReady | StateHardwareKnown | StateCatUpdateFresh | StateClusterHealthProbed,
			target: LayerConnReady | StateHardwareKnown,
			want:   true,
		},
		{
			name:   "TargetFullyReady requires all five layers and three state bits",
			state:  LayerConnReady | StateHardwareKnown | StateCatUpdateFresh | StateClusterHealthProbed,
			target: TargetFullyReady,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.state.Satisfies(tt.target))
		})
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()

	state := LayerHTTP | StateHardwareKnown
	target := LayerStatsReady | StateHardwareKnown | StateCatUpdateFresh

	missing := state.Missing(target)
	// Missing layer bits: LayerStatsReady contains bits 0-3; state has 0-1; missing 2-3.
	require.Equal(t, LayerStatsReady&^LayerHTTP, missing&layerMask)
	require.Equal(t, StateCatUpdateFresh, missing&stateMask)
}

func TestHighestLayer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state State
		want  State
	}{
		{"empty", 0, 0},
		{"only TCP", LayerTCP, LayerTCP},
		{"HTTP returns HTTP", LayerHTTP, LayerHTTP},
		{"StatsReady returns StatsReady", LayerStatsReady, LayerStatsReady},
		{"ConnReady with state bits ignores state bits", LayerConnReady | StateHardwareKnown, LayerConnReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.state.HighestLayer())
		})
	}
}

func TestLayerStateNonOverlap(t *testing.T) {
	t.Parallel()

	// layerMask and stateMask must partition the uint32 with no overlap.
	require.Zero(t, layerMask&stateMask)
	require.Equal(t, ^State(0), layerMask|stateMask)
}

func TestNodeFSM_HasGuardsClientStateBelowLayerConnReady(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	// Force state to LayerStatsReady plus a client-state bit (which would
	// be impossible in real operation but tests the guard).
	n.state.Store(uint32(LayerStatsReady | StateHardwareKnown))

	// Target requiring only the layer should pass.
	require.True(t, n.Has(LayerStatsReady))
	// Target requiring a client-state bit must NOT pass below LayerConnReady,
	// even though the bit is technically set.
	require.False(t, n.Has(LayerStatsReady|StateHardwareKnown))

	// Once the node reaches LayerConnReady, the bit becomes meaningful.
	n.state.Store(uint32(LayerConnReady | StateHardwareKnown))
	require.True(t, n.Has(LayerConnReady|StateHardwareKnown))
}

func popcount(x uint32) int {
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}

func TestNodeFSM_AdvanceForward(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	changed, regressed := n.Advance(LayerTCP, "tcp ok")
	require.True(t, changed)
	require.False(t, regressed)
	require.Equal(t, LayerTCP, n.State().LayerBits())

	changed, regressed = n.Advance(LayerHTTP, "http ok")
	require.True(t, changed)
	require.False(t, regressed)
	require.Equal(t, LayerHTTP, n.State().LayerBits())
}

func TestNodeFSM_AdvanceBackwardIsRegression(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerStatsReady, "stats ready")
	require.Zero(t, n.Regressions())

	changed, regressed := n.Advance(LayerHTTP, "stats fan-out failed")
	require.True(t, changed)
	require.True(t, regressed)
	require.Equal(t, LayerHTTP, n.State().LayerBits())
	require.Equal(t, uint32(1), n.Regressions())
}

func TestNodeFSM_AdvanceNoOpWhenSame(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerTCP, "tcp ok")
	changed, regressed := n.Advance(LayerTCP, "tcp still ok")
	require.False(t, changed)
	require.False(t, regressed)
}

func TestNodeFSM_AdvancePreservesClientStateBits(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerConnReady, "conn ready")
	n.UpdateClientState(StateHardwareKnown|StateCatUpdateFresh, 0, "client up")

	// Regress to LayerHTTP. Layer bits drop, client-state bits stay.
	n.Advance(LayerHTTP, "stats lost")
	require.Equal(t, LayerHTTP, n.State().LayerBits())
	require.Equal(t, StateHardwareKnown|StateCatUpdateFresh, n.State().ClientStateBits())
}

func TestNodeFSM_AdvanceNormalizesNonCumulativeBits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    State
		wantBits State
	}{
		{"single bit 1<<1 normalizes to LayerHTTP", State(1 << 1), LayerHTTP},
		{"single bit 1<<2 normalizes to LayerClusterJoin", State(1 << 2), LayerClusterJoin},
		{"single bit 1<<3 normalizes to LayerStatsReady", State(1 << 3), LayerStatsReady},
		{"single bit 1<<4 normalizes to LayerConnReady", State(1 << 4), LayerConnReady},
		{"already-cumulative LayerClusterJoin stays unchanged", LayerClusterJoin, LayerClusterJoin},
		{"already-cumulative LayerStatsReady stays unchanged", LayerStatsReady, LayerStatsReady},
		{"sparse bits 0b10100 (LayerStatsReady|LayerConnReady) normalize to LayerConnReady", State(1<<2 | 1<<4), LayerConnReady},
		{"client-state bits in input get masked away", LayerHTTP | StateCatUpdateFresh, LayerHTTP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var n NodeFSM
			n.Advance(tt.input, "test")
			require.Equal(t, tt.wantBits, n.State().LayerBits(),
				"Advance(%v) layer bits", tt.input)
			// HighestLayer agrees with Has after normalization.
			require.True(t, n.State().Satisfies(tt.wantBits),
				"node should Satisfy its own normalized layer")
		})
	}
}

func TestNodeFSM_UpdateClientStateFlapping(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerConnReady, "conn ready")

	// Set bit, then clear it, then set it again. None of these are regressions.
	require.True(t, n.UpdateClientState(StateCatUpdateFresh, 0, "cat update done"))
	require.True(t, n.UpdateClientState(0, StateCatUpdateFresh, "cache invalidated"))
	require.True(t, n.UpdateClientState(StateCatUpdateFresh, 0, "cat update done again"))
	require.Zero(t, n.Regressions(), "client-state flipping must not count as a regression")
}

func TestNodeFSM_UpdateClientStateMasksToUpperBits(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerConnReady, "conn ready")
	// Try to clobber a layer bit through UpdateClientState - must be ignored.
	n.UpdateClientState(LayerTCP, 0, "should be masked away")
	require.Equal(t, LayerConnReady, n.State().LayerBits())
}

func TestNodeFSM_HistoryMarksRegressions(t *testing.T) {
	t.Parallel()

	var n NodeFSM
	n.Advance(LayerStatsReady, "ok")
	n.Advance(LayerHTTP, "stats lost")

	hist := n.History()
	require.Len(t, hist, 2)
	require.False(t, hist[0].Regression)
	require.True(t, hist[1].Regression)
}
