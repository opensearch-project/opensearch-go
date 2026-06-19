// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// errReadCloser fails on the first Read and records whether Close was called.
type errReadCloser struct {
	err    error
	closed bool
}

func (e *errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e *errReadCloser) Close() error             { e.closed = true; return nil }

// TestGzipCompressorBufferPoolReuse exercises the buffer-pool nil-poisoning fix:
// compress must hand a non-nil buffer back to the pool even on a read error so a
// later Get().Reset() does not panic, and collectBuffer must tolerate a nil buffer.
func TestGzipCompressorBufferPoolReuse(t *testing.T) {
	t.Parallel()

	t.Run("compress error returns reusable buffer", func(t *testing.T) {
		t.Parallel()

		gz := newGzipCompressor()
		rc := io.NopCloser(iotest.ErrReader(errors.New("boom")))

		buf, err := gz.compress(rc)
		require.Error(t, err)
		require.NotNil(t, buf, "compress must return a non-nil buffer on error so the pool is not poisoned")

		// Returning the buffer and reusing the pool must not panic on Reset().
		gz.collectBuffer(buf)
		require.NotPanics(t, func() {
			next, cerr := gz.compress(io.NopCloser(strings.NewReader("opensearch")))
			require.NoError(t, cerr)
			gz.collectBuffer(next)
		})
	})

	t.Run("collectBuffer tolerates nil", func(t *testing.T) {
		t.Parallel()

		gz := newGzipCompressor()
		require.NotPanics(t, func() { gz.collectBuffer(nil) })
	})
}

// TestSetReqGlobalHeaderOverride pins the per-request header override semantics:
// a request-level header value must fully suppress the matching global default,
// leaving exactly one value (the request's) rather than appending both. The
// len() assertion is what distinguishes the fix from the prior buggy code, which
// the value-only Get() check could not detect.
func TestSetReqGlobalHeaderOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setReqVal string // per-request header value; empty means unset
		wantVals  []string
	}{
		{name: "request overrides global", setReqVal: "baz", wantVals: []string{"baz"}},
		{name: "global applies when unset", setReqVal: "", wantVals: []string{"bar"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hdr := http.Header{}
			hdr.Set("X-Foo", "bar")
			tp, err := New(Config{Header: hdr})
			require.NoError(t, err)

			req, err := http.NewRequest(http.MethodGet, "/abc", nil)
			require.NoError(t, err)
			if tt.setReqVal != "" {
				req.Header.Set("X-Foo", tt.setReqVal)
			}

			tp.setReqGlobalHeader(req)

			require.Equal(t, tt.wantVals, req.Header["X-Foo"],
				"expected exactly the override values, not global+request appended")
			require.Len(t, req.Header["X-Foo"], len(tt.wantVals),
				"duplicate-header regression: global default was appended alongside the request value")
		})
	}
}

// TestStreamReturnsBodyUnbuffered verifies that Stream returns the raw response
// body without reading or closing it: a failing body reader must not be
// triggered by Stream itself — the caller owns the body lifecycle.
func TestStreamReturnsBodyUnbuffered(t *testing.T) {
	t.Parallel()

	body := &errReadCloser{err: errors.New("truncated body")}

	u, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:              []*url.URL{u},
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		DisableRetry:      true,
		Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
		}),
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, "/test", nil)
	require.NoError(t, err)

	res, err := tp.Stream(req)
	require.NoError(t, err, "Stream must not read the body, so the read error must not surface here")
	require.NotNil(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.False(t, body.closed, "Stream must not close the body before the caller does")
	res.Body.Close()
}
