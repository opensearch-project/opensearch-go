// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package compress_test

import (
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	compress "github.com/opensearch-project/opensearch-go/compress/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// healthCheckBody is a root-endpoint response the transport accepts as a
// healthy node.
const healthCheckBody = `{"name":"n","cluster_name":"c","version":{"number":"3.0.0"}}`

// nodesBody answers the GET /_nodes a CompressorTable sends, for a
// one-node cluster at the version healthCheckBody reports.
const nodesBody = `{"nodes":{"n":{"version":"3.0.0"}}}`

// decodeFunc decodes one compressed request body, using only a decoder that is
// not part of the compress module under test.
type decodeFunc func(r io.Reader) ([]byte, error)

// newCompressorFunc builds the Compressor under test. The tables in this
// package's test, benchmark and integration files share it.
type newCompressorFunc func() (opensearchtransport.Compressor, error)

// leveledFunc builds a Compressor at a compression level.
type leveledFunc func(level int) (opensearchtransport.Compressor, error)

// The codecs under test, each at its default level.
func newGzip() (opensearchtransport.Compressor, error) { return compress.Gzip(compress.DefaultLevel) }

func newDeflate() (opensearchtransport.Compressor, error) {
	return compress.Deflate(compress.DefaultLevel)
}

func newSnappy() (opensearchtransport.Compressor, error) { return compress.Snappy(), nil }

func newZstd() (opensearchtransport.Compressor, error) { return zstdAt(compress.DefaultLevel) }

// zstdAt returns the zstd Compressor of ZstdTable(level), which is the table's
// only rule.
func zstdAt(level int) (opensearchtransport.Compressor, error) {
	tbl, err := compress.ZstdTable(level)
	if err != nil {
		return nil, err
	}
	return tbl.Rules[0].Compressor, nil
}

// decodeVia decodes a body with the reader newReader builds over it.
func decodeVia[R io.Reader](newReader func(io.Reader) (R, error)) decodeFunc {
	return func(r io.Reader) ([]byte, error) {
		zr, err := newReader(r)
		if err != nil {
			return nil, err
		}
		return io.ReadAll(zr)
	}
}

var (
	decodeGzip    = decodeVia(gzip.NewReader)
	decodeDeflate = decodeVia(zlib.NewReader)
)

func decodeSnappy(r io.Reader) ([]byte, error) {
	return io.ReadAll(snappy.NewReader(r))
}

func decodeZstd(r io.Reader) ([]byte, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// TestCompressorRoundTrip sends several bodies through each compressor and
// decodes what the server receives, which covers the Content-Encoding token,
// the wire format, and Encoder reuse across requests.
func TestCompressorRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		newCompressor newCompressorFunc
		token         string
		decode        decodeFunc
	}{
		{
			name:          "gzip default level",
			newCompressor: newGzip,
			token:         "gzip",
			decode:        decodeGzip,
		},
		{
			name:          "gzip level 9",
			newCompressor: func() (opensearchtransport.Compressor, error) { return compress.Gzip(9) },
			token:         "gzip",
			decode:        decodeGzip,
		},
		{
			name:          "deflate default level",
			newCompressor: newDeflate,
			token:         "deflate",
			decode:        decodeDeflate,
		},
		{
			name:          "snappy",
			newCompressor: newSnappy,
			token:         "snappy",
			decode:        decodeSnappy,
		},
		{
			name:          "zstd default level",
			newCompressor: newZstd,
			token:         "zstd",
			decode:        decodeZstd,
		},
		{
			name:          "zstd level 22",
			newCompressor: func() (opensearchtransport.Compressor, error) { return zstdAt(22) },
			token:         "zstd",
			decode:        decodeZstd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := tt.newCompressor()
			require.NoError(t, err)
			require.Equal(t, tt.token, c.ContentEncoding())

			var received struct {
				sync.Mutex
				encodings []string
				bodies    []string
				errs      []error
				primed    bool // a /prime request arrived compressed
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/prime" {
					received.Lock()
					received.primed = received.primed || r.Header.Get("Content-Encoding") == tt.token
					received.Unlock()
					return
				}
				if r.URL.Path == "/_nodes" {
					_, _ = io.WriteString(w, nodesBody)
					return
				}
				// Record only the test's own requests; answer the transport's
				// health check without recording it.
				if r.Method != http.MethodPost || r.URL.Path != "/bulk" {
					_, _ = io.WriteString(w, healthCheckBody)
					return
				}

				plain, err := tt.decode(r.Body)

				received.Lock()
				defer received.Unlock()
				if err != nil {
					received.errs = append(received.errs, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				received.encodings = append(received.encodings, r.Header.Get("Content-Encoding"))
				received.bodies = append(received.bodies, string(plain))
			}))
			t.Cleanup(srv.Close)

			u, err := url.Parse(srv.URL)
			require.NoError(t, err)

			tp, err := opensearchtransport.New(opensearchtransport.Config{
				URLs:              []*url.URL{u},
				Compressor:        c,
				CompressMinBytes:  -1,
				DisableRetry:      true,
				NodeStatsInterval: -1,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			send := func(path, body string) {
				req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
				require.NoError(t, err)
				res, err := tp.Stream(req)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode)
			}

			// A table sends a rule's encoding only once the
			// transport has read the cluster's node versions, so wait for that.
			require.Eventually(t, func() bool {
				send("/prime", "prime")
				received.Lock()
				defer received.Unlock()
				return received.primed
			}, 5*time.Second, 10*time.Millisecond)

			// The later bodies go through an Encoder the transport pooled after
			// the first one.
			want := []string{
				strings.Repeat("first|", 64),
				strings.Repeat("second|", 256),
				strings.Repeat("third|", 16),
			}
			for _, body := range want {
				send("/bulk", body)
			}

			received.Lock()
			defer received.Unlock()
			require.Empty(t, received.errs, "%q bodies the server could not decode", tt.token)
			require.Equal(t, []string{tt.token, tt.token, tt.token}, received.encodings)
			require.Equal(t, want, received.bodies)
		})
	}
}

// TestZstdTable pins ZstdTable's contents: a zstd rule from 3.0.0, where zstd is
// supported, and gzip at level 6, which the standard library decodes, as Default.
func TestZstdTable(t *testing.T) {
	t.Parallel()

	tbl, err := compress.ZstdTable(compress.DefaultLevel)
	require.NoError(t, err)

	require.Len(t, tbl.Rules, 1)
	require.Equal(t, "v3.0.0", tbl.Rules[0].MinVersion)
	require.Equal(t, "zstd", tbl.Rules[0].Compressor.ContentEncoding())

	fb := tbl.Default
	require.NotNil(t, fb)
	require.Equal(t, "gzip", fb.ContentEncoding())

	want := strings.Repeat("fallback body|", 64)
	var buf strings.Builder
	enc, err := fb.NewEncoder(&buf)
	require.NoError(t, err)
	_, err = io.WriteString(enc, want)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	got, err := decodeGzip(strings.NewReader(buf.String()))
	require.NoError(t, err)
	require.Equal(t, want, string(got))
	require.Less(t, buf.Len(), len(want)/4, "fallback barely compressed")
}

// TestInvalidLevel verifies that a constructor rejects a level outside the range
// it documents and returns a nil Compressor, so a bad level fails at
// construction rather than on the first request.
func TestInvalidLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ctor  leveledFunc
		level int
	}{
		{name: "gzip below default", ctor: compress.Gzip, level: -2},
		{name: "gzip above 9", ctor: compress.Gzip, level: 10},
		{name: "deflate below default", ctor: compress.Deflate, level: -2},
		{name: "deflate above 9", ctor: compress.Deflate, level: 10},
		{name: "zstd below default", ctor: zstdAt, level: -2},
		{name: "zstd 0", ctor: zstdAt, level: 0},
		{name: "zstd above 22", ctor: zstdAt, level: 23},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := tt.ctor(tt.level)
			require.EqualError(t, err, fmt.Sprintf("compress: invalid compression level: %d", tt.level))
			require.Nil(t, c)
		})
	}
}
