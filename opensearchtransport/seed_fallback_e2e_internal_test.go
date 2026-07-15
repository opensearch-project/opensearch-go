// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// hostRecorder returns a RoundTripper that records the host of every request it
// serves and dispatches by host: the seed host succeeds with 200, any other
// host fails with a transport error (simulating an unroutable discovered node,
// e.g. a NAT'd or misconfigured publish_address as seen in CI).
func hostRecorder(t *testing.T, seedHost string) (http.RoundTripper, func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		hosts []string
	)
	rt := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		hosts = append(hosts, req.URL.Host)
		mu.Unlock()
		if req.URL.Host == seedHost {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}
		return nil, fmt.Errorf("connection refused: %s unroutable", req.URL.Host)
	})
	served := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), hosts...)
	}
	return rt, served
}

// TestSeedFallbackAfterDiscoveryReplacesSeed drives the bug end-to-end through
// the public Stream API, reproducing Sean's scenario exactly: node discovery
// returns addresses that are not reachable, and the seed URL must keep serving
// until a discovered node health-checks clean.
//
// Unlike TestSeedFallback (which forces ErrNoConnections by hand-draining the
// pool or plugging in an always-empty router), this test exercises the real
// discovery -> policy-enabled -> Route -> seed-fallback path. It is the
// end-to-end counterpart to the policy-layer repro in
// TestPolicyEnabledExcludesUnverifiedDiscovered.
//
// Timeline:
//  1. New() with one seed URL. The seed lands in both the router's policy pool
//     and the separate seedFallbackPool.
//  2. Discovery replaces the seed in the policy pool with a single unroutable,
//     never-verified discovered node (added=[dead-node], removed=[seed]). This
//     is what "discovery ran" means: the policy pool no longer holds the seed.
//  3. Stream() a request.
//
// Correct behavior: the policy reports NOT enabled (its only connection is
// never-verified), Route returns ErrNoConnections, and the request cascades to
// the seed fallback -- so it is served by the seed host and discoveryNeeded is
// set.
//
// Buggy behavior (pre-fix): the policy reports enabled because len(dead) > 0,
// Route hands the request to the zombie discovered node, the mock rejects it as
// unroutable, and -- because a transport error is not ErrNoConnections -- the
// seed fallback never triggers. Stream returns the connection error instead of
// the healthy seed's 200.
func TestSeedFallbackAfterDiscoveryReplacesSeed(t *testing.T) {
	const (
		seedHost = "seed-node:9200"
		deadHost = "dead-node:9200"
	)
	seedURL, _ := url.Parse("http://" + seedHost)

	rt, servedHosts := hostRecorder(t, seedHost)

	tp, err := New(Config{
		URLs:                  []*url.URL{seedURL},
		Router:                &PolicyChain{policies: []Policy{NewRoundRobinPolicy()}},
		SkipConnectionShuffle: true,
		HealthCheck:           NoOpHealthCheck,
		NodeStatsInterval:     -1, // disable stats poller: no background requests
		DisableRetry:          true,
		Transport:             rt,
	})
	require.NoError(t, err)
	require.NotNil(t, tp.seedFallbackPool)

	// Discovery replaces the seed with an unroutable, never-verified node.
	deadConn := newUnverifiedDiscoveredConn("http://"+deadHost, RoleData)
	seedInPool := createTestConnection("http://" + seedHost) // matches by URL for removal
	require.NoError(t, tp.router.DiscoveryUpdate(
		[]*Connection{deadConn}, []*Connection{seedInPool}, nil,
	))

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req)
	require.NoError(t, err, "request must succeed via seed fallback, not die on the zombie discovered node")
	require.NotNil(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	if res.Body != nil {
		res.Body.Close()
	}

	require.Contains(t, servedHosts(), seedHost, "request must be served by the seed host")
	require.NotContains(t, servedHosts(), deadHost, "request must NOT be routed to the never-verified discovered node")
	require.True(t, tp.discoveryNeeded.Load(), "seed fallback must set discoveryNeeded")
}

// TestRoutingLeavesSeedOnceDiscoveredNodeVerified is the positive control for
// the end-to-end path: once a discovered node is confirmed reachable, routing
// must move onto it and stop using the seed fallback. Guards the fix against
// pinning permanently to the seed.
func TestRoutingLeavesSeedOnceDiscoveredNodeVerified(t *testing.T) {
	const (
		seedHost = "seed-node:9200"
		liveHost = "live-node:9200"
	)
	seedURL, _ := url.Parse("http://" + seedHost)

	// Both hosts succeed here; the discriminator is which host serves.
	var (
		mu    sync.Mutex
		hosts []string
	)
	rt := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		hosts = append(hosts, req.URL.Host)
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
	})

	tp, err := New(Config{
		URLs:                  []*url.URL{seedURL},
		Router:                &PolicyChain{policies: []Policy{NewRoundRobinPolicy()}},
		SkipConnectionShuffle: true,
		HealthCheck:           NoOpHealthCheck,
		NodeStatsInterval:     -1,
		DisableRetry:          true,
		Transport:             rt,
	})
	require.NoError(t, err)

	// Discovery adds a VERIFIED, reachable node (lcActive, no lcNeedsHardware)
	// and drops the seed from the policy pool.
	liveConn := createTestConnection("http://" + liveHost)
	seedInPool := createTestConnection("http://" + seedHost)
	require.NoError(t, tp.router.DiscoveryUpdate(
		[]*Connection{liveConn}, []*Connection{seedInPool}, nil,
	))

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	if res.Body != nil {
		res.Body.Close()
	}

	mu.Lock()
	served := append([]string(nil), hosts...)
	mu.Unlock()
	require.Contains(t, served, liveHost, "request must be routed to the verified discovered node")
	require.NotContains(t, served, seedHost, "routing must leave the seed once a discovered node is verified")
	require.False(t, tp.discoveryNeeded.Load(), "no seed fallback should occur when a verified node is available")
}

// TestSeedFallbackAfterDiscoveryReplacesSeedDefaultRouter is the same scenario
// as TestSeedFallbackAfterDiscoveryReplacesSeed but drives it through the REAL
// default router topology (NewDefaultRouter) rather than a flat
// PolicyChain{RoundRobinPolicy}.
//
// This distinction matters. The default router nests the catch-all round-robin
// policy inside a chain reached via Eval, not Route:
//
//	PolicyChain.Route
//	  └─ IfEnabledPolicy.Eval        (coordinating condition false: data-only)
//	       └─ NewPolicy(mux, roundRobin).Eval   (a nested PolicyChain)
//	            ├─ mux.Eval        -> data RolePolicy not-enabled -> nil
//	            └─ roundRobin.Eval -> would serve a zombie
//
// The flat-topology test hits PolicyChain.Route's own IsEnabled() gate, so the
// never-verified round-robin pool is skipped there. The nested round-robin is
// reached via Eval, which historically ran the leaf's Eval with no enabled
// check -- and RoundRobinPolicy.Eval did not self-gate either, so it served the
// unroutable discovered node as a zombie and masked the seed fallback. This
// test locks in both fixes (RoundRobinPolicy.Eval self-gate and
// PolicyChain.Eval IsEnabled gate) against the production topology.
func TestSeedFallbackAfterDiscoveryReplacesSeedDefaultRouter(t *testing.T) {
	const (
		seedHost = "seed-node:9200"
		deadHost = "dead-node:9200"
	)
	seedURL, _ := url.Parse("http://" + seedHost)

	rt, servedHosts := hostRecorder(t, seedHost)

	router, err := NewDefaultRouter()
	require.NoError(t, err)

	tp, err := New(Config{
		URLs:                  []*url.URL{seedURL},
		Router:                router,
		SkipConnectionShuffle: true,
		HealthCheck:           NoOpHealthCheck,
		NodeStatsInterval:     -1, // disable stats poller: no background requests
		DisableRetry:          true,
		Transport:             rt,
	})
	require.NoError(t, err)
	require.NotNil(t, tp.seedFallbackPool)

	// Discovery replaces the seed with an unroutable, never-verified data node.
	deadConn := newUnverifiedDiscoveredConn("http://"+deadHost, RoleData)
	seedInPool := createTestConnection("http://" + seedHost) // matches by URL for removal
	require.NoError(t, tp.router.DiscoveryUpdate(
		[]*Connection{deadConn}, []*Connection{seedInPool}, nil,
	))

	req, _ := http.NewRequest(http.MethodGet, "/test-index/_doc/1", nil)
	res, err := tp.Stream(req)
	require.NoError(t, err, "request must succeed via seed fallback, not die on the zombie discovered node")
	require.NotNil(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	if res.Body != nil {
		res.Body.Close()
	}

	require.Contains(t, servedHosts(), seedHost, "request must be served by the seed host")
	require.NotContains(t, servedHosts(), deadHost, "request must NOT be routed to the never-verified discovered node")
	require.True(t, tp.discoveryNeeded.Load(), "seed fallback must set discoveryNeeded")
}
