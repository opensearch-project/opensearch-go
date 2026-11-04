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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compress "github.com/opensearch-project/opensearch-go/compress/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
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
		newTable      func() (*opensearchtransport.CompressorTable, error) // set instead of newCompressor for a codec chosen by a table
		token         string                                               // the Content-Encoding the compressor sends
		minVersion    string                                               // the first server version the codec is sent to, when it has one
		defaultToken  string                                               // the Content-Encoding sent below minVersion
	}{
		{name: "gzip", newCompressor: newGzip, token: "gzip"},
		{name: "deflate", newCompressor: newDeflate, token: "deflate"},
		{name: "snappy", newCompressor: newSnappy, token: "snappy"},
		{
			name: "zstd",
			newTable: func() (*opensearchtransport.CompressorTable, error) {
				return compress.ZstdTable(compress.DefaultLevel)
			},
			token:        "zstd",
			minVersion:   "3.0.0",
			defaultToken: "gzip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := tt.token
			if tt.minVersion != "" && tptestutil.EvaluateVersionCondition(t, tptestutil.GetServerVersion(t), "<"+tt.minVersion) {
				want = tt.defaultToken
			}

			cfg := testutil.ClientConfig(t)
			base := cfg.Client.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			rec := &encodingRecorder{base: base}
			cfg.Client.Transport = rec
			if tt.newTable != nil {
				table, err := tt.newTable()
				require.NoError(t, err)
				cfg.Client.CompressorTable = table
			} else {
				c, err := tt.newCompressor()
				require.NoError(t, err)
				cfg.Client.Compressor = c
			}

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

			bulk := func(rt require.TestingT) {
				resp, err := client.Bulk(t.Context(), opensearchapi.BulkReq{
					Index:  index,
					Body:   strings.NewReader(body.String()),
					Params: &opensearchapi.BulkParams{Refresh: "true"},
				})
				require.NoError(rt, err)
				require.False(rt, resp.Errors)
				require.Len(rt, resp.Items, bulkDocs)
			}
			encodings := func() []string {
				rec.sent.Lock()
				defer rec.sent.Unlock()
				return append([]string(nil), rec.sent.encodings...)
			}

			if tt.minVersion == "" {
				bulk(t)
				require.Equal(t, []string{tt.token}, encodings())
			} else {
				// A table sends its Default until the transport has
				// read the cluster's node versions, so repeat the bulk, which
				// indexes the same documents each time, until one goes out with
				// want. assert.CollectT is the type EventuallyWithT takes; the
				// checks on it are require's.
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					bulk(c)
					got := encodings()
					require.Equal(c, want, got[len(got)-1], "bulk sent without %q", want)
				}, 30*time.Second, 500*time.Millisecond)

				// Every bulk is compressed, and one the server cannot decode is
				// never sent.
				allowed := []string{tt.defaultToken, want}
				for _, e := range encodings() {
					require.Contains(t, allowed, e)
				}
			}

			count, err := client.Count(t.Context(), &opensearchapi.CountReq{Indices: []string{index}})
			require.NoError(t, err)
			require.Equal(t, int64(bulkDocs), count.Count)
		})
	}
}
