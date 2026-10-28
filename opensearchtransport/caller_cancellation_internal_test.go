// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestCallerCancellationPreservesConnectionHealth(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"pool", "router", "seed fallback"} {
		for _, failure := range []string{"caller cancel", "caller deadline"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				t.Parallel()

				testConnectionFailureHealth(t, path, failure)
			})
		}
	}
}

func TestTransportFailureStillDemotesConnection(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"pool", "router", "seed fallback"} {
		for _, failure := range []string{"attempt timeout", "network error"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				t.Parallel()

				testConnectionFailureHealth(t, path, failure)
			})
		}
	}

	for _, path := range []string{"pool", "router"} {
		t.Run(path+"/observer cancel", func(t *testing.T) {
			t.Parallel()

			testConnectionFailureHealth(t, path, "observer cancel")
		})
	}
}

func testConnectionFailureHealth(t *testing.T, path, failure string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	wantErr := context.Canceled
	callerCanceled := failure == "caller cancel" || failure == "caller deadline"

	if failure == "caller deadline" {
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		t.Cleanup(cancel)
		wantErr = context.DeadlineExceeded
	}

	if failure == "attempt timeout" {
		wantErr = context.DeadlineExceeded
	}

	if failure == "network error" || failure == "observer cancel" {
		wantErr = syscall.ECONNRESET
	}

	obs := &callerHealthObserver{recordingObserver: newRecordingObserver()}

	if failure == "observer cancel" {
		obs.onAttemptEnd = cancel
	}

	tr, pool, conns := newCallerHealthTransport(t, path, obs)
	tr.disableRetry = !callerCanceled

	if failure == "attempt timeout" {
		tr.requestTimeout = 200 * time.Millisecond
	}

	var (
		selected *Connection
		attempts int
	)

	tr.transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		attempts++

		for _, conn := range conns {
			if conn.URL.Host == req.URL.Host {
				selected = conn
			}
		}

		require.NotNil(t, selected)

		if path == "router" {
			require.EqualValues(t, 1, selected.loadInFlight(poolGet))
		}

		if attempts == 1 && failure != "attempt timeout" {
			require.NoError(t, req.Context().Err(), "the caller must cancel an active round trip")
		}

		if failure == "network error" || failure == "observer cancel" {
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
		}

		if failure == "caller cancel" {
			cancel()
		}

		<-req.Context().Done()

		return nil, fmt.Errorf("round trip interrupted: %w", req.Context().Err())
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/index/_doc/1", nil)
	require.NoError(t, err)

	res, err := tr.Stream(req) //nolint:bodyclose // all cases return a nil response and an error
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, res)
	require.Equal(t, 1, attempts, "caller cancellation and DisableRetry must prevent further attempts")
	require.NotNil(t, selected)
	require.Zero(t, selected.loadInFlight(poolGet), "the completed attempt must release its pool slot")

	if failure == "observer cancel" {
		require.ErrorIs(t, ctx.Err(), context.Canceled, "the observer must cancel after RoundTrip returned its network error")
	} else if !callerCanceled {
		require.NoError(t, ctx.Err(), "an attempt timeout must not cancel its caller")
	}

	for _, conn := range conns {
		wantDead := conn == selected && !callerCanceled
		require.Equal(t, wantDead, conn.loadConnState().lifecycle().has(lcDead), conn.Name)
		require.Equal(t, !wantDead, conn.isReady(), conn.Name)
		require.Equal(t, !wantDead, conn.deadSinceIsZero(), conn.Name)
		require.EqualValues(t, boolToInt(wantDead), conn.failures.Load(), conn.Name)
	}

	if callerCanceled {
		require.Zero(t, selected.drainingConn.Load(), "caller cancellation must not request a socket drain")
	} else if failure == "attempt timeout" {
		require.EqualValues(t, 1, selected.drainingConn.Load(), "a genuine attempt timeout must still request a drain")
	}

	if path != "router" {
		ready, dead := pool.connectionsByState()
		wantDead := boolToInt(!callerCanceled)
		require.Len(t, dead, wantDead)
		require.Len(t, ready, len(conns)-wantDead)
		require.EqualValues(t, wantDead, pool.poolFailures.Load())
		require.Equal(t, wantDead, obs.count("demote"))
	}

	if path != "seed fallback" {
		require.Equal(t, !callerCanceled, selected.needsCatUpdate())
		require.Equal(t, !callerCanceled, tr.catRefreshNeeded.Load())
		require.Equal(t, boolToInt(!callerCanceled), obs.invalidations)
		require.Equal(t, 1, obs.attemptStarts)
		require.Equal(t, 1, obs.attemptEnds)
		require.Zero(t, obs.attemptStatus)
		require.ErrorIs(t, obs.attemptErr, wantErr)
	}

	streamEvents := obs.getStreamEvents()
	require.Len(t, streamEvents, 1)
	require.Zero(t, streamEvents[0].StatusCode)
	require.ErrorIs(t, streamEvents[0].Err, wantErr, "cancellation remains a request failure")
}

// Construct the real pools and policies directly: no discovery loop can erase
// a shard invalidation or resurrect a node before the assertions inspect it.
func newCallerHealthTransport(t *testing.T, path string, obs *callerHealthObserver) (*Transport, *multiServerPool, []*Connection) {
	t.Helper()

	conns := []*Connection{
		routableConn(t, "node-a", time.Millisecond, "data"),
		routableConn(t, "node-b", time.Millisecond, "data"),
	}

	if path == "seed fallback" {
		conns = conns[:1]
		conns[0].seed = true
	}

	var observer ConnectionObserver = obs

	pool := testPool(slices.Clone(conns))
	pool.ctx = t.Context()
	pool.resurrectTimeoutInitial = time.Hour
	pool.resurrectTimeoutMax = time.Hour
	pool.minimumResurrectTimeout = time.Hour
	pool.resurrectTimeoutFactorCutoff = 5
	pool.observer.Store(&observer)

	tr := &Transport{
		compressor:           &requestCompressor{},
		maxRetries:           2,
		enableRetryOnTimeout: true,
		seedFallbackDisabled: true,
	}
	tr.mu.connectionPool = pool
	tr.observer.Store(&observer)

	switch path {
	case "router":
		policy := &RoundRobinPolicy{pool: pool}
		psSetEnabled(&policy.policyState, true)
		tr.router = callerHealthRouter{Router: NewRouter(policy)}

		for _, conn := range conns {
			conn.pools.getOrCreate(poolGet)
		}
	case "seed fallback":
		tr.router = &emptyRouter{}
		tr.seedFallbackPool = pool
		tr.seedFallbackDisabled = false
	}

	return tr, pool, conns
}

// Keep the SDK's Route and OnFailure implementations, adding only the pool
// metadata normally supplied by a scored route to exercise in-flight cleanup.
type callerHealthRouter struct {
	Router
}

func (r callerHealthRouter) Route(ctx context.Context, req *http.Request) (NextHop, error) {
	hop, err := r.Router.Route(ctx, req)
	if err != nil {
		return hop, err
	}

	hop.PoolName = poolGet

	return hop, nil
}

type callerHealthObserver struct {
	*recordingObserver

	attemptStarts int
	attemptEnds   int
	attemptStatus int
	attemptErr    error
	invalidations int
	onAttemptEnd  func()
}

func (o *callerHealthObserver) OnAttemptStart(ctx context.Context, _ int) context.Context {
	o.attemptStarts++
	return ctx
}

func (o *callerHealthObserver) OnAttemptEnd(_ context.Context, _ int, status int, err error) {
	o.attemptEnds++
	o.attemptStatus = status
	o.attemptErr = err

	if o.onAttemptEnd != nil {
		o.onAttemptEnd()
	}
}

func (o *callerHealthObserver) OnShardMapInvalidation(ShardMapInvalidationEvent) {
	o.invalidations++
}

func boolToInt(value bool) int {
	if value {
		return 1
	}

	return 0
}
