// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package compress_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// benchPayloadBytes is the size of the synthetic bulk body each benchmark
// encodes.
const benchPayloadBytes = 1 << 20

// bulkNDJSON returns at least size bytes of bulk-API NDJSON, an action line then
// a log-shaped document, from a fixed seed so every run encodes the same bytes.
func bulkNDJSON(size int) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	paths := []string{"/api/v1/orders", "/api/v1/users", "/healthz", "/api/v2/search", "/static/app.js"}
	levels := []string{"info", "info", "info", "warn", "error"}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	for i := 0; buf.Len() < size; i++ {
		fmt.Fprintf(&buf, "{\"index\":{\"_id\":\"%d\"}}\n", i)
		fmt.Fprintf(&buf,
			"{\"@timestamp\":%q,\"level\":%q,\"host\":\"web-%02d\",\"path\":%q,\"status\":%d,"+
				"\"bytes\":%d,\"duration_ms\":%d,\"message\":\"request completed for session %016x\"}\n",
			start.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano),
			levels[rng.IntN(len(levels))],
			rng.IntN(40),
			paths[rng.IntN(len(paths))],
			200+100*rng.IntN(4),
			rng.IntN(1<<16),
			rng.IntN(500),
			rng.Uint64(),
		)
	}
	return buf.Bytes()
}

// countingWriter counts the bytes written to it and keeps none of them.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// BenchmarkEncodeNDJSON measures one encode cycle (Reset, Write, Close) of a
// bulk body per codec, the work the transport repeats per request on a pooled
// Encoder, and reports the compression ratio.
func BenchmarkEncodeNDJSON(b *testing.B) {
	payload := bulkNDJSON(benchPayloadBytes)

	benchmarks := []struct {
		name          string
		newCompressor newCompressorFunc
	}{
		{name: "gzip-klauspost", newCompressor: newGzip},
		{name: "gzip-stdlib", newCompressor: func() (opensearchtransport.Compressor, error) {
			return opensearchtransport.GZip(gzip.DefaultCompression)
		}},
		{name: "deflate", newCompressor: newDeflate},
		{name: "snappy", newCompressor: newSnappy},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			c, err := bm.newCompressor()
			require.NoError(b, err)

			var out countingWriter
			enc, err := c.NewEncoder(&out)
			require.NoError(b, err)

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				out.n = 0
				enc.Reset(&out)
				_, err := enc.Write(payload)
				require.NoError(b, err)
				require.NoError(b, enc.Close())
			}
			b.ReportMetric(float64(len(payload))/float64(out.n), "ratio")
		})
	}
}
