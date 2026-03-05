// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchtransport)

package opensearchtransport //nolint:testpackage // requires internal access to Client and transport internals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// TestMurmur3ShardRouting_Integration creates a real index, queries the
// server's _search_shards API for various routing values, and verifies
// that our client-side shardForRouting() computes the same shard number
// as the server. This is the definitive test that our murmur3
// implementation matches OpenSearch's Murmur3HashFunction.
func TestMurmur3ShardRouting_Integration(t *testing.T) {
	testutil.WaitForCluster(t)

	u := testutil.GetTestURL(t)
	cfg := getTestConfig(t, []*url.URL{u})
	transport, err := New(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const numShards = 5
	index := testutil.MustUniqueString(t, "murmur3-shard-test")

	// --- Create index with known shard count ---
	createBody := fmt.Sprintf(`{
		"settings": {
			"number_of_shards": %d,
			"number_of_replicas": 0
		}
	}`, numShards)
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("/%s", index),
		bytes.NewReader([]byte(createBody)))
	require.NoError(t, err)
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := transport.Perform(createReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, createResp.StatusCode,
		"failed to create index %s", index)
	createResp.Body.Close()

	t.Cleanup(func() {
		delReq, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
			fmt.Sprintf("/%s", index), nil)
		resp, _ := transport.Perform(delReq)
		if resp != nil {
			resp.Body.Close()
		}
	})

	// --- Wait for index to be green ---
	waitForGreen(t, transport, ctx, index)

	// Fetch routing_num_shards from cluster state metadata.
	// This is the actual hash modulus used by the server (e.g. 640 for 5 shards).
	routingNumShards := fetchRoutingNumShardsForTest(t, transport, ctx, index)
	if testutil.IsDebugEnabled(t) {
		t.Logf("index %s: routing_num_shards=%d (numShards=%d)", index, routingNumShards, numShards)
	}

	// --- Verify murmur3 against _search_shards for various routing values ---
	routingValues := []string{
		"user42", "order-12345", "hello", "abc", "0", "1",
		"tenant:acme", "documents/nested/id", "session_9999",
		"deterministic-key", "a", "zzz", "café",
	}

	for _, routing := range routingValues {
		t.Run("routing="+routing, func(t *testing.T) {
			// Ask the server which shard it would use for this routing value.
			serverShard := querySearchShardsForRouting(t, transport, ctx, index, routing)

			// Compute client-side using the scaled routing formula.
			clientShard := shardForRouting(routing, routingNumShards, numShards)

			require.Equal(t, serverShard, clientShard,
				"murmur3 mismatch for routing=%q: server=%d client=%d (numShards=%d)",
				routing, serverShard, clientShard, numShards)

			if testutil.IsDebugEnabled(t) {
				t.Logf("routing=%q -> shard %d (server agrees)", routing, clientShard)
			}
		})
	}

	// --- Verify doc ID as default routing (no ?routing= param) ---
	// Index a document, then verify _search_shards without routing gives
	// the same shard as shardForRouting(docID, routingNumShards, numShards).
	docIDs := []string{"doc-001", "user:alice", "order-99"}
	for _, docID := range docIDs {
		t.Run("docID="+docID, func(t *testing.T) {
			// Index the document.
			indexDoc(t, transport, ctx, index, docID, `{"name":"test"}`)

			// _search_shards with routing=docID should give the same shard as
			// if we hash the docID (OpenSearch uses _id as default routing).
			serverShard := querySearchShardsForRouting(t, transport, ctx, index, docID)
			clientShard := shardForRouting(docID, routingNumShards, numShards)

			require.Equal(t, serverShard, clientShard,
				"docID-as-routing mismatch for docID=%q: server=%d client=%d",
				docID, serverShard, clientShard)

			if testutil.IsDebugEnabled(t) {
				t.Logf("docID=%q -> shard %d (server agrees)", docID, clientShard)
			}
		})
	}
}

// querySearchShardsForRouting calls /{index}/_search_shards?routing=X and
// returns the shard number from the server's response.
func querySearchShardsForRouting(t *testing.T, transport *Client, ctx context.Context, index, routing string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("/%s/_search_shards?routing=%s", index, url.QueryEscape(routing)),
		nil)
	require.NoError(t, err)

	resp, err := transport.Perform(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"_search_shards failed for routing=%q: %s", routing, string(body))

	// Parse the response to extract the shard number.
	// _search_shards returns:
	//   {"shards": [[{"shard": N, ...}]], "nodes": {...}}
	// With ?routing=X, there should be exactly 1 shard group.
	var result struct {
		Shards [][]struct {
			Shard int `json:"shard"`
		} `json:"shards"`
	}
	require.NoError(t, json.Unmarshal(body, &result),
		"failed to parse _search_shards response for routing=%q", routing)

	require.NotEmpty(t, result.Shards,
		"_search_shards returned no shards for routing=%q", routing)

	// All copies in the first (and only) shard group should have the same number.
	shardNum := result.Shards[0][0].Shard
	for _, copy := range result.Shards[0] {
		require.Equal(t, shardNum, copy.Shard,
			"inconsistent shard numbers within shard group for routing=%q", routing)
	}

	return shardNum
}

// indexDoc indexes a JSON document at /{index}/_doc/{id}.
func indexDoc(t *testing.T, transport *Client, ctx context.Context, index, docID, body string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("/%s/_doc/%s", index, url.PathEscape(docID)),
		bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := transport.Perform(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	require.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated,
		"indexDoc failed for %s/%s: %d %s", index, docID, resp.StatusCode, string(respBody))
}

// waitForGreen polls the cluster health for the index until it turns green.
func waitForGreen(t *testing.T, transport *Client, ctx context.Context, index string) {
	t.Helper()

	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("/_cluster/health/%s?wait_for_status=green&timeout=1s", url.PathEscape(index)),
			nil)
		if err != nil {
			return false
		}
		resp, err := transport.Perform(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 500*time.Millisecond,
		"index %s did not reach green status", index)
}

// TestShardExactRouting_FullPipeline_Integration verifies the complete
// shard-exact routing pipeline against a real cluster:
//
//  1. Creates a client with DefaultRouter and an observer
//  2. Creates a 5-shard index with 1 replica (distributed across cluster nodes)
//  3. Runs DiscoverNodes to populate node topology and shard placement via /_cat/shards
//  4. Issues search requests with ?routing=X through Perform()
//  5. Verifies via observer that:
//     a. ShardExactMatch is true (shard-exact routing path was taken)
//     b. TargetShard matches the server's _search_shards response
//     c. The selected node actually hosts the target shard (cross-referenced)
func TestShardExactRouting_FullPipeline_Integration(t *testing.T) {
	testutil.WaitForCluster(t)
	testutil.SkipIfSingleNode(t, 2) // 1 replica requires at least 2 nodes for green health

	// OpenSearch 2.1.0 with the security plugin returns HTTP 500 on search
	// requests routed to specific shard-hosting nodes. The insecure 2.1.0
	// compat job passes; this is a server-side security plugin bug.
	if testutil.IsSecure(t) {
		testutil.SkipIfVersion(t, "=", "2.1.0", "shard-exact pipeline (security plugin HTTP 500)")
	}

	u := testutil.GetTestURL(t)

	// --- Observer that captures RouteEvent per request ---
	obs := &integrationShardObserver{}

	// --- Build client with DefaultRouter + observer ---
	cfg := getTestConfig(t, []*url.URL{u})
	cfg.Router = NewDefaultRouter()
	cfg.Observer = obs

	transport, err := New(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	// --- Warm up the DefaultRouter ---
	// The router needs multiple discovery cycles to fully initialize:
	//   Cycle 1: cold start -- discovers nodes, health checks run async,
	//            connections enter role pools as dead (not yet healthy)
	//   Cycle 2: checkDead promotes connections dead->active
	//   Cycle 3: rebuildSortedConns sees active connections, builds sorted snapshot
	// On slower clusters (e.g. v2.0.1 with security plugin), more cycles may be
	// needed. Poll until a probe request produces a RouteEvent, which proves the
	// pool router's sortedConns is populated and the observer pipeline works.
	require.Eventually(t, func() bool {
		if err := transport.DiscoverNodes(ctx); err != nil {
			return false
		}
		obs.reset()
		probeReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
		resp, err := transport.Perform(probeReq)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return obs.lastEvent() != nil
	}, 30*time.Second, 200*time.Millisecond, "router did not stabilize: no RouteEvent after repeated DiscoverNodes")
	obs.reset()

	const numShards = 5
	index := testutil.MustUniqueString(t, "shard-pipeline-test")

	// --- Create index with 5 primaries and 1 replica ---
	// With 1 replica on a 3-node cluster, each shard has 2 copies
	// spread across nodes. This exercises the candidate selection logic.
	createBody := fmt.Sprintf(`{
		"settings": {
			"number_of_shards": %d,
			"number_of_replicas": 1
		}
	}`, numShards)
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("/%s", index),
		bytes.NewReader([]byte(createBody)))
	require.NoError(t, err)
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := transport.Perform(createReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, createResp.StatusCode,
		"failed to create index %s", index)
	createResp.Body.Close()

	t.Cleanup(func() {
		delReq, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
			fmt.Sprintf("/%s", index), nil)
		resp, _ := transport.Perform(delReq)
		if resp != nil {
			resp.Body.Close()
		}
	})

	// Wait for all shard copies to be allocated and started.
	waitForGreen(t, transport, ctx, index)

	// Fetch routing_num_shards for the scaled shard formula.
	routingNumShards := fetchRoutingNumShardsForTest(t, transport, ctx, index)
	if testutil.IsDebugEnabled(t) {
		t.Logf("index %s: routing_num_shards=%d (numShards=%d)", index, routingNumShards, numShards)
	}

	// --- Warm the router cache for this index ---
	// The router cache creates index slots lazily (on first request).
	// We need the slot to exist before DiscoverNodes so that
	// fetchAndUpdateShardPlacement can populate its shardMap.
	warmReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("/%s/_search", index),
		bytes.NewReader([]byte(`{"query":{"match_all":{}},"size":0}`)))
	require.NoError(t, err)
	warmReq.Header.Set("Content-Type", "application/json")
	warmResp, err := transport.Perform(warmReq)
	require.NoError(t, err)
	warmResp.Body.Close()

	// --- Run DiscoverNodes until the shard map is fully populated ---
	// A single DiscoverNodes call may not suffice: the /_cat/shards
	// response, routing_num_shards metadata fetch, and connection name
	// population all need to converge. On slower CI clusters the first
	// cycle may see incomplete data. Poll until the index slot's shardMap
	// has RoutingNumShards > 0 and at least one shard entry, proving the
	// full pipeline (cat shards + cluster state metadata) completed.
	routerPolicy, ok := transport.router.(Policy)
	require.True(t, ok, "router does not implement Policy")
	cache := findRouterCache(routerPolicy)
	require.NotNil(t, cache, "router has no indexSlotCache")

	require.Eventually(t, func() bool {
		if err := transport.DiscoverNodes(ctx); err != nil {
			return false
		}
		slot := cache.slotFor(index)
		if slot == nil {
			return false
		}
		sm := slot.shardMap.Load()
		if sm == nil || sm.RoutingNumShards == 0 || len(sm.Shards) < numShards {
			return false
		}
		// Verify every shard has at least one node with a matching connection.
		// This catches the race where /_cat/shards returned data but the
		// poolRouter's sortedConns don't yet have Name fields populated.
		for _, sn := range sm.Shards {
			if sn == nil || (sn.Primary == "" && len(sn.Replicas) == 0) {
				return false
			}
		}
		return true
	}, 30*time.Second, 500*time.Millisecond,
		"shard map for %s not populated after repeated DiscoverNodes", index)

	// Build a plain transport (no router) for ground truth queries.
	plainCfg := getTestConfig(t, []*url.URL{u})
	plainTransport, err := New(plainCfg)
	require.NoError(t, err)

	// Build ground truth: for each routing value, ask the server which
	// shard and nodes it would use via _search_shards.
	type groundTruth struct {
		shardNum  int
		nodeNames map[string]struct{}
	}

	routingValues := []string{
		"user42", "order-12345", "hello", "abc", "0", "1",
		"tenant:acme", "session_9999", "deterministic-key",
	}

	truth := make(map[string]groundTruth, len(routingValues))
	for _, routing := range routingValues {
		shard, nodes := querySearchShardsWithNodes(t, plainTransport, ctx, index, routing)
		truth[routing] = groundTruth{shardNum: shard, nodeNames: nodes}
		if testutil.IsDebugEnabled(t) {
			t.Logf("ground truth: routing=%q -> shard %d nodes=%v", routing, shard, nodes)
		}
	}

	// --- Issue requests through the full pipeline ---
	// For each routing value, issue a /{index}/_search?routing=X request
	// through Perform(). The router should:
	// - Extract ?routing=X
	// - Compute murmur3 -> shard number
	// - Select a node hosting that shard
	for _, routing := range routingValues {
		t.Run("pipeline/routing="+routing, func(t *testing.T) {
			gt := truth[routing]

			// Clear the observer's last event.
			obs.reset()

			// Build the search request with ?routing=.
			searchReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
				fmt.Sprintf("/%s/_search?routing=%s", index, url.QueryEscape(routing)),
				bytes.NewReader([]byte(`{"query":{"match_all":{}}}`)))
			require.NoError(t, err)
			searchReq.Header.Set("Content-Type", "application/json")

			resp, err := transport.Perform(searchReq)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			require.Equal(t, http.StatusOK, resp.StatusCode,
				"search failed for routing=%q: %s", routing, string(body))

			// Verify the observer captured the routing event.
			event := obs.lastEvent()
			require.NotNil(t, event,
				"observer did not capture an RouteEvent for routing=%q", routing)

			// (a) Shard-exact routing was used.
			require.True(t, event.ShardExactMatch,
				"routing=%q: expected ShardExactMatch=true, got false (shard map not loaded?)", routing)

			// (b) TargetShard matches server ground truth.
			require.Equal(t, gt.shardNum, event.TargetShard,
				"routing=%q: TargetShard=%d but server says shard=%d",
				routing, event.TargetShard, gt.shardNum)

			// Also verify against our client-side murmur3.
			clientShard := shardForRouting(routing, routingNumShards, numShards)
			require.Equal(t, clientShard, event.TargetShard,
				"routing=%q: observer TargetShard=%d but shardForRouting=%d",
				routing, event.TargetShard, clientShard)

			// (c) Selected node hosts the target shard.
			selectedNode := event.Selected.Name
			_, nodeHostsShard := gt.nodeNames[selectedNode]
			require.True(t, nodeHostsShard,
				"routing=%q shard=%d: selected node %q does not host this shard; "+
					"nodes hosting shard: %v",
				routing, gt.shardNum, selectedNode, gt.nodeNames)

			// (d) RoutingValue was captured correctly.
			require.Equal(t, routing, event.RoutingValue)

			if testutil.IsDebugEnabled(t) {
				t.Logf("routing=%q -> shard %d -> node %q (verified against server)",
					routing, event.TargetShard, selectedNode)
			}
		})
	}
}

// integrationShardObserver captures the most recent RouteEvent.
type integrationShardObserver struct {
	BaseConnectionObserver
	mu    sync.Mutex
	event *RouteEvent
}

func (o *integrationShardObserver) OnRoute(e RouteEvent) {
	o.mu.Lock()
	o.event = &e
	o.mu.Unlock()
}

func (o *integrationShardObserver) reset() {
	o.mu.Lock()
	o.event = nil
	o.mu.Unlock()
}

func (o *integrationShardObserver) lastEvent() *RouteEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.event
}

// querySearchShardsWithNodes calls /{index}/_search_shards?routing=X and
// returns both the shard number and the set of node names hosting that shard.
func querySearchShardsWithNodes( //nolint:nonamedreturns // named returns document the two result values
	t *testing.T, transport *Client, ctx context.Context, index, routing string,
) (shardNum int, nodeNames map[string]struct{}) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("/%s/_search_shards?routing=%s", index, url.QueryEscape(routing)),
		nil)
	require.NoError(t, err)

	resp, err := transport.Perform(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"_search_shards failed for routing=%q: %s", routing, string(body))

	var result struct {
		Shards [][]struct {
			Shard int    `json:"shard"`
			Node  string `json:"node"` // Node ID (not name)
		} `json:"shards"`
		Nodes map[string]struct {
			Name string `json:"name"` // Human-readable node name
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(body, &result),
		"failed to parse _search_shards response for routing=%q", routing)
	require.NotEmpty(t, result.Shards,
		"_search_shards returned no shards for routing=%q", routing)

	shardNum = result.Shards[0][0].Shard

	// Build the set of node names that host this shard.
	nodeNames = make(map[string]struct{})
	for _, copy := range result.Shards[0] {
		require.Equal(t, shardNum, copy.Shard)
		if node, ok := result.Nodes[copy.Node]; ok {
			nodeNames[node.Name] = struct{}{}
		}
	}
	require.NotEmpty(t, nodeNames,
		"_search_shards returned no node names for routing=%q shard=%d", routing, shardNum)

	return shardNum, nodeNames
}

// fetchRoutingNumShardsForTest queries _cluster/state/metadata/{index} to get
// the index's routing_num_shards value. This is the hash modulus used by
// OpenSearch's OperationRouting.calculateScaledShardId.
//
// Early OpenSearch versions (e.g. 2.0.1) can return HTTP 500 with
// java.io.OptionalDataException when the cluster state is still settling
// after index creation. The function retries on 5xx to tolerate this.
func fetchRoutingNumShardsForTest(t *testing.T, transport *Client, ctx context.Context, index string) int {
	t.Helper()

	var routingNumShards int
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("/_cluster/state/metadata/%s?filter_path=metadata.indices.*.routing_num_shards",
				url.PathEscape(index)),
			nil)
		if err != nil {
			return false
		}

		resp, err := transport.Perform(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return false
		}

		// Retry on 5xx — server may still be settling cluster state
		// (e.g. OptionalDataException on OpenSearch 2.0.x).
		if resp.StatusCode >= 500 {
			if testutil.IsDebugEnabled(t) {
				t.Logf("_cluster/state/metadata/%s returned %d, retrying: %s",
					index, resp.StatusCode, string(body))
			}
			return false
		}

		if resp.StatusCode != http.StatusOK {
			t.Logf("_cluster/state/metadata/%s returned unexpected %d: %s",
				index, resp.StatusCode, string(body))
			return false
		}

		// Response: {"metadata":{"indices":{"<index>":{"routing_num_shards":N}}}}
		var result struct {
			Metadata struct {
				Indices map[string]struct {
					RoutingNumShards int `json:"routing_num_shards"`
				} `json:"indices"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return false
		}

		idx, ok := result.Metadata.Indices[index]
		if !ok || idx.RoutingNumShards <= 0 {
			return false
		}

		routingNumShards = idx.RoutingNumShards
		return true
	}, 30*time.Second, 500*time.Millisecond,
		"_cluster/state/metadata for %s did not return routing_num_shards", index)

	return routingNumShards
}
