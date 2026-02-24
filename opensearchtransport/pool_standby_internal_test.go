// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newStandbyConn creates a connection in connStandby state.
func newStandbyConn(host string) *Connection {
	c := &Connection{URL: &url.URL{Scheme: "http", Host: host}}
	c.state.Store(int64(newConnState(lcStandby)))
	return c
}

// newActiveConn creates a connection in connActive state.
func newActiveConn(host string) *Connection {
	c := &Connection{URL: &url.URL{Scheme: "http", Host: host}}
	c.state.Store(int64(newConnState(lcActive)))
	return c
}

// newStandbyPool creates a multiServerPool with the given active and standby connections.
// Active connections are placed in ready[:len(active)], standby in ready[len(active):].
func newStandbyPool(active, standby []*Connection) *multiServerPool {
	pool := &multiServerPool{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
		jitterScale:                  defaultJitterScale,
		standbyPromotionChecks:       1, // Single health check for faster tests
	}

	pool.mu.ready = make([]*Connection, 0, len(active)+len(standby))
	for _, c := range active {
		c.state.Store(int64(newConnState(lcActive)))
		pool.mu.ready = append(pool.mu.ready, c)
	}
	pool.mu.activeCount = len(active)
	for _, c := range standby {
		c.state.Store(int64(newConnState(lcStandby)))
		pool.mu.ready = append(pool.mu.ready, c)
	}
	pool.mu.dead = []*Connection{}
	return pool
}

// alwaysHealthy is a health check function that always reports healthy.
func alwaysHealthy(_ context.Context, _ *Connection, _ *url.URL) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// alwaysUnhealthy is a health check function that always reports unhealthy.
func alwaysUnhealthy(_ context.Context, _ *Connection, _ *url.URL) (*http.Response, error) {
	return nil, errors.New("health check failed")
}

func TestEnforceReadyCapWithLock(t *testing.T) {
	t.Run("No cap", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1"), newActiveConn("a2"), newActiveConn("a3")},
			nil,
		)
		pool.activeListCap = 0 // disabled

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 3 {
			t.Errorf("Expected activeCount=3 (cap disabled), got=%d", pool.mu.activeCount)
		}
	})

	t.Run("Cap reduces active to standby", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1"), newActiveConn("a2"), newActiveConn("a3"), newActiveConn("a4")},
			nil,
		)
		pool.activeListCap = 2

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 2 {
			t.Errorf("Expected activeCount=2, got=%d", pool.mu.activeCount)
		}

		// Observer should have received standby_demote events for the 2 demoted connections
		require.Equal(t, 2, obs.count("standby_demote"))
		if len(pool.mu.ready) != 4 {
			t.Errorf("Expected total ready=4, got=%d", len(pool.mu.ready))
		}

		// Verify active connections have connActive state
		for i := range pool.mu.activeCount {
			state := pool.mu.ready[i].loadConnState()
			if !state.lifecycle().has(lcActive) {
				t.Errorf("ready[%d] expected connActive, got=%d", i, state)
			}
		}
		// Verify standby connections have connStandby state
		for i := pool.mu.activeCount; i < len(pool.mu.ready); i++ {
			state := pool.mu.ready[i].loadConnState()
			if !state.lifecycle().has(lcStandby) {
				t.Errorf("ready[%d] expected connStandby, got=%d", i, state)
			}
		}
	})

	t.Run("Cap already satisfied", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1"), newActiveConn("a2")},
			[]*Connection{newStandbyConn("s1")},
		)
		pool.activeListCap = 2

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 2 {
			t.Errorf("Expected activeCount=2 (unchanged), got=%d", pool.mu.activeCount)
		}
	})
}

func TestTryStandbyWithLock(t *testing.T) {
	t.Run("Promotes standby when active empty", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool(nil, []*Connection{s1, s2})

		pool.mu.Lock()
		promoted := pool.tryStandbyWithLock()
		pool.mu.Unlock()

		if promoted == nil {
			t.Fatal("Expected tryStandbyWithLock to return a connection")
		}
		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1 after promotion, got=%d", pool.mu.activeCount)
		}
		// First standby should now be active
		state := promoted.loadConnState()
		if !state.lifecycle().has(lcActive) {
			t.Errorf("Expected promoted connection to be connActive, got=%d", state)
		}
	})

	t.Run("Returns nil when no standby", func(t *testing.T) {
		pool := newStandbyPool(nil, nil)

		pool.mu.Lock()
		promoted := pool.tryStandbyWithLock()
		pool.mu.Unlock()

		if promoted != nil {
			t.Errorf("Expected nil with no standby, got=%s", promoted.URL)
		}
	})
}

func TestPromoteStandbyWithLock(t *testing.T) {
	t.Run("Promotes specific standby", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1, s2})

		pool.mu.Lock()
		pool.promoteStandbyWithLock(s2)
		pool.mu.Unlock()

		if pool.mu.activeCount != 2 {
			t.Errorf("Expected activeCount=2, got=%d", pool.mu.activeCount)
		}

		// s2 should now be in the active partition
		state := s2.loadConnState()
		if !state.lifecycle().has(lcActive) {
			t.Errorf("Expected promoted s2 to be connActive, got=%d", state)
		}
		inActive := false
		for i := range pool.mu.activeCount {
			if pool.mu.ready[i] == s2 {
				inActive = true
				break
			}
		}
		if !inActive {
			t.Error("Expected s2 in active partition")
		}
	})
}

func TestEnforceCapDemotesToStandby(t *testing.T) {
	t.Run("Reduces active partition and sets standby state", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		a3 := newActiveConn("a3")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1, a2, a3}, []*Connection{s1})
		pool.activeListCap = 1

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1, got=%d", pool.mu.activeCount)
		}
		if len(pool.mu.ready) != 4 {
			t.Errorf("Expected total ready=4, got=%d", len(pool.mu.ready))
		}

		// Verify the 3 standby connections have the correct state
		standbyCount := 0
		for i := pool.mu.activeCount; i < len(pool.mu.ready); i++ {
			state := pool.mu.ready[i].loadConnState()
			if state.lifecycle().has(lcStandby) {
				standbyCount++
			}
		}
		if standbyCount != 3 {
			t.Errorf("Expected 3 connections in standby state, got=%d", standbyCount)
		}
	})

	t.Run("Noop when active count at or below cap", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1")},
			[]*Connection{newStandbyConn("s1")},
		)
		pool.activeListCap = 2

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1 (unchanged), got=%d", pool.mu.activeCount)
		}
	})
}

func TestFindActiveCandidate(t *testing.T) {
	t.Run("Finds standby connection", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, []*Connection{s1, s2})

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found == nil {
			t.Fatal("Expected to find a standby connection")
		}

		// Should be marked with lcNeedsWarmup while retaining lcStandby
		state := found.loadConnState()
		if !state.lifecycle().has(lcStandby | lcNeedsWarmup) {
			t.Errorf("Expected lcStandby|lcNeedsWarmup, got=%s", state.lifecycle())
		}
	})

	t.Run("Finds demoted standby with lcNeedsWarmup already set", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, []*Connection{s1, s2})
		// Mark s2 as demoted (already has lcNeedsWarmup from cap enforcement)
		s2.state.Store(int64(newConnState(lcStandby | lcNeedsWarmup)))

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found == nil {
			t.Fatal("Expected to find a standby connection (demoted or fresh)")
		}
	})

	t.Run("Returns nil when no standby", func(t *testing.T) {
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, nil)

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found != nil {
			t.Errorf("Expected nil, got=%s", found.URL)
		}
	})

	t.Run("Skips overloaded standby", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, []*Connection{s1})
		s1.state.Store(int64(newConnState(lcStandby | lcOverloaded)))

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found != nil {
			t.Errorf("Expected nil when standby is overloaded, got=%s", found.URL)
		}
	})

	t.Run("Prefers idle over health-checking standby", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, []*Connection{s1, s2})
		// s2 is at the tail (searched first); mark it as health-checking
		s2.state.Store(int64(newConnState(lcStandby | lcHealthChecking)))

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found == nil {
			t.Fatal("Expected to find s1 (idle)")
		}
		if found != s1 {
			t.Errorf("Expected idle s1 over health-checking s2, got=%s", found.URL)
		}
	})

	t.Run("Falls back to health-checking standby", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, []*Connection{s1})
		s1.state.Store(int64(newConnState(lcStandby | lcHealthChecking)))

		pool.mu.Lock()
		found := pool.findActiveCandidate()
		pool.mu.Unlock()

		if found == nil {
			t.Fatal("Expected to fall back to health-checking standby")
		}
		if found != s1 {
			t.Errorf("Expected s1, got=%s", found.URL)
		}
	})
}

func TestPerformStandbyHealthCheck(t *testing.T) {
	t.Run("Passes with healthy check", func(t *testing.T) {
		pool := newStandbyPool(nil, nil)
		pool.healthCheck = alwaysHealthy
		pool.standbyPromotionChecks = 3

		conn := newStandbyConn("s1")
		if !pool.performStandbyHealthCheck(context.Background(), conn) {
			t.Error("Expected healthy")
		}
	})

	t.Run("Fails on unhealthy check", func(t *testing.T) {
		pool := newStandbyPool(nil, nil)
		pool.healthCheck = alwaysUnhealthy
		pool.standbyPromotionChecks = 3

		conn := newStandbyConn("s1")
		if pool.performStandbyHealthCheck(context.Background(), conn) {
			t.Error("Expected unhealthy")
		}
	})

	t.Run("Succeeds when no health check configured", func(t *testing.T) {
		pool := newStandbyPool(nil, nil)
		pool.healthCheck = nil
		pool.standbyPromotionChecks = 3

		conn := newStandbyConn("s1")
		if !pool.performStandbyHealthCheck(context.Background(), conn) {
			t.Error("Expected healthy when no health check configured")
		}
	})

	t.Run("Fails on partial health check failure", func(t *testing.T) {
		calls := 0
		pool := newStandbyPool(nil, nil)
		pool.healthCheck = func(_ context.Context, _ *Connection, _ *url.URL) (*http.Response, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("intermittent failure")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}
		pool.standbyPromotionChecks = 3

		conn := newStandbyConn("s1")
		if pool.performStandbyHealthCheck(context.Background(), conn) {
			t.Error("Expected unhealthy on partial failure")
		}
		if calls != 2 {
			t.Errorf("Expected 2 calls (fail on 2nd), got=%d", calls)
		}
	})
}

func TestRotateStandbyOnce(t *testing.T) {
	t.Run("Successful rotation promotes standby with deferred eviction", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = alwaysHealthy

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		attempted, rotated := pool.rotateStandbyOnce(context.Background())

		if !attempted {
			t.Error("Expected attempted=true")
		}
		if !rotated {
			t.Error("Expected rotated=true")
		}
		// With deferred eviction, activeCount grows by 1 (no immediate swap)
		if pool.mu.activeCount != 2 {
			t.Errorf("Expected activeCount=2 (deferred eviction), got=%d", pool.mu.activeCount)
		}
		// s1 should now be in active partition
		s1InActive := false
		for i := range pool.mu.activeCount {
			if pool.mu.ready[i] == s1 {
				s1InActive = true
				break
			}
		}
		if !s1InActive {
			t.Error("Expected s1 in active partition")
		}
		state := s1.loadConnState()
		if !state.lifecycle().has(lcActive) {
			t.Errorf("Expected s1 to be connActive, got=%d", state)
		}
		// a1 should still be in active partition (eviction deferred)
		a1InActive := false
		for i := range pool.mu.activeCount {
			if pool.mu.ready[i] == a1 {
				a1InActive = true
				break
			}
		}
		if !a1InActive {
			t.Error("Expected a1 still in active partition (deferred eviction)")
		}
		state = a1.loadConnState()
		if !state.lifecycle().has(lcActive) {
			t.Errorf("Expected a1 to still be connActive, got=%d", state)
		}

		// Observer should have received a standby_promote event
		require.Equal(t, 1, obs.count("standby_promote"))
	})

	t.Run("No standby available", func(t *testing.T) {
		pool := newStandbyPool([]*Connection{newActiveConn("a1")}, nil)
		pool.healthCheck = alwaysHealthy

		attempted, rotated := pool.rotateStandbyOnce(context.Background())

		if attempted {
			t.Error("Expected attempted=false with no standby")
		}
		if rotated {
			t.Error("Expected rotated=false with no standby")
		}
	})

	t.Run("Health check failure moves standby to dead", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = alwaysUnhealthy

		attempted, rotated := pool.rotateStandbyOnce(context.Background())

		if !attempted {
			t.Error("Expected attempted=true")
		}
		if rotated {
			t.Error("Expected rotated=false on health check failure")
		}
		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1 (unchanged), got=%d", pool.mu.activeCount)
		}
		// s1 should be in dead list, not ready
		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready connection (only a1), got=%d", len(pool.mu.ready))
		}
		if len(pool.mu.dead) != 1 {
			t.Errorf("Expected 1 dead connection (s1), got=%d", len(pool.mu.dead))
		}
	})

	t.Run("Promotes to active when no active connections exist", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool(nil, []*Connection{s1})
		pool.healthCheck = alwaysHealthy

		attempted, rotated := pool.rotateStandbyOnce(context.Background())

		if !attempted || !rotated {
			t.Error("Expected attempted=true, rotated=true")
		}
		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1, got=%d", pool.mu.activeCount)
		}
		state := s1.loadConnState()
		if !state.lifecycle().has(lcActive) {
			t.Errorf("Expected s1 to be connActive, got=%d", state)
		}
	})
}

func TestRotateStandby(t *testing.T) {
	t.Run("Rotates requested count", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		s3 := newStandbyConn("s3")
		pool := newStandbyPool(
			[]*Connection{a1, a2},
			[]*Connection{s1, s2, s3},
		)
		pool.healthCheck = alwaysHealthy

		rotated := pool.rotateStandby(context.Background(), 2)

		if rotated != 2 {
			t.Errorf("Expected 2 rotations, got=%d", rotated)
		}
		// With deferred eviction, activeCount grows by 2 (one per rotation)
		if pool.mu.activeCount != 4 {
			t.Errorf("Expected activeCount=4 (deferred eviction), got=%d", pool.mu.activeCount)
		}
	})

	t.Run("Stops when no standby remaining", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1")},
			[]*Connection{newStandbyConn("s1")},
		)
		pool.healthCheck = alwaysHealthy

		rotated := pool.rotateStandby(context.Background(), 5)

		// With deferred eviction, first rotation promotes s1 to active (activeCount 1->2).
		// No standby remains, so only 1 rotation is possible.
		if rotated != 1 {
			t.Errorf("Expected 1 rotation (no standby after first promote), got=%d", rotated)
		}
	})

	t.Run("Continues past health check failures", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1-good")
		s2 := newStandbyConn("s2-bad")
		// s2 is at the tail, so findActiveCandidate picks it first (backward search)
		pool := newStandbyPool(
			[]*Connection{a1},
			[]*Connection{s1, s2},
		)
		pool.healthCheck = func(_ context.Context, c *Connection, _ *url.URL) (*http.Response, error) {
			if c.URL.Host == "s2-bad" {
				return nil, errors.New("unhealthy")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}

		rotated := pool.rotateStandby(context.Background(), 1)

		if rotated != 1 {
			t.Errorf("Expected 1 rotation (s2 fails, s1 succeeds), got=%d", rotated)
		}
		// s2 should be dead, s1 should have been rotated in
		if len(pool.mu.dead) != 1 {
			t.Errorf("Expected 1 dead (s2-bad), got=%d", len(pool.mu.dead))
		}
	})

	t.Run("Returns zero when all standby fail health checks", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1")},
			[]*Connection{newStandbyConn("s1"), newStandbyConn("s2")},
		)
		pool.healthCheck = alwaysUnhealthy

		rotated := pool.rotateStandby(context.Background(), 2)

		if rotated != 0 {
			t.Errorf("Expected 0 rotations (all unhealthy), got=%d", rotated)
		}
		// Both standbys should be dead now
		if len(pool.mu.dead) != 2 {
			t.Errorf("Expected 2 dead connections, got=%d", len(pool.mu.dead))
		}
		// Only active connections remain ready
		if len(pool.mu.ready) != 1 {
			t.Errorf("Expected 1 ready (a1 only), got=%d", len(pool.mu.ready))
		}
	})

	t.Run("Zero count returns zero", func(t *testing.T) {
		pool := newStandbyPool(
			[]*Connection{newActiveConn("a1")},
			[]*Connection{newStandbyConn("s1")},
		)
		pool.healthCheck = alwaysHealthy

		rotated := pool.rotateStandby(context.Background(), 0)
		if rotated != 0 {
			t.Errorf("Expected 0 rotations for count=0, got=%d", rotated)
		}
	})
}

func TestNextWithStandby(t *testing.T) {
	t.Run("Next only returns active connections", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1, a2}, []*Connection{s1})

		seen := make(map[string]bool)
		for range 10 {
			c, err := pool.Next()
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
			seen[c.URL.Host] = true
		}

		if seen["s1"] {
			t.Error("Next() returned standby connection s1")
		}
		if !seen["a1"] || !seen["a2"] {
			t.Errorf("Expected both a1 and a2 to be returned, seen=%v", seen)
		}
	})

	t.Run("Next promotes standby when all active exhausted", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool(nil, []*Connection{s1})

		c, err := pool.Next()
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if c.URL.Host != "s1" {
			t.Errorf("Expected s1 promoted, got=%s", c.URL.Host)
		}
		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1 after promotion, got=%d", pool.mu.activeCount)
		}
	})
}

func TestDemoteOverloaded(t *testing.T) {
	t.Run("active to standby with overloaded flag", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		s1 := newStandbyConn("s1") // non-overloaded standby to fill the gap
		pool := newStandbyPool([]*Connection{a1, a2}, []*Connection{s1})

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		pool.demoteOverloaded(a1)

		// a1 should have overloaded flag
		lc := a1.loadConnState().lifecycle()
		if !lc.has(lcOverloaded) {
			t.Errorf("Expected lcOverloaded set, got=%s", lc)
		}
		a1.mu.RLock()
		if a1.mu.overloadedAt.IsZero() {
			t.Error("Expected overloadedAt to be set")
		}
		a1.mu.RUnlock()

		// Verify a1 is NOT in active partition
		a1InActive := false
		for i := range pool.mu.activeCount {
			if pool.mu.ready[i] == a1 {
				a1InActive = true
			}
		}
		if a1InActive {
			t.Error("Expected a1 removed from active partition")
		}

		// Observer should have received an overload_detected event
		require.Equal(t, 1, obs.count("overload_detected"))
		events := obs.get("overload_detected")
		require.Equal(t, "http://a1", events[0].URL)
	})

	t.Run("already standby just adds flag", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})

		pool.demoteOverloaded(s1)

		// activeCount unchanged
		if pool.mu.activeCount != 1 {
			t.Errorf("Expected activeCount=1 (unchanged), got=%d", pool.mu.activeCount)
		}
		lc := s1.loadConnState().lifecycle()
		if !lc.has(lcOverloaded) {
			t.Errorf("Expected lcOverloaded set, got=%s", lc)
		}
	})

	t.Run("already dead just adds flag", func(t *testing.T) {
		a1 := newActiveConn("a1")
		pool := newStandbyPool([]*Connection{a1}, nil)
		deadConn := &Connection{URL: &url.URL{Scheme: "http", Host: "dead1"}}
		deadConn.state.Store(int64(newConnState(lcUnknown)))
		pool.mu.dead = append(pool.mu.dead, deadConn)

		pool.demoteOverloaded(deadConn)

		lc := deadConn.loadConnState().lifecycle()
		if !lc.has(lcOverloaded) {
			t.Errorf("Expected lcOverloaded set on dead conn, got=%s", lc)
		}
	})
}

func TestPromoteFromOverloaded(t *testing.T) {
	t.Run("clears overloaded flag and timestamp", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})

		obs := newRecordingObserver()
		var iface ConnectionObserver = obs
		pool.observer.Store(&iface)

		// Set overloaded
		s1.setLifecycleBit(lcOverloaded)
		s1.mu.Lock()
		s1.mu.overloadedAt = time.Now()
		s1.mu.Unlock()

		pool.promoteFromOverloaded(s1)

		lc := s1.loadConnState().lifecycle()
		if lc.has(lcOverloaded) {
			t.Errorf("Expected lcOverloaded cleared, got=%s", lc)
		}
		s1.mu.RLock()
		if !s1.mu.overloadedAt.IsZero() {
			t.Error("Expected overloadedAt to be zeroed")
		}
		s1.mu.RUnlock()

		// Observer should have received an overload_cleared event
		require.Equal(t, 1, obs.count("overload_cleared"))
		events := obs.get("overload_cleared")
		require.Equal(t, "http://s1", events[0].URL)
	})

	t.Run("no-op when not overloaded", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool(nil, []*Connection{s1})

		pool.promoteFromOverloaded(s1)

		lc := s1.loadConnState().lifecycle()
		if lc.has(lcOverloaded) {
			t.Errorf("Expected no lcOverloaded, got=%s", lc)
		}
	})
}

func TestEvictUnknownFromReadyWithLock(t *testing.T) {
	t.Run("moves unknown from ready to dead", func(t *testing.T) {
		a1 := newActiveConn("a1")
		unknown := &Connection{URL: &url.URL{Scheme: "http", Host: "unknown1"}}
		unknown.state.Store(int64(newConnState(lcUnknown)))
		pool := newStandbyPool([]*Connection{a1}, []*Connection{unknown})

		pool.mu.Lock()
		pool.evictUnknownFromReadyWithLock(unknown)
		pool.mu.Unlock()

		if len(pool.mu.dead) != 1 {
			t.Errorf("Expected 1 dead, got=%d", len(pool.mu.dead))
		}
		if pool.mu.dead[0] != unknown {
			t.Error("Expected unknown conn in dead list")
		}
		// Should not be in ready anymore
		for _, c := range pool.mu.ready {
			if c == unknown {
				t.Error("Expected unknown conn removed from ready list")
			}
		}
	})
}

func TestEnforceCapWithWarmingConnections(t *testing.T) {
	t.Run("warming connections exempt from cap enforcement", func(t *testing.T) {
		// 3 active connections: 1 warming, 2 non-warming. Cap = 2.
		// The warming connection should be exempt, so nonWarmCount = 2 == cap -> no-op.
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		a3 := newActiveConn("a3-warming")

		pool := newStandbyPool([]*Connection{a1, a2, a3}, nil)
		pool.activeListCap = 2

		// Set warming state AFTER pool creation (newStandbyPool overwrites state)
		a3.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 10, 5)))

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		// nonWarmCount (2) <= cap (2), so no eviction should happen
		require.Equal(t, 3, pool.mu.activeCount)
	})

	t.Run("warming connections interleaved during cap enforcement", func(t *testing.T) {
		// 4 active: 1 warming + 3 non-warming. Cap = 2.
		// nonWarmCount = 3 > cap = 2 -> evict 1 non-warming, keep warming.
		a1 := newActiveConn("a1")
		warming := newActiveConn("w1")
		a2 := newActiveConn("a2")
		a3 := newActiveConn("a3")

		pool := newStandbyPool([]*Connection{a1, warming, a2, a3}, nil)
		pool.activeListCap = 2

		// Set warming state AFTER pool creation
		warming.state.Store(int64(warmupState(lcActive|lcNeedsWarmup, 10, 5)))

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		// newActiveCount = warmCount(1) + cap(2) = 3
		require.Equal(t, 3, pool.mu.activeCount)
		require.Len(t, pool.mu.ready, 4)

		// The demoted connection should be standby
		demoted := pool.mu.ready[3]
		require.True(t, demoted.loadConnState().lifecycle().has(lcStandby))
	})
}

func TestPromoteStandbyWithLockNotFound(t *testing.T) {
	t.Run("returns false when connection not in standby partition", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})

		notInPool := newStandbyConn("orphan")

		pool.mu.Lock()
		ok := pool.promoteStandbyWithLock(notInPool)
		pool.mu.Unlock()

		require.False(t, ok)
		require.Equal(t, 1, pool.mu.activeCount) // unchanged
	})
}

func TestHealthcheckStartPaths(t *testing.T) {
	t.Run("candidate becomes lcUnknown during health check", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = func(_ context.Context, c *Connection, _ *url.URL) (*http.Response, error) {
			// Simulate concurrent state change to lcUnknown during health check
			c.state.Store(int64(newConnState(lcUnknown)))
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}

		candidate, attempted := pool.healthcheckStart(context.Background())

		require.True(t, attempted)
		require.Nil(t, candidate) // evicted as unknown
		require.Len(t, pool.mu.dead, 1)
		require.Equal(t, "s1", pool.mu.dead[0].URL.Host)
	})

	t.Run("unhealthy candidate moved to dead", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = alwaysUnhealthy

		candidate, attempted := pool.healthcheckStart(context.Background())

		require.True(t, attempted)
		require.Nil(t, candidate)
		require.Len(t, pool.mu.dead, 1)
		require.Equal(t, "s1", pool.mu.dead[0].URL.Host)
	})

	t.Run("candidate removed from standby during health check", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = func(_ context.Context, _ *Connection, _ *url.URL) (*http.Response, error) {
			// Simulate concurrent removal: remove s1 from ready
			pool.mu.Lock()
			pool.mu.ready = pool.mu.ready[:pool.mu.activeCount]
			pool.mu.Unlock()
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}

		candidate, attempted := pool.healthcheckStart(context.Background())

		require.True(t, attempted)
		require.Nil(t, candidate) // not found in standby after health check
	})

	t.Run("healthy candidate returned", func(t *testing.T) {
		a1 := newActiveConn("a1")
		s1 := newStandbyConn("s1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})
		pool.healthCheck = alwaysHealthy

		candidate, attempted := pool.healthcheckStart(context.Background())

		require.True(t, attempted)
		require.NotNil(t, candidate)
		require.Equal(t, "s1", candidate.URL.Host)
	})
}

func TestPromoteStandbyGracefullyWithLock(t *testing.T) {
	t.Run("no-op when gap is zero", func(t *testing.T) {
		s1 := newStandbyConn("s1")
		pool := newStandbyPool(nil, []*Connection{s1})

		pool.mu.Lock()
		pool.promoteStandbyGracefullyWithLock(context.Background(), 0)
		pool.mu.Unlock()

		// Nothing should change
		require.Equal(t, 0, pool.mu.activeCount)
	})

	t.Run("no-op when no standby available", func(t *testing.T) {
		a1 := newActiveConn("a1")
		pool := newStandbyPool([]*Connection{a1}, nil)

		pool.mu.Lock()
		pool.promoteStandbyGracefullyWithLock(context.Background(), 2)
		pool.mu.Unlock()

		require.Equal(t, 1, pool.mu.activeCount)
	})
}

func TestPartitionConsistency(t *testing.T) {
	t.Run("partition integrity after enforce cap", func(t *testing.T) {
		conns := make([]*Connection, 6)
		for i := range conns {
			conns[i] = newActiveConn("a" + string(rune('1'+i)))
		}
		pool := newStandbyPool(conns, nil)
		pool.activeListCap = 3

		pool.mu.Lock()
		pool.enforceActiveCapWithLock()
		pool.mu.Unlock()

		if pool.mu.activeCount != 3 {
			t.Errorf("Expected activeCount=3, got=%d", pool.mu.activeCount)
		}
		for i := range pool.mu.activeCount {
			if !pool.mu.ready[i].loadConnState().lifecycle().has(lcActive) {
				t.Errorf("ready[%d] expected connActive, got=%d", i, pool.mu.ready[i].loadConnState())
			}
		}
		for i := pool.mu.activeCount; i < len(pool.mu.ready); i++ {
			if !pool.mu.ready[i].loadConnState().lifecycle().has(lcStandby) {
				t.Errorf("ready[%d] expected connStandby, got=%d", i, pool.mu.ready[i].loadConnState())
			}
		}
	})

	t.Run("partition integrity after rotation", func(t *testing.T) {
		a1 := newActiveConn("a1")
		a2 := newActiveConn("a2")
		s1 := newStandbyConn("s1")
		s2 := newStandbyConn("s2")
		pool := newStandbyPool([]*Connection{a1, a2}, []*Connection{s1, s2})
		pool.healthCheck = alwaysHealthy

		pool.rotateStandby(context.Background(), 2)

		for i := range pool.mu.activeCount {
			if !pool.mu.ready[i].loadConnState().lifecycle().has(lcActive) {
				t.Errorf("ready[%d] expected connActive, got=%d url=%s", i, pool.mu.ready[i].loadConnState(), pool.mu.ready[i].URL.Host)
			}
		}
		for i := pool.mu.activeCount; i < len(pool.mu.ready); i++ {
			if !pool.mu.ready[i].loadConnState().lifecycle().has(lcStandby) {
				t.Errorf("ready[%d] expected connStandby, got=%d url=%s", i, pool.mu.ready[i].loadConnState(), pool.mu.ready[i].URL.Host)
			}
		}
	})
}
