// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// casLifecycle unit tests
// ---------------------------------------------------------------------------

func TestCasLifecycle_SetAndClear(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive)))

	// Set lcNeedsWarmup
	err := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if err != nil {
		t.Fatalf("casLifecycle set should return nil, got %v", err)
	}
	lc := c.loadConnState().lifecycle()
	if !lc.has(lcNeedsWarmup) {
		t.Fatalf("expected lcNeedsWarmup set, got %s", lc)
	}
	if !lc.has(lcReady | lcActive) {
		t.Fatalf("expected lcReady|lcActive preserved, got %s", lc)
	}

	// Clear lcNeedsWarmup
	err = c.casLifecycle(c.loadConnState(), 0, 0, lcNeedsWarmup)
	if err != nil {
		t.Fatalf("casLifecycle clear should return nil, got %v", err)
	}
	lc = c.loadConnState().lifecycle()
	if lc.has(lcNeedsWarmup) {
		t.Fatalf("expected lcNeedsWarmup cleared, got %s", lc)
	}
}

func TestCasLifecycle_ConflictBails(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}

	// Scenario 1: conflict bit changes between load and CAS.
	// Load with lcOverloaded set, then externally clear it before CAS.
	c.state.Store(int64(newConnState(lcReady | lcStandby | lcOverloaded)))
	stale := c.loadConnState()
	// Externally clear lcOverloaded (simulates concurrent health check clearing overload)
	c.state.Store(int64(newConnState(lcReady | lcStandby)))
	err := c.casLifecycle(stale, lcOverloaded, lcActive, lcStandby)
	if !errors.Is(err, errLifecycleConflict) {
		t.Fatalf("casLifecycle should return errLifecycleConflict when conflict bits are mutated, got %v", err)
	}

	// Scenario 2: set/clear bits change between load and CAS.
	// Simulate: load state, then externally modify a masked bit before CAS.
	// From the stale snapshot's perspective, lcActive is already set, so setting
	// it again is a noop (errLifecycleNoop). The CAS never reaches the point
	// where it would detect the conflict via CompareAndSwap.
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	stale = c.loadConnState()
	// Externally mutate lcActive -> lcStandby (lcActive is in our set mask, so auto-monitored)
	c.state.Store(int64(newConnState(lcReady | lcStandby)))
	err = c.casLifecycle(stale, 0, lcActive, lcStandby)
	if err == nil {
		t.Fatal("casLifecycle should fail when set/clear bits mutated between load and CAS")
	}
}

func TestCasLifecycle_NoChangeNoop(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive | lcNeedsWarmup)))

	// Try to set a bit that's already set
	err := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if !errors.Is(err, errLifecycleNoop) {
		t.Fatalf("casLifecycle should return errLifecycleNoop when no change needed, got %v", err)
	}

	// Try to clear a bit that's already clear
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	err = c.casLifecycle(c.loadConnState(), 0, 0, lcNeedsWarmup)
	if !errors.Is(err, errLifecycleNoop) {
		t.Fatalf("casLifecycle should return errLifecycleNoop when clearing an already-clear bit, got %v", err)
	}
}

func TestCasLifecycle_PreservesWarmup(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	// Set up state with warmup managers in the lower 52 bits
	lcMgr := packWarmupManager(16, 32)
	rdMgr := packWarmupManager(8, 4)
	state := packConnState(lcReady|lcActive, lcMgr, rdMgr)
	c.state.Store(int64(state))

	// Modify lifecycle bits
	err := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if err != nil {
		t.Fatalf("casLifecycle should succeed, got %v", err)
	}

	// Verify warmup managers are preserved
	newState := c.loadConnState()
	if newState.lifecycleManager() != lcMgr {
		t.Fatalf("lifecycleManager corrupted: got %v, want %v", newState.lifecycleManager(), lcMgr)
	}
	if newState.roundManager() != rdMgr {
		t.Fatalf("roundManager corrupted: got %v, want %v", newState.roundManager(), rdMgr)
	}
	if !newState.lifecycle().has(lcNeedsWarmup | lcReady | lcActive) {
		t.Fatalf("lifecycle bits wrong: got %s", newState.lifecycle())
	}
}

func TestSetLifecycleBit_Idempotent(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive)))

	err := c.setLifecycleBit(lcHealthChecking)
	if err != nil {
		t.Fatalf("first setLifecycleBit should return nil, got %v", err)
	}

	err = c.setLifecycleBit(lcHealthChecking)
	if err == nil {
		t.Fatal("second setLifecycleBit should return non-nil (idempotent)")
	}

	if !c.loadConnState().lifecycle().has(lcHealthChecking) {
		t.Fatal("bit should still be set after idempotent call")
	}
}

func TestClearLifecycleBit_Idempotent(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive | lcHealthChecking)))

	err := c.clearLifecycleBit(lcHealthChecking)
	if err != nil {
		t.Fatalf("first clearLifecycleBit should return nil, got %v", err)
	}

	err = c.clearLifecycleBit(lcHealthChecking)
	if err == nil {
		t.Fatal("second clearLifecycleBit should return non-nil (idempotent)")
	}

	if c.loadConnState().lifecycle().has(lcHealthChecking) {
		t.Fatal("bit should be clear after idempotent call")
	}
}

func TestConnectionMetric_IsHealthChecking(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}

	// Not health-checking
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	cm := buildConnectionMetric(c)
	if cm.IsHealthChecking {
		t.Fatal("expected IsHealthChecking=false for active connection")
	}

	// Health-checking
	c.state.Store(int64(newConnState(lcReady | lcActive | lcHealthChecking)))
	cm = buildConnectionMetric(c)
	if !cm.IsHealthChecking {
		t.Fatal("expected IsHealthChecking=true when lcHealthChecking set")
	}
}

func TestConnState_IsHealthChecking(t *testing.T) {
	t.Parallel()

	// Without lcHealthChecking
	state := newConnState(lcReady | lcActive)
	cs := ConnState{packed: int64(state)}
	if cs.IsHealthChecking() {
		t.Fatal("expected IsHealthChecking()=false")
	}

	// With lcHealthChecking
	state = newConnState(lcReady | lcActive | lcHealthChecking)
	cs = ConnState{packed: int64(state)}
	if !cs.IsHealthChecking() {
		t.Fatal("expected IsHealthChecking()=true")
	}
}

func TestCasLifecycle_MultipleSetClear(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive)))

	// Transition: active->standby with overloaded flag
	err := c.casLifecycle(c.loadConnState(), 0, lcStandby|lcOverloaded, lcActive)
	if err != nil {
		t.Fatalf("casLifecycle should succeed for multi-bit set+clear, got %v", err)
	}
	lc := c.loadConnState().lifecycle()
	if !lc.has(lcReady | lcStandby | lcOverloaded) {
		t.Fatalf("expected ready+standby+overloaded, got %s", lc)
	}
	if lc.has(lcActive) {
		t.Fatalf("expected lcActive cleared, got %s", lc)
	}
}

// ---------------------------------------------------------------------------
// ConnState exported methods
// ---------------------------------------------------------------------------

func TestConnState_WarmupRoundsRemaining(t *testing.T) {
	t.Parallel()

	t.Run("zero state returns 0", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		require.Equal(t, 0, cs.WarmupRoundsRemaining())
	})

	t.Run("warmup state returns configured rounds", func(t *testing.T) {
		t.Parallel()
		state := warmupState(lcReady|lcActive|lcNeedsWarmup, 10, 5)
		cs := ConnState{packed: int64(state)}
		require.Equal(t, 10, cs.WarmupRoundsRemaining())
	})

	t.Run("custom rdMgr rounds", func(t *testing.T) {
		t.Parallel()
		lcMgr := packWarmupManager(16, 32)
		rdMgr := packWarmupManager(7, 3)
		state := packConnState(lcReady|lcActive, lcMgr, rdMgr)
		cs := ConnState{packed: int64(state)}
		require.Equal(t, 7, cs.WarmupRoundsRemaining())
	})
}

func TestConnState_WarmupSkipRemaining(t *testing.T) {
	t.Parallel()

	t.Run("zero state returns 0", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		require.Equal(t, 0, cs.WarmupSkipRemaining())
	})

	t.Run("warmup state returns configured skip", func(t *testing.T) {
		t.Parallel()
		state := warmupState(lcReady|lcActive|lcNeedsWarmup, 10, 5)
		cs := ConnState{packed: int64(state)}
		require.Equal(t, 5, cs.WarmupSkipRemaining())
	})

	t.Run("custom rdMgr skip", func(t *testing.T) {
		t.Parallel()
		lcMgr := packWarmupManager(16, 32)
		rdMgr := packWarmupManager(8, 19)
		state := packConnState(lcReady|lcActive, lcMgr, rdMgr)
		cs := ConnState{packed: int64(state)}
		require.Equal(t, 19, cs.WarmupSkipRemaining())
	})
}

func TestConnState_Hex(t *testing.T) {
	t.Parallel()

	t.Run("zero warmup", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		hex := cs.Hex()
		require.Contains(t, hex, "0x")
		require.Contains(t, hex, "cfg(rnds=0,skip=0)")
		require.Contains(t, hex, "rd(rnds=0,skip=0)")
	})

	t.Run("warmup state", func(t *testing.T) {
		t.Parallel()
		lcMgr := packWarmupManager(16, 32)
		rdMgr := packWarmupManager(8, 4)
		state := packConnState(lcReady|lcActive|lcNeedsWarmup, lcMgr, rdMgr)
		cs := ConnState{packed: int64(state)}
		hex := cs.Hex()
		require.Contains(t, hex, "cfg(rnds=16,skip=32)")
		require.Contains(t, hex, "rd(rnds=8,skip=4)")
	})
}

func TestConnState_String(t *testing.T) {
	t.Parallel()

	t.Run("non-warming state", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		s := cs.String()
		require.NotContains(t, s, "warmup:")
		require.Contains(t, s, "ready")
		require.Contains(t, s, "active")
	})

	t.Run("warming state", func(t *testing.T) {
		t.Parallel()
		state := warmupState(lcReady|lcActive|lcNeedsWarmup, 9, 6)
		cs := ConnState{packed: int64(state)}
		s := cs.String()
		require.Contains(t, s, "(warmup: rounds=9, skip=6)")
	})
}

func TestConnState_IsWarmingUp(t *testing.T) {
	t.Parallel()

	t.Run("false with no managers", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		require.False(t, cs.IsWarmingUp())
	})

	t.Run("true with non-zero lifecycleManager", func(t *testing.T) {
		t.Parallel()
		lcMgr := packWarmupManager(10, 5)
		state := packConnState(lcReady|lcActive|lcNeedsWarmup, lcMgr, lcMgr)
		cs := ConnState{packed: int64(state)}
		require.True(t, cs.IsWarmingUp())
	})

	t.Run("false when only roundManager is nonzero", func(t *testing.T) {
		t.Parallel()
		// lifecycleManager is zero, roundManager is non-zero
		rdMgr := packWarmupManager(5, 3)
		state := packConnState(lcReady|lcActive, 0, rdMgr)
		cs := ConnState{packed: int64(state)}
		require.False(t, cs.IsWarmingUp())
	})
}

func TestConnState_IsHealthChecking_Extended(t *testing.T) {
	t.Parallel()

	t.Run("true when set", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive | lcHealthChecking))}
		require.True(t, cs.IsHealthChecking())
	})

	t.Run("false when not set", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{packed: int64(newConnState(lcReady | lcActive))}
		require.False(t, cs.IsHealthChecking())
	})

	t.Run("zero state", func(t *testing.T) {
		t.Parallel()
		cs := ConnState{}
		require.False(t, cs.IsHealthChecking())
	})
}

// ---------------------------------------------------------------------------
// connLifecycle.String
// ---------------------------------------------------------------------------

func TestConnLifecycle_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lc       connLifecycle
		contains []string
	}{
		{name: "zero value", lc: 0, contains: []string{"none"}},
		{name: "ready+active", lc: lcReady | lcActive, contains: []string{"ready", "active"}},
		{name: "unknown dead", lc: lcUnknown, contains: []string{"unknown"}},
		{name: "warming", lc: lcReady | lcActive | lcNeedsWarmup, contains: []string{"ready", "active", "needsWarmup"}},
		{name: "health checking", lc: lcReady | lcActive | lcHealthChecking, contains: []string{"healthChecking"}},
		{name: "multiple metadata", lc: lcReady | lcStandby | lcOverloaded | lcDraining, contains: []string{"overloaded", "draining", "standby"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tc.lc.String()
			for _, substr := range tc.contains {
				require.Contains(t, s, substr)
			}
			// Should contain binary representation
			require.Contains(t, s, "(")
		})
	}
}

// ---------------------------------------------------------------------------
// warmupManager
// ---------------------------------------------------------------------------

func TestWarmupManager_PackAndExtract(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		wm := packWarmupManager(16, 32)
		require.Equal(t, 16, wm.rounds())
		require.Equal(t, 32, wm.skipCount())
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()
		wm := packWarmupManager(0, 0)
		require.Equal(t, 0, wm.rounds())
		require.Equal(t, 0, wm.skipCount())
	})

	t.Run("max 8-bit", func(t *testing.T) {
		t.Parallel()
		wm := packWarmupManager(255, 255)
		require.Equal(t, 255, wm.rounds())
		require.Equal(t, 255, wm.skipCount())
	})
}

func TestWarmupManager_IsZero(t *testing.T) {
	t.Parallel()

	require.True(t, packWarmupManager(0, 0).isZero())
	require.False(t, packWarmupManager(1, 0).isZero())
	require.False(t, packWarmupManager(0, 1).isZero())
	require.False(t, packWarmupManager(1, 1).isZero())
}

func TestWarmupManager_WithRounds(t *testing.T) {
	t.Parallel()

	wm := packWarmupManager(10, 20)
	wm2 := wm.withRounds(5)
	require.Equal(t, 5, wm2.rounds())
	require.Equal(t, 20, wm2.skipCount(), "skipCount should be preserved")

	wm3 := wm.withRounds(0)
	require.Equal(t, 0, wm3.rounds())
	require.Equal(t, 20, wm3.skipCount())
}

func TestWarmupManager_WithSkipCount(t *testing.T) {
	t.Parallel()

	wm := packWarmupManager(10, 20)
	wm2 := wm.withSkipCount(7)
	require.Equal(t, 10, wm2.rounds(), "rounds should be preserved")
	require.Equal(t, 7, wm2.skipCount())

	wm3 := wm.withSkipCount(0)
	require.Equal(t, 10, wm3.rounds())
	require.Equal(t, 0, wm3.skipCount())
}

// ---------------------------------------------------------------------------
// h2StreamError
// ---------------------------------------------------------------------------

func TestH2StreamError_Error(t *testing.T) {
	t.Parallel()

	t.Run("basic formatting", func(t *testing.T) {
		t.Parallel()
		e := h2StreamError{StreamID: 42, Code: 7}
		require.Equal(t, "stream error: stream ID 42; HTTP/2 error code = 7", e.Error())
	})

	t.Run("zero values", func(t *testing.T) {
		t.Parallel()
		e := h2StreamError{}
		require.Equal(t, "stream error: stream ID 0; HTTP/2 error code = 0", e.Error())
	})

	t.Run("large stream ID", func(t *testing.T) {
		t.Parallel()
		e := h2StreamError{StreamID: 1<<31 - 1, Code: 2}
		s := e.Error()
		require.Contains(t, s, "2147483647")
	})

	t.Run("implements error interface", func(t *testing.T) {
		t.Parallel()
		var err error = h2StreamError{StreamID: 1, Code: 1}
		require.NotEmpty(t, err.Error())
		_ = fmt.Sprintf("%v", err) // should not panic
	})
}

// ---------------------------------------------------------------------------
// connLifecycle.has and hasAny
// ---------------------------------------------------------------------------

func TestConnLifecycle_HasAndHasAny(t *testing.T) {
	t.Parallel()

	lc := lcReady | lcActive | lcNeedsWarmup

	require.True(t, lc.has(lcReady))
	require.True(t, lc.has(lcActive))
	require.True(t, lc.has(lcReady|lcActive))
	require.False(t, lc.has(lcStandby))

	require.True(t, lc.hasAny(lcReady|lcStandby))
	require.False(t, lc.hasAny(lcStandby|lcOverloaded))
}

// ---------------------------------------------------------------------------
// newConnState / packConnState round-trip
// ---------------------------------------------------------------------------

func TestNewConnState_RoundTrip(t *testing.T) {
	t.Parallel()

	state := newConnState(lcReady | lcActive | lcHealthChecking)
	lc := state.lifecycle()
	require.True(t, lc.has(lcReady|lcActive|lcHealthChecking))
	require.Equal(t, warmupManager(0), state.lifecycleManager())
	require.Equal(t, warmupManager(0), state.roundManager())
}

func TestPackConnState_RoundTrip(t *testing.T) {
	t.Parallel()

	lcMgr := packWarmupManager(16, 32)
	rdMgr := packWarmupManager(8, 4)
	state := packConnState(lcReady|lcActive|lcNeedsWarmup, lcMgr, rdMgr)

	require.True(t, state.lifecycle().has(lcReady|lcActive|lcNeedsWarmup))
	require.Equal(t, lcMgr, state.lifecycleManager())
	require.Equal(t, rdMgr, state.roundManager())
}

// ---------------------------------------------------------------------------
// needsCatUpdate / setNeedsCatUpdate / clearNeedsCatUpdate
// ---------------------------------------------------------------------------

func TestNeedsCatUpdate(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive)))

	require.False(t, c.needsCatUpdate())

	err := c.setNeedsCatUpdate()
	require.NoError(t, err)
	require.True(t, c.needsCatUpdate())

	err = c.clearNeedsCatUpdate()
	require.NoError(t, err)
	require.False(t, c.needsCatUpdate())
}

// ---------------------------------------------------------------------------
// isReady
// ---------------------------------------------------------------------------

func TestIsReady(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}

	// Active -> ready
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	require.True(t, c.isReady())

	// Standby -> ready
	c.state.Store(int64(newConnState(lcReady | lcStandby)))
	require.True(t, c.isReady())

	// Unknown (dead) -> not ready
	c.state.Store(int64(newConnState(lcUnknown)))
	require.False(t, c.isReady())
}

// ---------------------------------------------------------------------------
// connLifecycle bit names coverage
// ---------------------------------------------------------------------------

func TestConnLifecycle_AllBitsNamed(t *testing.T) {
	t.Parallel()

	// Verify each named bit appears in its own String output
	bits := []struct {
		lc   connLifecycle
		name string
	}{
		{lcReady, "ready"},
		{lcUnknown, "unknown"},
		{lcActive, "active"},
		{lcStandby, "standby"},
		{lcNeedsWarmup, "needsWarmup"},
		{lcOverloaded, "overloaded"},
		{lcHealthChecking, "healthChecking"},
		{lcDraining, "draining"},
		{lcNeedsHardware, "needsHardware"},
		{lcNeedsCatUpdate, "needsCatUpdate"},
	}

	for _, b := range bits {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()
			s := b.lc.String()
			require.Contains(t, s, b.name,
				"expected %q in String() output %q", b.name, s)
		})
	}
}
