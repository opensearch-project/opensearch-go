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
// transport, which finds the node, the index's shard map and the node's write
// pool as it does against a cluster, and checks that what the transport makes of
// the pool does not hold the bulk client below NumWorkers requests to the node.
// TestBulkIndexerPoolWindowDoesNotCapRequests sets the pool's window by hand.
func TestBulkIndexerTransportDoesNotCapRequests(t *testing.T) {
	t.Parallel()

	const (
		shards     = 8
		numWorkers = 4
		statsPath  = "/_nodes/_local/stats/jvm,breaker,thread_pool"
	)

	tests := []struct {
		name string
		// statsInterval is the node statistics poll interval; below zero, the
		// node's statistics are never polled.
		statsInterval time.Duration
	}{
		{name: "before any statistics poll", statsInterval: -1},
		{name: "after a poll of a node that rejected writes before the client started", statsInterval: 10 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := DefaultShardMap(shards)
			catShards := make([]string, shards)
			for shard := range shards {
				catShards[shard] = fmt.Sprintf(`{"index":"alpha","shard":"%d","prirep":"p","state":"STARTED","node":"n0"}`, shard)
			}

			// What the node answers, other than bulk requests. Its write pool has 8
			// threads and has rejected 144 tasks since it started, all before this
			// client.
			responses := map[string]string{
				"/": `{"name":"n0","cluster_name":"test","version":{"number":"3.0.0","distribution":"opensearch"}}`,
				"/_nodes/http": `{"nodes":{"n0":{"name":"n0","roles":["data","ingest","cluster_manager"],` +
					`"http":{"publish_address":"127.0.0.1:9200"}}}}`,
				"/_cluster/health": `{"cluster_name":"test","status":"green","number_of_nodes":1,"number_of_data_nodes":1}`,
				"/_cat/shards":     "[" + strings.Join(catShards, ",") + "]",
				"/_cluster/state/metadata/alpha": fmt.Sprintf(`{"metadata":{"indices":{"alpha":{"routing_num_shards":%d,`+
					`"settings":{"index":{"number_of_shards":"%d"}}}}}}`, m.RoutingNumShards, m.PrimaryShards),
				"/_nodes/_local/http,os,thread_pool": `{"nodes":{"n0":{"os":{"allocated_processors":8},` +
					`"thread_pool":{"write":{"type":"fixed","size":8,"queue_size":10000}}}}}`,
				statsPath: `{"nodes":{"n0":{"jvm":{"mem":{"heap_used_percent":10}},"breakers":{},` +
					`"thread_pool":{"write":{"threads":8,"queue":0,"active":0,"rejected":144,"completed":100000}}}}}`,
			}

			server := newFakeServer(newFakeCluster(map[string]ShardMap{"alpha": m}, "n0"))
			server.gate = make(chan struct{})
			server.entered = make(chan string, shards)

			// The second statistics poll waits here, so the test sees the pool after
			// the first poll and before any other.
			secondPoll, resume := make(chan struct{}), make(chan struct{})
			polls := 0

			client := newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
				body, ok := responses[req.URL.Path]
				if !ok {
					return server.roundTrip(req)
				}
				if req.URL.Path == statsPath {
					if polls++; polls == 2 {
						close(secondPoll)
						<-resume
					}
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}, func(cfg *opensearchapi.Config) {
				cfg.Client.Addresses = []string{"http://127.0.0.1:9200"}
				cfg.Client.DiscoverNodesOnStart = new(false)
				cfg.Client.NodeStatsInterval = tt.statsInterval
			})

			// The first lookup registers the index; discovery then fetches its shard
			// map and where its shards are.
			router := ClientShardRouter(client)
			require.NotNil(t, router)
			require.Eventually(t, func() bool {
				if target, known := lookupOne(router, "alpha", "x"); known && target.Node.Conn != nil {
					return true
				}
				require.NoError(t, client.Client.DiscoverNodes(t.Context()))

				return false
			}, 10*time.Second, 10*time.Millisecond, "the transport never placed the index's shards")

			if tt.statsInterval > 0 {
				<-secondPoll
				defer close(resume)
			}
			for _, n := range router.WriteNodes() {
				require.False(t, n.writeSaturated(), "%s reported refusing writes", n.Name)
			}

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

			// The server holds every request, so these are out at once.
			for range numWorkers {
				require.Equal(t, "n0", receive(t, server.entered))
			}
			require.EqualValues(t, numWorkers, bi.Stats().InFlightRequests)

			close(server.gate)
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, numWorkers, server.maxInflight(), "never more than NumWorkers")
			require.EqualValues(t, shards, bi.Stats().NumFlushed)
		})
	}
}
