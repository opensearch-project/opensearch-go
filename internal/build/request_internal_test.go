// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		path        string
		body        io.Reader
		params      map[string]string
		headers     http.Header
		wantMethod  string
		wantPath    string
		wantQuery   map[string]string
		wantHeaders map[string][]string
		wantErr     error
	}{
		{
			name:       "basic GET without body",
			method:     "GET",
			path:       "/test",
			wantMethod: "GET",
			wantPath:   "/test",
		},
		{
			name:       "POST with body sets content-type",
			method:     "POST",
			path:       "/test",
			body:       bytes.NewReader([]byte(`{"test": "data"}`)),
			wantMethod: "POST",
			wantPath:   "/test",
			wantHeaders: map[string][]string{
				"Content-Type": {"application/json"},
			},
		},
		{
			name:   "query parameters encoded",
			method: "GET",
			path:   "/test",
			params: map[string]string{
				"filter_path": "took,hits.hits._source",
				"pretty":      "true",
			},
			wantMethod: "GET",
			wantPath:   "/test",
			wantQuery: map[string]string{
				"filter_path": "took,hits.hits._source",
				"pretty":      "true",
			},
		},
		{
			name:   "custom headers without body",
			method: "GET",
			path:   "/test",
			headers: http.Header{
				"X-Custom-Header": {"custom-value"},
				"Authorization":   {"Bearer token123"},
			},
			wantMethod: "GET",
			wantPath:   "/test",
			wantHeaders: map[string][]string{
				"X-Custom-Header": {"custom-value"},
				"Authorization":   {"Bearer token123"},
			},
		},
		{
			name:   "body and custom headers merge",
			method: "POST",
			path:   "/test",
			body:   bytes.NewReader([]byte(`{"test": "data"}`)),
			headers: http.Header{
				"X-Custom-Header": {"custom-value"},
			},
			wantMethod: "POST",
			wantPath:   "/test",
			wantHeaders: map[string][]string{
				"Content-Type":    {"application/json"},
				"X-Custom-Header": {"custom-value"},
			},
		},
		{
			name:   "multiple values for same header",
			method: "GET",
			path:   "/test",
			headers: http.Header{
				"X-Custom-Header": {"value1", "value2"},
			},
			wantMethod: "GET",
			wantPath:   "/test",
			wantHeaders: map[string][]string{
				"X-Custom-Header": {"value1", "value2"},
			},
		},
		{
			name:   "all options combined",
			method: "POST",
			path:   "/test/_search",
			body:   bytes.NewReader([]byte(`{"test": "data"}`)),
			params: map[string]string{"timeout": "30s"},
			headers: http.Header{
				"X-Request-ID": {"req-123"},
			},
			wantMethod: "POST",
			wantPath:   "/test/_search",
			wantQuery:  map[string]string{"timeout": "30s"},
			wantHeaders: map[string][]string{
				"Content-Type": {"application/json"},
				"X-Request-ID": {"req-123"},
			},
		},
		{
			name:    "empty method",
			method:  "",
			path:    "/test",
			wantErr: errMissingMethod,
		},
		{
			name:    "invalid method with control character",
			method:  "INVALID\nMETHOD",
			path:    "/test",
			wantErr: errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := Request(tt.method, tt.path, tt.body, tt.params, tt.headers)
			if tt.wantErr != nil {
				if errors.Is(tt.wantErr, errAny) {
					require.Error(t, err)
				} else {
					require.ErrorIs(t, err, tt.wantErr)
				}
				require.Nil(t, req)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantMethod, req.Method)
			require.Equal(t, tt.wantPath, req.URL.Path)

			for k, want := range tt.wantQuery {
				require.Equal(t, want, req.URL.Query().Get(k))
			}

			for k, wantVals := range tt.wantHeaders {
				require.Equal(t, wantVals, req.Header.Values(k))
			}
		})
	}
}

// errAny is a sentinel used in the table to mean "any error is acceptable."
var errAny = &sentinelError{}

type sentinelError struct{}

func (*sentinelError) Error() string { return "any error" }

func TestRequest_RawPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		path            string
		wantPath        string
		wantRawPath     string
		wantEscapedPath string
	}{
		{
			name:            "plain path has no-op RawPath",
			path:            "/logs/_doc/abc",
			wantPath:        "/logs/_doc/abc",
			wantRawPath:     "/logs/_doc/abc",
			wantEscapedPath: "/logs/_doc/abc",
		},
		{
			name:            "percent-encoded slash preserved on wire",
			path:            "/logs/_doc/a%2Fb",
			wantPath:        "/logs/_doc/a/b",
			wantRawPath:     "/logs/_doc/a%2Fb",
			wantEscapedPath: "/logs/_doc/a%2Fb",
		},
		{
			name:            "encoded dot-dot preserved on wire",
			path:            "/audit-log/_doc/%2E%2E%2F%2E%2E%2F%2A",
			wantPath:        "/audit-log/_doc/../../*",
			wantRawPath:     "/audit-log/_doc/%2E%2E%2F%2E%2E%2F%2A",
			wantEscapedPath: "/audit-log/_doc/%2E%2E%2F%2E%2E%2F%2A",
		},
		{
			name:            "no double-encoding of pre-encoded percent",
			path:            "/idx/_doc/a%252Fb",
			wantPath:        "/idx/_doc/a%2Fb",
			wantRawPath:     "/idx/_doc/a%252Fb",
			wantEscapedPath: "/idx/_doc/a%252Fb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := Request(http.MethodGet, tt.path, nil, nil, nil)
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, req.URL.Path)
			require.Equal(t, tt.wantRawPath, req.URL.RawPath)
			require.Equal(t, tt.wantEscapedPath, req.URL.EscapedPath())
		})
	}
}

func TestRequest_GetBody(t *testing.T) {
	t.Parallel()

	const payload = `{"hello":"world"}`

	tests := []struct {
		name string
		body func() io.Reader
		// wantLength is the ContentLength the request must carry; zero means none.
		wantLength int64
		// wantReplay is whether GetBody must exist, and then what it must yield on
		// every call.
		wantReplay bool
		// wantNilHeader is set for a request that has neither body nor headers.
		wantNilHeader bool
	}{
		{
			name:       "bytes.Buffer is replayable",
			body:       func() io.Reader { return bytes.NewBufferString(payload) },
			wantLength: int64(len(payload)),
			wantReplay: true,
		},
		{
			name:       "bytes.Reader is replayable",
			body:       func() io.Reader { return bytes.NewReader([]byte(payload)) },
			wantLength: int64(len(payload)),
			wantReplay: true,
		},
		{
			name:       "strings.Reader is replayable",
			body:       func() io.Reader { return strings.NewReader(payload) },
			wantLength: int64(len(payload)),
			wantReplay: true,
		},
		{
			name:       "a BufferedReader body gets a length and a replay",
			body:       func() io.Reader { return &chunkedBody{chunks: []string{`{"hello":`, `"world"}`}} },
			wantLength: int64(len(payload)),
			wantReplay: true,
		},
		{
			name: "a plain reader has neither",
			body: func() io.Reader { return io.MultiReader(strings.NewReader(payload)) },
		},
		{
			name:          "no body and no headers leaves Header nil",
			body:          func() io.Reader { return nil },
			wantNilHeader: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := "POST"
			if tt.wantNilHeader {
				method = "GET"
			}

			req, err := Request(method, "/test", tt.body(), nil, nil)
			require.NoError(t, err)
			require.Equal(t, tt.wantLength, req.ContentLength)

			if tt.wantNilHeader {
				require.Nil(t, req.Header)
			}

			if !tt.wantReplay {
				require.Nil(t, req.GetBody)
				return
			}

			// A request is sent again after the first attempt has read its body to the
			// end, so each replay must be of the whole body whatever was read before it,
			// and independent of the others.
			require.NotNil(t, req.GetBody)
			_, drainErr := io.Copy(io.Discard, req.Body)
			require.NoError(t, drainErr)
			for range 2 {
				replay, replayErr := req.GetBody()
				require.NoError(t, replayErr)
				got, readErr := io.ReadAll(replay)
				require.NoError(t, readErr)
				require.JSONEq(t, payload, string(got))
			}
		})
	}
}

// chunkedBody is a body.BufferedReader made of several pieces.
type chunkedBody struct {
	chunks []string
	read   io.Reader
}

func (b *chunkedBody) Read(p []byte) (int, error) {
	if b.read == nil {
		b.read = b.NewReader()
	}
	return b.read.Read(p)
}

func (b *chunkedBody) Len() int {
	n := 0
	for _, c := range b.chunks {
		n += len(c)
	}
	return n
}

func (b *chunkedBody) NewReader() io.Reader {
	readers := make([]io.Reader, len(b.chunks))
	for i, c := range b.chunks {
		readers[i] = strings.NewReader(c)
	}
	return io.MultiReader(readers...)
}

// TestEncodeQuery checks that encodeQuery writes what url.Values.Encode writes.
func TestEncodeQuery(t *testing.T) {
	t.Parallel()

	many := make(map[string]string)
	for i := range 20 {
		many[fmt.Sprintf("k%02d", 19-i)] = fmt.Sprintf("v%d", i)
	}
	tests := map[string]map[string]string{
		"one":                         {"timeout": "30s"},
		"sorted":                      {"z": "1", "a": "2", "m": "3"},
		"an empty value":              {"refresh": ""},
		"values that need escaping":   {"q": "a b&c=d/e?f#g+h%i", "routing": "é,ü"},
		"a key that needs escaping":   {"a b": "1", "x&y": "2"},
		"more keys than on the stack": many,
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := url.Values{}
			for k, v := range params {
				want.Set(k, v)
			}
			require.Equal(t, want.Encode(), encodeQuery(params))
		})
	}
}

// TestEncodeQueryAllocations checks that parameters that need no escaping cost
// the one string.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestEncodeQueryAllocations(t *testing.T) {
	params := map[string]string{"preference": "_local", "refresh": "true", "timeout": "30s"}
	var got string
	allocs := testing.AllocsPerRun(100, func() { got = encodeQuery(params) })
	require.Equal(t, "preference=_local&refresh=true&timeout=30s", got)
	require.LessOrEqual(t, allocs, 1.0)
}
