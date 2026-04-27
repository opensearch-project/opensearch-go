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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- NewDefaultPolicy env var override coverage ---

func TestNewDefaultPolicy_RoutingConfigEnv(t *testing.T) {
	t.Setenv(envRoutingConfig, "-shard_exact")

	policy := NewDefaultPolicy()
	require.NotNil(t, policy)
}

func TestNewDefaultPolicy_DiscoveryConfigEnv(t *testing.T) {
	t.Setenv(envDiscoveryConfig, "-cat_shards")

	policy := NewDefaultPolicy()
	require.NotNil(t, policy)
}

func TestNewDefaultPolicy_ShardRequestsEnv(t *testing.T) {
	t.Setenv(envShardRequests, "10:256")

	policy := NewDefaultPolicy()
	require.NotNil(t, policy)
}

func TestNewDefaultPolicy_ShardRequestsEnv_InvalidMinMax(t *testing.T) {
	// min > max should record an error (logged via debugLogger)
	t.Setenv(envShardRequests, "500:10")

	enableTestDebugLogger(t)

	policy := NewDefaultPolicy()
	require.NotNil(t, policy)
}

func TestNewDefaultPolicy_AllEnvOverrides(t *testing.T) {
	t.Setenv(envRoutingConfig, "-shard_exact")
	t.Setenv(envDiscoveryConfig, "-cat_shards")
	t.Setenv(envShardRequests, "5:128")

	policy := NewDefaultPolicy()
	require.NotNil(t, policy)
}

func TestNewDefaultPolicy_DebugLogging(t *testing.T) {
	enableTestDebugLogger(t)

	// Pass an invalid option to generate a config error that will be logged.
	policy := NewDefaultPolicy(WithAdaptiveConcurrencyLimits(500, 10))
	require.NotNil(t, policy)
}

// --- IsEnabled closure coverage via Router.Route ---
// These exercise the closures defined inside NewDefaultRoutes,
// NewRoundRobinDefaultPolicy, NewDefaultPolicy, and newScoredRoutes
// by routing actual requests through a fully constructed router.

func TestNewMuxRouter_ExercisesIsEnabledClosures(t *testing.T) {
	t.Parallel()

	router := NewMuxRouter()
	require.NotNil(t, router)

	// PolicyChain requires configurePolicySettings before DiscoveryUpdate.
	chain := router.(*PolicyChain)
	err := chain.configurePolicySettings(createTestConfig())
	require.NoError(t, err)

	conns := makeRoleConnections(t)
	err = router.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Exercise routes that trigger various IsEnabled closures.
	paths := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/test-index/_search"},
		{"GET", "/test-index/_doc/1"},
		{"POST", "/_bulk"},
		{"GET", "/test-index/_refresh"},
		{"GET", "/test-index/_flush"},
		{"POST", "/test-index/_settings"},
		{"GET", "/_ingest/pipeline/my-pipeline"},
		{"POST", "/test-index/_forcemerge"},
		{"GET", "/test-index/_segments"},
		{"POST", "/_snapshot/my-repo/_mount"},
	}

	for _, tc := range paths {
		hop, routeErr := router.Route(ctx, mustReq(t, tc.method, tc.path))
		require.NoError(t, routeErr, "path=%s %s", tc.method, tc.path)
		require.NotNil(t, hop.Conn, "path=%s %s should return a connection", tc.method, tc.path)
	}
}

func TestNewRoundRobinRouter_ExercisesIsEnabledClosure(t *testing.T) {
	t.Parallel()

	router := NewRoundRobinRouter()
	require.NotNil(t, router)

	chain := router.(*PolicyChain)
	err := chain.configurePolicySettings(createTestConfig())
	require.NoError(t, err)

	conns := makeRoleConnections(t)
	err = router.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()
	hop, routeErr := router.Route(ctx, mustReq(t, "GET", "/"))
	require.NoError(t, routeErr)
	require.NotNil(t, hop.Conn)
}

func TestNewDefaultRouter_ExercisesIsEnabledClosures(t *testing.T) {
	t.Parallel()

	router := NewDefaultRouter()
	require.NotNil(t, router)

	chain := router.(*PolicyChain)
	err := chain.configurePolicySettings(createTestConfig())
	require.NoError(t, err)

	conns := makeRoleConnections(t)
	err = router.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	paths := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/test-index/_search"},
		{"POST", "/test-index/_search"},
		{"GET", "/test-index/_doc/1"},
		{"POST", "/_bulk"},
		{"GET", "/test-index/_refresh"},
		{"POST", "/test-index/_forcemerge"},
		{"GET", "/test-index/_segments"},
	}

	for _, tc := range paths {
		hop, routeErr := router.Route(ctx, mustReq(t, tc.method, tc.path))
		require.NoError(t, routeErr, "path=%s %s", tc.method, tc.path)
		require.NotNil(t, hop.Conn, "path=%s %s should return a connection", tc.method, tc.path)
	}
}

// --- helpers ---

func makeRoleConnections(t *testing.T) []*Connection {
	t.Helper()
	conns := make([]*Connection, 5)
	for i := range conns {
		u, err := url.Parse(fmt.Sprintf("https://node%d:9200", i))
		require.NoError(t, err)
		conns[i] = &Connection{
			URL:       u,
			URLString: u.String(),
			ID:        fmt.Sprintf("node%d", i),
			Name:      fmt.Sprintf("node%d", i),
			rttRing:   newRTTRing(4),
			Roles:     newRoleSet([]string{"data", "ingest", "search"}),
		}
		conns[i].rttRing.add(200 * time.Microsecond)
		conns[i].weight.Store(1)
		conns[i].state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	}
	return conns
}

func mustReq(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	require.NoError(t, err)
	return req
}
