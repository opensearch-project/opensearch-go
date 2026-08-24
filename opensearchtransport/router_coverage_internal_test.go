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

func TestNewDefaultPolicy_EnvOverrides(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		opts        []RouterOption
		enableDebug bool
	}{
		{
			name:    "routing config disables shard_exact",
			envVars: map[string]string{envRoutingConfig: "-shard_exact"},
		},
		{
			name:    "discovery config disables cat_shards",
			envVars: map[string]string{envDiscoveryConfig: "-cat_shards"},
		},
		{
			name:    "shard requests min:max",
			envVars: map[string]string{envShardRequests: "10:256"},
		},
		{
			name:        "shard requests invalid min>max logs error",
			envVars:     map[string]string{envShardRequests: "500:10"},
			enableDebug: true,
		},
		{
			name: "all env overrides combined",
			envVars: map[string]string{
				envRoutingConfig:   "-shard_exact",
				envDiscoveryConfig: "-cat_shards",
				envShardRequests:   "5:128",
			},
		},
		{
			name:        "invalid option logs via debug",
			opts:        []RouterOption{WithAdaptiveConcurrencyLimits(500, 10)},
			enableDebug: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			if tt.enableDebug {
				enableTestDebugLogger(t)
			}

			policy, err := NewDefaultPolicy(tt.opts...)
			require.NoError(t, err)
			require.NotNil(t, policy)
		})
	}
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

	router, err := NewDefaultRouter()
	require.NoError(t, err)
	require.NotNil(t, router)

	chain := router.(*PolicyChain)
	err = chain.configurePolicySettings(createTestConfig())
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
		conns[i].setLifecycleBit(lcActive | lcNeedsWarmup)
	}
	return conns
}

func mustReq(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	require.NoError(t, err)
	return req
}
