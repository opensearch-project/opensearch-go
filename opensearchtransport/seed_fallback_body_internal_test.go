// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// closeCountBody is a request body that records how often it is closed, so a
// test can tell a body the transport closed from one it dropped.
type closeCountBody struct {
	io.Reader
	closes atomic.Int32
}

func newCloseCountBody() *closeCountBody {
	return &closeCountBody{Reader: strings.NewReader("{}")}
}

func (b *closeCountBody) Close() error {
	b.closes.Add(1)
	return nil
}

// TestStreamClosesBodyOnErrNoConnections checks that a request whose first
// attempt finds no connection has its body closed exactly once, as an
// http.RoundTripper does for a request it fails. A body that lends memory, such
// as a bulk indexer's buffer, is released by its Close.
func TestStreamClosesBodyOnErrNoConnections(t *testing.T) {
	t.Run("seed fallback disabled", func(t *testing.T) {
		t.Parallel()

		tp := newNoConnectionsTransport(t, func(*http.Request) (*http.Response, error) {
			t.Error("no request should be sent")
			return nil, io.EOF
		}, func(cfg *Config) {
			cfg.lookupEnv = envLookup(map[string]string{envFallbackConfig: "false"})
		})
		require.Nil(t, tp.seedFallbackPool)

		body := newCloseCountBody()
		req := newBodyRequest(t, body)

		res, err := tp.Stream(req) //nolint:bodyclose // error path: res is nil
		require.ErrorIs(t, err, ErrNoConnections)
		require.Nil(t, res)
		require.EqualValues(t, 1, body.closes.Load(), "the caller's body must be closed when nothing is sent")
	})

	t.Run("seed fallback replaces the body from GetBody", func(t *testing.T) {
		var sent *closeCountBody
		tp := newNoConnectionsTransport(t, func(req *http.Request) (*http.Response, error) {
			sent, _ = req.Body.(*closeCountBody)
			_ = req.Body.Close() // a RoundTripper closes the body it is given
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: http.NoBody}, nil
		})
		require.NotNil(t, tp.seedFallbackPool)

		body := newCloseCountBody()
		req := newBodyRequest(t, body)
		req.GetBody = func() (io.ReadCloser, error) { return newCloseCountBody(), nil }

		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		_ = res.Body.Close()

		require.NotNil(t, sent, "the fallback must have sent a body")
		require.NotSame(t, body, sent, "the fallback sends the body GetBody returned")
		require.EqualValues(t, 1, sent.closes.Load())
		require.EqualValues(t, 1, body.closes.Load(), "the replaced body must be closed, not dropped")
	})

	t.Run("seed fallback sends the body when there is no GetBody", func(t *testing.T) {
		body := newCloseCountBody()
		var closesAtSend int32 = -1
		var sent *closeCountBody
		tp := newNoConnectionsTransport(t, func(req *http.Request) (*http.Response, error) {
			sent, _ = req.Body.(*closeCountBody)
			closesAtSend = body.closes.Load()
			_ = req.Body.Close() // a RoundTripper closes the body it is given
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: http.NoBody}, nil
		})

		req := newBodyRequest(t, body)
		require.Nil(t, req.GetBody)

		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		_ = res.Body.Close()

		require.Same(t, body, sent, "without GetBody the fallback sends the caller's body")
		require.EqualValues(t, 0, closesAtSend, "the body must still be open when it is sent")
		require.EqualValues(t, 1, body.closes.Load(), "the transport closes it once, and nothing closes it again")
	})

	t.Run("seed fallback signing fails after GetBody replaced the body", func(t *testing.T) {
		tp := newNoConnectionsTransport(t, func(*http.Request) (*http.Response, error) {
			t.Error("a request that failed to sign must not be sent")
			return nil, io.EOF
		}, withFailingSigner)

		body := newCloseCountBody()
		req := newBodyRequest(t, body)
		var replacement *closeCountBody
		req.GetBody = func() (io.ReadCloser, error) {
			replacement = newCloseCountBody()
			return replacement, nil
		}

		res, err := tp.Stream(req) //nolint:bodyclose // error path: res is nil
		require.Error(t, err)
		require.Nil(t, res)

		require.NotNil(t, replacement, "the fallback must have asked GetBody for a body")
		require.EqualValues(t, 1, replacement.closes.Load(), "the body that was not sent must be closed")
		require.EqualValues(t, 1, body.closes.Load(), "the caller's body must be closed")
	})

	t.Run("seed fallback signing fails without GetBody", func(t *testing.T) {
		tp := newNoConnectionsTransport(t, func(*http.Request) (*http.Response, error) {
			t.Error("a request that failed to sign must not be sent")
			return nil, io.EOF
		}, withFailingSigner)

		body := newCloseCountBody()
		req := newBodyRequest(t, body)

		res, err := tp.Stream(req) //nolint:bodyclose // error path: res is nil
		require.Error(t, err)
		require.Nil(t, res)
		require.EqualValues(t, 1, body.closes.Load(), "the body must be closed once, not twice")
	})
}

// withFailingSigner configures a signer whose every signature fails.
func withFailingSigner(cfg *Config) {
	cfg.Signer = &mockSigner{ReturnError: true}
}

// newNoConnectionsTransport returns a transport whose router never yields a
// connection, so every request falls through to the seed fallback.
func newNoConnectionsTransport(t *testing.T, rt func(*http.Request) (*http.Response, error), opts ...func(*Config)) *Transport {
	t.Helper()

	seedURL, err := url.Parse("http://seed-node:9200")
	require.NoError(t, err)

	cfg := Config{
		URLs:                  []*url.URL{seedURL},
		SkipConnectionShuffle: true,
		HealthCheck:           NoOpHealthCheck,
		NodeStatsInterval:     -1, // Disable stats poller to avoid background requests through mock transport
		Router:                &emptyRouter{},
		DisableRetry:          true,
		Transport:             mockhttp.NewRoundTripFunc(t, rt),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	tp, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	return tp
}

// newBodyRequest builds a POST around body without letting net/http infer a
// GetBody for it, as it would for a bytes.Reader.
func newBodyRequest(t *testing.T, body io.ReadCloser) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "/test", body)
	require.NoError(t, err)

	return req
}
