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

	"github.com/klauspost/compress/snappy"
	"github.com/stretchr/testify/require"

	compress "github.com/opensearch-project/opensearch-go/compress/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// healthCheckBody is a root-endpoint response the transport accepts as a
// healthy node.
const healthCheckBody = `{"name":"n","cluster_name":"c","version":{"number":"3.0.0"}}`

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
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				DisableRetry:      true,
				NodeStatsInterval: -1,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			// The later bodies go through an Encoder the transport pooled after
			// the first one.
			want := []string{
				strings.Repeat("first|", 64),
				strings.Repeat("second|", 256),
				strings.Repeat("third|", 16),
			}
			for _, body := range want {
				req, err := http.NewRequest(http.MethodPost, "/bulk", strings.NewReader(body))
				require.NoError(t, err)
				res, err := tp.Stream(req)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode)
			}

			received.Lock()
			defer received.Unlock()
			require.Empty(t, received.errs, "%q bodies the server could not decode", tt.token)
			require.Equal(t, []string{tt.token, tt.token, tt.token}, received.encodings)
			require.Equal(t, want, received.bodies)
		})
	}
}

// TestInvalidLevel verifies that a constructor rejects a level outside
// [compress.DefaultLevel] through 9 and returns a nil Compressor, so a bad
// level fails at construction rather than on the first request.
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
