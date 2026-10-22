// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestConnScoreSelectRoutesARetryAroundTriedNodes: a retried request skips the
// candidates it tried in the current pass, starts a new pass once it has tried
// them all, and ranks each candidate lower for every time it tried it.
func TestConnScoreSelectRoutesARetryAroundTriedNodes(t *testing.T) {
	t.Parallel()

	type try struct {
		node   int
		tries  int
		inPass bool
	}

	tests := []struct {
		name       string
		tried      []try
		want       int
		wantInPass []bool
	}{
		{name: "a first attempt takes the best node", want: 0},
		{name: "a retry skips the node it tried", tried: []try{{0, 1, true}}, want: 1},
		{name: "and the next one", tried: []try{{0, 1, true}, {1, 1, true}}, want: 2},
		{
			name:       "once every candidate is tried a new pass begins",
			tried:      []try{{0, 1, true}, {1, 1, true}, {2, 1, true}},
			want:       0,
			wantInPass: []bool{false, false, false},
		},
		{
			name:  "a node tried more often ranks behind one tried less",
			tried: []try{{0, 6, false}},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conns := []*Connection{
				// RTT buckets 8, 22 and 24: close enough that one earlier try (twice the
				// score) leaves near the best, so only the skip moves a retry off it.
				scoreTestConn(t, "near", 100*time.Microsecond, 0),
				scoreTestConn(t, "mid", 5*time.Second, 0),
				scoreTestConn(t, "far", 30*time.Second, 0),
			}
			for _, c := range conns {
				c.setLifecycleBit(lcActive)
			}

			var att *routeAttempt
			if tt.tried != nil {
				att = &routeAttempt{}
				for _, tr := range tt.tried {
					att.tried = append(att.tried, triedConn{conn: conns[tr.node], tries: tr.tries, inPass: tr.inPass})
				}
			}

			scores := acquireFloats(len(conns))
			best := connScoreSelect(conns, nil, nil, &shardCostForReads, "", true, scores.Slice(), nil, nil, att)
			scores.Release()

			require.Same(t, conns[tt.want], best)
			if tt.wantInPass != nil {
				inPass := make([]bool, len(att.tried))
				for i, tc := range att.tried {
					inPass[i] = tc.inPass
				}
				require.Equal(t, tt.wantInPass, inPass)
			}
		})
	}
}

func TestRouteAttemptRecord(t *testing.T) {
	t.Parallel()

	a, b := scoreTestConn(t, "a", time.Millisecond, 0), scoreTestConn(t, "b", time.Millisecond, 0)
	att := &routeAttempt{}
	att.record(a)
	att.record(b)
	att.newPass()
	att.record(a)

	tries, inPass := att.lookup(a)
	require.Equal(t, 2, tries)
	require.True(t, inPass)
	tries, inPass = att.lookup(b)
	require.Equal(t, 1, tries)
	require.False(t, inPass)

	ctx := withRouteAttempt(context.Background(), att)
	require.Same(t, att, routeAttemptFrom(ctx))
	require.Same(t, ctx, withRouteAttempt(ctx, att), "the same attempt is not wrapped twice")
	require.Nil(t, routeAttemptFrom(context.Background()))
}
