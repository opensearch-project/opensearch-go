// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchtransport)

package opensearchtransport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestRoutingMetadataURLBatches_Integration(t *testing.T) {
	testutil.WaitForCluster(t)
	testutil.SkipIfVersion(t, "<", "2.0", "routing_num_shards unavailable")

	const metadataPath = "/_cluster/state/metadata"

	u := testutil.GetTestURL(t)
	cfg := getTestConfig(t, []*url.URL{u})
	cfg.lookupEnv = envLookup(map[string]string{envRouter: "false"})
	cfg.DiscoverNodesInterval = -1
	cfg.NodeStatsInterval = -1

	base := testutil.GetTestTransport(t).(*http.Transport).Clone()
	base.Protocols = new(http.Protocols)
	base.Protocols.SetHTTP1(true)
	// A clone can retain h2 in ALPN; this test exercises the HTTP/1.1 line limit.
	if base.TLSClientConfig != nil {
		base.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	t.Cleanup(base.CloseIdleConnections)

	var mu sync.Mutex
	var metadataURIs []string

	cfg.Transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, metadataPath) {
			mu.Lock()
			metadataURIs = append(metadataURIs, req.URL.RequestURI())
			mu.Unlock()
		}

		return base.RoundTrip(req)
	})

	transport, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, transport.Close())
	})

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	prefix := testutil.MustUniqueString(t, "routing-metadata-batches")
	indexes := make([]string, 20)

	for i := range indexes {
		index := fmt.Sprintf("%s-%02d-%s", prefix, i, strings.Repeat("x", 230-len(prefix)-4))
		indexes[i] = index

		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()

			req, reqErr := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, "/"+index, nil)
			require.NoError(t, reqErr)

			resp, requestErr := transport.Stream(req)
			require.NoError(t, requestErr)
			defer resp.Body.Close()

			_, readErr := io.Copy(io.Discard, resp.Body)
			require.NoError(t, readErr)
			require.Contains(t, []int{http.StatusOK, http.StatusNotFound}, resp.StatusCode, "cleanup index %q", index)
		})

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPut, "/"+index, strings.NewReader(
			`{"settings":{"number_of_shards":1,"number_of_replicas":0,"number_of_routing_shards":2}}`))
		require.NoError(t, reqErr)
		req.Header.Set("Content-Type", "application/json")

		resp, requestErr := transport.Stream(req)
		require.NoError(t, requestErr)

		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, resp.StatusCode, "create index %q: %s", index, body)
	}

	metadata, err := transport.getRoutingMeta(ctx, indexes)
	require.NoError(t, err)
	require.Len(t, metadata, len(indexes))

	mu.Lock()
	uris := slices.Clone(metadataURIs)
	mu.Unlock()

	require.GreaterOrEqual(t, len(uris), 2, "long index names must require multiple scoped requests")

	wantFilter := "filter_path=metadata.indices.*.routing_num_shards," +
		"metadata.indices.*.settings.index.number_of_shards," +
		"metadata.indices.*.settings.index.routing_partition_size," +
		"metadata.indices.*.settings.index.replication.type"
	wantPrefix := strings.TrimRight(u.Path, "/") + metadataPath + "/"
	unsplit := &url.URL{Path: wantPrefix + strings.Join(indexes, ","), RawQuery: wantFilter}
	require.Greater(t, calcHTTPLineLen(&http.Request{Method: http.MethodGet, URL: unsplit}), routingMetadataMaxLineLength,
		"the fixture must exceed one request")

	requested := make([]string, 0, len(indexes))

	for _, uri := range uris {
		u, parseErr := url.ParseRequestURI(uri)
		require.NoError(t, parseErr)
		require.LessOrEqual(t, calcHTTPLineLen(&http.Request{Method: http.MethodGet, URL: u}), routingMetadataMaxLineLength)
		require.Equal(t, wantFilter, u.RawQuery)
		require.True(t, strings.HasPrefix(u.Path, wantPrefix), "metadata request must remain scoped: %s", uri)

		batch := strings.TrimPrefix(u.Path, wantPrefix)
		require.NotEmpty(t, batch, "metadata request must name its indexes")
		requested = append(requested, strings.Split(batch, ",")...)
	}

	require.Equal(t, indexes, requested, "each index must be requested exactly once and in order")

	for _, index := range indexes {
		require.Contains(t, metadata, index)
		require.Equal(t, 2, metadata[index].RoutingNumShards, index)
		require.Equal(t, 1, metadata[index].NumberOfShards, index)
	}
}
