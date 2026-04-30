// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package build

import (
	"bytes"
	"io"
	"net/http"
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
				if tt.wantErr == errAny {
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

	t.Run("bytes.Buffer is replayable", func(t *testing.T) {
		t.Parallel()
		body := bytes.NewBuffer([]byte(`{"hello":"world"}`))
		req, err := Request("POST", "/test", body, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, req.GetBody)

		replay, err := req.GetBody()
		require.NoError(t, err)
		got, _ := io.ReadAll(replay)
		require.Equal(t, `{"hello":"world"}`, string(got))
	})

	t.Run("bytes.Reader is replayable", func(t *testing.T) {
		t.Parallel()
		body := bytes.NewReader([]byte(`{"hello":"world"}`))
		req, err := Request("POST", "/test", body, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, req.GetBody)

		replay, err := req.GetBody()
		require.NoError(t, err)
		got, _ := io.ReadAll(replay)
		require.Equal(t, `{"hello":"world"}`, string(got))
	})

	t.Run("nil Header on GET with no body", func(t *testing.T) {
		t.Parallel()
		req, err := Request("GET", "/test", nil, nil, nil)
		require.NoError(t, err)
		require.Nil(t, req.Header)
	})
}
