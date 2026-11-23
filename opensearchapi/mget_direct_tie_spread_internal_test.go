// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// TestMGetDirectSpreadsAcrossTiedCopies sends mgets one at a time, each of
// documents in one shard of the fake, whose two copies are idle. Both copies
// must serve some of them rather than the copy whose node name sorts first
// serving all of them. The fewest-nodes fan-out, the default, sends a whole
// mget to one node, so its tie decides every document. The least-loaded
// fan-out counts each document it routes, so its tie decides only each mget's
// first document.
func TestMGetDirectSpreadsAcrossTiedCopies(t *testing.T) {
	t.Parallel()

	const mgets = 400

	tests := []struct {
		name   string
		fanOut MGetFanOut
	}{
		{name: "fewest nodes", fanOut: MGetFanOutFewestNodes},
		{name: "least loaded", fanOut: MGetFanOutLeastLoaded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{FanOut: tt.fanOut})

			// Shard 0 of idx has copies on n0 and n1.
			ops := mgetDocs("idx", "d0", "d3", "d6", "d9")
			for range mgets {
				resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
				require.NoError(t, err)
				requireAnswers(t, ops, resp)
			}

			docs := make(map[string]int)
			for _, s := range f.sent() {
				docs[s.node] += len(s.ids)
			}
			total := mgets * len(ops)
			for _, node := range []string{"n0", "n1"} {
				require.Greater(t, docs[node], total/4, "copy %s starved: %v", node, docs)
			}
			require.Zero(t, docs["n2"], "n2 holds no copy of shard 0: %v", docs)
		})
	}
}

// TestFindShardNodeTies checks how findShardNode breaks ties between equally
// idle copies of a shard: a replica beats the primary, and equal replicas are
// each chosen with about the same chance rather than the first listed every
// time.
func TestFindShardNodeTies(t *testing.T) {
	t.Parallel()

	const picks = 600

	tests := []struct {
		name string
		// primary lists, for each copy n0, n1, ..., whether it is the primary.
		primary []bool
		// want lists the nodes that must be chosen; no other node may be.
		want []string
	}{
		{name: "replica beats an idle primary", primary: []bool{true, false}, want: []string{"n1"}},
		{name: "replica listed first beats the primary", primary: []bool{false, true}, want: []string{"n0"}},
		{name: "two replicas share the picks", primary: []bool{true, false, false}, want: []string{"n1", "n2"}},
		{name: "three replicas share the picks", primary: []bool{false, false, false}, want: []string{"n0", "n1", "n2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			targets := make([]*opensearchtransport.ShardTarget, len(tt.primary))
			for i, p := range tt.primary {
				targets[i] = &opensearchtransport.ShardTarget{
					Node:        &opensearchtransport.Connection{Name: fmt.Sprintf("n%d", i)},
					Primary:     p,
					Replication: opensearchtransport.ReplicationDocument,
				}
			}
			call := &mgetCall{
				d:        &mgetDriver{windows: scheduler.NewNodeWindows(scheduler.WindowConfig{Start: 1, Ceiling: 1, Floor: 1})},
				realtime: true,
			}

			chosen := make(map[string]int)
			for range picks {
				sn, err := call.findShardNode(targets, "", nil)
				require.NoError(t, err)
				require.True(t, sn.local)
				chosen[sn.node.Name]++
			}

			require.Len(t, chosen, len(tt.want), "chosen: %v", chosen)
			for _, node := range tt.want {
				require.Greater(t, chosen[node], picks/len(tt.want)/2, "copy %q starved: %v", node, chosen)
			}
		})
	}
}
