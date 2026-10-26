// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration && !race

// The race detector instruments allocation, so testing.AllocsPerRun reports counts
// that describe the instrumentation rather than the code. Every CI test job runs with
// -race; `make test-alloc` runs this file without it.

package opensearchutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestSchedulerPublishLoadAllocations: publishLoad runs once for every completed
// request with the scheduler's lock held, and allocates nothing once it has seen
// every node.
func TestSchedulerPublishLoadAllocations(t *testing.T) {
	cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, "n0", "n1", "n2")
	server := newFakeServer(cluster)

	// AllocsPerRun counts mallocs process-wide, so the client must start no
	// background discovery, stats polling or seed health check that could
	// allocate mid-measurement. Only turning the router off stops the health check.
	t.Setenv("OPENSEARCH_GO_ROUTER", "false")
	client := newBulkTestClient(t, server.roundTrip, func(c *opensearchapi.Config) {
		c.Client.DiscoverNodesOnStart = new(false)
		c.Client.NodeStatsInterval = -1
	})
	bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client, ShardRouter: cluster, FlushInterval: time.Hour})
	require.NoError(t, err)
	s := bi.(*bulkClient).sched

	s.Lock()
	for _, name := range []string{"n0", "n1", "n2"} {
		s.nodeFor(Node{Name: name})
	}
	s.Unlock()

	publish := func() {
		s.Lock()
		s.publishLoad(flushResult{})
		s.Unlock()
	}

	// AllocsPerRun warms up publish once before it measures.
	require.Zero(t, testing.AllocsPerRun(100, publish), "publishLoad allocates on the completion path")

	require.EqualValues(t, 3, bi.Stats().NumTargets)
	require.NoError(t, bi.Close(t.Context()))
}
