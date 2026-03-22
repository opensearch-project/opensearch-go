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
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRequestManagerPackUnpack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rounds    int
		skipCount int
	}{
		{"zero values", 0, 0},
		{"default warmup", 8, 8},
		{"max rounds", 255, 0},
		{"max skip count", 0, 255},
		{"both max", 255, 255},
		{"typical values", 16, 16},
		{"rounds only", 42, 0},
		{"skip only", 0, 42},
		{"asymmetric", 8, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rm := packWarmupManager(tt.rounds, tt.skipCount)

			if got := rm.rounds(); got != tt.rounds {
				t.Errorf("rounds: got=%d want=%d", got, tt.rounds)
			}
			if got := rm.skipCount(); got != tt.skipCount {
				t.Errorf("skipCount: got=%d want=%d", got, tt.skipCount)
			}
		})
	}
}

// TestRequestManagerWithSkipCountPreservesRounds validates that withSkipCount
// replaces the skip field without corrupting the rounds field.
// This is a targeted regression test for operator-precedence bugs in
// the bit manipulation.
func TestRequestManagerWithSkipCountPreservesRounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		initRounds int
		initSkip   int
		newSkip    int
		wantRounds int
		wantSkip   int
	}{
		{"8,8 -> skip=7", 8, 8, 7, 8, 7},
		{"8,8 -> skip=0", 8, 8, 0, 8, 0},
		{"8,0 -> skip=5", 8, 0, 5, 8, 5},
		{"1,1 -> skip=0", 1, 1, 0, 1, 0},
		{"255,255 -> skip=0", 255, 255, 0, 255, 0},
		{"255,0 -> skip=255", 255, 0, 255, 255, 255},
		{"7,0 -> skip=6", 7, 0, 6, 7, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rm := packWarmupManager(tt.initRounds, tt.initSkip)
			rm2 := rm.withSkipCount(tt.newSkip)

			if got := rm2.rounds(); got != tt.wantRounds {
				t.Errorf("rounds after withSkipCount: got=%d want=%d", got, tt.wantRounds)
			}
			if got := rm2.skipCount(); got != tt.wantSkip {
				t.Errorf("skipCount after withSkipCount: got=%d want=%d", got, tt.wantSkip)
			}
		})
	}
}

// TestRequestManagerWithRoundsPreservesSkip validates that withRounds
// replaces the rounds field without corrupting the skip field.
func TestRequestManagerWithRoundsPreservesSkip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		initRounds int
		initSkip   int
		newRounds  int
		wantRounds int
		wantSkip   int
	}{
		{"8,8 -> rounds=7", 8, 8, 7, 7, 8},
		{"8,8 -> rounds=0", 8, 8, 0, 0, 8},
		{"1,255 -> rounds=0", 1, 255, 0, 0, 255},
		{"255,255 -> rounds=1", 255, 255, 1, 1, 255},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rm := packWarmupManager(tt.initRounds, tt.initSkip)
			rm2 := rm.withRounds(tt.newRounds)

			if got := rm2.rounds(); got != tt.wantRounds {
				t.Errorf("rounds after withRounds: got=%d want=%d", got, tt.wantRounds)
			}
			if got := rm2.skipCount(); got != tt.wantSkip {
				t.Errorf("skipCount after withRounds: got=%d want=%d", got, tt.wantSkip)
			}
		})
	}
}

// TestConnStateRoundTrip validates that all connState fields survive pack -> unpack
// without cross-contamination between the lifecycle, lcMgr, and rdMgr fields.
func TestConnStateRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lc       connLifecycle
		lcRounds int
		lcSkip   int
		rdRounds int
		rdSkip   int
	}{
		{"all zero", lcActive, 0, 0, 0, 0},
		{"default warmup", lcActive, 8, 8, 8, 8},
		{"asymmetric managers", lcActive, 8, 4, 6, 3},
		{"rd partially consumed", lcActive, 8, 8, 5, 3},
		{"standby warming", lcStandby | lcNeedsWarmup, 8, 8, 8, 8},
		{"dead with warmup", lcDead, 8, 8, 4, 2},
		{"max values", lcDraining, 255, 255, 255, 255},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lcMgr := packWarmupManager(tt.lcRounds, tt.lcSkip)
			rdMgr := packWarmupManager(tt.rdRounds, tt.rdSkip)
			state := packConnState(tt.lc, lcMgr, rdMgr)

			if got := state.lifecycle(); got != tt.lc {
				t.Errorf("lifecycle: got=%d want=%d", got, tt.lc)
			}

			gotLcMgr := state.lifecycleManager()
			if gotLcMgr.rounds() != tt.lcRounds {
				t.Errorf("lcMgr.rounds: got=%d want=%d", gotLcMgr.rounds(), tt.lcRounds)
			}
			if gotLcMgr.skipCount() != tt.lcSkip {
				t.Errorf("lcMgr.skipCount: got=%d want=%d", gotLcMgr.skipCount(), tt.lcSkip)
			}

			gotRdMgr := state.roundManager()
			if gotRdMgr.rounds() != tt.rdRounds {
				t.Errorf("rdMgr.rounds: got=%d want=%d", gotRdMgr.rounds(), tt.rdRounds)
			}
			if gotRdMgr.skipCount() != tt.rdSkip {
				t.Errorf("rdMgr.skipCount: got=%d want=%d", gotRdMgr.skipCount(), tt.rdSkip)
			}
		})
	}
}

// TestConnStateWithManagersPreservesLifecycle validates that withManagers
// replaces both managers without corrupting the lifecycle field.
func TestConnStateWithManagersPreservesLifecycle(t *testing.T) {
	t.Parallel()

	for _, lc := range []connLifecycle{lcActive, lcStandby, lcDead, lcUnknown | lcOverloaded, lcDraining} {
		state := packConnState(lc, packWarmupManager(8, 8), packWarmupManager(8, 8))
		newLcMgr := packWarmupManager(3, 2)
		newRdMgr := packWarmupManager(1, 0)
		state2 := state.withManagers(newLcMgr, newRdMgr)

		if got := state2.lifecycle(); got != lc {
			t.Errorf("lc=%d: lifecycle changed to %d after withManagers", lc, got)
		}
		if got := state2.lifecycleManager(); got != newLcMgr {
			t.Errorf("lc=%d: lcMgr mismatch after withManagers", lc)
		}
		if got := state2.roundManager(); got != newRdMgr {
			t.Errorf("lc=%d: rdMgr mismatch after withManagers", lc)
		}
	}
}

func TestConnLifecycleHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lc        connLifecycle
		active    bool
		dead      bool
		standby   bool
		overload  bool
		sbWarming bool
	}{
		{lcActive, true, false, false, false, false},
		{lcStandby, false, false, true, false, false},
		{lcStandby | lcNeedsWarmup, false, false, true, false, true},
		{lcDead, false, true, false, false, false},
		{lcUnknown | lcOverloaded, false, true, false, true, false},
		{lcDraining, false, false, false, false, false},
	}

	for _, tt := range tests {
		if got := tt.lc.has(lcActive); got != tt.active {
			t.Errorf("lc=%d has(lcActive): got=%v want=%v", tt.lc, got, tt.active)
		}
		// isDead: lcUnknown set and no position bits (lcActive|lcStandby)
		if got := tt.lc.has(lcUnknown) && tt.lc&(lcActive|lcStandby) == 0; got != tt.dead {
			t.Errorf("lc=%d isDead: got=%v want=%v", tt.lc, got, tt.dead)
		}
		if got := tt.lc.has(lcStandby); got != tt.standby {
			t.Errorf("lc=%d has(lcStandby): got=%v want=%v", tt.lc, got, tt.standby)
		}
		if got := tt.lc.has(lcOverloaded); got != tt.overload {
			t.Errorf("lc=%d has(lcOverloaded): got=%v want=%v", tt.lc, got, tt.overload)
		}
		if got := tt.lc.has(lcStandby | lcNeedsWarmup); got != tt.sbWarming {
			t.Errorf("lc=%d has(lcStandby|lcNeedsWarmup): got=%v want=%v", tt.lc, got, tt.sbWarming)
		}
	}
}

// TestWarmupProgression drives tryWarmupSkip in a simple loop -- no foreknowledge
// of when skips vs accepts should happen -- and proves the total evaluation count.
//
// For warmupState(8, 8) the smoothstep-decayed progression is:
//
//	Round 8: skip 8, accept 1   (evaluations: 9)
//	Round 7: skip 7, accept 1   (evaluations: 8)
//	Round 6: skip 6, accept 1   (evaluations: 7)
//	Round 5: skip 5, accept 1   (evaluations: 6)
//	Round 4: skip 4, accept 1   (evaluations: 5)
//	Round 3: skip 2, accept 1   (evaluations: 3)
//	Round 2: skip 1, accept 1   (evaluations: 2)
//	Round 1: skip 0, accept 1   (evaluations: 1)
//	                              ------------
//	Total skips:   33
//	Total accepts:  8  (one per round)
//	Total evals:   41
func TestWarmupProgression(t *testing.T) {
	t.Parallel()

	conn := &Connection{URL: &url.URL{Scheme: "http", Host: "warmup-test:9200"}}
	conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	conn.startWarmup(8, 8)

	state := conn.loadConnState()
	if !state.lifecycle().has(lcActive) {
		t.Fatalf("Expected lcActive bit set in lifecycle, got=%s", state.lifecycle())
	}
	if !state.isWarmingUp() {
		t.Fatal("Expected warming up after startWarmup")
	}

	// Verify initial state: both managers should be (8, 8).
	lcMgr := state.lifecycleManager()
	rdMgr := state.roundManager()
	if lcMgr.rounds() != 8 || lcMgr.skipCount() != 8 {
		t.Fatalf("Initial lcMgr: got (%d,%d), want (8,8)", lcMgr.rounds(), lcMgr.skipCount())
	}
	if rdMgr.rounds() != 8 || rdMgr.skipCount() != 8 {
		t.Fatalf("Initial rdMgr: got (%d,%d), want (8,8)", rdMgr.rounds(), rdMgr.skipCount())
	}

	// Drive tryWarmupSkip until warmup completes, recording what happens.
	type roundRecord struct {
		round      int // rdMgr.rounds() at the start of this round
		skips      int // number of skip=true results in this round
		acceptSkip int // rdMgr.skipCount() at accept (should be 0)
	}

	var (
		totalEvals   int
		totalSkips   int
		totalAccepts int
		rounds       []roundRecord
		curRound     = 8 // expected first round
		curSkips     int
	)

	// Smoothstep-decayed expected skips per round (8,8): 8,7,6,5,4,2,1,0
	expectedSkipsPerRound := []int{8, 7, 6, 5, 4, 2, 1, 0}

	const maxEvals = 200 // safety limit to prevent infinite loop on bugs
	for totalEvals < maxEvals {
		result := conn.tryWarmupSkip()
		if result == warmupInactive {
			break
		}
		totalEvals++

		switch result { //nolint:exhaustive // warmupInactive handled above
		case warmupSkipped:
			totalSkips++
			curSkips++
		case warmupAccepted:
			totalAccepts++
			// Record the round that just completed.
			st := conn.loadConnState()
			rounds = append(rounds, roundRecord{
				round:      curRound,
				skips:      curSkips,
				acceptSkip: st.roundManager().skipCount(),
			})
			curRound--
			curSkips = 0
		}
	}

	// Warmup must be complete.
	if conn.loadConnState().isWarmingUp() {
		t.Fatal("Warmup did not complete within maxEvals")
	}

	// Verify totals (smoothstep for 8,8: 33 skips, 8 accepts, 41 evals).
	if totalSkips != 33 {
		t.Errorf("Total skips: got=%d want=33", totalSkips)
	}
	if totalAccepts != 8 {
		t.Errorf("Total accepts: got=%d want=8", totalAccepts)
	}
	if totalEvals != 41 {
		t.Errorf("Total evaluations: got=%d want=41", totalEvals)
	}

	// Verify per-round skip counts match smoothstep progression.
	for i, rec := range rounds {
		if i < len(expectedSkipsPerRound) && rec.skips != expectedSkipsPerRound[i] {
			t.Errorf("Round %d: got %d skips, want %d", rec.round, rec.skips, expectedSkipsPerRound[i])
		}
	}

	// Verify lifecycle is still lcActive and lcNeedsWarmup is cleared after warmup completion.
	finalState := conn.loadConnState()
	if !finalState.lifecycle().has(lcActive) {
		t.Errorf("Final lifecycle: expected lcActive bit set, got=%s", finalState.lifecycle())
	}
	if finalState.lifecycle().has(lcNeedsWarmup) {
		t.Error("Final lifecycle should not have lcNeedsWarmup after warmup completion")
	}

	// After warmup complete, tryWarmupSkip should return warmupInactive.
	if result := conn.tryWarmupSkip(); result != warmupInactive {
		t.Errorf("tryWarmupSkip after warmup complete: got=%d want=%d (warmupInactive)", result, warmupInactive)
	}
}

// TestWarmupProgressionTable is a table-driven test that validates warmup
// evaluation counts for various (rounds, skipCount) combinations. Each test
// case blindly calls tryWarmupSkip() until warmup completes and verifies
// the total skip, accept, and evaluation counts.
//
// The smoothstep decay formula is:
//
//	skip = maxSkip * (R^3 - 3*d^2*R + 2*d^3) / R^3
//
// where d = rounds elapsed, R = maxRounds. This produces an S-shaped decay
// that always reaches 0 by the final round.
//
// +----------------------------------------------------------------------+
// | rounds=8, skip=8  (symmetric -- default)                            |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   8   |   8   | initial                                            |
// |   7   |   7   | d=1, 8*(504)/512=7                                 |
// |   6   |   6   | d=2, 8*(432)/512=6                                 |
// |   5   |   5   | d=3, 8*(350)/512=5                                 |
// |   4   |   4   | d=4, 8*(256)/512=4                                 |
// |   3   |   2   | d=5, 8*(162)/512=2                                 |
// |   2   |   1   | d=6, 8*(80)/512=1                                  |
// |   1   |   0   | d=7, 8*(22)/512=0                                  |
// +-------+-------+-----------------------------------------------------+
// | Skips: 33   Accepts: 8   Evals: 41                                 |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=8, skip=4  (skip < rounds)                                  |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   8   |   4   | initial                                            |
// |   7   |   3   | d=1, 4*(504)/512=3                                 |
// |   6   |   3   | d=2, 4*(432)/512=3                                 |
// |   5   |   2   | d=3, 4*(350)/512=2                                 |
// |   4   |   2   | d=4, 4*(256)/512=2                                 |
// |   3   |   1   | d=5, 4*(162)/512=1                                 |
// |   2   |   0   | d=6, 4*(80)/512=0                                  |
// |   1   |   0   | d=7, 4*(22)/512=0                                  |
// +-------+-------+-----------------------------------------------------+
// | Skips: 15   Accepts: 8   Evals: 23                                 |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=4, skip=8  (skip > rounds)                                  |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   4   |   8   | initial                                            |
// |   3   |   6   | d=1, 8*(54)/64=6                                   |
// |   2   |   4   | d=2, 8*(32)/64=4                                   |
// |   1   |   1   | d=3, 8*(10)/64=1                                   |
// +-------+-------+-----------------------------------------------------+
// | Skips: 19   Accepts: 4   Evals: 23                                 |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=4, skip=16  (skip >> rounds -- JVM soak period)              |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   4   |  16   | initial                                            |
// |   3   |  13   | d=1, 16*(54)/64=13                                 |
// |   2   |   8   | d=2, 16*(32)/64=8                                  |
// |   1   |   2   | d=3, 16*(10)/64=2                                  |
// +-------+-------+-----------------------------------------------------+
// | Skips: 39   Accepts: 4   Evals: 43                                 |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=1, skip=1  (minimal warmup)                                 |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation                                         |
// |   1   |   1   | initial                                            |
// +-------+-------+-----------------------------------------------------+
// | Skips: 1    Accepts: 1   Evals: 2                                  |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=2, skip=8  (few rounds, many skips)                         |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   2   |   8   | initial                                            |
// |   1   |   4   | d=1, 8*(6)/8=4 [note: not 8*(4)/8 due to R^3=8]    |
// +-------+-------+-----------------------------------------------------+
// | Skips: 12   Accepts: 2   Evals: 14                                 |
// +---------------------------------------------------------------------+
//
// +----------------------------------------------------------------------+
// | rounds=3, skip=1  (many rounds, few skips -- early zero-skip tail)  |
// +-------+-------+-----------------------------------------------------+
// | Round | Skips | Derivation (smoothstep)                             |
// |   3   |   1   | initial                                            |
// |   2   |   0   | d=1, 1*(20)/27=0                                   |
// |   1   |   0   | d=2, 1*(5)/27=0                                    |
// +-------+-------+-----------------------------------------------------+
// | Skips: 1    Accepts: 3   Evals: 4                                  |
// +---------------------------------------------------------------------+
func TestWarmupProgressionTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rounds      int
		skipCount   int
		wantSkips   int
		wantAccepts int
		wantEvals   int
		perRound    []int // expected skip count per round, from first to last
	}{
		{
			name:   "rounds=8,skip=8 (symmetric default)",
			rounds: 8, skipCount: 8,
			wantSkips: 33, wantAccepts: 8, wantEvals: 41,
			perRound: []int{8, 7, 6, 5, 4, 2, 1, 0},
		},
		{
			name:   "rounds=8,skip=4 (skip < rounds)",
			rounds: 8, skipCount: 4,
			wantSkips: 15, wantAccepts: 8, wantEvals: 23,
			perRound: []int{4, 3, 3, 2, 2, 1, 0, 0},
		},
		{
			name:   "rounds=4,skip=8 (skip > rounds)",
			rounds: 4, skipCount: 8,
			wantSkips: 19, wantAccepts: 4, wantEvals: 23,
			perRound: []int{8, 6, 4, 1},
		},
		{
			name:   "rounds=4,skip=16 (skip >> rounds)",
			rounds: 4, skipCount: 16,
			wantSkips: 39, wantAccepts: 4, wantEvals: 43,
			perRound: []int{16, 13, 8, 2},
		},
		{
			name:   "rounds=1,skip=1 (minimal)",
			rounds: 1, skipCount: 1,
			wantSkips: 1, wantAccepts: 1, wantEvals: 2,
			perRound: []int{1},
		},
		{
			name:   "rounds=2,skip=8 (few rounds, many skips)",
			rounds: 2, skipCount: 8,
			wantSkips: 12, wantAccepts: 2, wantEvals: 14,
			perRound: []int{8, 4},
		},
		{
			name:   "rounds=3,skip=1 (many rounds, few skips)",
			rounds: 3, skipCount: 1,
			wantSkips: 1, wantAccepts: 3, wantEvals: 4,
			perRound: []int{1, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := &Connection{URL: &url.URL{Scheme: "http", Host: "table-test:9200"}}
			conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
			conn.startWarmup(tt.rounds, tt.skipCount)

			var (
				totalEvals   int
				totalSkips   int
				totalAccepts int
				roundSkips   []int // observed skips per round
				curSkips     int
			)

			const maxEvals = 1000
			for totalEvals < maxEvals {
				result := conn.tryWarmupSkip()
				if result == warmupInactive {
					break
				}
				totalEvals++

				switch result { //nolint:exhaustive // warmupInactive handled above
				case warmupSkipped:
					totalSkips++
					curSkips++
				case warmupAccepted:
					totalAccepts++
					roundSkips = append(roundSkips, curSkips)
					curSkips = 0
				}
			}

			if conn.loadConnState().isWarmingUp() {
				t.Fatal("Warmup did not complete within maxEvals")
			}

			if totalSkips != tt.wantSkips {
				t.Errorf("Total skips: got=%d want=%d", totalSkips, tt.wantSkips)
			}
			if totalAccepts != tt.wantAccepts {
				t.Errorf("Total accepts: got=%d want=%d", totalAccepts, tt.wantAccepts)
			}
			if totalEvals != tt.wantEvals {
				t.Errorf("Total evaluations: got=%d want=%d", totalEvals, tt.wantEvals)
			}

			// Verify per-round skip counts match expected progression.
			if len(roundSkips) != len(tt.perRound) {
				t.Fatalf("Round count: got=%d want=%d (skips per round: %v)",
					len(roundSkips), len(tt.perRound), roundSkips)
			}
			for i, want := range tt.perRound {
				if roundSkips[i] != want {
					t.Errorf("Round %d: got %d skips, want %d (full: %v)",
						tt.rounds-i, roundSkips[i], want, roundSkips)
				}
			}

			// Lifecycle must still have lcActive with warmup cleared.
			finalState := conn.loadConnState()
			if !finalState.lifecycle().has(lcActive) {
				t.Errorf("Final lifecycle: expected lcActive bit set, got=%s", finalState.lifecycle())
			}
			if finalState.lifecycle().has(lcNeedsWarmup) {
				t.Error("Final lifecycle should not have lcNeedsWarmup after warmup completion")
			}
			if finalState.isWarmingUp() {
				t.Error("isWarmingUp() should be false after completion")
			}

			// Post-warmup tryWarmupSkip must return warmupInactive.
			if result := conn.tryWarmupSkip(); result != warmupInactive {
				t.Errorf("tryWarmupSkip after warmup complete: got=%d want=%d (warmupInactive)", result, warmupInactive)
			}
		})
	}
}

func TestWarmupNoWarmup(t *testing.T) {
	t.Parallel()

	conn := &Connection{URL: &url.URL{Scheme: "http", Host: "no-warmup:9200"}}
	conn.state.Store(int64(newConnState(lcActive)))

	// No warmup configured -- tryWarmupSkip should return warmupInactive.
	if result := conn.tryWarmupSkip(); result != warmupInactive {
		t.Errorf("tryWarmupSkip with no warmup: got=%d want=%d (warmupInactive)", result, warmupInactive)
	}
}

func TestWarmupClearOnDeath(t *testing.T) {
	t.Parallel()

	conn := &Connection{URL: &url.URL{Scheme: "http", Host: "clear-test:9200"}}
	conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	conn.startWarmup(8, 8)

	if !conn.loadConnState().isWarmingUp() {
		t.Fatal("Expected warming up")
	}

	// Simulate death -- clear warmup
	conn.clearWarmup()

	state := conn.loadConnState()
	if state.isWarmingUp() {
		t.Error("Expected warmup cleared after clearWarmup()")
	}
	if !state.lifecycle().has(lcActive) {
		t.Errorf("clearWarmup should preserve lcActive, got=%s", state.lifecycle())
	}
}

// TestWarmupConcurrentSkip proves that tryWarmupSkip's CAS loop produces
// exactly the expected skip and accept totals even under heavy goroutine
// contention. Uses the same parameter combinations as TestWarmupProgressionTable
// to ensure the concurrent path converges to the same invariants as the
// single-threaded path.
//
// Per-round ordering cannot be verified under concurrency (goroutines
// interleave freely), but the aggregate totals must be exact because every
// CAS operation atomically moves the state forward by exactly one step.
func TestWarmupConcurrentSkip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rounds      int
		skipCount   int
		wantSkips   int64
		wantAccepts int64
	}{
		{"rounds=8,skip=8", 8, 8, 33, 8},
		{"rounds=8,skip=4", 8, 4, 15, 8},
		{"rounds=4,skip=8", 4, 8, 19, 4},
		{"rounds=4,skip=16", 4, 16, 39, 4},
		{"rounds=1,skip=1", 1, 1, 1, 1},
		{"rounds=2,skip=8", 2, 8, 12, 2},
		{"rounds=3,skip=1", 3, 1, 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := &Connection{URL: &url.URL{Scheme: "http", Host: "concurrent:9200"}}
			conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
			conn.startWarmup(tt.rounds, tt.skipCount)

			var wg sync.WaitGroup
			var skips, accepts atomic.Int64

			const goroutines = 16
			for range goroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						switch conn.tryWarmupSkip() {
						case warmupSkipped:
							skips.Add(1)
						case warmupAccepted:
							accepts.Add(1)
						case warmupInactive:
							return
						}
					}
				}()
			}

			wg.Wait()

			if conn.loadConnState().isWarmingUp() {
				t.Fatal("Warmup did not complete")
			}
			if got := skips.Load(); got != tt.wantSkips {
				t.Errorf("Concurrent skips: got=%d want=%d", got, tt.wantSkips)
			}
			if got := accepts.Load(); got != tt.wantAccepts {
				t.Errorf("Concurrent accepts: got=%d want=%d", got, tt.wantAccepts)
			}
		})
	}
}

func TestWarmupSmallValues(t *testing.T) {
	t.Parallel()

	// Test with minimal warmup: 1 round, 1 skip
	conn := &Connection{URL: &url.URL{Scheme: "http", Host: "small:9200"}}
	conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	conn.startWarmup(1, 1)

	// Should skip once
	if result := conn.tryWarmupSkip(); result != warmupSkipped {
		t.Errorf("Expected warmupSkipped on first call, got=%d", result)
	}

	// Should accept and complete warmup
	if result := conn.tryWarmupSkip(); result != warmupAccepted {
		t.Errorf("Expected warmupAccepted on second call, got=%d", result)
	}

	if conn.loadConnState().isWarmingUp() {
		t.Error("Expected warmup complete after 1 round, 1 skip")
	}

	// Post-warmup should return warmupInactive
	if result := conn.tryWarmupSkip(); result != warmupInactive {
		t.Errorf("Expected warmupInactive after completion, got=%d", result)
	}
}

func TestWarmupState(t *testing.T) {
	t.Parallel()

	state := warmupState(lcReady|lcActive, 8, 8)
	if !state.lifecycle().has(lcActive) {
		t.Errorf("warmupState lifecycle: got=%d, expected lcActive bit set", state.lifecycle())
	}

	lcMgr := state.lifecycleManager()
	rdMgr := state.roundManager()

	if lcMgr.rounds() != 8 || lcMgr.skipCount() != 8 {
		t.Errorf("warmupState lcMgr: got=(%d,%d) want=(8,8)", lcMgr.rounds(), lcMgr.skipCount())
	}
	if rdMgr.rounds() != 8 || rdMgr.skipCount() != 8 {
		t.Errorf("warmupState rdMgr: got=(%d,%d) want=(8,8)", rdMgr.rounds(), rdMgr.skipCount())
	}
}
