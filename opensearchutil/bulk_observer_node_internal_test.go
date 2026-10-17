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
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nodeObserver records the node every event names.
type nodeObserver struct {
	BaseBulkIndexerObserver

	mu       sync.Mutex
	requests []string
	failures []string
	settled  []string
}

func (o *nodeObserver) OnRequestEnd(_ context.Context, e BulkRequestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.requests = append(o.requests, e.Node)
}

func (o *nodeObserver) OnItemFailure(_ context.Context, e BulkFailureEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.failures = append(o.failures, e.Node)
}

func (o *nodeObserver) OnItemSettled(_ context.Context, e BulkItemEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.settled = append(o.settled, e.Node)
}

// TestBulkIndexerObserverEventsNameTheNode: a request event names the node the
// request was sent to, and the failure and item events of the items it carried
// name the node the work was bound for.
func TestBulkIndexerObserverEventsNameTheNode(t *testing.T) {
	t.Parallel()

	const docs = 40

	nodes := []string{"n1", "n2", "n3"}
	server := newFakeServer(newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(8)}, nodes...))

	// The first request is answered with a 429 for each of its items, so the
	// run has item failures and a retry as well as requests.
	var requests atomic.Int64
	server.respond = func(r sentRequest, body string) (int, string) {
		if requests.Add(1) == 1 {
			return reply(http.StatusTooManyRequests)(r, body)
		}

		return reply(http.StatusCreated)(r, body)
	}

	obs := &nodeObserver{}
	bi := newClusterIndexer(t, server, BulkIndexerConfig{
		FlushInterval: time.Hour,
		Observer:      obs,
		RetryPolicy:   retryOnce,
	})

	for i := range docs {
		addDoc(t, bi, "idx", fmt.Sprint("doc-", i), i)
	}
	require.NoError(t, bi.Close(t.Context()))

	var sentTo []string
	for _, r := range server.requests() {
		sentTo = append(sentTo, r.node)
	}
	slices.Sort(sentTo)

	obs.mu.Lock()
	defer obs.mu.Unlock()

	reported := slices.Clone(obs.requests)
	slices.Sort(reported)
	require.Equal(t, sentTo, reported, "each request event names the node its request went to")

	require.NotEmpty(t, obs.failures)
	require.Len(t, obs.settled, docs)
	for _, name := range slices.Concat(obs.failures, obs.settled) {
		require.Contains(t, nodes, name)
	}
}
