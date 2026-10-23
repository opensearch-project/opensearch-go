// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchutil)

package opensearchutil_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

type mgetDoneCounter struct {
	opensearchapi.BaseMGetObserver

	mu struct {
		sync.Mutex
		done int
	}
}

func (c *mgetDoneCounter) OnMGetDone(context.Context, opensearchapi.MGetDoneEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.done++
}

func (c *mgetDoneCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.mu.done
}

func TestMGetUsesTheDirectDriver_Integration(t *testing.T) {
	counter := &mgetDoneCounter{}
	cfg := testutil.ClientConfig(t)
	cfg.MGet.Driver = opensearchapi.MGetDriverCoordinator
	cfg.MGet.Observer = counter
	client, err := opensearchapi.NewClient(*cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	index := testutil.MustUniqueString(t, "test-opensearchutil-mget")
	createShardTestIndex(t, ctx, client, index, `{"index":{"number_of_shards":3,"number_of_replicas":0}}`)

	const indexed = 6
	ops := make([]opensearchapi.MGetOperation, 0, indexed)
	for i := range indexed {
		id := fmt.Sprintf("doc-%d", i)
		_, err := client.Doc.Index(ctx, opensearchapi.IndexReq{Index: index, ID: id, Body: seqBody(i)})
		require.NoError(t, err)
		ops = append(ops, opensearchapi.MGetOperation{ID: id, Index: &index})
		awaitShardTarget(t, ctx, client, index, id)
	}
	req := opensearchapi.MGetReq{Body: &opensearchapi.MGetBody{Docs: ops}}

	// The call selects the coordinator itself, so OPENSEARCH_GO_MGET, which
	// overrides the client's configuration, cannot send it direct.
	_, err = client.Doc.MGet(opensearchapi.WithMGetDriver(ctx, opensearchapi.MGetDriverCoordinator), req)
	require.NoError(t, err)
	require.Zero(t, counter.count(), "a coordinator call never reaches the direct driver")

	resp, err := opensearchutil.MGet(ctx, client, req)
	require.NoError(t, err)
	require.Equal(t, 1, counter.count(), "opensearchutil.MGet answers through the direct driver")
	require.Len(t, resp.Docs, indexed)
	for i, item := range resp.Docs {
		doc, err := item.GetResult()
		require.NoError(t, err)
		require.Equal(t, ops[i].ID, doc.ID)
		require.True(t, doc.Found, "document %q is found", doc.ID)
	}
}
