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
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTimeoutDoesNotFailConcurrentRequest is the case that motivated draining
// rather than closing. HTTP/2 multiplexes, so closing the socket a timed-out
// attempt was on fails every other stream on it. Request.Close instead sets
// doNotReuse, which stops the connection being offered to new requests and lets
// the streams already on it finish, so a healthy concurrent request completes.
//
// Two clients share one *http.Transport, and so one pooled HTTP/2 connection:
// an impatient one whose request stalls and times out, and a patient one whose
// request is merely slow. The patient request must survive.
func TestTimeoutDoesNotFailConcurrentRequest(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var slowStarted sync.WaitGroup
	slowStarted.Add(1)

	backend := newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("kind") {
		case "slow":
			// Healthy but unhurried: answers once the test says so, which is
			// after a concurrent request has timed out on this connection.
			slowStarted.Done()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, `{"slow":true}`)
		case "stall":
			<-r.Context().Done()
		default:
			_, _ = io.WriteString(w, "{}")
		}
	}))

	var dials atomic.Int64
	dialer := &net.Dialer{Timeout: time.Second}
	transport := newH2Transport(func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		return dialer.DialContext(ctx, "tcp", backend.Listener.Addr().String())
	})

	patient := newH2Client(t, transport, Config{MaxRetries: 0, RequestTimeout: 10 * time.Second})
	impatient := newH2Client(t, transport, Config{MaxRetries: 0, RequestTimeout: 500 * time.Millisecond})

	// Warm one connection; both clients multiplex onto it.
	warm, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	require.NoError(t, err)
	res, err := patient.Stream(warm)
	require.NoError(t, err)
	require.Equal(t, 2, res.ProtoMajor, "test requires HTTP/2")
	_, _ = io.Copy(io.Discard, res.Body)
	require.NoError(t, res.Body.Close())
	require.Equal(t, int64(1), dials.Load())

	type result struct {
		body string
		err  error
	}
	slowDone := make(chan result, 1)
	go func() {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "/?kind=slow", nil)
		if reqErr != nil {
			slowDone <- result{err: reqErr}
			return
		}
		sres, sErr := patient.Stream(req)
		if sErr != nil {
			slowDone <- result{err: sErr}
			return
		}
		buf, readErr := io.ReadAll(sres.Body)
		_ = sres.Body.Close()
		slowDone <- result{body: string(buf), err: readErr}
	}()
	slowStarted.Wait()

	// A request on the same connection times out.
	stall, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/?kind=stall", nil)
	require.NoError(t, err)
	//nolint:bodyclose // times out; there is no body to close
	_, err = impatient.Stream(stall)
	require.Error(t, err, "the stalled request must time out")

	// The timeout must not have taken the slow request's stream with it.
	close(release)
	got := <-slowDone
	require.NoError(t, got.err,
		"a concurrent request on the same HTTP/2 connection must survive another request's timeout")
	require.Equal(t, `{"slow":true}`, got.body)
	require.Equal(t, int64(1), dials.Load(),
		"the timeout must not have forced a redial while the connection was still in use")
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
	h.transport = newH2Transport(func(ctx context.Context, _, _ string) (net.Conn, error) {
		h.dials.Add(1)
		addr := h.oldBackend.Listener.Addr().String()
		if h.useNew.Load() {
			addr = h.newBackend.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, "tcp", addr)
	})
	return h
}

// client builds a Transport against the fixture. Only the retry/timeout fields
// of cfg matter; URLs, Transport, and the background pollers are filled in.
func (h *h2Cutover) client(cfg Config) *Transport {
	return newH2Client(h.t, h.transport, cfg)
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

// TestTimeoutDrainsConnWithoutRetry pins that the drain is deliberately not
// gated on a retry following it: with EnableRetryOnTimeout false the timed-out
// request fails, but the node is still marked so the connection is retired.
//
// It also pins the cost of using Request.Close. doNotReuse is set after the
// stream is assigned, so the request carrying the flag still rides the stale
// connection and fails too; recovery lands on the request after it. Closing the
// socket outright recovered one request sooner, at the price of failing every
// concurrent stream on that connection -- see
// TestTimeoutDoesNotFailConcurrentRequest.
func TestTimeoutDrainsConnWithoutRetry(t *testing.T) {
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
	require.Equal(t, int64(1), h.dials.Load(),
		"the timing-out request itself must not redial")

	// Carries Request.Close, but is assigned the stale connection before
	// doNotReuse takes effect, so it fails as well.
	_, err = h.get(tp, t.Context())
	require.Error(t, err, "the request carrying the drain still rides the stale connection")

	// Now the connection has been retired and the next request dials.
	_, err = h.get(tp, t.Context())
	require.NoError(t, err, "once drained, the next request must dial the replacement backend")
	require.Greater(t, h.dials.Load(), int64(1),
		"a timeout must retire the connection even when no retry follows, dials=%d", h.dials.Load())
}

// TestSeedFallbackTimeoutDrainsConn drives the #1121 drain through the seed-
// fallback path: with the router exhausted (emptyRouter), stream() serves the
// request from the seed pool, and a per-attempt timeout there must retire the
// stalled HTTP/2 connection just as the main retry loop does. As on the no-retry
// path, the request carrying Request.Close still rides the stale connection, so
// recovery lands on the one after it.
func TestSeedFallbackTimeoutDrainsConn(t *testing.T) {
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
	require.Error(t, err, "the fallback carrying the drain still rides the stale connection")

	_, err = h.get(tp, t.Context())
	require.NoError(t, err, "once drained, the next seed fallback must dial the replacement backend")
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

// newH2Transport clones the default transport for HTTP/2-over-TLS against a
// local httptest backend, routing every dial through dial so a fixture can count
// dials and choose the target.
func newH2Transport(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local httptest only
	tr.DialContext = dial
	return tr
}

// newH2Client builds a Transport with the retry/timeout fields from cfg and the
// rest filled in for the single logical host backed by transport.
func newH2Client(t *testing.T, transport *http.Transport, cfg Config) *Transport {
	t.Helper()
	u, err := url.Parse("https://cluster.test")
	require.NoError(t, err)
	cfg.URLs = []*url.URL{u}
	cfg.Transport = transport
	cfg.HealthCheck = NoOpHealthCheck
	cfg.NodeStatsInterval = -1
	tp, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// TestDrainMarkSurvivesPreparationFailure pins that the drain mark is spent only
// by a request that actually reaches the wire. Picking it up consumes it, so a
// request that fails while being prepared -- signing being the live case, since
// a signer can fail to refresh credentials -- must leave the mark for the next
// request. Otherwise one unlucky failure swallows the drain and the stale
// connection is never retired, which is the whole point of the mark.
func TestDrainMarkSurvivesPreparationFailure(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// router sends the request down the intended path: nil keeps the normal
		// pool, and an exhausted one cascades to seed fallback.
		router Router
		// markConn returns the connection whose mark the request should spend.
		markConn func(t *testing.T, tp *Transport) *Connection
		wantErr  string
	}{
		{
			name: "stream path",
			markConn: func(t *testing.T, tp *Transport) *Connection {
				t.Helper()
				tp.mu.Lock()
				pool := tp.mu.connectionPool
				tp.mu.Unlock()
				conn, err := pool.Next()
				require.NoError(t, err)
				return conn
			},
			wantErr: "failed to sign request",
		},
		{
			name:   "seed fallback path",
			router: &emptyRouter{},
			markConn: func(t *testing.T, tp *Transport) *Connection {
				t.Helper()
				require.NotNil(t, tp.seedFallbackPool, "seed fallback pool must exist")
				conn, err := tp.seedFallbackPool.Next()
				require.NoError(t, err)
				return conn
			},
			wantErr: "failed to sign seed fallback request",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend := newHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "{}")
			}))
			dialer := &net.Dialer{Timeout: time.Second}
			transport := newH2Transport(func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp", backend.Listener.Addr().String())
			})
			tp := newH2Client(t, transport, Config{
				MaxRetries: 0,
				Router:     tt.router,
				Signer:     failingSigner(errors.New("no credentials")),
			})

			conn := tt.markConn(t, tp)
			// A previous timeout on this node asked for a drain.
			conn.drainingConn.Store(1)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			require.NoError(t, err)
			//nolint:bodyclose // signing fails before the wire; there is no body
			_, err = tp.Stream(req)
			require.ErrorContains(t, err, tt.wantErr, "signing must fail before the wire")

			require.Equal(t, int64(1), conn.drainingConn.Load(),
				"a request that never reached the wire must leave the drain mark for the next one")
		})
	}
}
