// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Tests that path segment encoding (encSeg) correctly percent-encodes
// special characters in user-supplied values end-to-end through to the
// HTTP request line.

package path_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/internal/build"
	"github.com/opensearch-project/opensearch-go/v4/internal/path"
)

func captureWire(t *testing.T) (*httptest.Server, *struct{ Method, URI string }) {
	t.Helper()
	got := &struct{ Method, URI string }{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.URI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func sendVia(t *testing.T, srv *httptest.Server, req *http.Request) {
	t.Helper()
	base, _ := url.Parse(srv.URL)
	req.URL.Scheme = base.Scheme
	req.URL.Host = base.Host
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

func TestPathSegmentEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       func() (string, error)
		wantSubstr string
		denySubstr string
	}{
		{
			name:   "dot-dot with slashes are encoded",
			method: http.MethodDelete,
			path: func() (string, error) {
				return path.DeletePath{Index: "audit-log", ID: "../../*"}.Build()
			},
			wantSubstr: "..%2F..%2F",
			denySubstr: "/../",
		},
		{
			name:   "slash in value encoded as %2F",
			method: http.MethodGet,
			path: func() (string, error) {
				return path.GetPath{Index: "logs", ID: "1/_source"}.Build()
			},
			wantSubstr: "1%2F_source",
			denySubstr: "/1/_source",
		},
		{
			name:   "question mark encoded as %3F",
			method: http.MethodPut,
			path: func() (string, error) {
				return path.CreatePath{Index: "idx", ID: "x?pipeline=evil"}.Build()
			},
			wantSubstr: "%3Fpipeline",
			denySubstr: "?pipeline",
		},
		{
			name:   "fragment hash encoded as %23",
			method: http.MethodPut,
			path: func() (string, error) {
				return path.CreatePath{Index: "idx", ID: "x#frag"}.Build()
			},
			wantSubstr: "%23frag",
			denySubstr: "#frag",
		},
		{
			name:   "percent encoded as %25",
			method: http.MethodGet,
			path: func() (string, error) {
				return path.GetPath{Index: "idx", ID: "100%done"}.Build()
			},
			wantSubstr: "100%25done",
		},
		{
			name:   "space in index name encoded as %20",
			method: http.MethodGet,
			path: func() (string, error) {
				return path.GetPath{Index: "my index", ID: "doc-1"}.Build()
			},
			wantSubstr: "my%20index",
			denySubstr: "my index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, got := captureWire(t)

			p, err := tt.path()
			require.NoError(t, err)

			req, err := build.Request(tt.method, p, nil, nil, nil)
			require.NoError(t, err)
			sendVia(t, srv, req)

			if tt.denySubstr != "" {
				require.NotContains(t, got.URI, tt.denySubstr)
			}
			require.Contains(t, got.URI, tt.wantSubstr)
		})
	}
}
