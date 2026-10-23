// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestGetRoutingMetaReadsTheReplicationType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		want     ReplicationType
	}{
		{
			name:     "segment replication",
			settings: `{"index":{"number_of_shards":"5","replication":{"type":"SEGMENT"}}}`,
			want:     ReplicationSegment,
		},
		{
			name:     "segment replication in lower case",
			settings: `{"index":{"number_of_shards":"5","replication":{"type":"segment"}}}`,
			want:     ReplicationSegment,
		},
		{
			name:     "document replication",
			settings: `{"index":{"number_of_shards":"5","replication":{"type":"DOCUMENT"}}}`,
			want:     ReplicationDocument,
		},
		{
			name:     "no replication setting reads as document, as the server does",
			settings: `{"index":{"number_of_shards":"5"}}`,
			want:     ReplicationDocument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var query string
			tr, err := New(Config{
				URLs:              []*url.URL{{Scheme: "http", Host: "node-a:9200"}},
				NodeStatsInterval: -1,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if !strings.HasPrefix(req.URL.Path, "/_cluster/state/metadata/") {
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
					}
					query = req.URL.RawQuery
					body := `{"metadata":{"indices":{"idx":{"routing_num_shards":640,"settings":` + tt.settings + `}}}}`

					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tr.Close() })

			meta, err := tr.getRoutingMeta(t.Context(), []string{"idx"})
			require.NoError(t, err)
			require.Contains(t, query, "settings.index.replication.type", "the filter must ask for the setting")
			require.Equal(t, indexRoutingMeta{RoutingNumShards: 640, NumberOfShards: 5, Replication: tt.want}, meta["idx"])
		})
	}
}

func TestReplicationTypeReachesEveryShardCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		replication ReplicationType
	}{
		{name: "segment replication", replication: ReplicationSegment},
		{name: "document replication", replication: ReplicationDocument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := routableConn(t, "p", time.Millisecond)
			r1 := routableConn(t, "r1", time.Millisecond)
			r2 := routableConn(t, "r2", time.Millisecond)

			m := ShardMap{RoutingNumShards: 640, PrimaryShards: 5}
			placement := &indexShardPlacement{
				NumberOfPrimaryShards: m.PrimaryShards,
				RoutingNumShards:      m.RoutingNumShards,
				Replication:           tt.replication,
				ShardToNodes:          map[int]*shardNodes{m.Shard("doc-1"): {Primary: "p", Replicas: []string{"r1", "r2"}}},
			}

			tr, r := shardMapTransport(p, r1, r2)
			r.cache.getOrCreate("idx").mergeShardMap(placement)

			var targets []*ShardTarget
			require.NoError(t, tr.LookupShard("idx", "doc-1", &targets))
			require.Len(t, targets, 3)
			for _, target := range targets {
				require.Equal(t, tt.replication, target.Replication, "copy on %q", target.Node.Name)
			}
		})
	}
}

func TestReplicationTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   ReplicationType
		want string
	}{
		{in: ReplicationUnknown, want: "UNKNOWN"},
		{in: ReplicationDocument, want: "DOCUMENT"},
		{in: ReplicationSegment, want: "SEGMENT"},
		{in: ReplicationType(9), want: "ReplicationType(9)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.in.String())
		})
	}
}

func TestConnectionGetPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(conn *Connection)
		want  PoolWindow
	}{
		{
			name:  "before the node reports its pools, the initial window",
			setup: func(*Connection) {},
			want:  PoolWindow{Window: defaultSyntheticCwndMultiplier * defaultServerCoreCount},
		},
		{
			name: "the get pool, not the write pool",
			setup: func(conn *Connection) {
				get := conn.pools.getOrCreate(poolGet)
				get.cwnd.Store(3)
				get.mu.Lock()
				get.mu.maxCwnd = 12
				get.mu.Unlock()
				conn.addInFlight(poolGet)
				get.overloaded.Store(true)

				write := conn.pools.getOrCreate(poolWrite)
				write.cwnd.Store(9)
				conn.addInFlight(poolWrite)
				conn.addInFlight(poolWrite)
			},
			want: PoolWindow{Window: 3, MaxWindow: 12, InFlight: 1, Overloaded: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := routableConn(t, "n1", time.Millisecond)
			tt.setup(conn)

			require.Equal(t, tt.want, conn.GetPool())
		})
	}
}

// TestPinnedRequestIsAccountedToItsPool sends one request pinned to node-b and
// records, while the request is in flight, how many requests node-b has
// outstanding to each pool.
func TestPinnedRequestIsAccountedToItsPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		pin       func(context.Context, *Connection) context.Context
		path      string
		wantGet   int
		wantWrite int
	}{
		{name: "a write pin counts against the write pool", pin: WithWriteNode, path: "/_bulk", wantWrite: 1},
		{name: "a read pin counts against the get pool", pin: WithReadNode, path: "/_mget", wantGet: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu        sync.Mutex
				pinned    *Connection
				gotHost   string
				gotGet    int
				gotWrite  int
				transport *Transport
			)
			tr, err := New(Config{
				URLs: []*url.URL{
					{Scheme: "http", Host: "node-a:9200"},
					{Scheme: "http", Host: "node-b:9200"},
				},
				NodeStatsInterval: -1,
				Router: fixedRouter{pick: func() *Connection {
					for _, conn := range transport.readyConnections() {
						if conn.URL.Host == "node-a:9200" {
							return conn
						}
					}

					return nil
				}},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == tt.path {
						mu.Lock()
						gotHost = req.URL.Host
						gotGet = int(pinned.loadInFlight(poolGet))
						gotWrite = int(pinned.loadInFlight(poolWrite))
						mu.Unlock()
					}

					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)}, nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tr.Close() })
			transport = tr

			for _, conn := range tr.readyConnections() {
				if conn.URL.Host == "node-b:9200" {
					pinned = conn
				}
			}
			require.NotNil(t, pinned)
			pinned.pools.getOrCreate(poolGet)
			pinned.pools.getOrCreate(poolWrite)

			req, err := http.NewRequestWithContext(tt.pin(t.Context(), pinned), http.MethodPost, tt.path, nil)
			require.NoError(t, err)
			res, err := tr.Stream(req)
			require.NoError(t, err)
			_ = res.Body.Close()

			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, "node-b:9200", gotHost)
			require.Equal(t, tt.wantGet, gotGet, "get pool in flight")
			require.Equal(t, tt.wantWrite, gotWrite, "write pool in flight")
		})
	}
}
