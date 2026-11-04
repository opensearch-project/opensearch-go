// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchtransport)

package opensearchtransport //nolint:testpackage // requires internal access to Transport and transport internals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ospath "github.com/opensearch-project/opensearch-go/v5/internal/path"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// TestRoutingPartitionSizeShardPrediction_Integration indexes documents into an
// index with routing_partition_size > 1 and verifies that
// Transport.LookupDocShard names the shard the server stored each document on.
func TestRoutingPartitionSizeShardPrediction_Integration(t *testing.T) {
	testutil.WaitForCluster(t)
	testutil.SkipIfVersion(t, "<", "2.0", "routing_num_shards unavailable")
	if testutil.IsSecure(t) {
		testutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	u := testutil.GetTestURL(t)
	cfg, obs := getTestConfigWithReadiness(t, []*url.URL{u})
	misses := &shardMissObserver{readinessObserver: obs}
	cfg.Observer = misses
	router, err := NewDefaultRouter()
	require.NoError(t, err)
	cfg.Router = router
	transport, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = transport.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	const (
		numShards     = 8
		partitionSize = 4
		numDocs       = 200
	)
	index := testutil.MustUniqueString(t, "routing-partition-test")

	// number_of_routing_shards equals number_of_shards so the routing factor is
	// 1. With the default routing_num_shards (1024 for 8 shards, factor 128) the
	// partition offset rarely moves a document to another shard, and the test
	// could not tell the partition rule from ordinary routing.
	createBody := fmt.Sprintf(`{
		"settings": {"number_of_shards": %d, "number_of_routing_shards": %d, "number_of_replicas": 0, "routing_partition_size": %d},
		"mappings": {"_routing": {"required": true}}
	}`, numShards, numShards, partitionSize)

	var createResp *http.Response
	createPath, _ := ospath.IndicesCreatePath{Index: index}.Build()
	requireMinConnsObs(t, transport, obs, ctx, 1, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPut, createPath, bytes.NewReader([]byte(createBody)))
		if reqErr != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, perfErr := transport.Stream(req)
		if perfErr != nil {
			return false
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			resp.Body.Close()
			return false
		}
		createResp = resp
		return true
	})
	createBytes, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode, "failed to create index %q: %s", index, createBytes)

	t.Cleanup(func() {
		delPath, _ := ospath.IndicesDeletePath{Indices: []string{index}}.Build()
		delReq, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, delPath, nil)
		if resp, _ := transport.Stream(delReq); resp != nil {
			resp.Body.Close()
		}
	})

	waitForGreen(t, transport, obs, ctx, index)

	type doc struct{ routing, id string }
	docs := make([]doc, numDocs)
	for i := range docs {
		// Few routing values and many ids, so a partition's shards all get used.
		docs[i] = doc{routing: fmt.Sprintf("tenant-%d", i%10), id: fmt.Sprintf("doc-%d", i)}
		p, _ := ospath.IndexPath{Index: index, ID: docs[i].id}.Build()
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPut, p+"?routing="+url.QueryEscape(docs[i].routing),
			bytes.NewReader([]byte(`{"n":1}`)))
		require.NoError(t, reqErr)
		req.Header.Set("Content-Type", "application/json")
		resp, perfErr := transport.Stream(req)
		require.NoError(t, perfErr)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Contains(t, []int{http.StatusOK, http.StatusCreated}, resp.StatusCode, "index %q: %s", docs[i].id, body)
	}

	refreshPath, _ := ospath.IndicesRefreshPath{Indices: []string{index}}.Build()
	refreshReq, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshPath, nil)
	require.NoError(t, err)
	refreshResp, err := transport.Stream(refreshReq)
	require.NoError(t, err)
	refreshResp.Body.Close()
	require.Equal(t, http.StatusOK, refreshResp.StatusCode)

	// Ask each shard which documents it holds.
	serverShard := make(map[string]int, numDocs)
	for shard := range numShards {
		searchPath, _ := ospath.SearchPath{Indices: []string{index}}.Build()
		q := url.Values{"preference": {fmt.Sprintf("_shards:%d", shard)}, "size": {"1000"}, "_source": {"false"}}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, searchPath+"?"+q.Encode(), nil)
		require.NoError(t, reqErr)
		resp, perfErr := transport.Stream(req)
		require.NoError(t, perfErr)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "search shard %d: %s", shard, body)

		var result struct {
			Hits struct {
				Hits []struct {
					ID string `json:"_id"`
				} `json:"hits"`
			} `json:"hits"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		for _, h := range result.Hits.Hits {
			serverShard[h.ID] = shard
		}
	}
	require.Len(t, serverShard, numDocs, "every document must be found on exactly one shard")

	// Discovery fetches the index's shard map, routing_partition_size included.
	require.Eventually(t, func() bool {
		_ = transport.DiscoverNodes(ctx)
		m, ok := transport.ShardMap(index)

		return ok && m.RoutingPartitionSize == partitionSize
	}, 60*time.Second, time.Second, "the shard map of %q was not discovered with its routing_partition_size", index)

	// The partition offset must have moved documents, or the checks below would
	// pass for ordinary routing too.
	shardsByTenant := make(map[string]map[int]struct{})
	var movedByOffset bool
	for _, d := range docs {
		if shardsByTenant[d.routing] == nil {
			shardsByTenant[d.routing] = make(map[int]struct{})
		}
		shardsByTenant[d.routing][serverShard[d.id]] = struct{}{}
		if serverShard[d.id] != shardhash.ForRouting(d.routing, numShards, numShards) {
			movedByOffset = true
		}
	}
	require.True(t, movedByOffset, "no document sits on a shard other than its routing value alone names")
	var spread bool
	for _, shards := range shardsByTenant {
		spread = spread || len(shards) > 1
	}
	require.True(t, spread, "no tenant's documents span more than one shard")

	for _, d := range docs {
		var targets []*ShardTarget
		require.NoError(t, transport.LookupDocShard(index, d.routing, d.id, &targets), "doc %q", d.id)
		require.NotEmpty(t, targets)
		require.Equal(t, serverShard[d.id], targets[0].Shard,
			"routing=%q id=%q: server shard %d, predicted %d", d.routing, d.id, serverShard[d.id], targets[0].Shard)

		// A key-only lookup cannot name the shard of a partitioned index.
		require.ErrorIs(t, transport.LookupShard(index, d.routing, &targets), ErrDocumentIDRequired)
	}

	// A key-only lookup on the partitioned index reports one miss.
	misses.reset()
	var missTargets []*ShardTarget
	require.ErrorIs(t, transport.LookupShard(index, "tenant-0", &missTargets), ErrDocumentIDRequired)
	require.Equal(t, []ShardLookupMissEvent{{Index: index, Reason: ShardMissDocumentIDRequired}}, misses.snapshot())

	// A search with only a routing value targets the shards of every partition
	// offset; the server names them in _search_shards.
	m, ok := transport.ShardMap(index)
	require.True(t, ok)
	searchShardsPath, _ := ospath.SearchShardsPath{Indices: []string{index}}.Build()
	for tenant := range shardsByTenant {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, searchShardsPath+"?routing="+url.QueryEscape(tenant), nil)
		require.NoError(t, reqErr)
		resp, perfErr := transport.Stream(req)
		require.NoError(t, perfErr)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "_search_shards routing=%q: %s", tenant, body)

		var result struct {
			Shards [][]struct {
				Shard int `json:"shard"`
			} `json:"shards"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		got := make(map[int]struct{})
		for _, group := range result.Shards {
			for _, c := range group {
				got[c.Shard] = struct{}{}
			}
		}

		want := make(map[int]struct{}, partitionSize)
		for i := range partitionSize {
			want[shardhash.ForRoutingOffset(tenant, i, m.RoutingNumShards, m.PrimaryShards)] = struct{}{}
		}
		require.Equal(t, want, got, "routing=%q: _search_shards targets %v, shardhash.ForRoutingOffset predicts %v", tenant, got, want)
	}
}

// shardMissObserver records shard lookup misses on top of the readiness
// observer the transport config already uses.
type shardMissObserver struct {
	*readinessObserver

	mu     sync.Mutex
	events []ShardLookupMissEvent
}

func (o *shardMissObserver) OnShardLookupMiss(e ShardLookupMissEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *shardMissObserver) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = nil
}

func (o *shardMissObserver) snapshot() []ShardLookupMissEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.events)
}
