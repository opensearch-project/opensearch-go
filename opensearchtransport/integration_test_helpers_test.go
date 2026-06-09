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
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	ospath "github.com/opensearch-project/opensearch-go/v5/internal/path"
	"github.com/opensearch-project/opensearch-go/v5/internal/test/readiness"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
)

// getTestConfig returns a Config configured for the test environment (secure or insecure).
// This must live here (not in testutil) because it returns the internal Config type.
//
// EnableMetrics is set true so the readiness FSM helpers
// (transportLensFSMCheck) can observe connection-pool state via
// transport.Metrics() during integration polling.
func getTestConfig(t *testing.T, urls []*url.URL) Config {
	t.Helper()

	cfg := Config{URLs: urls, EnableMetrics: true}

	if testutil.IsSecure(t) {
		cfg.Transport = testutil.GetTestTransport(t)
		cfg.Username = "admin"
		cfg.Password = testutil.GetPassword(t)
	}

	return cfg
}

// --- Selector factories ---
//
// Each factory returns a func() bool suitable for passing to a polling helper
// as the selector argument. They capture the transport and threshold in a
// closure so callers stay concise.

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

// readinessObserver is a transport ConnectionObserver that pushes
// lifecycle transitions into a readiness.Cluster. The cluster is bound
// lazily because the readiness aggregator is built per Wait call,
// after the transport (and therefore this observer) already exists.
//
// Events received before BindCluster lands are dropped; the polling
// transportLensFSMCheck closes that window by reading Metrics() each
// tick and back-filling any state the observer missed. Events with no
// FSM mapping (e.g. OnRoute, OnShardMapInvalidation) are ignored.
type readinessObserver struct {
	BaseConnectionObserver
	cluster atomic.Pointer[readiness.Cluster]
}

// BindCluster wires this observer to a readiness.Cluster. Subsequent
// transport events are pushed into the cluster's per-node FSMs.
func (o *readinessObserver) BindCluster(c *readiness.Cluster) {
	o.cluster.Store(c)
}

func (o *readinessObserver) advance(e ConnectionEvent, layer readiness.State, note string) {
	c := o.cluster.Load()
	if c == nil {
		return
	}
	id := e.ID
	if id == "" {
		id = e.Name
	}
	if id == "" {
		id = e.URL
	}
	if id == "" {
		return
	}
	c.Node(id, e.Name, e.URL).Advance(layer, note)
}

// OnPromote, OnHealthCheckPass, and OnStandbyPromote each signal that
// the connection is ready to serve. Repeated promotions are no-ops in
// the FSM.
func (o *readinessObserver) OnPromote(e ConnectionEvent) {
	o.advance(e, readiness.LayerConnReady, "observer: promoted")
}

func (o *readinessObserver) OnHealthCheckPass(e ConnectionEvent) {
	o.advance(e, readiness.LayerConnReady, "observer: health check ok")
}

func (o *readinessObserver) OnStandbyPromote(e ConnectionEvent) {
	o.advance(e, readiness.LayerConnReady, "observer: standby promoted")
}

// getTestConfigWithReadiness returns a test Config wired with a
// readinessObserver that callers BindCluster on once they construct the
// readiness.Cluster (typically as the first FSMCheck inside
// readiness.Wait). The observer is the push-based counterpart to the
// polling transportLensFSMCheck; callers usually register both for
// race-free coverage.
func getTestConfigWithReadiness(t *testing.T, urls []*url.URL) (Config, *readinessObserver) {
	t.Helper()
	cfg := getTestConfig(t, urls)
	obs := &readinessObserver{}
	cfg.Observer = obs
	return cfg, obs
}

// transportLensFSMCheck returns an FSMCheck that mirrors the transport
// client's connection pool into the readiness FSM: each non-dead,
// non-standby connection's NodeFSM advances to LayerConnReady, and the
// StateCatUpdateFresh client-state bit tracks NeedsCatUpdate.
//
// On 1-node clusters (OPENSEARCH_NODE_COUNT=1) the pool stays as
// singleServerPool, whose connection never transitions to lcActive
// (its OnSuccess/OnFailure are no-ops). Treat NeedsCatUpdate clearance
// as the readiness signal in that mode instead of waiting for
// IsDead/IsStandby to flip.
func transportLensFSMCheck(transport *Client) readiness.FSMCheck {
	singleNode := expectedNodeCount() == 1
	return func(_ context.Context, cluster *readiness.Cluster) error {
		metrics, err := transport.Metrics()
		if err != nil {
			cluster.RecordError(err)
			return nil
		}
		for _, raw := range metrics.Connections {
			cm, ok := raw.(ConnectionMetric)
			if !ok {
				continue
			}
			id := cm.Meta.ID
			if id == "" {
				id = cm.Meta.Name
			}
			if id == "" {
				id = cm.URL
			}
			if id == "" {
				continue
			}
			node := cluster.Node(id, cm.Meta.Name, cm.URL)
			ready := !cm.IsDead && !cm.IsStandby
			if singleNode {
				ready = !cm.NeedsCatUpdate
			}
			if ready {
				node.Advance(readiness.LayerConnReady, "transport pool: ready+active")
			}
			if cm.NeedsCatUpdate {
				node.UpdateClientState(0, readiness.StateCatUpdateFresh, "needsCatUpdate set")
			} else {
				node.UpdateClientState(readiness.StateCatUpdateFresh, 0, "needsCatUpdate cleared")
			}
		}
		return nil
	}
}

// expectedNodeCount mirrors the readiness package's expectedNodesFromEnv:
// it reads OPENSEARCH_NODE_COUNT to know what the CI cluster looks like.
// Returns 0 when the variable is unset or invalid (callers treat 0 as
// "unknown, fall back to default behavior").
func expectedNodeCount() int {
	v := os.Getenv("OPENSEARCH_NODE_COUNT")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// requireMinReadyConns ensures at least minConns connections are discovered,
// named, and have no pending needsCatUpdate flag (shard placement is fresh).
// observer may be nil; when non-nil it provides push-based lifecycle
// transitions alongside the polling fallback.
func requireMinReadyConns(t *testing.T, transport *Client, observer *readinessObserver, ctx context.Context, minConns int) {
	t.Helper()
	opts := []readiness.Option{
		readiness.WithMinNodes(minConns),
		readiness.WithFSMCheck(discoverFSMCheck(transport)),
		readiness.WithFSMCheck(transportLensFSMCheck(transport)),
	}
	if observer != nil {
		opts = append([]readiness.Option{
			readiness.WithFSMCheck(func(_ context.Context, c *readiness.Cluster) error {
				observer.BindCluster(c)
				return nil
			}),
		}, opts...)
	}
	readiness.Wait(t, ctx, readiness.LayerConnReady|readiness.StateCatUpdateFresh, opts...)
}

// requireMinConnsObsOrPoll dispatches to requireMinConns when observer
// is nil, requireMinConnsObs otherwise. Lets shared helpers accept an
// optional observer without forking the call sites.
func requireMinConnsObsOrPoll(
	t *testing.T, transport *Client, observer *readinessObserver,
	ctx context.Context, minConns int, selector func() bool,
) {
	t.Helper()
	if observer == nil {
		requireMinConns(t, transport, ctx, minConns, selector)
		return
	}
	requireMinConnsObs(t, transport, observer, ctx, minConns, selector)
}

// requireMinConns mirrors the old testutil.RequireMinConns shape: it
// triggers discovery, mirrors the transport pool into the readiness FSM,
// and gates satisfaction on a custom selector.
func requireMinConns(t *testing.T, transport *Client, ctx context.Context, minConns int, selector func() bool) {
	t.Helper()
	readiness.Wait(t, ctx, readiness.LayerConnReady,
		readiness.WithMinNodes(minConns),
		readiness.WithFSMCheck(discoverFSMCheck(transport)),
		readiness.WithFSMCheck(transportLensFSMCheck(transport)),
		readiness.WithReadyFunc(selector))
}

// requireMinConnsObs is the observer-aware variant of requireMinConns.
// The observer pushes lifecycle transitions into the readiness FSM the
// instant they happen (no polling window); the polling FSMCheck remains
// as a fallback for state the observer doesn't expose (e.g. shard-cache
// freshness via NeedsCatUpdate).
func requireMinConnsObs(
	t *testing.T, transport *Client, observer *readinessObserver,
	ctx context.Context, minConns int, selector func() bool,
) {
	t.Helper()
	readiness.Wait(t, ctx, readiness.LayerConnReady,
		readiness.WithMinNodes(minConns),
		readiness.WithFSMCheck(func(_ context.Context, c *readiness.Cluster) error {
			observer.BindCluster(c)
			return nil
		}),
		readiness.WithFSMCheck(discoverFSMCheck(transport)),
		readiness.WithFSMCheck(transportLensFSMCheck(transport)),
		readiness.WithReadyFunc(selector))
}

// discoverFSMCheck triggers a node-discovery cycle each tick. Errors are
// recorded as transient so polling continues.
func discoverFSMCheck(transport *Client) readiness.FSMCheck {
	return func(ctx context.Context, cluster *readiness.Cluster) error {
		if err := transport.DiscoverNodes(ctx); err != nil {
			cluster.RecordError(err)
		}
		return nil
	}
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
