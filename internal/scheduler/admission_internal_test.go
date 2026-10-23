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

// admissionConfig is a window of 100 units that starts full, with requests of 10.
func admissionConfig() WindowConfig {
	return WindowConfig{Start: 100, Ceiling: 100, Floor: 10, Unit: 10, Backoff: 0.5}
}

func TestAdmissionAcquire(t *testing.T) {
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

			a := NewAdmission(admissionConfig())
			for _, units := range tt.held {
				_, err := a.Acquire(t.Context(), "a", units)
				require.NoError(t, err)
			}

			ctx := t.Context()
			if !tt.wantAdmitted {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, err := a.Acquire(ctx, "a", tt.units)
			if !tt.wantAdmitted {
				require.ErrorIs(t, err, context.Canceled)

				st, ok := a.State("a")
				require.True(t, ok)
				require.Zero(t, st.Waiters, "a cancelled wait leaves no waiter behind")

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAdmissionKeepsNodesApart(t *testing.T) {
	t.Parallel()

	a := NewAdmission(admissionConfig())
	_, err := a.Acquire(t.Context(), "a", 100)
	require.NoError(t, err)

	_, err = a.Acquire(t.Context(), "b", 100)
	require.NoError(t, err, "a full node does not hold up another")
}

func TestAdmissionReleaseWakesAWaiter(t *testing.T) {
	t.Parallel()

	a := NewAdmission(admissionConfig())
	held, err := a.Acquire(t.Context(), "a", 100)
	require.NoError(t, err)

	admitted := make(chan error, 1)
	go func() {
		_, err := a.Acquire(t.Context(), "a", 10)
		admitted <- err
	}()

	require.Eventually(t, func() bool {
		st, _ := a.State("a")
		return st.Waiters == 1
	}, 5*time.Second, time.Millisecond, "the second request waits for room")

	a.Release(held, Accepted, false)

	select {
	case err := <-admitted:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the waiter was not admitted after the release")
	}

	st, ok := a.State("a")
	require.True(t, ok)
	require.Equal(t, NodeState{Window: 100, Fraction: 1, Outstanding: 10}, st)
}

func TestAdmissionRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// held are the units admitted to node "a"; the first is released.
		held          []int64
		outcome       Outcome
		saturated     bool
		wantWindow    float64
		wantPinned    bool
		wantOutstding int64
	}{
		{
			name: "an accepted request leaves a full window full", held: []int64{10},
			outcome: Accepted, wantWindow: 100,
		},
		{
			name: "a rejected request shrinks from what was out, its own units among them", held: []int64{40, 20},
			outcome: Rejected, wantWindow: 30, wantPinned: true, wantOutstding: 20,
		},
		{
			name: "a failed request leaves the window alone", held: []int64{40, 20},
			outcome: Failed, wantWindow: 100, wantOutstding: 20,
		},
		{
			name: "a node that becomes saturated is pinned but not shrunk", held: []int64{10},
			outcome: Accepted, saturated: true, wantWindow: 100, wantPinned: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := NewAdmission(admissionConfig())
			tickets := make([]Ticket, len(tt.held))
			for i, units := range tt.held {
				var err error
				tickets[i], err = a.Acquire(t.Context(), "a", units)
				require.NoError(t, err)
			}

			a.Release(tickets[0], tt.outcome, tt.saturated)

			st, ok := a.State("a")
			require.True(t, ok)
			require.InDelta(t, tt.wantWindow, st.Window, 1e-9)
			require.Equal(t, tt.wantOutstding, st.Outstanding)
			require.Equal(t, tt.wantPinned, a.mu.nodes["a"].window.pinned > 0)
		})
	}
}

func TestAdmissionPinsOnceWhenANodeStaysSaturated(t *testing.T) {
	t.Parallel()

	a := NewAdmission(admissionConfig())
	release := func(saturated bool) {
		t.Helper()

		ticket, err := a.Acquire(t.Context(), "a", 10)
		require.NoError(t, err)
		a.Release(ticket, Accepted, saturated)
	}

	release(true)
	require.Equal(t, PinCompletions-1, a.mu.nodes["a"].window.pinned, "the pin starts on the first saturated release")

	release(true)
	require.Equal(t, PinCompletions-2, a.mu.nodes["a"].window.pinned, "a node that stays saturated is not pinned again")
}

func TestAdmissionReleaseOfZeroTicket(t *testing.T) {
	t.Parallel()

	a := NewAdmission(admissionConfig())
	a.Release(Ticket{}, Rejected, true)

	_, ok := a.State("a")
	require.False(t, ok)
}
