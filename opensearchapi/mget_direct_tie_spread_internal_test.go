// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/require"
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
