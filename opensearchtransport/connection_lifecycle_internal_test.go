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
	"testing"
)

// ---------------------------------------------------------------------------
// casLifecycle unit tests
// ---------------------------------------------------------------------------

func TestCasLifecycle_SetAndClear(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive)))

	// Set lcNeedsWarmup
	ok := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if !ok {
		t.Fatal("casLifecycle set should return true")
	}
	lc := c.loadConnState().lifecycle()
	if !lc.has(lcNeedsWarmup) {
		t.Fatalf("expected lcNeedsWarmup set, got %s", lc)
	}
	if !lc.has(lcReady | lcActive) {
		t.Fatalf("expected lcReady|lcActive preserved, got %s", lc)
	}

	// Clear lcNeedsWarmup
	ok = c.casLifecycle(c.loadConnState(), 0, 0, lcNeedsWarmup)
	if !ok {
		t.Fatal("casLifecycle clear should return true")
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
	ok := c.casLifecycle(stale, lcOverloaded, lcActive, lcStandby)
	if ok {
		t.Fatal("casLifecycle should bail when conflict bits are mutated between load and CAS")
	}

	// Scenario 2: set/clear bits change between load and CAS.
	// Simulate: load state, then externally modify a masked bit before CAS.
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	stale = c.loadConnState()
	// Externally mutate lcActive -> lcStandby (lcActive is in our set mask, so auto-monitored)
	c.state.Store(int64(newConnState(lcReady | lcStandby)))
	ok = c.casLifecycle(stale, 0, lcActive, lcStandby)
	if ok {
		t.Fatal("casLifecycle should bail when set/clear bits mutated between load and CAS")
	}
}

func TestCasLifecycle_NoChangeNoop(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive | lcNeedsWarmup)))

	// Try to set a bit that's already set
	ok := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if ok {
		t.Fatal("casLifecycle should return false when no change needed")
	}

	// Try to clear a bit that's already clear
	c.state.Store(int64(newConnState(lcReady | lcActive)))
	ok = c.casLifecycle(c.loadConnState(), 0, 0, lcNeedsWarmup)
	if ok {
		t.Fatal("casLifecycle should return false when clearing an already-clear bit")
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
	ok := c.casLifecycle(c.loadConnState(), 0, lcNeedsWarmup, 0)
	if !ok {
		t.Fatal("casLifecycle should succeed")
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

	ok := c.setLifecycleBit(lcHealthChecking)
	if !ok {
		t.Fatal("first setLifecycleBit should return true")
	}

	ok = c.setLifecycleBit(lcHealthChecking)
	if ok {
		t.Fatal("second setLifecycleBit should return false (idempotent)")
	}

	if !c.loadConnState().lifecycle().has(lcHealthChecking) {
		t.Fatal("bit should still be set after idempotent call")
	}
}

func TestClearLifecycleBit_Idempotent(t *testing.T) {
	t.Parallel()

	c := &Connection{URL: &url.URL{Scheme: "http", Host: "test:9200"}}
	c.state.Store(int64(newConnState(lcReady | lcActive | lcHealthChecking)))

	ok := c.clearLifecycleBit(lcHealthChecking)
	if !ok {
		t.Fatal("first clearLifecycleBit should return true")
	}

	ok = c.clearLifecycleBit(lcHealthChecking)
	if ok {
		t.Fatal("second clearLifecycleBit should return false (idempotent)")
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
	ok := c.casLifecycle(c.loadConnState(), 0, lcStandby|lcOverloaded, lcActive)
	if !ok {
		t.Fatal("casLifecycle should succeed for multi-bit set+clear")
	}
	lc := c.loadConnState().lifecycle()
	if !lc.has(lcReady | lcStandby | lcOverloaded) {
		t.Fatalf("expected ready+standby+overloaded, got %s", lc)
	}
	if lc.has(lcActive) {
		t.Fatalf("expected lcActive cleared, got %s", lc)
	}
}
