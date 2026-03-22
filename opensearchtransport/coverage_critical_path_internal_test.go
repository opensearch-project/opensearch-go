// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Phase 1a: PolicyChain.OnSuccess branch coverage
// ---------------------------------------------------------------------------

func TestPolicyChainOnSuccess(t *testing.T) {
	t.Parallel()

	makeChain := func(t *testing.T) *PolicyChain {
		t.Helper()
		rr := NewRoundRobinPolicy().(*RoundRobinPolicy)
		chain := &PolicyChain{policies: []Policy{rr}}
		require.NoError(t, chain.configurePolicySettings(createTestConfig()))
		return chain
	}

	t.Run("alive connection fast path", func(t *testing.T) {
		t.Parallel()
		chain := makeChain(t)
		conn := createTestConnection("http://node:9200")
		// conn is alive (deadSince is zero) -- OnSuccess should return immediately
		chain.OnSuccess(conn)
		// Verify still alive
		conn.mu.RLock()
		require.True(t, conn.mu.deadSince.IsZero())
		conn.mu.RUnlock()
	})

	t.Run("dead connection resurrected", func(t *testing.T) {
		t.Parallel()
		chain := makeChain(t)
		conn := createDeadTestConnection("http://node:9200")

		conn.mu.RLock()
		require.False(t, conn.mu.deadSince.IsZero(), "precondition: must be dead")
		conn.mu.RUnlock()

		chain.OnSuccess(conn)

		conn.mu.RLock()
		require.True(t, conn.mu.deadSince.IsZero(), "OnSuccess should mark dead connection healthy")
		conn.mu.RUnlock()
	})

	t.Run("draining connection skipped", func(t *testing.T) {
		t.Parallel()
		chain := makeChain(t)
		conn := createDeadTestConnection("http://node:9200")
		conn.drainingQuiescingRemaining.Store(3)

		conn.mu.RLock()
		deadBefore := conn.mu.deadSince
		conn.mu.RUnlock()

		chain.OnSuccess(conn)

		conn.mu.RLock()
		require.Equal(t, deadBefore, conn.mu.deadSince, "draining connection must stay dead")
		conn.mu.RUnlock()
	})

	t.Run("overloaded connection skipped", func(t *testing.T) {
		t.Parallel()
		chain := makeChain(t)
		conn := createDeadTestConnection("http://node:9200")
		// Add lcOverloaded to lifecycle bits
		conn.state.Store(int64(newConnState(lcDead | lcOverloaded | lcNeedsWarmup)))

		conn.mu.RLock()
		deadBefore := conn.mu.deadSince
		conn.mu.RUnlock()

		chain.OnSuccess(conn)

		conn.mu.RLock()
		require.Equal(t, deadBefore, conn.mu.deadSince, "overloaded connection must stay dead")
		conn.mu.RUnlock()
	})

	t.Run("concurrent resurrection race", func(t *testing.T) {
		t.Parallel()
		chain := makeChain(t)
		conn := createDeadTestConnection("http://node:9200")

		var wg sync.WaitGroup
		const goroutines = 10
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				chain.OnSuccess(conn)
			}()
		}
		wg.Wait()

		conn.mu.RLock()
		require.True(t, conn.mu.deadSince.IsZero(), "after concurrent OnSuccess, connection must be healthy")
		conn.mu.RUnlock()
	})
}

// ---------------------------------------------------------------------------
// Phase 1b: getNextActiveConnWithLock + deferredStandbyPromotion
// ---------------------------------------------------------------------------

// testSelector is a mock poolSelector that returns controlled values.
type testSelector struct {
	conn      *Connection
	activeCap int
	err       error
}

func (s *testSelector) selectNext(ready []*Connection, activeCount int) (*Connection, int, int, error) {
	if s.err != nil {
		return nil, 0, 0, s.err
	}
	if s.conn != nil {
		return s.conn, s.activeCap, capRemain, nil
	}
	return ready[0], s.activeCap, capRemain, nil
}

func makeTestPool(name string, conns []*Connection, activeCount int, sel poolSelector) *multiServerPool {
	pool := &multiServerPool{
		name:     name,
		selector: sel,
	}
	pool.mu.ready = conns
	pool.mu.activeCount = activeCount
	return pool
}

func TestGetNextActiveConnWithLock(t *testing.T) {
	t.Parallel()

	makeConns := func(n int) []*Connection {
		conns := make([]*Connection, n)
		for i := range n {
			u, _ := url.Parse("http://node:920" + string(rune('0'+i)))
			conns[i] = &Connection{URL: u}
			conns[i].state.Store(int64(newConnState(lcActive)))
		}
		return conns
	}

	t.Run("selector capRemain returns conn", func(t *testing.T) {
		t.Parallel()
		conns := makeConns(3)
		sel := &testSelector{conn: conns[1], activeCap: capRemain}
		pool := makeTestPool("test", conns, 3, sel)

		pool.mu.RLock()
		got := pool.getNextActiveConnWithLock()
		pool.mu.RUnlock()

		require.Same(t, conns[1], got)
	})

	t.Run("selector error returns nil", func(t *testing.T) {
		t.Parallel()
		conns := makeConns(2)
		sel := &testSelector{err: errors.New("selection failed")}
		pool := makeTestPool("test", conns, 2, sel)

		pool.mu.RLock()
		got := pool.getNextActiveConnWithLock()
		pool.mu.RUnlock()

		require.Nil(t, got)
	})

	t.Run("selector capGrow triggers standby promotion", func(t *testing.T) {
		t.Parallel()
		conns := makeConns(4) // 2 active + 2 standby
		for i := 2; i < 4; i++ {
			conns[i].state.Store(int64(newConnState(lcStandby)))
		}
		sel := &testSelector{conn: conns[0], activeCap: capGrow}
		pool := makeTestPool("test", conns, 2, sel)

		pool.mu.RLock()
		got := pool.getNextActiveConnWithLock()
		pool.mu.RUnlock()

		require.Same(t, conns[0], got)
		// deferredStandbyPromotion runs async -- wait for it
		require.Eventually(t, func() bool {
			pool.mu.RLock()
			defer pool.mu.RUnlock()
			return pool.mu.activeCount == 3
		}, 2*time.Second, 10*time.Millisecond, "activeCount should increase from standby promotion")
	})

	t.Run("nil selector uses round-robin", func(t *testing.T) {
		t.Parallel()
		conns := makeConns(3)
		pool := makeTestPool("test", conns, 3, nil)

		pool.mu.RLock()
		c1 := pool.getNextActiveConnWithLock()
		c2 := pool.getNextActiveConnWithLock()
		c3 := pool.getNextActiveConnWithLock()
		pool.mu.RUnlock()

		// Round-robin should cycle through all connections
		seen := map[*Connection]bool{c1: true, c2: true, c3: true}
		require.Len(t, seen, 3, "round-robin should cycle through all connections")
	})
}

func TestDeferredStandbyPromotion(t *testing.T) {
	t.Parallel()

	t.Run("promotes standby to active", func(t *testing.T) {
		t.Parallel()
		conns := make([]*Connection, 3)
		for i := range conns {
			u, _ := url.Parse("http://node:920" + string(rune('0'+i)))
			conns[i] = &Connection{URL: u}
		}
		conns[0].state.Store(int64(newConnState(lcActive)))
		conns[1].state.Store(int64(newConnState(lcActive)))
		conns[2].state.Store(int64(newConnState(lcStandby | lcNeedsWarmup)))

		pool := makeTestPool("test", conns, 2, nil)

		pool.deferredStandbyPromotion()

		require.Equal(t, 3, pool.mu.activeCount)
	})

	t.Run("no-op when all connections active", func(t *testing.T) {
		t.Parallel()
		conns := make([]*Connection, 2)
		for i := range conns {
			u, _ := url.Parse("http://node:920" + string(rune('0'+i)))
			conns[i] = &Connection{URL: u}
			conns[i].state.Store(int64(newConnState(lcActive)))
		}
		pool := makeTestPool("test", conns, 2, nil)

		pool.deferredStandbyPromotion()

		require.Equal(t, 2, pool.mu.activeCount, "should not change when no standby")
	})
}

// ---------------------------------------------------------------------------
// Phase 1d: extractActiveConnsFromPolicy
// ---------------------------------------------------------------------------

func TestExtractActiveConnsFromPolicy(t *testing.T) {
	t.Parallel()

	makePoolWithConns := func(name string, conns []*Connection) *multiServerPool {
		pool := &multiServerPool{name: name}
		pool.mu.ready = conns
		pool.mu.activeCount = len(conns)
		return pool
	}

	t.Run("RolePolicy with active conns", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{createTestConnection("http://n1:9200", RoleData)}
		rp := &RolePolicy{pool: makePoolWithConns("role:data", conns)}
		got := extractActiveConnsFromPolicy(rp)
		require.Len(t, got, 1)
		require.Same(t, conns[0], got[0])
	})

	t.Run("RolePolicy with nil pool", func(t *testing.T) {
		t.Parallel()
		rp := &RolePolicy{pool: nil}
		require.Nil(t, extractActiveConnsFromPolicy(rp))
	})

	t.Run("RolePolicy with zero active", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{name: "role:data"}
		pool.mu.ready = []*Connection{createTestConnection("http://n1:9200")}
		pool.mu.activeCount = 0
		rp := &RolePolicy{pool: pool}
		require.Nil(t, extractActiveConnsFromPolicy(rp))
	})

	t.Run("RoundRobinPolicy with active conns", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{createTestConnection("http://n1:9200")}
		rr := &RoundRobinPolicy{pool: makePoolWithConns("roundrobin", conns)}
		got := extractActiveConnsFromPolicy(rr)
		require.Len(t, got, 1)
	})

	t.Run("RoundRobinPolicy with nil pool", func(t *testing.T) {
		t.Parallel()
		rr := &RoundRobinPolicy{pool: nil}
		require.Nil(t, extractActiveConnsFromPolicy(rr))
	})

	t.Run("RoundRobinPolicy with zero active", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{name: "roundrobin"}
		pool.mu.ready = []*Connection{createTestConnection("http://n1:9200")}
		pool.mu.activeCount = 0
		rr := &RoundRobinPolicy{pool: pool}
		require.Nil(t, extractActiveConnsFromPolicy(rr))
	})

	t.Run("IfEnabledPolicy recurses into true branch", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{createTestConnection("http://n1:9200", RoleData)}
		truePolicy := &RolePolicy{pool: makePoolWithConns("role:data", conns)}
		falsePolicy := &NullPolicy{}
		ie := &IfEnabledPolicy{truePolicy: truePolicy, falsePolicy: falsePolicy}
		got := extractActiveConnsFromPolicy(ie)
		require.Len(t, got, 1)
	})

	t.Run("IfEnabledPolicy falls through to false branch", func(t *testing.T) {
		t.Parallel()
		emptyPool := &multiServerPool{name: "empty"}
		emptyPool.mu.ready = nil
		emptyPool.mu.activeCount = 0
		truePolicy := &RolePolicy{pool: nil}
		conns := []*Connection{createTestConnection("http://n1:9200")}
		falsePolicy := &RoundRobinPolicy{pool: makePoolWithConns("roundrobin", conns)}
		ie := &IfEnabledPolicy{truePolicy: truePolicy, falsePolicy: falsePolicy}
		got := extractActiveConnsFromPolicy(ie)
		require.Len(t, got, 1)
	})

	t.Run("IfEnabledPolicy nil false branch", func(t *testing.T) {
		t.Parallel()
		truePolicy := &RolePolicy{pool: nil}
		ie := &IfEnabledPolicy{truePolicy: truePolicy, falsePolicy: nil}
		require.Nil(t, extractActiveConnsFromPolicy(ie))
	})

	t.Run("PolicyChain recurses into sub-policies", func(t *testing.T) {
		t.Parallel()
		emptyPolicy := &RolePolicy{pool: nil}
		conns := []*Connection{createTestConnection("http://n1:9200")}
		filledPolicy := &RoundRobinPolicy{pool: makePoolWithConns("roundrobin", conns)}
		chain := &PolicyChain{policies: []Policy{emptyPolicy, filledPolicy}}
		got := extractActiveConnsFromPolicy(chain)
		require.Len(t, got, 1)
	})

	t.Run("PolicyChain empty returns nil", func(t *testing.T) {
		t.Parallel()
		chain := &PolicyChain{policies: []Policy{&NullPolicy{}}}
		require.Nil(t, extractActiveConnsFromPolicy(chain))
	})

	t.Run("unknown policy type returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, extractActiveConnsFromPolicy(&NullPolicy{}))
	})
}
