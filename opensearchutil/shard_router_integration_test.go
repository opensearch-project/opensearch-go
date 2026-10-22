// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchutil)

package opensearchutil_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	transporttestutil "github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// seqBody returns the source document the tests below index.
func seqBody(seq int) *strings.Reader {
	return strings.NewReader(fmt.Sprintf(`{"seq":%d}`, seq))
}

// createShardTestIndex creates an index with no replicas, waits for it to be
// usable, and deletes it when the test ends. Its shards stay where they are
// first allocated: a test compares the router's placement with the server's,
// and a rebalance between the two reads makes them disagree.
func createShardTestIndex(
	t *testing.T, ctx context.Context, client *opensearchapi.Client, name string, settings string,
) {
	t.Helper()

	var body struct {
		Settings map[string]map[string]any `json:"settings"`
	}
	require.NoError(t, json.Unmarshal([]byte(settings), &body.Settings))
	require.Contains(t, body.Settings, "index")
	body.Settings["index"]["routing.rebalance.enable"] = "none"
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	_, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index:      name,
		BodyReader: bytes.NewReader(raw),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.WithoutCancel(ctx), &opensearchapi.IndicesDeleteReq{Indices: []string{name}})
	})

	waitForShardTestIndex(t, ctx, client, name)
}

func waitForShardTestIndex(t *testing.T, ctx context.Context, client *opensearchapi.Client, name string) {
	t.Helper()

	_, err := client.Cluster.Health(ctx, &opensearchapi.ClusterHealthReq{
		Indices: []string{name},
		Params:  &opensearchapi.ClusterHealthParams{WaitForStatus: "green"},
	})
	require.NoError(t, err)
}

// clusterShardMap reads the authoritative routing parameters of an index from
// the cluster state: routing_num_shards and settings.index.number_of_shards.
func clusterShardMap(
	t *testing.T, ctx context.Context, client *opensearchapi.Client, index string,
) opensearchutil.ShardMap {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"/_cluster/state/metadata/"+index+"?filter_path=metadata.indices.*.routing_num_shards,metadata.indices.*.settings.index.number_of_shards",
		nil)
	require.NoError(t, err)

	res, err := client.Client.Request(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var state struct {
		Metadata struct {
			Indices map[string]struct {
				RoutingNumShards int `json:"routing_num_shards"`
				Settings         struct {
					Index struct {
						NumberOfShards json.Number `json:"number_of_shards"`
					} `json:"index"`
				} `json:"settings"`
			} `json:"indices"`
		} `json:"metadata"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&state))

	meta, ok := state.Metadata.Indices[index]
	require.True(t, ok, "cluster state has no metadata for %q", index)

	primaries, err := meta.Settings.Index.NumberOfShards.Int64()
	require.NoError(t, err)

	return opensearchutil.ShardMap{RoutingNumShards: meta.RoutingNumShards, PrimaryShards: int(primaries)}
}

// awaitShardTarget looks a routing value up through the client's shard router
// and runs discovery until the router answers for the index. The first lookup
// registers the index; discovery then fetches its shard map and placement.
func awaitShardTarget(
	t *testing.T, ctx context.Context, client *opensearchapi.Client, index, routing string,
) opensearchutil.ShardTarget {
	t.Helper()

	router := opensearchutil.ClientShardRouter(client)
	require.NotNil(t, router, "%T does not offer a shard router", client.Client.Transport)

	var got opensearchutil.ShardTarget
	err := transporttestutil.PollUntil(t, ctx, 100*time.Millisecond, 100, 0.1, func() (bool, error) {
		target, known := lookupOne(router, index, routing)
		if known && target.Node.Name != "" {
			got = target
			return true, nil
		}

		if err := client.Client.DiscoverNodes(ctx); err != nil {
			return false, err
		}

		return false, nil
	})
	require.NoError(t, err, "the router never placed %q in %q", routing, index)

	return got
}

// TestDefaultRoutingNumShardsMatchesCluster_Integration verifies the no-cluster
// derivation against the routing_num_shards a live cluster assigns.
func TestDefaultRoutingNumShardsMatchesCluster_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	tests := []struct {
		name      string
		numShards int
	}{
		{name: "1 shard", numShards: 1},
		{name: "2 shards", numShards: 2},
		{name: "3 shards", numShards: 3},
		{name: "5 shards", numShards: 5},
		{name: "8 shards", numShards: 8},
		{name: "12 shards", numShards: 12},
		{name: "32 shards", numShards: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := transporttestutil.MustUniqueString(t, "shard-derive")
			createShardTestIndex(t, ctx, client, index,
				fmt.Sprintf(`{"index":{"number_of_shards":%d,"number_of_replicas":0}}`, tt.numShards))

			got := clusterShardMap(t, ctx, client, index)

			require.Equal(t, tt.numShards, got.PrimaryShards)
			require.Equal(t, opensearchutil.DefaultRoutingNumShards(tt.numShards), got.RoutingNumShards,
				"derived routing_num_shards differs from the cluster for %d shards", tt.numShards)
			require.Equal(t, opensearchutil.DefaultShardMap(tt.numShards), got)
		})
	}
}

// primaryNodeOf asks the server which node hosts the primary of the shard a
// routing value maps to, by name.
func primaryNodeOf(
	t *testing.T, ctx context.Context, client *opensearchapi.Client, index, routing string,
) (int, string) {
	t.Helper()

	res, err := client.SearchShards(ctx, &opensearchapi.SearchShardsReq{
		Indices: []string{index},
		Params:  &opensearchapi.SearchShardsParams{Routing: []string{routing}},
	})
	require.NoError(t, err)
	require.Len(t, res.Shards, 1)

	for _, shardCopy := range res.Shards[0] {
		if !shardCopy.Primary {
			continue
		}

		require.NotNil(t, shardCopy.Node)
		node := *shardCopy.Node
		if attrs, ok := res.Nodes[node]; ok {
			node = attrs.Name
		}

		return shardCopy.Shard, node
	}

	t.Fatalf("no primary for routing %q in %q", routing, index)

	return 0, ""
}

// TestShardRouterPlacesDocumentsOnTheirPrimary_Integration checks the router
// against the server: for many routing values, the shard it computes and the node
// it names are the ones the server reports for the primary.
func TestShardRouterPlacesDocumentsOnTheirPrimary_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	index := transporttestutil.MustUniqueString(t, "shard-place")
	createShardTestIndex(t, ctx, client, index, `{"index":{"number_of_shards":6,"number_of_replicas":0}}`)

	awaitShardTarget(t, ctx, client, index, "warm-up")
	router := opensearchutil.ClientShardRouter(client)

	nodes := map[string]struct{}{}
	for i := range 60 {
		routing := "doc-" + strconv.Itoa(i)

		target, ok := lookupOne(router, index, routing)
		require.True(t, ok)
		require.NotEmpty(t, target.Node.Name, "placement of %q is unknown", routing)

		wantShard, wantNode := primaryNodeOf(t, ctx, client, index, routing)
		require.Equal(t, wantShard, target.Shard, "shard of %q", routing)
		require.Equal(t, wantNode, target.Node.Name, "the node hosting the primary of %q", routing)
		nodes[target.Node.Name] = struct{}{}
	}

	t.Run("shards spread across nodes", func(t *testing.T) {
		transporttestutil.RequireMinNodes(t, ctx, 2)
		require.Greater(t, len(nodes), 1, "six shards should land on more than one node of a multi-node cluster")
	})
}

// TestShardRouterClusterWinsAfterSplit_Integration splits an index whose
// routing_num_shards was set explicitly. A split keeps the source's
// routing_num_shards, so the no-cluster derivation is wrong for the target, and a
// router chain must prefer what the cluster reports.
func TestShardRouterClusterWinsAfterSplit_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	const (
		sourceShards  = 2
		targetShards  = 4
		routingShards = 8
	)

	source := transporttestutil.MustUniqueString(t, "shard-split-src")
	target := transporttestutil.MustUniqueString(t, "shard-split-dst")

	createShardTestIndex(t, ctx, client, source, fmt.Sprintf(
		`{"index":{"number_of_shards":%d,"number_of_routing_shards":%d,"number_of_replicas":0}}`,
		sourceShards, routingShards))

	_, err = client.Indices.AddBlock(ctx, opensearchapi.IndicesAddBlockReq{Indices: []string{source}, Block: "write"})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.WithoutCancel(ctx), &opensearchapi.IndicesDeleteReq{Indices: []string{target}})
	})

	_, err = client.Indices.Split(ctx, opensearchapi.IndicesSplitReq{
		Index:  source,
		Target: target,
		BodyReader: strings.NewReader(fmt.Sprintf(
			`{"settings":{"index":{"number_of_shards":%d,"number_of_replicas":0}}}`, targetShards)),
	})
	require.NoError(t, err)
	waitForShardTestIndex(t, ctx, client, target)

	want := opensearchutil.ShardMap{RoutingNumShards: routingShards, PrimaryShards: targetShards}
	require.Equal(t, want, clusterShardMap(t, ctx, client, target), "a split must keep routing_num_shards")

	derived := opensearchutil.DefaultShardMap(targetShards)
	require.NotEqual(t, want, derived, "the derivation must be wrong here, or the test proves nothing")

	// Find a routing value the two disagree on.
	var routing string
	for i := 0; ; i++ {
		routing = "probe-" + strconv.Itoa(i)
		if want.Shard(routing) != derived.Shard(routing) {
			break
		}
	}

	chain := opensearchutil.ChainShardRouter(
		opensearchutil.ClientShardRouter(client),
		opensearchutil.StaticShardRouter{target: derived},
	)

	// Until the transport has the shard map the chain falls back to the derivation.
	before, ok := lookupOne(chain, target, routing)
	require.True(t, ok)
	if before.Shard != derived.Shard(routing) {
		require.Equal(t, want.Shard(routing), before.Shard, "discovery had already run")
	}

	awaitShardTarget(t, ctx, client, target, routing)

	after, ok := lookupOne(chain, target, routing)
	require.True(t, ok)
	require.Equal(t, want.Shard(routing), after.Shard, "the cluster's shard map must win over the derivation")
	wantShard, _ := primaryNodeOf(t, ctx, client, target, routing)
	require.Equal(t, wantShard, after.Shard, "and agree with the server")
	_ = shardhash.ForRouting
}

// requestHosts records the node of every bulk request, as the transport sent it.
type requestHosts struct {
	opensearchtransport.BaseConnectionObserver

	mu    sync.Mutex
	hosts []string
}

// OnRequestResponse sees the node the request was finally sent to; at the start
// of the request none has been chosen yet.
func (o *requestHosts) OnRequestResponse(_ context.Context, e opensearchtransport.RequestResponseEvent) {
	if strings.HasSuffix(e.Request.Path, "/_bulk") {
		o.mu.Lock()
		o.hosts = append(o.hosts, e.Request.Host)
		o.mu.Unlock()
	}
}

func (o *requestHosts) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]string(nil), o.hosts...)
}

// TestBulkIndexerSendsEachBatchToItsPrimary_Integration indexes through the
// client's own transport, with no router configured, and checks on the wire that
// each request went to the node hosting the shards it carries, that the request
// count is a few per node and not one per shard, and that the server holds every
// document.
func TestBulkIndexerSendsEachBatchToItsPrimary_Integration(t *testing.T) {
	observer := &requestHosts{}
	cfg := testutil.ClientConfig(t)
	cfg.Client.Observer = observer
	client, err := opensearchapi.NewClient(*cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	const (
		numShards = 9
		numDocs   = 2000
	)

	index := transporttestutil.MustUniqueString(t, "bulk-shard-route")
	createShardTestIndex(t, ctx, client, index,
		fmt.Sprintf(`{"index":{"number_of_shards":%d,"number_of_replicas":0}}`, numShards))

	// Let the transport learn the index before indexing, so every item can be
	// placed from the first one.
	awaitShardTarget(t, ctx, client, index, "warm-up")
	router := opensearchutil.ClientShardRouter(client)

	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client:        client,
		FlushInterval: time.Hour, // Only Close flushes.
	})
	require.NoError(t, err)

	hostOf := map[string]string{} // node name -> host the transport contacts it at
	for i := range numDocs {
		docID := "doc-" + strconv.Itoa(i)
		require.NoError(t, bi.Add(ctx, opensearchutil.BulkIndexerItem{
			Index: index, Action: "index", DocumentID: docID, Body: seqBody(i),
		}))

		target, ok := lookupOne(router, index, docID)
		require.True(t, ok)
		require.NotNil(t, target.Node.Conn)
		hostOf[target.Node.Name] = target.Node.Conn.URL.Host
	}
	require.NoError(t, bi.Close(ctx))

	stats := bi.Stats()
	require.EqualValues(t, numDocs, stats.NumIndexed)
	require.Zero(t, stats.NumFailed)
	require.EqualValues(t, numDocs, stats.NumGroupedItems)

	// Each request went to a node that hosts shards of the index, and every such
	// node was written to.
	wantHosts := map[string]struct{}{}
	for _, host := range hostOf {
		wantHosts[host] = struct{}{}
	}
	gotHosts := map[string]int{}
	for _, host := range observer.seen() {
		gotHosts[strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")]++
	}
	for host := range gotHosts {
		require.Contains(t, wantHosts, host, "a bulk request went to %q, which hosts no shard of the index", host)
	}
	require.Len(t, gotHosts, len(wantHosts), "every node hosting shards must have been written to")

	// Per-shard counts on the server match the client's grouping.
	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
	require.NoError(t, err)

	shards, err := client.Cat.Shards(ctx, &opensearchapi.CatShardsReq{Indices: []string{index}})
	require.NoError(t, err)

	m := clusterShardMap(t, ctx, client, index)
	wantPerShard := map[int]int{}
	for i := range numDocs {
		wantPerShard[shardhash.ForRouting("doc-"+strconv.Itoa(i), m.RoutingNumShards, m.PrimaryShards)]++
	}
	gotPerShard := map[int]int{}
	for _, rec := range shards.Records {
		shard, shardErr := strconv.Atoi(*rec.Shard)
		require.NoError(t, shardErr)
		docs, docsErr := strconv.Atoi(*rec.Docs)
		require.NoError(t, docsErr)
		gotPerShard[shard] += docs
	}
	require.Equal(t, wantPerShard, gotPerShard)
}

// TestBulkIndexerKeepsOrderWhenShardMapArrives_Integration writes the same
// documents repeatedly while the index goes from unknown to known to the
// transport, and requires every document to end up holding its last write.
func TestBulkIndexerKeepsOrderWhenShardMapArrives_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	const (
		numShards = 4
		numIDs    = 40
		rounds    = 12
	)

	index := transporttestutil.MustUniqueString(t, "bulk-shard-order")
	createShardTestIndex(t, ctx, client, index,
		fmt.Sprintf(`{"index":{"number_of_shards":%d,"number_of_replicas":0}}`, numShards))

	router := opensearchutil.ClientShardRouter(client)
	require.NotNil(t, router, "the default client's transport must offer a shard router")
	_, known := lookupOne(router, index, "x")
	require.False(t, known, "the index must be unknown to the transport when the writes start")

	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client:        client,
		FlushBytes:    2000, // Small, so batches fill and are sent many times.
		Sizing:        opensearchutil.BulkSizing{Shard: opensearchutil.ShardFixed, Target: opensearchutil.TargetFixed},
		FlushInterval: time.Hour,
	})
	require.NoError(t, err)

	last := map[string]int{}
	seq := 0
	addRound := func() {
		for id := range numIDs {
			seq++
			docID := "doc-" + strconv.Itoa(id)
			require.NoError(t, bi.Add(ctx, opensearchutil.BulkIndexerItem{
				Index: index, Action: "index", DocumentID: docID, Body: seqBody(seq),
			}))
			last[docID] = seq
		}
	}

	for range rounds / 2 {
		addRound() // Shard map unknown: buffered without a shard.
	}

	require.NoError(t, client.Client.DiscoverNodes(ctx)) // The transport fetches the index's shard map.
	_, known = lookupOne(router, index, "x")
	require.True(t, known, "discovery must have fetched the shard map")

	for range rounds / 2 {
		addRound() // Shard map known: the first of these drains, then places.
	}
	require.NoError(t, bi.Close(ctx))

	stats := bi.Stats()
	require.EqualValues(t, numIDs*rounds, stats.NumIndexed)
	require.Zero(t, stats.NumFailed)

	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
	require.NoError(t, err)

	size := numIDs * 2
	res, err := client.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    &opensearchapi.SearchBody{Size: &size},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits.Hits, numIDs)

	got := map[string]int{}
	for _, hit := range res.Hits.Hits {
		var src struct {
			Seq int `json:"seq"`
		}
		require.NoError(t, json.Unmarshal(hit.Source, &src))
		got[*hit.ID] = src.Seq
	}
	require.Equal(t, last, got, "every document must hold the last write made to it")
}

// TestBulkIndexerWithWrongShardMapStillIndexesEverything_Integration gives the
// bulk client a shard map that disagrees with the cluster, as a stale one would. The
// server regroups every bulk by the shard it really maps each document to, so
// every document must land, and the wrong guess costs efficiency and nothing else.
func TestBulkIndexerWithWrongShardMapStillIndexesEverything_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	const (
		realShards    = 8
		claimedShards = 3
		numDocs       = 500
	)

	index := transporttestutil.MustUniqueString(t, "bulk-shard-stale")
	createShardTestIndex(t, ctx, client, index,
		fmt.Sprintf(`{"index":{"number_of_shards":%d,"number_of_replicas":0}}`, realShards))

	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client:        client,
		FlushInterval: time.Hour,
		ShardRouter:   opensearchutil.StaticShardRouter{index: opensearchutil.DefaultShardMap(claimedShards)},
	})
	require.NoError(t, err)

	for i := range numDocs {
		require.NoError(t, bi.Add(ctx, opensearchutil.BulkIndexerItem{
			Index: index, Action: "index", DocumentID: "doc-" + strconv.Itoa(i), Body: seqBody(i),
		}))
	}
	require.NoError(t, bi.Close(ctx))

	stats := bi.Stats()
	require.EqualValues(t, numDocs, stats.NumIndexed)
	require.Zero(t, stats.NumFailed)
	require.EqualValues(t, numDocs, stats.NumGroupedItems, "the indexer placed documents by the shard map it was given")

	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
	require.NoError(t, err)

	counted, err := client.Count(ctx, &opensearchapi.CountReq{Indices: []string{index}})
	require.NoError(t, err)
	require.EqualValues(t, numDocs, counted.Count, "every document must be in the index")
}

// TestBulkIndexerSendsOneShardsBatchesToItsPrimary_Integration: every request for
// a single-shard index goes to the one node hosting its primary, however many
// requests there are. Left to the transport's routing, requests for all the
// indexes would go to the same node, so the test uses indexes whose primaries
// are on different nodes.
func TestBulkIndexerSendsOneShardsBatchesToItsPrimary_Integration(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	transporttestutil.RequireMinNodes(t, ctx, 2)

	const (
		numIndexes = 8
		numDocs    = 300
	)

	type sent struct {
		index, wantHost string
	}
	indexes := make([]sent, 0, numIndexes)
	primaries := map[string]struct{}{}
	for range numIndexes {
		index := transporttestutil.MustUniqueString(t, "bulk-one-shard")
		createShardTestIndex(t, ctx, client, index, `{"index":{"number_of_shards":1,"number_of_replicas":0}}`)

		target := awaitShardTarget(t, ctx, client, index, "warm-up")
		_, primary := primaryNodeOf(t, ctx, client, index, "warm-up")
		require.Equal(t, primary, target.Node.Name)

		indexes = append(indexes, sent{index: index, wantHost: target.Node.Conn.URL.Host})
		primaries[primary] = struct{}{}
	}
	require.Greater(t, len(primaries), 1, "the indexes' primaries must be on different nodes for the test to mean anything")

	for _, tt := range indexes {
		observer := &requestHosts{}
		cfg := testutil.ClientConfig(t)
		cfg.Client.Observer = observer
		perIndex, err := opensearchapi.NewClient(*cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = perIndex.Close() })

		// This client has not seen the index, so let it learn it first, and take the
		// primary's node as this client sees it now: on a busy cluster a primary can
		// have moved since the first client looked.
		seen := awaitShardTarget(t, ctx, perIndex, tt.index, "warm-up")
		tt.wantHost = seen.Node.Conn.URL.Host

		bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
			Client:        perIndex,
			FlushBytes:    2000,
			Sizing:        opensearchutil.BulkSizing{Shard: opensearchutil.ShardFixed, Target: opensearchutil.TargetFixed},
			FlushInterval: time.Hour,
		})
		require.NoError(t, err)

		for i := range numDocs {
			require.NoError(t, bi.Add(ctx, opensearchutil.BulkIndexerItem{
				Index: tt.index, Action: "index", DocumentID: "doc-" + strconv.Itoa(i), Body: seqBody(i),
			}))
		}
		require.NoError(t, bi.Close(ctx))
		require.EqualValues(t, numDocs, bi.Stats().NumIndexed)

		hosts := observer.seen()
		require.Greater(t, len(hosts), 3, "the workload must have made several requests")

		// The observer reports the node of a request's final attempt, and a request
		// that fails and is retried moves on from the node it was pinned to, so on a
		// busy cluster a few can end up elsewhere. Without the pin about a third would
		// land on the primary, which is a long way from nearly all.
		onPrimary := 0
		for _, host := range hosts {
			if strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://") == tt.wantHost {
				onPrimary++
			}
		}
		require.GreaterOrEqual(t, onPrimary*10, len(hosts)*9,
			"nearly every request for %s must go to its primary's node: %d of %d did", tt.index, onPrimary, len(hosts))
	}
}
