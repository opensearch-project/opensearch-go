// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil/shardhash"
)

// shardMissRecorder records the shard lookup misses it is told about.
type shardMissRecorder struct {
	BaseConnectionObserver

	events []ShardLookupMissEvent
}

func (r *shardMissRecorder) OnShardLookupMiss(e ShardLookupMissEvent) { r.events = append(r.events, e) }

// plainObserver is a ConnectionObserver that is not a ShardLookupObserver: only
// the embedded interface's methods are promoted.
type plainObserver struct{ ConnectionObserver }

func TestLookupDocShardReportsMisses(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	id := docWithOffset(t, routing)
	shard, _ := shardhash.ForDocument(routing, id, partitionedRoutingNumShards, partitionedPrimaryShards, partitionSize)

	partitioned := func(shards map[int]*shardNodes) func(*Transport, *IndexRouter) {
		return func(_ *Transport, r *IndexRouter) {
			r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
				NumberOfPrimaryShards: partitionedPrimaryShards,
				RoutingNumShards:      partitionedRoutingNumShards,
				RoutingPartitionSize:  partitionSize,
				Shards:                shards,
			})
		}
	}

	tests := []struct {
		name       string
		noRouter   bool
		setup      func(*Transport, *IndexRouter)
		routing    string
		id         string
		wantReason ShardMissReason
	}{
		{name: "transport collects no shard map", noRouter: true, routing: routing, id: id, wantReason: ShardMissMapUnknown},
		{name: "shard map not fetched yet", setup: func(*Transport, *IndexRouter) {}, routing: routing, id: id, wantReason: ShardMissMapUnknown},
		{
			name: "partitioned index without a document ID", setup: partitioned(nil),
			routing: routing, wantReason: ShardMissDocumentIDRequired,
		},
		{
			name: "primary node is not a ready connection", setup: partitioned(map[int]*shardNodes{shard: {Primary: "gone"}}),
			routing: routing, id: id, wantReason: ShardMissNoNode,
		},
		{
			name: "shard has no placement", setup: partitioned(map[int]*shardNodes{}),
			routing: routing, id: id, wantReason: ShardMissNoNode,
		},
		{name: "hit", setup: partitioned(map[int]*shardNodes{shard: {Primary: "n1"}}), routing: routing, id: id},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, r := shardMapTransport(routableConn(t, "n1", time.Millisecond))
			if tt.noRouter {
				tr.router = nil
			} else {
				tt.setup(tr, r)
			}
			rec := &shardMissRecorder{}
			var obs ConnectionObserver = rec
			tr.observer.Store(&obs)

			var dst []*ShardTarget
			_ = tr.LookupDocShard("idx", tt.routing, tt.id, &dst)

			if tt.wantReason == ShardMissNone {
				require.Empty(t, rec.events)

				return
			}
			require.Equal(t, []ShardLookupMissEvent{{Index: "idx", Reason: tt.wantReason}}, rec.events)
		})
	}
}

func TestLookupDocShardWithAnObserverThatIgnoresMisses(t *testing.T) {
	t.Parallel()

	tr, _ := shardMapTransport(routableConn(t, "n1", time.Millisecond))
	var obs ConnectionObserver = plainObserver{&BaseConnectionObserver{}}
	tr.observer.Store(&obs)

	var dst []*ShardTarget
	require.NotPanics(t, func() {
		require.ErrorIs(t, tr.LookupDocShard("idx", "k", "", &dst), ErrShardMapUnknown)
	})

	tr.observer.Store(nil)
	require.NotPanics(t, func() {
		require.ErrorIs(t, tr.LookupDocShard("idx", "k", "", &dst), ErrShardMapUnknown)
	})
}

func TestShardMissReasonString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason ShardMissReason
		want   string
	}{
		{ShardMissNone, "none"},
		{ShardMissMapUnknown, "map_unknown"},
		{ShardMissDocumentIDRequired, "document_id_required"},
		{ShardMissNoRoutingKey, "no_routing_key"},
		{ShardMissNoNode, "no_node"},
		{ShardMissReason(200), "unknown"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, tt.reason.String())
	}
}

func TestRouteEventReportsWhyShardExactRoutingMissed(t *testing.T) {
	t.Parallel()

	const routing = "user-7"
	partitionTargets := func() []int {
		shards := make([]int, 0, partitionSize)
		for i := range partitionSize {
			shards = append(shards, shardhash.ForRoutingOffset(routing, i, partitionedRoutingNumShards, partitionedPrimaryShards))
		}

		return shards
	}()
	hosted := func(node string, shards ...int) map[int]string {
		m := make(map[int]string, len(shards))
		for _, shard := range shards {
			m[shard] = node
		}

		return m
	}

	tests := []struct {
		name       string
		index      string
		query      string
		slot       *indexSlot
		wantReason ShardMissReason
		wantExact  bool
	}{
		{name: "routing value on a known shard", index: "my-index", query: "routing=" + routing, wantExact: true},
		{name: "no routing value", index: "my-index", query: "", wantReason: ShardMissNoRoutingKey},
		{name: "no shard map", index: "no-map", query: "routing=" + routing, wantReason: ShardMissMapUnknown},
		{
			name: "no connection hosts the shard", index: "ghost", query: "routing=" + routing,
			slot:       partitionedSlot(3, 0, hosted("nodeZ", 0, 1, 2)),
			wantReason: ShardMissNoNode,
		},
		{
			name: "no connection hosts a partitioned index's shards", index: "ghost-partitioned", query: "routing=" + routing,
			slot:       partitionedSlot(partitionedPrimaryShards, partitionSize, hosted("nodeZ", partitionTargets...)),
			wantReason: ShardMissNoNode,
		},
		{
			name: "connection hosts a partitioned index's shards", index: "partitioned", query: "routing=" + routing,
			slot:      partitionedSlot(partitionedPrimaryShards, partitionSize, hosted("nodeA", partitionTargets...)),
			wantExact: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newShardRoutingE2EFixture(t)
			if tt.slot != nil {
				f.policy.cache.getOrCreate(tt.index).shardMap.Store(tt.slot.shardMap.Load())
			}
			var captured RouteEvent
			var obs ConnectionObserver = &shardRoutingObserver{onRoute: func(e RouteEvent) { captured = e }}
			f.policy.observer.Store(&obs)

			req := &http.Request{URL: &url.URL{Path: "/" + tt.index + "/_search", RawQuery: tt.query}}
			hop, err := f.policy.Eval(t.Context(), req)
			require.NoError(t, err)
			require.NotNil(t, hop.Conn)

			require.Equal(t, tt.wantExact, captured.ShardExactMatch)
			require.Equal(t, tt.wantReason, captured.ShardMissReason)
		})
	}
}
