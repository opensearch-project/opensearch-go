// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchtransport) && multinode

package opensearchtransport

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
)

// noIndexRouteCounter counts the node the router selects for each request
// with no index in its path.
type noIndexRouteCounter struct {
	BaseConnectionObserver
	mu       sync.Mutex
	selected map[string]int
	rtt      map[string]int64
}

func (o *noIndexRouteCounter) OnRoute(e RouteEvent) {
	if e.IndexName != "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.selected[e.Selected.Name]++
	for _, c := range e.Candidates {
		o.rtt[c.Name] = c.RTTBucket
	}
}

func (o *noIndexRouteCounter) snapshot() (map[string]int, map[string]int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return maps.Clone(o.selected), maps.Clone(o.rtt)
}

func (o *noIndexRouteCounter) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	clear(o.selected)
	clear(o.rtt)
}

// measuredCandidates returns how many named candidates with a measured RTT the
// route events since the last reset have offered.
func (o *noIndexRouteCounter) measuredCandidates() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for name, b := range o.rtt {
		if name != "" && b >= 0 {
			n++
		}
	}
	return n
}

// sendNoIndex sends one GET /_count and requires it to succeed.
func sendNoIndex(t *testing.T, ctx context.Context, tr *Transport) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_count", nil)
	require.NoError(t, err)
	res, err := tr.Stream(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// TestNoIndexRequestsSpreadAcrossNodes_Integration sends requests with no index
// in their path one at a time through the default router and checks, from the
// router's own route events, that more than one node of the nearest RTT tier
// was selected. Before the fix every such request went to the node whose URL
// sorted first in that tier.
//
// It sends GET /_count rather than POST /_bulk: both take the router's no-index
// branch, but _bulk is routed to ingest nodes, and a cluster with none sends a
// bulk to round-robin before that branch runs. _count is routed to data nodes
// when there are no search nodes. The router is set explicitly because
// TestMain turns the default router off for this package.
func TestNoIndexRequestsSpreadAcrossNodes_Integration(t *testing.T) {
	testutil.WaitForCluster(t)
	if testutil.IsSecure(t) {
		testutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	u := testutil.GetTestURL(t)
	urls := []*url.URL{
		{Scheme: u.Scheme, Host: "localhost:9200"},
		{Scheme: u.Scheme, Host: "localhost:9201"},
		{Scheme: u.Scheme, Host: "localhost:9202"},
	}
	obs := &noIndexRouteCounter{selected: map[string]int{}, rtt: map[string]int64{}}
	router, err := NewDefaultRouter()
	require.NoError(t, err)
	cfg := getTestConfig(t, urls)
	cfg.Observer = obs
	cfg.Router = router
	tr, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	nodes := clusterNodeCount(t, tr, ctx)
	if nodes < 2 {
		t.Skipf("need at least 2 nodes, cluster has %d", nodes)
	}
	require.NoError(t, tr.DiscoverNodes(ctx))
	requireMinConns(t, tr, ctx, 2, func() bool { return len(tr.WriteNodes()) >= 2 })

	// The router's role pools fill after the transport's: wait until no-index
	// route events offer at least two discovered nodes with a measured RTT.
	require.Eventually(t, func() bool {
		sendNoIndex(t, ctx, tr)
		return obs.measuredCandidates() >= 2
	}, 60*time.Second, 100*time.Millisecond, "router never offered two measured nodes")
	obs.reset()

	const requests = 60
	for range requests {
		sendNoIndex(t, ctx, tr)
	}

	selected, rtt := obs.snapshot()
	t.Logf("no-index selections by node: %v; RTT buckets: %v", selected, rtt)

	// Only nodes in the nearest RTT tier are expected to be chosen, and each of
	// them must take a fair part of the requests. The old selection sent nearly
	// all of them to the node whose URL sorted first.
	nearest := int64(math.MaxInt64)
	for _, b := range rtt {
		if b >= 0 && b < nearest {
			nearest = b
		}
	}
	tier := 0
	for _, b := range rtt {
		if b == nearest {
			tier++
		}
	}
	if tier < 2 {
		t.Skipf("nearest RTT tier holds %d node(s), nothing to spread across: %v", tier, rtt)
	}
	floor := requests / tier / 3
	for name, b := range rtt {
		if b != nearest {
			continue
		}
		require.GreaterOrEqual(t, selected[name], floor,
			"node %s of a %d-node nearest tier got %d of %d no-index requests: %v",
			name, tier, selected[name], requests, selected)
	}
}

// TestNodeStatsPollReadsIndexingPressure_Integration checks that a real node's
// stats response carries the indexing_pressure counter the router weights by.
func TestNodeStatsPollReadsIndexingPressure_Integration(t *testing.T) {
	testutil.WaitForCluster(t)

	u := testutil.GetTestURL(t)
	cfg := getTestConfig(t, []*url.URL{u})
	tr, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeStatsPath, nil)
	require.NoError(t, err)
	res, err := tr.Stream(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var stats NodeStatsResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&stats))
	require.Len(t, stats.Nodes, 1)
	for _, ns := range stats.Nodes {
		require.NotNil(t, ns.IndexingPressure, "node stats carry no indexing_pressure")
		require.GreaterOrEqual(t, ns.IndexingPressure.Memory.Total.CoordinatingInBytes, int64(0))
	}
}
