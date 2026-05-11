// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration

package opensearchtransport //nolint:testpackage // internal test helpers shared across integration tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	ospath "github.com/opensearch-project/opensearch-go/v4/internal/path"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// getTestConfig returns a Config configured for the test environment (secure or insecure).
// This must live here (not in testutil) because it returns the internal Config type.
func getTestConfig(t *testing.T, urls []*url.URL) Config {
	t.Helper()

	cfg := Config{URLs: urls}

	if testutil.IsSecure(t) {
		cfg.Transport = testutil.GetTestTransport(t)
		cfg.Username = "admin"
		cfg.Password = testutil.GetPassword(t)
	}

	return cfg
}

// --- Selector factories ---
//
// Each factory returns a func() bool suitable for passing to
// testutil.RequireMinConns as the selector argument. They capture the
// transport and threshold in a closure so callers stay concise:
//
//   testutil.RequireMinConns(t, ctx, n, transport.DiscoverNodes,
//       selectorShardReady(transport, n), nil)

// selectorShardReady returns a selector that passes when at least minConns
// ready connections are named and have no pending needsCatUpdate flag (shard
// placement is confirmed fresh). This is the standard gate for shard-exact
// routing tests.
func selectorShardReady(transport *Client, minConns int) func() bool {
	return func() bool {
		transport.mu.RLock()
		pool, ok := transport.mu.connectionPool.(*multiServerPool)
		transport.mu.RUnlock()

		if !ok || pool == nil {
			return false
		}

		pool.mu.RLock()
		defer pool.mu.RUnlock()

		if len(pool.mu.ready) < minConns {
			return false
		}

		for _, conn := range pool.mu.ready {
			if conn.Name == "" || conn.needsCatUpdate() {
				return false
			}
		}
		return true
	}
}

// routeObserver is the minimal interface for selector factories that check
// whether the router produced a RouteEvent. Any observer that captures route
// events and supports reset/check can satisfy this.
type routeObserver interface {
	reset()
	lastEvent() *RouteEvent
}

// selectorRouteObserved returns a selector that passes when a probe request
// through the transport produces a RouteEvent in the observer. This proves
// the router pipeline is fully wired: nodes discovered, health checks passed,
// sorted connections built, and the observer is receiving events.
//
// The selector resets the observer, fires a GET / probe, and checks whether
// the observer captured a RouteEvent.
func selectorRouteObserved(transport *Client, ctx context.Context, obs routeObserver) func() bool {
	var seen atomic.Bool
	return func() bool {
		if seen.Load() {
			return true
		}
		obs.reset()
		probeReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
		resp, err := transport.Perform(probeReq)
		if err != nil {
			return false
		}
		resp.Body.Close()
		if obs.lastEvent() != nil {
			seen.CompareAndSwap(false, true)
			return true
		}
		return false
	}
}

// --- Convenience wrappers ---

// requireMinReadyConns ensures at least minConns connections are discovered,
// named, and have no pending needsCatUpdate flag (shard placement is fresh).
func requireMinReadyConns(t *testing.T, transport *Client, ctx context.Context, minConns int) {
	t.Helper()
	testutil.RequireMinConns(t, ctx, minConns, transport.DiscoverNodes,
		selectorShardReady(transport, minConns), nil)
}

// selectorShardMapReady returns a selector that passes when the index's shard
// map is fully populated in the router cache: RoutingNumShards > 0 and every
// shard from 0..numShards-1 has a non-empty Primary node assignment.
func selectorShardMapReady(cache *indexSlotCache, index string, numShards int) func() bool {
	var seen atomic.Bool
	return func() bool {
		if seen.Load() {
			return true
		}
		slot := cache.slotFor(index)
		if slot == nil {
			return false
		}
		sm := slot.shardMap.Load()
		if sm == nil || sm.RoutingNumShards == 0 || len(sm.Shards) < numShards {
			return false
		}
		for shard := range numShards {
			s := sm.Shards[shard]
			if s == nil || s.Primary == "" {
				return false
			}
		}
		seen.CompareAndSwap(false, true)
		return true
	}
}

// selectorIndexGreen returns a selector that passes when the cluster reports
// green health for the given index.
func selectorIndexGreen(transport *Client, ctx context.Context, indexName string) func() bool {
	var seen atomic.Bool
	p, _ := ospath.ClusterHealthPath{Index: []string{indexName}}.Build()
	healthURL := url.URL{
		Path:     p,
		RawQuery: url.Values{"wait_for_status": {"green"}, "timeout": {"1s"}}.Encode(),
	}
	return func() bool {
		if seen.Load() {
			return true
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL.String(), nil)
		if err != nil {
			return false
		}
		resp, err := transport.Perform(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {
			seen.CompareAndSwap(false, true)
			return true
		}
		return false
	}
}

// clusterNodeCount queries /_nodes via the transport and returns the total
// number of nodes reported by the cluster. The transport must already be
// warmed up (able to serve requests) before calling this.
func clusterNodeCount(t *testing.T, transport *Client, ctx context.Context) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_nodes", nil)
	require.NoError(t, err)

	resp, err := transport.Perform(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var env map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))

	var meta _NodesMeta
	require.NoError(t, json.Unmarshal(env["_nodes"], &meta))
	require.Positive(t, meta.Total, "cluster reported 0 nodes")
	return meta.Total
}
