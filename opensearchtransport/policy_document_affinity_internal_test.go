// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractDocumentFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantIndex string
		wantDocID string
	}{
		{
			name:      "doc endpoint",
			path:      "/my-index/_doc/123",
			wantIndex: "my-index",
			wantDocID: "123",
		},
		{
			name:      "source endpoint",
			path:      "/my-index/_source/abc",
			wantIndex: "my-index",
			wantDocID: "abc",
		},
		{
			name:      "update endpoint",
			path:      "/my-index/_update/456",
			wantIndex: "my-index",
			wantDocID: "456",
		},
		{
			name:      "explain endpoint",
			path:      "/my-index/_explain/789",
			wantIndex: "my-index",
			wantDocID: "789",
		},
		{
			name:      "termvectors endpoint",
			path:      "/my-index/_termvectors/xyz",
			wantIndex: "my-index",
			wantDocID: "xyz",
		},
		{
			name:      "search is not a document endpoint",
			path:      "/my-index/_search",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "bulk is not a document endpoint",
			path:      "/my-index/_bulk",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "system endpoint",
			path:      "/_cluster/health",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "root path",
			path:      "/",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "empty path",
			path:      "",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "index only no endpoint or id segments",
			path:      "/my-index",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "empty docID after endpoint",
			path:      "/my-index/_doc/",
			wantIndex: "",
			wantDocID: "",
		},
		{
			name:      "trailing slash stripped from docID",
			path:      "/my-index/_doc/123/",
			wantIndex: "my-index",
			wantDocID: "123",
		},
		{
			name:      "query string stripped from docID",
			path:      "/my-index/_doc/123?routing=abc",
			wantIndex: "my-index",
			wantDocID: "123",
		},
		{
			name:      "unknown endpoint returns empty",
			path:      "/my-index/_unknown/123",
			wantIndex: "",
			wantDocID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotIndex, gotDocID := extractDocumentFromPath(tt.path)
			require.Equal(t, tt.wantIndex, gotIndex, "index name mismatch")
			require.Equal(t, tt.wantDocID, gotDocID, "docID mismatch")
		})
	}
}

// newDocAffinityTestPolicy creates a DocumentAffinityPolicy with test connections
// pre-populated via DiscoveryUpdate. Returns the policy and the connections.
func newDocAffinityTestPolicy(t *testing.T, nodeCount int) (*DocumentAffinityPolicy, []*Connection) {
	t.Helper()

	cache := newIndexSlotCache(indexSlotCacheConfig{})
	p := NewDocumentAffinityPolicy(cache, defaultDecayFactor)

	conns := make([]*Connection, nodeCount)
	for i := range conns {
		u, err := url.Parse(fmt.Sprintf("https://node%d:9200", i))
		require.NoError(t, err)
		conns[i] = &Connection{
			URL:       u,
			URLString: u.String(),
			ID:        fmt.Sprintf("node%d", i),
			rttRing:   newRTTRing(4),
		}
		conns[i].rttRing.add(200 * time.Microsecond)
		conns[i].weight.Store(1)
		conns[i].state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	}

	err := p.DiscoveryUpdate(conns, nil, nil)
	require.NoError(t, err)

	return p, conns
}

func TestDocumentAffinityPolicyEval(t *testing.T) {
	t.Parallel()

	t.Run("non-document request falls through", func(t *testing.T) {
		t.Parallel()
		p, _ := newDocAffinityTestPolicy(t, 3)

		req, err := http.NewRequest(http.MethodGet, "/my-index/_search", nil)
		require.NoError(t, err)

		pool, evalErr := p.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Nil(t, pool, "non-document request should return nil pool")
	})

	t.Run("document request returns affinityPool with connection", func(t *testing.T) {
		t.Parallel()
		p, _ := newDocAffinityTestPolicy(t, 3)

		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/123", nil)
		require.NoError(t, err)

		pool, evalErr := p.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.NotNil(t, pool, "document request should return a pool")

		conn, nextErr := pool.Next()
		require.NoError(t, nextErr)
		require.NotNil(t, conn, "pool should contain a connection")
		require.NotEmpty(t, conn.ID, "connection should have an ID")
	})

	t.Run("no connections returns nil", func(t *testing.T) {
		t.Parallel()
		cache := newIndexSlotCache(indexSlotCacheConfig{})
		p := NewDocumentAffinityPolicy(cache, defaultDecayFactor)
		// Do not call DiscoveryUpdate -- no connections.

		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/123", nil)
		require.NoError(t, err)

		pool, evalErr := p.Eval(context.Background(), req)
		require.NoError(t, evalErr)
		require.Nil(t, pool, "no connections should return nil pool")
	})

	t.Run("same document key routes to same node", func(t *testing.T) {
		t.Parallel()
		p, _ := newDocAffinityTestPolicy(t, 5)

		req, err := http.NewRequest(http.MethodGet, "/my-index/_doc/abc", nil)
		require.NoError(t, err)

		pool1, err := p.Eval(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, pool1)
		conn1, err := pool1.Next()
		require.NoError(t, err)

		// Repeat with a fresh request to the same path.
		req2, err := http.NewRequest(http.MethodGet, "/my-index/_doc/abc", nil)
		require.NoError(t, err)

		pool2, err := p.Eval(context.Background(), req2)
		require.NoError(t, err)
		require.NotNil(t, pool2)
		conn2, err := pool2.Next()
		require.NoError(t, err)

		require.Equal(t, conn1.URL.String(), conn2.URL.String(),
			"same document key should consistently route to the same node")
	})
}

func TestDocumentAffinityPolicyDiscoveryUpdate(t *testing.T) {
	t.Parallel()

	t.Run("add connections makes IsEnabled true", func(t *testing.T) {
		t.Parallel()
		cache := newIndexSlotCache(indexSlotCacheConfig{})
		p := NewDocumentAffinityPolicy(cache, defaultDecayFactor)

		require.False(t, p.IsEnabled(), "should not be enabled before any connections")

		u, err := url.Parse("https://node0:9200")
		require.NoError(t, err)
		conn := &Connection{
			URL:       u,
			URLString: u.String(),
			ID:        "node0",
			rttRing:   newRTTRing(4),
		}
		conn.rttRing.add(200 * time.Microsecond)
		conn.weight.Store(1)

		err = p.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)
		require.True(t, p.IsEnabled(), "should be enabled after adding connections")
	})

	t.Run("remove all connections makes IsEnabled false", func(t *testing.T) {
		t.Parallel()
		p, conns := newDocAffinityTestPolicy(t, 2)
		require.True(t, p.IsEnabled())

		err := p.DiscoveryUpdate(nil, conns, nil)
		require.NoError(t, err)
		require.False(t, p.IsEnabled(), "should be disabled after removing all connections")
	})

	t.Run("added and removed changes connection set correctly", func(t *testing.T) {
		t.Parallel()
		p, conns := newDocAffinityTestPolicy(t, 3)
		require.True(t, p.IsEnabled())

		// Add a new node, remove one existing node.
		u, err := url.Parse("https://node99:9200")
		require.NoError(t, err)
		newConn := &Connection{
			URL:       u,
			URLString: u.String(),
			ID:        "node99",
			rttRing:   newRTTRing(4),
		}
		newConn.rttRing.add(200 * time.Microsecond)
		newConn.weight.Store(1)

		err = p.DiscoveryUpdate([]*Connection{newConn}, []*Connection{conns[0]}, nil)
		require.NoError(t, err)
		require.True(t, p.IsEnabled())

		// Verify the removed node is not selected and the new node is reachable.
		// We do this by checking the active connections list via a document request
		// that exercises the connection set.
		p.mu.RLock()
		activeConns := p.mu.activeConns
		p.mu.RUnlock()

		activeURLs := make(map[string]struct{}, len(activeConns))
		for _, c := range activeConns {
			activeURLs[c.URL.String()] = struct{}{}
		}

		_, removedPresent := activeURLs[conns[0].URL.String()]
		require.False(t, removedPresent, "removed connection should not be in active set")

		_, addedPresent := activeURLs[newConn.URL.String()]
		require.True(t, addedPresent, "newly added connection should be in active set")
	})
}

func TestDocumentAffinityPolicyConsistency(t *testing.T) {
	t.Parallel()

	t.Run("same index and docID consistently routes to same node", func(t *testing.T) {
		t.Parallel()
		p, _ := newDocAffinityTestPolicy(t, 5)

		req, err := http.NewRequest(http.MethodGet, "/orders/_doc/order-42", nil)
		require.NoError(t, err)

		// Route the same request 10 times and verify the same node is selected.
		var firstURL string
		for i := range 10 {
			pool, evalErr := p.Eval(context.Background(), req)
			require.NoError(t, evalErr)
			require.NotNil(t, pool)

			conn, nextErr := pool.Next()
			require.NoError(t, nextErr)

			if i == 0 {
				firstURL = conn.URL.String()
			} else {
				require.Equal(t, firstURL, conn.URL.String(),
					"iteration %d: same key should route to same node", i)
			}
		}
	})

	t.Run("different docIDs for same index may route to different nodes", func(t *testing.T) {
		t.Parallel()
		p, _ := newDocAffinityTestPolicy(t, 5)

		// Try enough different document IDs that at least two should route
		// to different nodes (by pigeonhole with 5 nodes).
		selectedNodes := make(map[string]struct{})
		for i := range 50 {
			path := fmt.Sprintf("/orders/_doc/doc-%d", i)
			req, err := http.NewRequest(http.MethodGet, path, nil)
			require.NoError(t, err)

			pool, evalErr := p.Eval(context.Background(), req)
			require.NoError(t, evalErr)
			require.NotNil(t, pool)

			conn, nextErr := pool.Next()
			require.NoError(t, nextErr)
			selectedNodes[conn.URL.String()] = struct{}{}
		}

		require.Greater(t, len(selectedNodes), 1,
			"different document IDs should spread across multiple nodes")
	})
}
