// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !race && !integration

// The race detector instruments allocation, so testing.AllocsPerRun reports counts
// that describe the instrumentation. Run this file without -race and without
// integration tests, whose background pollers allocate concurrently.

package opensearchtransport

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestC3ScoreSelectZeroAlloc guards that ranking search candidates with the C3
// queue term allocates nothing.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestC3ScoreSelectZeroAlloc(t *testing.T) {
	ago := 11 * time.Second
	conns := []*Connection{
		c3TestConn(t, "a", 2, 20, ago, c3TestServiceNs),
		c3TestConn(t, "b", 2, 0, ago, c3TestServiceNs),
		c3TestConn(t, "c", 2, 0, ago, c3TestServiceNs),
	}
	// Without this the closure below measures the linear form.
	require.NotEqual(t, c3Inputs{}, newC3Inputs(conns, poolSearch))

	rng := rand.New(rand.NewPCG(1, 2))
	scores := acquireFloats(len(conns))
	defer scores.Release()

	allocs := testing.AllocsPerRun(100, func() {
		_ = connScoreSelect(conns, nil, nil, &shardCostForReads, poolSearch, true, scores.Slice(), nil, nil, nil, 0.25, rng.Float64)
	})
	require.InDelta(t, 0.0, allocs, 0)
}
