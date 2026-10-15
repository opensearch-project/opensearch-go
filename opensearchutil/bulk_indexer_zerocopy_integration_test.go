// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && (core || opensearchutil)

package opensearchutil_test

import (
	"compress/gzip"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	transporttestutil "github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// TestBulkIndexerZeroCopy_Integration indexes documents lent to the indexer, which
// spans many chunks of the request body and, with the second case, is compressed on
// the way out, and requires the server to hold exactly what was lent and every
// document to have been released once.
func TestBulkIndexerZeroCopy_Integration(t *testing.T) {
	gzipCompressor, err := opensearchtransport.GZip(gzip.DefaultCompression)
	require.NoError(t, err)

	tests := []struct {
		name       string
		compressor opensearchtransport.Compressor
	}{
		{name: "plain"},
		{name: "compressed", compressor: gzipCompressor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testutil.ClientConfig(t)
			if cfg == nil {
				cfg = &opensearchapi.Config{Client: opensearch.Config{Context: t.Context()}}
			}
			cfg.Client.Compressor = tt.compressor
			client, clientErr := opensearchapi.NewClient(*cfg)
			require.NoError(t, clientErr)
			t.Cleanup(func() { _ = client.Close() })

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()

			const (
				numShards = 5
				numDocs   = 1500
				docBytes  = 4 << 10
			)

			index := transporttestutil.MustUniqueString(t, "bulk-zero-copy")
			createShardTestIndex(t, ctx, client, index,
				fmt.Sprintf(`{"index":{"number_of_shards":%d,"number_of_replicas":0}}`, numShards))
			awaitShardTarget(t, ctx, client, index, "warm-up")

			bi, biErr := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{Client: client})
			require.NoError(t, biErr)

			docs := make([][]byte, numDocs)
			var released, failed atomic.Int64
			for i := range docs {
				docs[i] = fmt.Appendf(nil, `{"seq":%d,"pad":"%s"}`, i, strings.Repeat(string(rune('a'+i%26)), docBytes))

				require.NoError(t, opensearchutil.AddZeroCopy(ctx, bi, opensearchutil.BulkIndexerZeroCopyItem{
					BulkIndexerItem: opensearchutil.BulkIndexerItem{
						Index: index, Action: "index", DocumentID: "doc-" + strconv.Itoa(i),
						OnFailure: func(context.Context, opensearchutil.BulkIndexerItem, opensearchapi.BulkRespItem, error) {
							failed.Add(1)
						},
					},
					Data:    docs[i],
					Release: func([]byte) { released.Add(1) },
				}))
			}
			require.NoError(t, bi.Close(ctx))

			stats := bi.Stats()
			require.EqualValues(t, numDocs, stats.NumIndexed)
			require.Zero(t, stats.NumFailed)
			require.Zero(t, failed.Load())
			require.EqualValues(t, numDocs, released.Load(), "every document is given back once")

			// What the server holds is what was lent.
			for _, i := range []int{0, 1, numDocs / 2, numDocs - 1} {
				got, getErr := client.Doc.Get(ctx, opensearchapi.GetReq{Index: index, ID: "doc-" + strconv.Itoa(i)})
				require.NoError(t, getErr)
				require.True(t, got.Found)
				require.JSONEq(t, string(docs[i]), string(got.Source), "document %d", i)
			}

			var total int
			_, refreshErr := client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			require.NoError(t, refreshErr)
			shards, catErr := client.Cat.Shards(ctx, &opensearchapi.CatShardsReq{Indices: []string{index}})
			require.NoError(t, catErr)
			for _, rec := range shards.Records {
				n, convErr := strconv.Atoi(*rec.Docs)
				require.NoError(t, convErr)
				total += n
			}
			require.Equal(t, numDocs, total)
		})
	}
}
