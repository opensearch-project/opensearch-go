// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

const routingMetadataFilter = "filter_path=metadata.indices.*.routing_num_shards," +
	"metadata.indices.*.settings.index.number_of_shards," +
	"metadata.indices.*.settings.index.routing_partition_size," +
	"metadata.indices.*.settings.index.replication.type"

func TestRoutingMetadataRequestLineLimit(t *testing.T) {
	t.Parallel()

	escapedIndexes := make([]string, 50)
	for i := range escapedIndexes {
		escapedIndexes[i] = fmt.Sprintf("index-%02d-%s", len(escapedIndexes)-i-1, strings.Repeat("é", 15))
	}

	tests := []struct {
		name           string
		basePath       string
		indexes        []string
		wantBatches    int
		wantLineLength int
	}{
		{name: "short request remains scoped", indexes: []string{"first", "second"}, wantBatches: 1},
		{
			name: "below the limit", indexes: routingMetadataIndexesForLine(t, routingMetadataMaxLineLength-1),
			wantBatches: 1, wantLineLength: routingMetadataMaxLineLength - 1,
		},
		{
			name: "at the limit", indexes: routingMetadataIndexesForLine(t, routingMetadataMaxLineLength),
			wantBatches: 1, wantLineLength: routingMetadataMaxLineLength,
		},
		{
			name: "above the limit including query", indexes: routingMetadataIndexesForLine(t, routingMetadataMaxLineLength+1),
			wantBatches: 2,
		},
		{
			name: "node base path crosses the limit", basePath: "/proxy/",
			indexes: routingMetadataIndexesForLine(t, routingMetadataMaxLineLength), wantBatches: 2,
		},
		{name: "escaped index names", indexes: escapedIndexes, wantBatches: 2},
		{
			name: "escaped node base path crosses the limit", basePath: "/" + strings.Repeat("é", 40),
			indexes: routingMetadataIndexesForLine(t, 4000), wantBatches: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := slices.Clone(tt.indexes)

			var (
				signedURIs, sentURIs, requested []string
				bodies                          []*closeCountBody
				sentLineLengths                 []int
			)

			tr, u, _ := newInternalReqTransport(t, nil, http.Header{"X-Test-Header": {"from-config"}},
				mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					batch := requireRoutingMetadataBatch(t, req, tt.basePath)
					sentURIs = append(sentURIs, req.URL.RequestURI())
					sentLineLengths = append(sentLineLengths, calcHTTPLineLen(req))
					requested = append(requested, batch...)
					require.Len(t, signedURIs, len(sentURIs), "each final batch must be signed before sending")
					require.Equal(t, signedURIs[len(signedURIs)-1], req.URL.RequestURI())
					require.Equal(t, "signed", req.Header.Get("X-Test-Signature"))
					require.Equal(t, "from-config", req.Header.Get("X-Test-Header"))
					for _, body := range bodies {
						require.EqualValues(t, 1, body.closes.Load(), "the previous batch must be closed before the next starts")
					}

					body := routingMetadataBody(t, batch)
					bodies = append(bodies, body)

					return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
				}))
			u.Path = tt.basePath
			u.User = url.UserPassword("reader", "password")
			tr.signer = &mockSigner{
				SampleKey: "X-Test-Signature", SampleValue: "signed",
				testHook: func(req *http.Request) {
					signedURIs = append(signedURIs, req.URL.RequestURI())
					require.Equal(t, "from-config", req.Header.Get("X-Test-Header"))
					user, password, ok := req.BasicAuth()
					require.True(t, ok)
					require.Equal(t, "reader", user)
					require.Equal(t, "password", password)
				},
			}

			metadata, err := tr.getRoutingMeta(t.Context(), tt.indexes)
			require.NoError(t, err)
			require.Len(t, sentURIs, tt.wantBatches)
			require.Equal(t, sentURIs, signedURIs)
			require.Equal(t, tt.indexes, requested, "each requested index must appear exactly once, in input order")
			require.Equal(t, before, tt.indexes, "batching must not mutate the input")
			if tt.wantLineLength > 0 {
				require.Equal(t, tt.wantLineLength, sentLineLengths[0])
			}

			require.Len(t, metadata, len(tt.indexes), "metadata from all batches must be merged")
			for _, index := range tt.indexes {
				require.Equal(t, indexRoutingMeta{
					RoutingNumShards: 640, NumberOfShards: 5, RoutingPartitionSize: 2, Replication: ReplicationSegment,
				}, metadata[index])
			}
			for _, body := range bodies {
				require.EqualValues(t, 1, body.closes.Load())
			}
		})
	}
}

func TestRoutingMetadataRejectsUnsendableRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		indexes      []string
		basePath     string
		signerGrowth bool
		wantErr      bool
	}{
		{name: "nil indexes"},
		{name: "empty indexes", indexes: []string{}},
		{name: "single index cannot fit", indexes: []string{strings.Repeat("x", routingMetadataMaxLineLength)}, wantErr: true},
		{
			name: "base path cannot fit", indexes: []string{"index"},
			basePath: "/" + strings.Repeat("p", routingMetadataMaxLineLength), wantErr: true,
		},
		{name: "signer expands final query", indexes: []string{"index"}, signerGrowth: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			tr, u, _ := newInternalReqTransport(t, nil, nil,
				mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}))
			u.Path = tt.basePath
			if tt.signerGrowth {
				tr.signer = &mockSigner{
					SampleKey: "X-Test-Signature", SampleValue: "signed",
					testHook: func(req *http.Request) {
						req.URL.RawQuery += "&signer=" + strings.Repeat("x", routingMetadataMaxLineLength)
					},
				}
			}

			metadata, err := tr.getRoutingMeta(t.Context(), tt.indexes)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, metadata)
			require.Zero(t, calls, "empty or oversized requests must not reach the network")
		})
	}
}

func TestRoutingMetadataAlreadyCanceled(t *testing.T) {
	t.Parallel()

	calls := 0
	tr, _, _ := newInternalReqTransport(t, nil, nil,
		mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: routingMetadataBody(t, []string{"index"})}, nil
		}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	metadata, err := tr.getRoutingMeta(ctx, []string{"index"})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, metadata)
	require.Zero(t, calls, "a canceled discovery must not send metadata requests")
}

func TestRoutingMetadataStopsAfterBatchFailure(t *testing.T) {
	t.Parallel()

	indexes := make([]string, 60)
	for i := range indexes {
		indexes[i] = fmt.Sprintf("index-%02d-%s", i, strings.Repeat("x", 171))
	}

	tests := []struct {
		name          string
		status        int
		malformed     bool
		cancelOnClose bool
		wantRequests  int
	}{
		{name: "later HTTP error", status: http.StatusForbidden, wantRequests: 2},
		{name: "later decode error", malformed: true, wantRequests: 2},
		{name: "caller canceled between batches", cancelOnClose: true, wantRequests: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var (
				bodies    []*closeCountBody
				requested []string
			)

			tr, _, _ := newInternalReqTransport(t, nil, nil,
				mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					batch := requireRoutingMetadataBatch(t, req, "")
					requested = append(requested, batch...)
					for _, body := range bodies {
						require.EqualValues(t, 1, body.closes.Load())
					}

					status := http.StatusOK
					body := routingMetadataBody(t, batch)
					if len(bodies) == 1 {
						if tt.status > 0 {
							status = tt.status
						}
						if tt.malformed {
							body.Reader = strings.NewReader(`{`)
						}
					}
					bodies = append(bodies, body)

					var responseBody io.ReadCloser = body
					if tt.cancelOnClose {
						responseBody = &cancelOnCloseBody{ReadCloser: body, cancel: cancel}
					}

					return &http.Response{StatusCode: status, Body: responseBody}, nil
				}))

			metadata, err := tr.getRoutingMeta(ctx, indexes)
			require.Error(t, err)
			require.Nil(t, metadata, "an incomplete batch sequence must not return partial metadata")
			require.Len(t, bodies, tt.wantRequests, "later batches must not be requested after an error")
			require.Less(t, len(requested), len(indexes), "the fixture must leave at least one unsent batch")
			require.Equal(t, indexes[:len(requested)], requested)
			if tt.cancelOnClose {
				require.ErrorIs(t, err, context.Canceled)
			}
			for _, body := range bodies {
				require.EqualValues(t, 1, body.closes.Load())
			}
		})
	}
}

func TestRoutingMetadataBatchesPopulateRequestedShardMaps(t *testing.T) {
	t.Parallel()

	indexes := routingMetadataIndexesForLine(t, routingMetadataMaxLineLength+1)
	shards := make([]map[string]string, 0, len(indexes))
	for _, index := range indexes {
		shards = append(shards, map[string]string{
			"index": index, "shard": "0", "prirep": "p", "state": "STARTED", "node": "node1",
		})
	}
	shardBody, err := json.Marshal(shards)
	require.NoError(t, err)

	var (
		catRequests, metadataRequests int
		requested                     []string
	)

	tr, _, conn := newInternalReqTransport(t, nil, nil,
		mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/_cat/shards" {
				catRequests++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(shardBody)))}, nil
			}

			batch := requireRoutingMetadataBatch(t, req, "")
			metadataRequests++
			requested = append(requested, batch...)
			return &http.Response{StatusCode: http.StatusOK, Body: routingMetadataBody(t, batch)}, nil
		}))
	cache := newIndexSlotCache(indexSlotCacheConfig{})
	tr.router = NewRouter(wrapWithRouter(NewNullPolicy(), cache, &shardCostForReads, poolSearch, nil))
	tr.seedFallbackPool = testPool([]*Connection{conn})
	for _, index := range indexes {
		_, known := tr.ShardMap(index)
		require.False(t, known)
	}

	for cycle := range 2 {
		tr.fetchAndUpdateShardPlacement(t.Context())
		require.Equal(t, cycle+1, catRequests)
		require.Equal(t, 2, metadataRequests, "the next discovery cycle must reuse cached metadata")

		for _, index := range indexes {
			got, known := tr.ShardMap(index)
			require.True(t, known, "routing metadata must reach the shard cache for %q", index)
			require.Equal(t, ShardMap{RoutingNumShards: 640, PrimaryShards: 5, RoutingPartitionSize: 2}, got)
			require.Equal(t, ReplicationSegment, cache.slotFor(index).shardMap.Load().Replication)
		}
	}

	require.Len(t, requested, len(indexes), "only the initial discovery cycle must fetch each index")
	require.ElementsMatch(t, indexes, requested)
}

func requireRoutingMetadataBatch(t *testing.T, req *http.Request, basePath string) []string {
	t.Helper()

	prefix := strings.TrimRight(basePath, "/") + "/_cluster/state/metadata/"
	require.True(t, strings.HasPrefix(req.URL.Path, prefix), "metadata requests must remain explicitly scoped: %s", req.URL.Path)
	indexes := strings.TrimPrefix(req.URL.Path, prefix)
	require.NotEmpty(t, indexes, "an all-index metadata fallback must never be sent")
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, routingMetadataFilter, req.URL.RawQuery)
	require.LessOrEqual(t, calcHTTPLineLen(req), routingMetadataMaxLineLength)

	return strings.Split(indexes, ",")
}

func routingMetadataBody(t *testing.T, indexes []string) *closeCountBody {
	t.Helper()

	metadata := make(map[string]any, len(indexes))
	for _, index := range indexes {
		metadata[index] = map[string]any{
			"routing_num_shards": 640,
			"settings": map[string]any{"index": map[string]any{
				"number_of_shards": "5", "routing_partition_size": "2", "replication": map[string]string{"type": "SEGMENT"},
			}},
		}
	}
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{"indices": metadata}})
	require.NoError(t, err)

	return &closeCountBody{Reader: strings.NewReader(string(body))}
}

func routingMetadataIndexesForLine(t *testing.T, length int) []string {
	t.Helper()

	indexes := make([]string, 21)
	for i := range 20 {
		indexes[i] = fmt.Sprintf("index-%02d-%s", i, strings.Repeat("x", 171))
	}
	indexes[20] = "i"
	u := &url.URL{Path: "/_cluster/state/metadata/" + strings.Join(indexes, ","), RawQuery: routingMetadataFilter}
	padding := length - calcHTTPLineLen(&http.Request{Method: http.MethodGet, URL: u})
	require.GreaterOrEqual(t, padding, 0)
	require.Less(t, padding, 255, "each generated index name must remain valid")
	indexes[20] += strings.Repeat("x", padding)

	return indexes
}
