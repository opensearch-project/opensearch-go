// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestBulkIndexerTransportDoesNotCapRequests runs the bulk client over the real
// transport, which finds the node and the index's shard map as it does against
// a cluster.
func TestBulkIndexerTransportDoesNotCapRequests(t *testing.T) {
	t.Parallel()

	const (
		shards     = 8
		numWorkers = 4
	)

	m := DefaultShardMap(shards)
	var catShards []string
	for shard := range shards {
		catShards = append(catShards,
			fmt.Sprintf(`{"index":"alpha","shard":"%d","prirep":"p","state":"STARTED","node":"n0"}`, shard))
	}

	// What the node answers, other than bulk requests.
	responses := map[string]string{
		"/": `{"name":"n0","cluster_name":"test","version":{"number":"3.0.0","distribution":"opensearch"}}`,
		"/_nodes/http": `{"nodes":{"n0":{"name":"n0","roles":["data","ingest","cluster_manager"],` +
			`"http":{"publish_address":"127.0.0.1:9200"}}}}`,
		"/_cluster/health": `{"cluster_name":"test","status":"green","number_of_nodes":1,"number_of_data_nodes":1}`,
		"/_cat/shards":     "[" + strings.Join(catShards, ",") + "]",
		"/_cluster/state/metadata/alpha": fmt.Sprintf(`{"metadata":{"indices":{"alpha":{"routing_num_shards":%d,`+
			`"settings":{"index":{"number_of_shards":"%d"}}}}}}`, m.RoutingNumShards, m.PrimaryShards),
	}

	server := newFakeServer(newFakeCluster(map[string]ShardMap{"alpha": m}, "n0"))

	client := newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
		body, ok := responses[req.URL.Path]
		if !ok {
			return server.roundTrip(req)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}, func(cfg *opensearchapi.Config) {
		cfg.Client.Addresses = []string{"http://127.0.0.1:9200"}
		cfg.Client.DiscoverNodesOnStart = new(false)
	})

	// The first lookup registers the index; discovery then fetches its shard map
	// and where its shards are.
	router := ClientShardRouter(client)
	require.NotNil(t, router)
	require.Eventually(t, func() bool {
		if target, known := lookupOne(router, "alpha", "x"); known && target.Node.Conn != nil {
			return true
		}
		require.NoError(t, client.Client.DiscoverNodes(t.Context()))

		return false
	}, 10*time.Second, 10*time.Millisecond, "the transport never placed the index's shards")

	// One document for each shard, each filling its buffer.
	ids := map[int]string{}
	for i := 0; len(ids) < shards; i++ {
		id := fmt.Sprintf("d%04d", i)
		if _, ok := ids[m.Shard(id)]; !ok {
			ids[m.Shard(id)] = id
		}
	}

	cfg := fixedBatches(docBytes("alpha", "d0000", 1000), 1)
	cfg.Client = client
	cfg.NumWorkers = numWorkers
	bi, err := NewBulkIndexer(cfg)
	require.NoError(t, err)

	for shard := range shards {
		addDoc(t, bi, "alpha", ids[shard], 1000+shard)
	}
	require.NoError(t, bi.Close(t.Context()))

	for _, req := range server.requests() {
		require.Equal(t, "n0", req.node)
	}
	require.EqualValues(t, shards, bi.Stats().NumFlushed)
}
