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

func TestCloseTimedOutConnNil(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { closeTimedOutConn(nil) })
}

func TestWithAttemptConnTracePreservesGotConn(t *testing.T) {
	t.Parallel()
	var seen atomic.Bool
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { seen.Store(true) },
	})
	var slot net.Conn
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
	require.NotNil(t, slot, "trace must record the selected net.Conn")
}

// h2Cutover is the shared fixture for the #1121 tests: two HTTP/2 backends
// behind one logical hostname, with a dial counter so a test can prove whether
// DialContext ran again. It models an endpoint cutover -- stall the old backend,
// point future dials at the new one, and observe whether the client follows.
type h2Cutover struct {
	t          *testing.T
	oldBackend *httptest.Server
	newBackend *httptest.Server
	transport  *http.Transport

	stallOld atomic.Bool // old backend accepts the stream and never answers
	useNew   atomic.Bool // future dials resolve to the new backend
	dials    atomic.Int64
}

func newH2Cutover(t *testing.T) *h2Cutover {
	t.Helper()
	h := &h2Cutover{t: t}
	h.oldBackend = newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.stallOld.Load() {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	h.newBackend = newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))

	dialer := &net.Dialer{Timeout: time.Second}
	h.transport = http.DefaultTransport.(*http.Transport).Clone()
	h.transport.ForceAttemptHTTP2 = true
	h.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local httptest only
	h.transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		h.dials.Add(1)
		addr := h.oldBackend.Listener.Addr().String()
		if h.useNew.Load() {
			addr = h.newBackend.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, "tcp", addr)
	}
	return h
}

// client builds a Transport against the fixture. Only the retry/timeout fields
// of cfg matter; URLs, Transport, and the background pollers are filled in.
func (h *h2Cutover) client(cfg Config) *Transport {
	h.t.Helper()
	u, err := url.Parse("https://cluster.test")
	require.NoError(h.t, err)
	cfg.URLs = []*url.URL{u}
	cfg.Transport = h.transport
	cfg.HealthCheck = NoOpHealthCheck
	cfg.NodeStatsInterval = -1
	tp, err := New(cfg)
	require.NoError(h.t, err)
	h.t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// get issues a request, drains and closes the body on success, and reports
// whether any attempt rode a pooled connection. It asserts HTTP/2 on every
// successful response so no test can pass on HTTP/1.1 by accident -- connection
// reuse after a timeout is an HTTP/2-only bug.
func (h *h2Cutover) get(tp *Transport, ctx context.Context) (bool, error) {
	h.t.Helper()

	var sawReuse atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				sawReuse.Store(true)
			}
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(h.t, err)

	res, err := tp.Stream(req)
	if err != nil {
		return sawReuse.Load(), err
	}
	require.Equal(h.t, 2, res.ProtoMajor,
		"test requires HTTP/2; connection reuse after timeout is an HTTP/2-only bug")
	_, _ = io.Copy(io.Discard, res.Body)
	require.NoError(h.t, res.Body.Close())
	return sawReuse.Load(), nil
}

// warmup establishes the pooled connection the tests then try to displace.
func (h *h2Cutover) warmup(tp *Transport) {
	h.t.Helper()
	_, err := h.get(tp, h.t.Context())
	require.NoError(h.t, err)
	require.Equal(h.t, int64(1), h.dials.Load(), "warmup should establish one HTTP/2 connection")
}

// TestTimeoutRetryDoesNotReuseHTTP2Conn is the #1121 reproducer: a warmed
// HTTP/2 connection to an old backend that then stops responding, while
// DialContext already points at a healthy replacement. Canceling the
// per-attempt context RSTs the stream but leaves the ClientConn pooled, so
// every retry would reuse the dead connection and never dial. After the
// fix the timed-out conn is closed and the retry dials the new backend.
func TestTimeoutRetryDoesNotReuseHTTP2Conn(t *testing.T) {
	t.Parallel()
	h := newH2Cutover(t)
	tp := h.client(Config{
		MaxRetries:           2,
		EnableRetryOnTimeout: true,
		RequestTimeout:       500 * time.Millisecond,
	})
	h.warmup(tp)

	h.stallOld.Store(true)
	h.useNew.Store(true)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	reused, err := h.get(tp, ctx)
	require.NoError(t, err,
		"timeout retry must dial the replacement backend instead of reusing the stalled HTTP/2 connection")
	require.Greater(t, h.dials.Load(), int64(1),
		"retry after timeout must DialContext again, dials=%d", h.dials.Load())
	require.True(t, reused, "first cutover attempt should reuse the warmed HTTP/2 connection")
}

// TestCallerCancellationDoesNotCloseConn pins the gate on the close: a caller's
// own expiring deadline reports Timeout() == true just like our RequestTimeout
// does, but it implicates the caller, not the connection. Closing a healthy
// pooled connection because the caller gave up is what golang/go#60818 was
// reverted for, so the connection must survive and be reused.
func TestCallerCancellationDoesNotCloseConn(t *testing.T) {
	t.Parallel()
	h := newH2Cutover(t)
	// No RequestTimeout, so the caller's deadline is the only timeout in play
	// and any close would have to stem from caller cancellation.
	// EnableRetryOnTimeout still installs the connection trace.
	tp := h.client(Config{
		MaxRetries:           0,
		EnableRetryOnTimeout: true,
	})
	h.warmup(tp)

	h.stallOld.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	_, err := h.get(tp, ctx)
	cancel()
	require.Error(t, err, "caller deadline must surface as an error")
	h.stallOld.Store(false)

	reused, err := h.get(tp, t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1), h.dials.Load(),
		"caller cancellation must not retire a healthy connection, dials=%d", h.dials.Load())
	require.True(t, reused, "connection should have been reused after caller cancellation")
}

// TestTimeoutClosesConnWithoutRetry pins that the close is deliberately not
// gated on a retry following it. With EnableRetryOnTimeout false the timed-out
// request fails, but the connection is still retired so the next request dials
// rather than inheriting the stalled backend.
func TestTimeoutClosesConnWithoutRetry(t *testing.T) {
	t.Parallel()
	h := newH2Cutover(t)
	tp := h.client(Config{
		MaxRetries:     0,
		RequestTimeout: 500 * time.Millisecond,
	})
	h.warmup(tp)

	h.stallOld.Store(true)
	h.useNew.Store(true)

	_, err := h.get(tp, t.Context())
	require.Error(t, err, "timeout without retry must still fail")

	_, err = h.get(tp, t.Context())
	require.NoError(t, err, "next request must dial the replacement backend")
	require.Greater(t, h.dials.Load(), int64(1),
		"a timeout must retire the connection even when no retry follows, dials=%d", h.dials.Load())
}

// TestSeedFallbackTimeoutClosesConn drives the #1121 close through the seed-
// fallback path: with the router exhausted (emptyRouter), stream() serves the
// request from the seed pool, and a per-attempt timeout there must retire the
// stalled HTTP/2 connection just as the main retry loop does -- so the next
// fallback dials the replacement backend instead of reusing the black-holed
// connection.
func TestSeedFallbackTimeoutClosesConn(t *testing.T) {
	t.Parallel()
	h := newH2Cutover(t)
	tp := h.client(Config{
		Router:         &emptyRouter{},
		MaxRetries:     0,
		RequestTimeout: 500 * time.Millisecond,
	})
	h.warmup(tp) // seed fallback establishes one HTTP/2 connection

	h.stallOld.Store(true)
	h.useNew.Store(true)

	_, err := h.get(tp, t.Context())
	require.Error(t, err, "seed fallback timeout must fail")

	_, err = h.get(tp, t.Context())
	require.NoError(t, err, "next seed fallback must dial the replacement backend")
	require.Greater(t, h.dials.Load(), int64(1),
		"a seed-fallback timeout must retire the connection, dials=%d", h.dials.Load())
}

func newHTTP2Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
