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

// callerHealthPath is a dispatch path a request takes to reach a connection.
type callerHealthPath struct {
	name         string
	newTransport func(t *testing.T, obs *callerHealthObserver) (*Transport, *multiServerPool, []*Connection)
	// routed requests go through a Router, which tracks in-flight requests
	// per connection and marks a failed connection dead itself; the pool
	// evicts it lazily, so the pool's ready and dead lists are not checked.
	routed bool
	// seedFallback requests fire no attempt observer hooks and carry no
	// shard placement to invalidate.
	seedFallback bool
}

func TestAttemptFailureConnectionHealth(t *testing.T) {
	t.Parallel()

	var (
		poolPath         = callerHealthPath{name: "pool", newTransport: newPoolPathTransport}
		routerPath       = callerHealthPath{name: "router", newTransport: newRouterPathTransport, routed: true}
		seedFallbackPath = callerHealthPath{name: "seed fallback", newTransport: newSeedFallbackPathTransport, seedFallback: true}
		allPaths         = []callerHealthPath{poolPath, routerPath, seedFallbackPath}
	)

	networkErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}

	tests := []struct {
		name  string
		paths []callerHealthPath
		// callerTimeout gives the caller's context a deadline.
		callerTimeout time.Duration
		// requestTimeout sets the transport's per-attempt timeout.
		requestTimeout time.Duration
		// cancelInRoundTrip cancels the caller while RoundTrip is running.
		cancelInRoundTrip bool
		// cancelOnAttemptEnd cancels the caller from OnAttemptEnd, after
		// RoundTrip has returned.
		cancelOnAttemptEnd bool
		// roundTripErr is returned at once; nil blocks until the attempt's
		// context ends.
		roundTripErr error
		// preservesHealth: the node stays ready and the request is not
		// retried; otherwise the node is demoted and retries are disabled.
		preservesHealth bool
		wantErr         error
		wantCallerErr   error
		wantDrains      int64
	}{
		{
			name:              "caller cancel",
			paths:             allPaths,
			cancelInRoundTrip: true,
			preservesHealth:   true,
			wantErr:           context.Canceled,
			wantCallerErr:     context.Canceled,
		},
		{
			name:            "caller deadline",
			paths:           allPaths,
			callerTimeout:   time.Second,
			preservesHealth: true,
			wantErr:         context.DeadlineExceeded,
			wantCallerErr:   context.DeadlineExceeded,
		},
		{
			name:           "attempt timeout",
			paths:          allPaths,
			requestTimeout: time.Second,
			wantErr:        context.DeadlineExceeded,
			wantDrains:     1,
		},
		{
			name:         "network error",
			paths:        allPaths,
			roundTripErr: networkErr,
			wantErr:      syscall.ECONNRESET,
		},
		{
			name:               "observer cancel",
			paths:              []callerHealthPath{poolPath, routerPath},
			roundTripErr:       networkErr,
			cancelOnAttemptEnd: true,
			wantErr:            syscall.ECONNRESET,
			wantCallerErr:      context.Canceled,
		},
	}

	for _, tt := range tests {
		for _, path := range tt.paths {
			t.Run(path.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				if tt.callerTimeout > 0 {
					ctx, cancel = context.WithTimeout(ctx, tt.callerTimeout)
					t.Cleanup(cancel)
				}

				obs := &callerHealthObserver{recordingObserver: newRecordingObserver()}
				if tt.cancelOnAttemptEnd {
					obs.onAttemptEnd = cancel
				}

				tr, pool, conns := path.newTransport(t, obs)
				tr.disableRetry = !tt.preservesHealth
				tr.requestTimeout = tt.requestTimeout

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
					require.NoError(t, req.Context().Err(), "the attempt must start with a live context")

					if path.routed {
						require.EqualValues(t, 1, selected.loadInFlight(poolGet))
					}

					if tt.roundTripErr != nil {
						return nil, tt.roundTripErr
					}

					if tt.cancelInRoundTrip {
						cancel()
					}

					<-req.Context().Done()

					return nil, fmt.Errorf("round trip interrupted: %w", req.Context().Err())
				})

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/index/_doc/1", nil)
				require.NoError(t, err)

				res, err := tr.Stream(req) //nolint:bodyclose // all cases return a nil response and an error
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, res)
				require.Equal(t, 1, attempts, "caller cancellation and DisableRetry must prevent further attempts")
				require.NotNil(t, selected)
				require.Zero(t, selected.loadInFlight(poolGet), "the completed attempt must release its pool slot")
				require.ErrorIs(t, ctx.Err(), tt.wantCallerErr)
				require.Equal(t, tt.wantDrains, selected.drainingConn.Load(), "only a timeout the transport generated requests a drain")

				for _, conn := range conns {
					wantDead := conn == selected && !tt.preservesHealth
					require.Equal(t, wantDead, conn.loadConnState().lifecycle().has(lcDead), conn.Name)
					require.Equal(t, !wantDead, conn.isReady(), conn.Name)
					require.Equal(t, !wantDead, conn.deadSinceIsZero(), conn.Name)
					require.EqualValues(t, boolToInt(wantDead), conn.failures.Load(), conn.Name)
				}

				if !path.routed {
					ready, dead := pool.connectionsByState()
					wantDead := boolToInt(!tt.preservesHealth)
					require.Len(t, dead, wantDead)
					require.Len(t, ready, len(conns)-wantDead)
					require.EqualValues(t, wantDead, pool.poolFailures.Load())
					require.Equal(t, wantDead, obs.count("demote"))
				}

				if !path.seedFallback {
					require.Equal(t, !tt.preservesHealth, selected.needsCatUpdate())
					require.Equal(t, !tt.preservesHealth, tr.catRefreshNeeded.Load())
					require.Equal(t, boolToInt(!tt.preservesHealth), obs.invalidations)
					require.Equal(t, 1, obs.attemptStarts)
					require.Equal(t, 1, obs.attemptEnds)
					require.Zero(t, obs.attemptStatus)
					require.ErrorIs(t, obs.attemptErr, tt.wantErr)
				}

				streamEvents := obs.getStreamEvents()
				require.Len(t, streamEvents, 1)
				require.Zero(t, streamEvents[0].StatusCode)
				require.ErrorIs(t, streamEvents[0].Err, tt.wantErr, "cancellation remains a request failure")
			})
		}
	}
}

func newPoolPathTransport(t *testing.T, obs *callerHealthObserver) (*Transport, *multiServerPool, []*Connection) {
	t.Helper()

	conns := []*Connection{
		routableConn(t, "node-a", time.Millisecond, "data"),
		routableConn(t, "node-b", time.Millisecond, "data"),
	}
	tr, pool := newCallerHealthTransport(t, conns, obs)

	return tr, pool, conns
}

func newRouterPathTransport(t *testing.T, obs *callerHealthObserver) (*Transport, *multiServerPool, []*Connection) {
	t.Helper()

	tr, pool, conns := newPoolPathTransport(t, obs)

	policy := &RoundRobinPolicy{pool: pool}
	psSetEnabled(&policy.policyState, true)
	tr.router = callerHealthRouter{Router: NewRouter(policy)}

	for _, conn := range conns {
		conn.pools.getOrCreate(poolGet)
	}

	return tr, pool, conns
}

func newSeedFallbackPathTransport(t *testing.T, obs *callerHealthObserver) (*Transport, *multiServerPool, []*Connection) {
	t.Helper()

	seed := routableConn(t, "node-a", time.Millisecond, "data")
	seed.seed = true
	conns := []*Connection{seed}

	tr, pool := newCallerHealthTransport(t, conns, obs)
	tr.router = &emptyRouter{}
	tr.seedFallbackPool = pool
	tr.seedFallbackDisabled = false

	return tr, pool, conns
}

// newCallerHealthTransport constructs the real pool directly: no discovery
// loop can erase a shard invalidation or resurrect a node before the
// assertions inspect it.
func newCallerHealthTransport(t *testing.T, conns []*Connection, obs *callerHealthObserver) (*Transport, *multiServerPool) {
	t.Helper()

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

	return tr, pool
}

// callerHealthRouter keeps the SDK's Route and OnFailure implementations,
// adding only the pool metadata normally supplied by a scored route to
// exercise in-flight cleanup.
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
