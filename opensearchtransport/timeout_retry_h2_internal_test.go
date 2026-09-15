// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAttemptConnCloseNil(t *testing.T) {
	var a attemptConn
	require.NotPanics(t, a.close)
}

func TestWithAttemptConnTracePreservesGotConn(t *testing.T) {
	var seen atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { seen.Store(true) },
	})
	var slot attemptConn
	ctx = withAttemptConnTrace(ctx, &slot)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })

	require.True(t, seen.Load(), "previous GotConn hook must still run")
	slot.mu.Lock()
	conn := slot.conn
	slot.mu.Unlock()
	require.NotNil(t, conn)
}

// TestTimeoutRetryDoesNotReuseHTTP2Conn is the #1121 reproducer: a warmed
// HTTP/2 connection to an old backend that then stops responding, while
// DialContext already points at a healthy replacement. Canceling the
// per-attempt context RSTs the stream but leaves the ClientConn pooled, so
// every retry would reuse the dead connection and never dial. After the
// fix the timed-out conn is closed and the retry dials the new backend.
func TestTimeoutRetryDoesNotReuseHTTP2Conn(t *testing.T) {
	var oldBlackhole atomic.Bool
	oldBackend := newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oldBlackhole.Load() {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	newBackend := newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))

	var useNew atomic.Bool
	var dials atomic.Int64
	dialer := &net.Dialer{Timeout: time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local httptest only
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		addr := oldBackend.Listener.Addr().String()
		if useNew.Load() {
			addr = newBackend.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, "tcp", addr)
	}

	u, err := url.Parse("https://cluster.test")
	require.NoError(t, err)
	tp, err := New(Config{
		URLs:                 []*url.URL{u},
		Transport:            transport,
		MaxRetries:           2,
		EnableRetryOnTimeout: true,
		RequestTimeout:       150 * time.Millisecond,
		HealthCheck:          NoOpHealthCheck,
		NodeStatsInterval:    -1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	warmup, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	res, err := tp.Stream(warmup)
	require.NoError(t, err)
	require.Equal(t, 2, res.ProtoMajor, "test requires HTTP/2; connection reuse after timeout is an HTTP/2-only bug")
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	require.Equal(t, int64(1), dials.Load(), "warmup should establish one HTTP/2 connection")

	oldBlackhole.Store(true)
	useNew.Store(true)

	var reused atomic.Int64
	cutover, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	cutover = cutover.WithContext(httptrace.WithClientTrace(cutover.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reused.Add(1)
			}
		},
	}))
	ctx, cancel := context.WithTimeout(cutover.Context(), time.Second)
	defer cancel()
	cutover = cutover.WithContext(ctx)

	res, err = tp.Stream(cutover)
	require.NoError(t, err, "timeout retry must dial the replacement backend instead of reusing the stalled HTTP/2 connection")
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	require.Greater(t, dials.Load(), int64(1), "retry after timeout must DialContext again, dials=%d reused=%d", dials.Load(), reused.Load())
	require.GreaterOrEqual(t, reused.Load(), int64(1), "first cutover attempt should reuse the warmed HTTP/2 connection, dials=%d reused=%d", dials.Load(), reused.Load())
}

func newHTTP2Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
