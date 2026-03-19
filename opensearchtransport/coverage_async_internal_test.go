// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

// ---------------------------------------------------------------------------
// BaseConnectionObserver no-op coverage
// ---------------------------------------------------------------------------

func TestBaseConnectionObserver_AllMethods(t *testing.T) {
	t.Parallel()
	var obs BaseConnectionObserver
	evt := ConnectionEvent{}

	// Each call should not panic.
	obs.OnPromote(evt)
	obs.OnDemote(evt)
	obs.OnOverloadDetected(evt)
	obs.OnOverloadCleared(evt)
	obs.OnDiscoveryAdd(evt)
	obs.OnDiscoveryRemove(evt)
	obs.OnDiscoveryUnchanged(evt)
	obs.OnHealthCheckPass(evt)
	obs.OnHealthCheckFail(evt)
	obs.OnStandbyPromote(evt)
	obs.OnStandbyDemote(evt)
	obs.OnWarmupRequest(evt)
	obs.OnRoute(RouteEvent{})
	obs.OnShardMapInvalidation(ShardMapInvalidationEvent{})
}

// ---------------------------------------------------------------------------
// healthCheckWithRetries
// ---------------------------------------------------------------------------

// validHealthCheckBody returns a minimal valid OpenSearch health check response.
func validHealthCheckBody() io.ReadCloser {
	return io.NopCloser(strings.NewReader(`{"name":"node-1","cluster_name":"test","version":{"number":"2.11.0"}}`))
}

func TestHealthCheckWithRetries(t *testing.T) {
	t.Parallel()

	t.Run("success on first attempt", func(t *testing.T) {
		t.Parallel()

		c := &Client{
			healthCheckTimeout: 100 * time.Millisecond,
			healthCheckJitter:  0,
			transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: validHealthCheckBody()}, nil
			}),
		}

		conn := createTestConnection("http://localhost:9200")
		ok := c.healthCheckWithRetries(context.Background(), conn, 3)
		require.True(t, ok)
	})

	t.Run("all retries fail", func(t *testing.T) {
		t.Parallel()

		c := &Client{
			healthCheckTimeout: 10 * time.Millisecond,
			healthCheckJitter:  0,
			transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return nil, &mockNetError{error: http.ErrServerClosed}
			}),
		}

		conn := createTestConnection("http://localhost:9200")
		ok := c.healthCheckWithRetries(context.Background(), conn, 2)
		require.False(t, ok)
	})
}

// ---------------------------------------------------------------------------
// scheduleProactiveHealthCheck
// ---------------------------------------------------------------------------

func TestScheduleProactiveHealthCheck(t *testing.T) {
	t.Parallel()

	t.Run("nil healthCheck is no-op", func(t *testing.T) {
		t.Parallel()
		c := &Client{} // healthCheck is nil
		conn := createTestConnection("http://localhost:9200")
		c.scheduleProactiveHealthCheck(conn) // should not panic
	})

	t.Run("invokes health check", func(t *testing.T) {
		t.Parallel()
		var checked atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c := &Client{
			ctx:                     ctx,
			resurrectTimeoutInitial: time.Millisecond,
			healthCheck: func(ctx context.Context, conn *Connection, u *url.URL) (*http.Response, error) {
				checked.Add(1)
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			},
		}

		conn := createTestConnection("http://localhost:9200")
		c.scheduleProactiveHealthCheck(conn)

		require.Eventually(t, func() bool {
			return checked.Load() >= 1
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("throttles repeated calls", func(t *testing.T) {
		t.Parallel()
		var checked atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c := &Client{
			ctx:                     ctx,
			resurrectTimeoutInitial: 5 * time.Second, // long throttle
			healthCheck: func(ctx context.Context, conn *Connection, u *url.URL) (*http.Response, error) {
				checked.Add(1)
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			},
		}

		conn := createTestConnection("http://localhost:9200")
		// First call should schedule
		c.scheduleProactiveHealthCheck(conn)
		require.Eventually(t, func() bool {
			return checked.Load() >= 1
		}, 2*time.Second, 10*time.Millisecond)

		// Second call within throttle window should be suppressed
		c.scheduleProactiveHealthCheck(conn)
		// Verify no additional health check fires
		require.Never(t, func() bool {
			return checked.Load() > 1
		}, 100*time.Millisecond, 10*time.Millisecond)
	})
}

// ---------------------------------------------------------------------------
// pollNodeStats
// ---------------------------------------------------------------------------

func TestPollNodeStats(t *testing.T) {
	t.Parallel()

	t.Run("singleServerPool with nil connection is skipped", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		c.mu.connectionPool = &singleServerPool{connection: nil}

		// Should not panic -- nil connection means nothing to poll.
		c.pollNodeStats()
	})

	t.Run("no-op for nil pool", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		c.pollNodeStats() // should not panic
	})
}

// ---------------------------------------------------------------------------
// PolicyChain.RotateStandby
// ---------------------------------------------------------------------------

func TestPolicyChainRotateStandby(t *testing.T) {
	t.Parallel()

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()
		chain := &PolicyChain{policies: []Policy{}}
		n, err := chain.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("delegates to sub-policies", func(t *testing.T) {
		t.Parallel()
		rr := NewRoundRobinPolicy()
		chain := &PolicyChain{policies: []Policy{rr}}
		n, err := chain.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n) // No standby connections
	})
}

// ---------------------------------------------------------------------------
// RolePolicy.RotateStandby and PoolSnapshot with pool
// ---------------------------------------------------------------------------

func TestRolePolicyRotateStandby(t *testing.T) {
	t.Parallel()

	policy, err := NewRolePolicy(RoleData)
	require.NoError(t, err)
	rp := policy.(*RolePolicy)

	// Give it an empty pool so RotateStandby doesn't nil-deref
	rp.pool = &multiServerPool{name: "role:data"}
	rp.pool.mu.members = map[*Connection]struct{}{}

	n, rotErr := rp.RotateStandby(context.Background(), 1)
	require.NoError(t, rotErr)
	require.Zero(t, n)
}

func TestRolePolicyPoolSnapshot_WithPool(t *testing.T) {
	t.Parallel()

	policy, err := NewRolePolicy(RoleData)
	require.NoError(t, err)
	rp := policy.(*RolePolicy)

	// Give it a pool with connections
	c1 := createTestConnection("http://n1:9200", "data")
	c2 := createTestConnection("http://n2:9200", "data")
	rp.pool = &multiServerPool{
		name: "role:data",
	}
	rp.pool.mu.ready = []*Connection{c1, c2}
	rp.pool.mu.activeCount = 2
	rp.pool.mu.members = map[*Connection]struct{}{c1: {}, c2: {}}

	snap := rp.PoolSnapshot()
	require.Equal(t, "role:data", snap.Name)
	require.Equal(t, 2, snap.ActiveCount)
}

// ---------------------------------------------------------------------------
// CoordinatorPolicy.PoolSnapshot with pool
// ---------------------------------------------------------------------------

func TestCoordinatorPolicyPoolSnapshot_WithPool(t *testing.T) {
	t.Parallel()

	cp := NewCoordinatorPolicy().(*CoordinatorPolicy)

	// Give it a pool
	c1 := createTestConnection("http://cm1:9200", "cluster_manager")
	cp.pool = &multiServerPool{
		name: "coordinator",
	}
	cp.pool.mu.ready = []*Connection{c1}
	cp.pool.mu.activeCount = 1
	cp.pool.mu.members = map[*Connection]struct{}{c1: {}}

	snap := cp.PoolSnapshot()
	require.Equal(t, "coordinator", snap.Name)
	require.Equal(t, 1, snap.ActiveCount)
}

// ---------------------------------------------------------------------------
// RouteBuilder.Pool
// ---------------------------------------------------------------------------

func TestRouteBuilderPool(t *testing.T) {
	t.Parallel()

	rr := NewRoundRobinPolicy()
	route := NewRoute("GET /_search", rr).Pool("search").MustBuild()
	rm := route.(*RouteMux)
	require.Equal(t, "search", rm.poolName)
}

// ---------------------------------------------------------------------------
// PolicyChain.setEnvOverride (router.go:65)
// ---------------------------------------------------------------------------

func TestPolicyChainSetEnvOverride(t *testing.T) {
	t.Parallel()

	chain := &PolicyChain{policies: []Policy{NewNullPolicy()}}

	chain.setEnvOverride(false)
	require.True(t, chain.policyState.Load()&psEnvDisabled != 0)

	chain.setEnvOverride(true)
	require.True(t, chain.policyState.Load()&psEnvEnabled != 0)
}
