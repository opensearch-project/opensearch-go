// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestSeedFallback(t *testing.T) {
	t.Run("Fallback succeeds when router returns ErrNoConnections", func(t *testing.T) {
		seedURL, _ := url.Parse("http://seed-node:9200")

		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)

		// Verify seed fallback pool was created.
		require.NotNil(t, tp.seedFallbackPool)

		// Drain the main connection pool so it returns ErrNoConnections.
		// Move all connections to dead with no resurrection.
		tp.mu.Lock()
		if pool, ok := tp.mu.connectionPool.(*singleServerPool); ok {
			pool.connection.mu.Lock()
			pool.connection.casLifecycle(pool.connection.loadConnState(), 0, lcDead, lcReady|lcActive|lcStandby)
			pool.connection.markAsDeadWithLock()
			pool.connection.mu.Unlock()
			tp.mu.connectionPool = &multiServerPool{}
		}
		tp.mu.Unlock()

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode)
		if res.Body != nil {
			res.Body.Close()
		}

		// Verify discoveryNeeded was set by the fallback.
		require.True(t, tp.discoveryNeeded.Load(), "discoveryNeeded should be set after seed fallback success")
	})

	t.Run("Fallback succeeds when router policy chain returns ErrNoConnections", func(t *testing.T) {
		seedURL, _ := url.Parse("http://seed-node:9200")

		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Router:                &emptyRouter{},
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)
		require.NotNil(t, tp.seedFallbackPool)

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		if res.Body != nil {
			res.Body.Close()
		}
		require.True(t, tp.discoveryNeeded.Load())
	})

	t.Run("Fallback disabled via OPENSEARCH_GO_FALLBACK=false", func(t *testing.T) {
		t.Setenv(envFallbackConfig, "false")

		seedURL, _ := url.Parse("http://seed-node:9200")
		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Router:                &emptyRouter{},
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)
		require.True(t, tp.seedFallbackDisabled)
		require.Nil(t, tp.seedFallbackPool)

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req) //nolint:bodyclose // error path: res is nil
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoConnections)
		require.Nil(t, res)
	})

	t.Run("Fallback triggers OnFailure when seed request fails", func(t *testing.T) {
		seedURL, _ := url.Parse("http://seed-node:9200")

		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Router:                &emptyRouter{},
			DisableRetry:          true,
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			}),
		})
		require.NoError(t, err)
		require.NotNil(t, tp.seedFallbackPool)

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req) //nolint:bodyclose // error path: res is nil
		require.Error(t, err)
		require.Nil(t, res)
		require.Contains(t, err.Error(), "seed fallback request failed")

		// Seed pool should have the connection in dead list after failure.
		tp.seedFallbackPool.mu.RLock()
		deadCount := len(tp.seedFallbackPool.mu.dead)
		tp.seedFallbackPool.mu.RUnlock()
		require.Equal(t, 1, deadCount, "seed connection should be in dead list after failure")
	})

	t.Run("Fallback pool has independent connections from main pool", func(t *testing.T) {
		seedURL, _ := url.Parse("http://seed-node:9200")

		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)
		require.NotNil(t, tp.seedFallbackPool)

		// Get the main pool connection and the seed pool connection.
		tp.seedFallbackPool.mu.RLock()
		seedConn := tp.seedFallbackPool.mu.ready[0]
		tp.seedFallbackPool.mu.RUnlock()

		// Verify they are different objects pointing to the same URL.
		if pool, ok := tp.mu.connectionPool.(*singleServerPool); ok {
			mainConn := pool.connection
			require.NotSame(t, mainConn, seedConn, "seed and main pool connections must be different objects")
			require.Equal(t, mainConn.URL.String(), seedConn.URL.String(), "seed and main pool connections must reference the same URL")
		}
	})

	t.Run("Fallback with multiple seed URLs rotates through them", func(t *testing.T) {
		url1, _ := url.Parse("http://seed1:9200")
		url2, _ := url.Parse("http://seed2:9200")

		var attempted atomic.Int32
		tp, err := New(Config{
			URLs:                  []*url.URL{url1, url2},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Router:                &emptyRouter{},
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				attempted.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)
		require.NotNil(t, tp.seedFallbackPool)

		tp.seedFallbackPool.mu.RLock()
		require.Equal(t, 2, tp.seedFallbackPool.mu.activeCount)
		tp.seedFallbackPool.mu.RUnlock()

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		if res.Body != nil {
			res.Body.Close()
		}
		require.Equal(t, int32(1), attempted.Load())
	})

	// Regression for the seed-fallback gap: when node discovery replaces the seed
	// with a single unreachable node, that node lands in a singleServerPool. Before
	// the fix, singleServerPool.Next() returned the never-verified pod-IP connection
	// unconditionally, the request dialed it, and it failed with a transport error
	// ("no route to host") that is not ErrNoConnections -- so the post-loop seed
	// fallback (which only triggers on ErrNoConnections) never fired and every
	// request died on the unroutable node. The gate on singleServerPool.Next() now
	// reports ErrNoConnections for the unavailable discovered node, so the request
	// is served by the seed instead. This drives the real request path end-to-end,
	// so it fails against the pre-fix code regardless of where the gate lives.
	t.Run("Fallback serves request when discovery yields a single unreachable node", func(t *testing.T) {
		seedURL, _ := url.Parse("http://seed-node:9200")
		// A black-holed publish_address, as a NAT'd / unroutable cluster would
		// report from GET /_nodes/http (TEST-NET-1, guaranteed unroutable).
		podURL, _ := url.Parse("http://192.0.2.1:9200")

		var servedHosts []string
		tp, err := New(Config{
			URLs:                  []*url.URL{seedURL},
			SkipConnectionShuffle: true,
			HealthCheck:           NoOpHealthCheck,
			NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				servedHosts = append(servedHosts, req.URL.Host)
				// The unroutable pod IP would fail to dial in the real world;
				// model that as a transport error (not ErrNoConnections) so this
				// test would catch a regression that hands out the pod connection.
				if req.URL.Host == podURL.Host {
					return nil, fmt.Errorf("dial tcp %s: connect: no route to host", podURL.Host)
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
			}),
		})
		require.NoError(t, err)
		require.NotNil(t, tp.seedFallbackPool)

		// Simulate the post-discovery state: the seed has been evicted from the
		// active pool (its Service URL didn't match the discovered pod IP) and
		// replaced by a singleServerPool holding the lone discovered node -- dead
		// and still needing hardware, exactly as a node minted from /_nodes/http is.
		podConn := &Connection{URL: podURL, URLString: podURL.String()}
		podConn.setLifecycleBit(lcDead | lcNeedsWarmup | lcNeedsHardware)
		tp.mu.Lock()
		tp.mu.connectionPool = newSingleServerPool(podConn, nil)
		tp.mu.Unlock()
		// newSingleServerPool marks the conn lcActive; restore the discovered
		// state so availableForRouting() reflects a never-verified node.
		podConn.mu.Lock()
		podConn.casLifecycle(podConn.loadConnState(), 0, lcDead|lcNeedsWarmup|lcNeedsHardware, lcReady|lcActive|lcStandby)
		podConn.mu.Unlock()

		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		res, err := tp.Stream(req)
		require.NoError(t, err, "request should be served by the seed, not fail on the unroutable pod IP")
		require.NotNil(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode)
		if res.Body != nil {
			res.Body.Close()
		}

		// The seed served it; the unroutable pod IP was never dialed.
		require.NotContains(t, servedHosts, podURL.Host, "the unroutable pod IP must not be dialed")
		require.Contains(t, servedHosts, seedURL.Host, "the seed must serve the request")
		require.True(t, tp.discoveryNeeded.Load(), "discoveryNeeded should be set after seed fallback success")
	})
}

// emptyRouter always returns ErrNoConnections, simulating a fully exhausted router.
type emptyRouter struct{}

func (r *emptyRouter) Route(_ context.Context, _ *http.Request) (NextHop, error) {
	return NextHop{}, ErrNoConnections
}

func (r *emptyRouter) OnSuccess(_ *Connection)                              {}
func (r *emptyRouter) OnFailure(_ *Connection) error                        { return nil }
func (r *emptyRouter) DiscoveryUpdate(_, _, _ []*Connection) error          { return nil }
func (r *emptyRouter) CheckDead(_ context.Context, _ HealthCheckFunc) error { return nil }
func (r *emptyRouter) RotateStandby(_ context.Context, _ int) (int, error)  { return 0, nil }

// Verify emptyRouter implements Router at compile time.
var _ Router = (*emptyRouter)(nil)
