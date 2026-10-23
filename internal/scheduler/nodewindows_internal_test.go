// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nodeWindowsConfig is a window of 100 units that starts full, with requests of 10.
func nodeWindowsConfig() WindowConfig {
	return WindowConfig{Start: 100, Ceiling: 100, Floor: 10, Increment: 10, Backoff: 0.5}
}

func TestNodeWindowsAcquire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// held are the units already admitted to node "a".
		held  []int64
		units int64
		// wantAdmitted is false when the acquire must wait, which the test sees as
		// the expiry of an already-cancelled context.
		wantAdmitted bool
	}{
		{name: "an idle node takes a request", units: 10, wantAdmitted: true},
		{name: "a request that fits", held: []int64{50, 40}, units: 10, wantAdmitted: true},
		{name: "a request that does not fit waits", held: []int64{50, 45}, units: 10},
		{name: "an idle node takes a request larger than its window", units: 500, wantAdmitted: true},
		{name: "a busy node holds a request larger than its window", held: []int64{1}, units: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nw := NewNodeWindows(nodeWindowsConfig())
			for _, units := range tt.held {
				_, err := nw.Acquire(t.Context(), "a", units)
				require.NoError(t, err)
			}

			ctx := t.Context()
			if !tt.wantAdmitted {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, err := nw.Acquire(ctx, "a", tt.units)
			if !tt.wantAdmitted {
				require.ErrorIs(t, err, context.Canceled)

				st, ok := nw.Stats("a")
				require.True(t, ok)
				require.Zero(t, st.Waiters, "a cancelled wait leaves no waiter behind")

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNodeWindowsKeepsNodesApart(t *testing.T) {
	t.Parallel()

	nw := NewNodeWindows(nodeWindowsConfig())
	_, err := nw.Acquire(t.Context(), "a", 100)
	require.NoError(t, err)

	_, err = nw.Acquire(t.Context(), "b", 100)
	require.NoError(t, err, "a full node does not hold up another")
}

func TestNodeWindowsReleaseWakesAWaiter(t *testing.T) {
	t.Parallel()

	nw := NewNodeWindows(nodeWindowsConfig())
	held, err := nw.Acquire(t.Context(), "a", 100)
	require.NoError(t, err)

	admitted := make(chan error, 1)
	go func() {
		_, err := nw.Acquire(t.Context(), "a", 10)
		admitted <- err
	}()

	require.Eventually(t, func() bool {
		st, _ := nw.Stats("a")
		return st.Waiters == 1
	}, 5*time.Second, time.Millisecond, "the second request waits for room")

	nw.Release(held, OutcomeAccepted, false)

	select {
	case err := <-admitted:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the waiter was not admitted after the release")
	}

	st, ok := nw.Stats("a")
	require.True(t, ok)
	require.Equal(t, NodeStats{Window: 100, InFlight: 10}, st)
}

func TestNodeWindowsRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// held are the units admitted to node "a"; the first is released.
		held         []int64
		outcome      Outcome
		saturated    bool
		wantWindow   float64
		wantPinned   bool
		wantInFlight int64
	}{
		{
			name: "an accepted request leaves a full window full", held: []int64{10},
			outcome: OutcomeAccepted, wantWindow: 100,
		},
		{
			name: "a rejected request shrinks from what was out, its own units among them", held: []int64{40, 20},
			outcome: OutcomeRejected, wantWindow: 30, wantPinned: true, wantInFlight: 20,
		},
		{
			name: "a failed request leaves the window alone", held: []int64{40, 20},
			outcome: OutcomeFailed, wantWindow: 100, wantInFlight: 20,
		},
		{
			name: "an unknown outcome leaves the window alone", held: []int64{40, 20},
			outcome: OutcomeUnknown, wantWindow: 100, wantInFlight: 20,
		},
		{
			name: "a node that becomes saturated is pinned but not shrunk", held: []int64{10},
			outcome: OutcomeAccepted, saturated: true, wantWindow: 100, wantPinned: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nw := NewNodeWindows(nodeWindowsConfig())
			reservations := make([]Reservation, len(tt.held))
			for i, units := range tt.held {
				var err error
				reservations[i], err = nw.Acquire(t.Context(), "a", units)
				require.NoError(t, err)
			}

			nw.Release(reservations[0], tt.outcome, tt.saturated)

			st, ok := nw.Stats("a")
			require.True(t, ok)
			require.InDelta(t, tt.wantWindow, st.Window, 1e-9)
			require.Equal(t, tt.wantInFlight, st.InFlight)
			require.Equal(t, tt.wantPinned, nw.mu.targets["a"].window.pinned > 0)
		})
	}
}

func TestNodeWindowsPinsOnceWhenANodeStaysSaturated(t *testing.T) {
	t.Parallel()

	nw := NewNodeWindows(nodeWindowsConfig())
	release := func(saturated bool) {
		t.Helper()

		r, err := nw.Acquire(t.Context(), "a", 10)
		require.NoError(t, err)
		nw.Release(r, OutcomeAccepted, saturated)
	}

	release(true)
	require.Equal(t, PinCompletions-1, nw.mu.targets["a"].window.pinned, "the pin starts on the first saturated release")

	release(true)
	require.Equal(t, PinCompletions-2, nw.mu.targets["a"].window.pinned, "a node that stays saturated is not pinned again")
}

func TestNodeWindowsReleaseOfZeroReservation(t *testing.T) {
	t.Parallel()

	nw := NewNodeWindows(nodeWindowsConfig())
	nw.Release(Reservation{}, OutcomeRejected, true)

	_, ok := nw.Stats("a")
	require.False(t, ok)
}
