// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	transporttestutil "github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
)

type mgetShardLookup interface {
	LookupShard(index, key string, dst *[]*opensearchtransport.ShardTarget) error
}

type mgetDoneRecorder struct {
	opensearchapi.BaseMGetObserver

	mu   sync.Mutex
	done []opensearchapi.MGetDoneEvent
}

func (r *mgetDoneRecorder) OnMGetDone(_ context.Context, e opensearchapi.MGetDoneEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done = append(r.done, e)
}

func (r *mgetDoneRecorder) last(t *testing.T) opensearchapi.MGetDoneEvent {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.done, "the direct engine answered no mget")

	return r.done[len(r.done)-1]
}

// awaitPlacement runs discovery until the transport knows the node of every
// shard of index, and returns the number of distinct nodes the shards are on.
func awaitPlacement(t *testing.T, ctx context.Context, client *opensearchapi.Client, index string, keys []string) int {
	t.Helper()

	lookup, ok := client.Client.Transport.(mgetShardLookup)
	require.True(t, ok, "%T keeps no shard map", client.Client.Transport)

	var nodes map[string]struct{}
	err := transporttestutil.PollUntil(t, ctx, 100*time.Millisecond, 100, 0.1, func() (bool, error) {
		nodes = make(map[string]struct{})
		var targets []*opensearchtransport.ShardTarget
		for _, key := range keys {
			if err := lookup.LookupShard(index, key, &targets); err != nil || targets[0].Node == nil {
				return false, client.Client.DiscoverNodes(ctx)
			}
			for _, target := range targets {
				if target.Node != nil {
					nodes[target.Node.Name] = struct{}{}
				}
			}
		}

		return true, nil
	})
	require.NoError(t, err, "the transport never learned where the shards of %s are", index)

	return len(nodes)
}

func TestMGetDirectAnswersAsTheCoordinatorDoes(t *testing.T) {
	recorder := &mgetDoneRecorder{}
	cfg := testutil.ClientConfig(t)
	cfg.MGet.Observer = recorder
	client, err := opensearchapi.NewClient(*cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	health, err := client.Cluster.Health(t.Context(), nil)
	require.NoError(t, err)
	replicas := min(1, health.NumberOfDataNodes-1)

	tests := []struct {
		name       string
		settings   string
		minVersion string
	}{
		{
			name:     "document replication",
			settings: `{"index":{"number_of_shards":6,"number_of_replicas":%d,"routing.rebalance.enable":"none"}}`,
		},
		{
			name: "segment replication",
			settings: `{"index":{"number_of_shards":6,"number_of_replicas":%d,"routing.rebalance.enable":"none",` +
				`"replication.type":"SEGMENT"}}`,
			minVersion: "2.7.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.minVersion != "" {
				testutil.SkipIfVersion(t, client, "<", tt.minVersion, "MGet direct with "+tt.name)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()

			index := testutil.MustUniqueString(t, "test-mget-direct")
			_, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
				Index:      index,
				BodyReader: strings.NewReader(fmt.Sprintf(`{"settings":`+tt.settings+`}`, replicas)),
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = client.Indices.Delete(context.WithoutCancel(ctx), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
			})
			_, err = client.Cluster.Health(ctx, &opensearchapi.ClusterHealthReq{
				Indices: []string{index},
				Params:  &opensearchapi.ClusterHealthParams{WaitForStatus: "green"},
			})
			require.NoError(t, err)

			// Not refreshed: a realtime mget must still find every document.
			const indexed = 30
			ops := make([]opensearchapi.MGetOperation, 0, indexed+3)
			keys := make([]string, 0, indexed)
			for i := range indexed {
				id := fmt.Sprintf("doc-%02d", i)
				req := opensearchapi.IndexReq{Index: index, ID: id, Body: strings.NewReader(fmt.Sprintf(`{"n":%d}`, i))}
				op := opensearchapi.MGetOperation{ID: id, Index: &index}
				key := id
				if i%5 == 0 {
					routing := fmt.Sprintf("r-%d", i)
					req.Params = &opensearchapi.IndexParams{Routing: []string{routing}}
					op.Routing = &routing
					key = routing
				}
				_, err := client.Doc.Index(ctx, req)
				require.NoError(t, err)
				ops = append(ops, op)
				keys = append(keys, key)
			}
			missing := "no-such-doc"
			unknown := "no-such-index-for-mget"
			ops = append(ops,
				opensearchapi.MGetOperation{ID: missing, Index: &index},
				opensearchapi.MGetOperation{ID: "doc-01", Index: &unknown},
				ops[3],
			)

			spread := awaitPlacement(t, ctx, client, index, keys)

			req := opensearchapi.MGetReq{Body: &opensearchapi.MGetBody{Docs: ops}}
			want, err := client.Doc.MGet(opensearchapi.WithMGetEngine(ctx, opensearchapi.MGetEngineCoordinator), req)
			require.NoError(t, err)
			got, err := client.Doc.MGet(opensearchapi.WithMGetEngine(ctx, opensearchapi.MGetEngineDirect), req)
			require.NoError(t, err)

			require.Equal(t, want.Inspect().Response.StatusCode, got.Inspect().Response.StatusCode)
			var wantRaw, gotRaw struct {
				Docs []json.RawMessage `json:"docs"`
			}
			require.NoError(t, json.Unmarshal(want.Inspect().Response.RawBody(), &wantRaw))
			require.NoError(t, json.Unmarshal(got.Inspect().Response.RawBody(), &gotRaw))
			require.Len(t, gotRaw.Docs, len(wantRaw.Docs))
			for i := range wantRaw.Docs {
				require.JSONEq(t, string(wantRaw.Docs[i]), string(gotRaw.Docs[i]), "document %d (%s)", i, ops[i].ID)
			}

			for i := range indexed {
				doc, err := got.Docs[i].GetResult()
				require.NoError(t, err)
				require.True(t, doc.Found, "unrefreshed document %s is found by a realtime read", doc.ID)
			}

			done := recorder.last(t)
			t.Logf("direct mget over %d shard-holding nodes: %+v", spread, done)
			require.Equal(t, len(ops), done.Docs)
			require.Equal(t, 1, done.CoordinatorDocs, "only the document in an unknown index goes through a coordinator")
			require.Zero(t, done.FailedDocs)
			require.False(t, done.Resent)
			if spread > 1 {
				require.Greater(t, done.Nodes, 1, "the documents fan out across the nodes")
			}
		})
	}
}
