// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

func TestBulkIndexerStatsTimerFlushRatio(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stats BulkIndexerStats
		want  float64
	}{
		{name: "no flushes", stats: BulkIndexerStats{}, want: 0},
		{name: "only size flushes", stats: BulkIndexerStats{NumByteFlushes: 7}, want: 0},
		{name: "only timer flushes", stats: BulkIndexerStats{NumTimerFlushes: 3}, want: 1},
		{name: "mixed", stats: BulkIndexerStats{NumByteFlushes: 1, NumTimerFlushes: 3}, want: 0.75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.InDelta(t, tt.want, tt.stats.TimerFlushRatio(), 1e-9)
		})
	}
}

// TestBulkIndexerCountsGroupedAndUngroupedItems: an item the indexer cannot place
// in a shard takes the unplaced path and is counted, and every item is still sent.
func TestBulkIndexerCountsGroupedAndUngroupedItems(t *testing.T) {
	t.Parallel()

	known := StaticShardRouter{"idx": DefaultShardMap(8)}

	const numItems = 40

	tests := []struct {
		name          string
		router        ShardRouter
		idOf          func(i int) string
		wantGrouped   uint64
		wantUngrouped uint64
	}{
		{
			name:          "no shard router",
			router:        NoShardRouter,
			idOf:          func(i int) string { return "doc-" + strconv.Itoa(i) },
			wantUngrouped: numItems,
		},
		{
			name:          "index unknown to the router",
			router:        StaticShardRouter{"other": DefaultShardMap(8)},
			idOf:          func(i int) string { return "doc-" + strconv.Itoa(i) },
			wantUngrouped: numItems,
		},
		{
			name:        "index known",
			router:      known,
			idOf:        func(i int) string { return "doc-" + strconv.Itoa(i) },
			wantGrouped: numItems,
		},
		{
			name:   "items with neither routing nor ID are ungrouped",
			router: known,
			idOf: func(i int) string {
				if i%4 == 0 {
					return ""
				}
				return "doc-" + strconv.Itoa(i)
			},
			wantGrouped:   numItems - numItems/4,
			wantUngrouped: numItems / 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer(newFakeCluster(nil))
			bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 2, ShardRouter: tt.router})

			for i := range numItems {
				require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
					Index: "idx", Action: actionIndex, DocumentID: tt.idOf(i), Body: seqBody(i),
				}))
			}
			require.NoError(t, bi.Close(t.Context()))

			stats := bi.Stats()
			require.Equal(t, tt.wantGrouped, stats.NumGroupedItems)
			require.Equal(t, tt.wantUngrouped, stats.NumUngroupedItems)
			require.EqualValues(t, numItems, stats.NumAdded)

			sent := 0
			for _, r := range server.requests() {
				sent += len(r.actions)
			}
			require.Equal(t, numItems, sent, "grouped or not, every item is sent")
		})
	}
}

// warnings collects what OnWarning receives.
type warnings struct {
	mu   sync.Mutex
	errs []error
}

func (w *warnings) add(_ context.Context, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.errs = append(w.errs, err)
}

func (w *warnings) all() []error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]error(nil), w.errs...)
}

// plainTransport is a transport that offers no shard router.
type plainTransport struct{}

var errPlainTransport = fmt.Errorf("plain transport answers nothing")

func (plainTransport) Stream(*http.Request) (*http.Response, error) { return nil, errPlainTransport }

func (plainTransport) Request(*http.Request) (*http.Response, error) { return nil, errPlainTransport }

// TestBulkIndexerWarnsOnceWhenShardRoutingIsUnavailable: a transport that can
// never say where shards live costs shard-aware buffering, which is said once at
// start-up and never again.
//
// The cases set environment variables, which the client reads when it is built,
// so they cannot run in parallel.
func TestBulkIndexerWarnsOnceWhenShardRoutingIsUnavailable(t *testing.T) { //nolint:paralleltest // Sets environment variables.
	const numItems = 10

	tests := []struct {
		name string
		env  map[string]string
		// client overrides the mock-backed client, for a transport that is not
		// opensearchtransport.Transport.
		client      func(t *testing.T) *opensearchapi.Client
		wantWarning string
	}{
		{name: "defaults: shard routing is available"},
		{
			name:        "routing_num_shards fetch switched off",
			env:         map[string]string{"OPENSEARCH_GO_DISCOVERY_CONFIG": "-routing_num_shards"},
			wantWarning: "-routing_num_shards",
		},
		{
			name:        "cat_shards fetch switched off",
			env:         map[string]string{"OPENSEARCH_GO_DISCOVERY_CONFIG": "-cat_shards"},
			wantWarning: "-cat_shards",
		},
		{
			name: "unrelated discovery calls switched off",
			env:  map[string]string{"OPENSEARCH_GO_DISCOVERY_CONFIG": "-node_stats"},
		},
		{
			name:        "router switched off",
			env:         map[string]string{"OPENSEARCH_GO_ROUTER": "false"},
			wantWarning: "no router",
		},
		{
			name: "transport that reports nothing",
			client: func(*testing.T) *opensearchapi.Client {
				return &opensearchapi.Client{Client: &opensearch.Client{Transport: plainTransport{}}}
			},
			wantWarning: "does not report where shards live",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			server := newFakeServer(newFakeCluster(nil))
			var client *opensearchapi.Client
			if tt.client != nil {
				client = tt.client(t)
			} else {
				client = newBulkTestClient(t, server.roundTrip)
			}

			var got warnings
			bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client, NumWorkers: 1, OnWarning: got.add})
			require.NoError(t, err)

			// The warning comes with the constructor, before any item.
			if tt.wantWarning == "" {
				require.Empty(t, got.all())
			} else {
				require.Len(t, got.all(), 1)
				require.ErrorIs(t, got.all()[0], ErrShardRoutingUnavailable)
				require.ErrorContains(t, got.all()[0], tt.wantWarning)
			}

			// Adding items warns no further, and an indexer whose transport can never
			// answer does not ask it about every item.
			if tt.client == nil {
				for i := range numItems {
					require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
						Index: "idx", Action: actionIndex, DocumentID: fmt.Sprintf("doc-%d", i), Body: seqBody(i),
					}))
				}
				require.NoError(t, bi.Close(t.Context()))

				wantWarnings := 0
				if tt.wantWarning != "" {
					wantWarnings = 1
					require.Equal(t, NoShardRouter, bi.(*bulkIndexer).router, "a router that can never answer is not kept")
					require.EqualValues(t, numItems, bi.Stats().NumUngroupedItems)
				}
				require.Len(t, got.all(), wantWarnings)
			}
		})
	}
}

// TestBulkIndexerUserRouterIsNotDiagnosed pins that the start-up warning is about
// the client's own router. One the caller supplied is theirs: it may be a
// StaticShardRouter on a client that has no router, and that works.
func TestBulkIndexerUserRouterIsNotDiagnosed(t *testing.T) { //nolint:paralleltest // Sets an environment variable.
	t.Setenv("OPENSEARCH_GO_ROUTER", "false")

	server := newFakeServer(newFakeCluster(nil))
	var got warnings
	router := StaticShardRouter{"idx": DefaultShardMap(8)}
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 1, ShardRouter: router, OnWarning: got.add})

	require.Empty(t, got.all())
	require.NotEqual(t, NoShardRouter, bi.(*bulkIndexer).router)
	require.NoError(t, bi.Close(t.Context()))
}

// TestBulkIndexerCountsFlushesByTrigger: a batch is counted by what sent it, and
// a batch a caller asked for is counted as neither.
func TestBulkIndexerCountsFlushesByTrigger(t *testing.T) {
	t.Parallel()

	t.Run("size", func(t *testing.T) {
		t.Parallel()

		server := newFakeServer(newFakeCluster(nil))
		bi := newClusterIndexer(t, server, BulkIndexerConfig{
			NumWorkers: 1, FlushBytes: 1, Sizing: BulkSizing{Shard: ShardFixed, Node: NodeFixed},
			FlushInterval: time.Hour, ShardRouter: NoShardRouter,
		})

		const items = 5
		for i := range items {
			addDoc(t, bi, "idx", fmt.Sprintf("d%d", i), i)
		}
		require.NoError(t, bi.Close(t.Context()))

		stats := bi.Stats()
		require.EqualValues(t, items, stats.NumByteFlushes, "each item fills a batch of one byte")
		require.Zero(t, stats.NumTimerFlushes)
		require.InDelta(t, 0.0, stats.TimerFlushRatio(), 1e-9)
	})

	t.Run("interval", func(t *testing.T) {
		t.Parallel()

		server := newFakeServer(newFakeCluster(nil))
		bi := newClusterIndexer(t, server, BulkIndexerConfig{
			NumWorkers: 1, FlushInterval: 5 * time.Millisecond, ShardRouter: NoShardRouter,
		})

		addDoc(t, bi, "idx", "d", 1)

		require.Eventually(t, func() bool { return bi.Stats().NumTimerFlushes >= 1 },
			10*time.Second, time.Millisecond, "the interval must send the buffered item")
		require.NoError(t, bi.Close(t.Context()))

		stats := bi.Stats()
		require.Zero(t, stats.NumByteFlushes)
		require.InDelta(t, 1.0, stats.TimerFlushRatio(), 1e-9)
	})

	t.Run("flush and close are neither", func(t *testing.T) {
		t.Parallel()

		server := newFakeServer(newFakeCluster(nil))
		bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 1, FlushInterval: time.Hour, ShardRouter: NoShardRouter})

		addDoc(t, bi, "idx", "a", 1)
		require.NoError(t, bi.Flush(t.Context()))
		addDoc(t, bi, "idx", "b", 2)
		require.NoError(t, bi.Close(t.Context()))

		stats := bi.Stats()
		require.EqualValues(t, 2, stats.NumRequests)
		require.Zero(t, stats.NumByteFlushes)
		require.Zero(t, stats.NumTimerFlushes)
	})
}

func TestBulkIndexerRejectsUseAfterClose(t *testing.T) {
	t.Parallel()

	server := newFakeServer(newFakeCluster(nil))
	bi := newClusterIndexer(t, server, BulkIndexerConfig{NumWorkers: 1, ShardRouter: NoShardRouter})
	require.NoError(t, bi.Close(t.Context()))

	require.ErrorIs(t, bi.Add(t.Context(), BulkIndexerItem{Action: actionIndex}), ErrBulkIndexerClosed)
	require.ErrorIs(t, bi.Flush(t.Context()), ErrBulkIndexerClosed)
}

// fakeTransportRouter is what a transport offers, for the adapter.
type fakeTransportRouter struct {
	target      opensearchtransport.ShardTarget
	found       bool
	nodes       []*opensearchtransport.Connection
	unavailable error
}

func (f fakeTransportRouter) LookupShard(string, string) (opensearchtransport.ShardTarget, bool) {
	return f.target, f.found
}

func (f fakeTransportRouter) WriteNodes() []*opensearchtransport.Connection { return f.nodes }
func (f fakeTransportRouter) ShardMapUnavailable() error                    { return f.unavailable }

func TestClientShardRouterAdapter(t *testing.T) {
	t.Parallel()

	n1 := &opensearchtransport.Connection{Name: "n1"}
	n2 := &opensearchtransport.Connection{Name: "n2"}

	t.Run("a lookup carries the shard and the node", func(t *testing.T) {
		t.Parallel()

		r := clientShardRouter{tr: fakeTransportRouter{
			target: opensearchtransport.ShardTarget{Shard: 3, Node: n1},
			found:  true,
		}}

		got, ok := r.LookupShard("idx", "doc")
		require.True(t, ok)
		require.Equal(t, 3, got.Shard)
		require.Equal(t, "n1", got.Node.Name)
		require.Same(t, n1, got.Node.Conn)
	})

	t.Run("a lookup carries the geometry generation", func(t *testing.T) {
		t.Parallel()

		r := clientShardRouter{tr: fakeTransportRouter{
			target: opensearchtransport.ShardTarget{Shard: 3, Generation: 7},
			found:  true,
		}}

		got, ok := r.LookupShard("idx", "doc")
		require.True(t, ok)
		require.EqualValues(t, 7, got.Generation)
	})

	t.Run("a shard whose node is unknown has no node", func(t *testing.T) {
		t.Parallel()

		r := clientShardRouter{tr: fakeTransportRouter{target: opensearchtransport.ShardTarget{Shard: 3}, found: true}}

		got, ok := r.LookupShard("idx", "doc")
		require.True(t, ok)
		require.Equal(t, 3, got.Shard)
		require.Empty(t, got.Node.Name)
		require.Nil(t, got.Node.Conn)
	})

	t.Run("an unknown index is not found", func(t *testing.T) {
		t.Parallel()

		_, ok := clientShardRouter{tr: fakeTransportRouter{}}.LookupShard("idx", "doc")
		require.False(t, ok)
	})

	t.Run("write nodes keep the transport's order", func(t *testing.T) {
		t.Parallel()

		nodes := clientShardRouter{tr: fakeTransportRouter{nodes: []*opensearchtransport.Connection{n2, n1}}}.WriteNodes()
		require.Len(t, nodes, 2)
		require.Equal(t, "n2", nodes[0].Name)
		require.Equal(t, "n1", nodes[1].Name)
	})

	t.Run("the transport's diagnosis is passed on", func(t *testing.T) {
		t.Parallel()

		cause := fmt.Errorf("because")
		require.Equal(t, cause, clientShardRouter{tr: fakeTransportRouter{unavailable: cause}}.unavailable())
		require.NoError(t, clientShardRouter{tr: fakeTransportRouter{}}.unavailable())
	})

	t.Run("a transport that cannot diagnose is not unavailable", func(t *testing.T) {
		t.Parallel()

		// Only a lookup and a node list: nothing to ask why it would never answer,
		// though the transport underneath has a reason.
		type plainRouter struct{ transportRouter }

		require.NoError(t, clientShardRouter{tr: plainRouter{fakeTransportRouter{unavailable: fmt.Errorf("hidden")}}}.unavailable())
	})
}

func TestClientShardRouter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		client *opensearchapi.Client
		want   bool
	}{
		{name: "nil client"},
		{name: "client without a core client", client: &opensearchapi.Client{}},
		{
			name:   "transport that reports nothing",
			client: &opensearchapi.Client{Client: &opensearch.Client{Transport: plainTransport{}}},
		},
		{
			name:   "the built-in transport",
			client: newBulkTestClient(t, defaultRoundTripFunc),
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := ClientShardRouter(tt.client)
			if !tt.want {
				require.Nil(t, router)
				return
			}
			require.NotNil(t, router)
		})
	}
}
