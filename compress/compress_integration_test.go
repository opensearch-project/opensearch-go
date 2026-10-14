// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration && core

package compress_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	tptestutil "github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
)

// bulkDocs is how many documents each codec indexes.
const bulkDocs = 200

// encodingRecorder records the Content-Encoding of each bulk request the client
// sends, so the test asserts what went over the wire rather than what the
// compressor reports.
type encodingRecorder struct {
	base http.RoundTripper
	sent struct {
		sync.Mutex
		encodings []string
	}
}

func (r *encodingRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/_bulk") {
		r.sent.Lock()
		r.sent.encodings = append(r.sent.encodings, req.Header.Get("Content-Encoding"))
		r.sent.Unlock()
	}
	return r.base.RoundTrip(req)
}

// TestBulkAgainstCluster indexes documents through a client using each
// compressor and counts them, which holds only if the server decodes the
// Content-Encoding the compressor sends.
func TestBulkAgainstCluster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		newCompressor newCompressorFunc
		token         string // the Content-Encoding the compressor sends
	}{
		{name: "gzip", newCompressor: newGzip, token: "gzip"},
		{name: "deflate", newCompressor: newDeflate, token: "deflate"},
		{name: "snappy", newCompressor: newSnappy, token: "snappy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := tt.newCompressor()
			require.NoError(t, err)

			cfg := testutil.ClientConfig(t)
			base := cfg.Client.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			rec := &encodingRecorder{base: base}
			cfg.Client.Transport = rec
			cfg.Client.Compressor = c

			client, err := opensearchapi.NewClient(*cfg)
			require.NoError(t, err)

			index := tptestutil.MustUniqueString(t, "test-compress-"+tt.name)
			t.Cleanup(func() {
				_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
			})

			var body strings.Builder
			for i := range bulkDocs {
				fmt.Fprintf(&body, "{\"index\":{\"_id\":\"%d\"}}\n{\"title\":\"document %d\",\"padding\":%q}\n", i, i, strings.Repeat("x", 64))
			}

			resp, err := client.Bulk(t.Context(), opensearchapi.BulkReq{
				Index:  index,
				Body:   strings.NewReader(body.String()),
				Params: &opensearchapi.BulkParams{Refresh: "true"},
			})
			require.NoError(t, err)
			require.False(t, resp.Errors)
			require.Len(t, resp.Items, bulkDocs)

			count, err := client.Count(t.Context(), &opensearchapi.CountReq{Indices: []string{index}})
			require.NoError(t, err)
			require.Equal(t, int64(bulkDocs), count.Count)

			rec.sent.Lock()
			defer rec.sent.Unlock()
			require.Equal(t, []string{tt.token}, rec.sent.encodings)
		})
	}
}
