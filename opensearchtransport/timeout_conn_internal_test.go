// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Fail loudly on a should-never-happen invariant violation during tests; a
// shipped binary logs and self-heals instead. Mirrors internal/ttlcache.
func init() { panicOnInvariantViolation = true }

// h2Shared is one HTTP/2 backend plus a dial counter, shared by the tests that
// need several requests multiplexed onto a single connection. Handlers are
// driven by channels rather than clocks: /park sends its headers at once and
// then holds the response body open until released, /stall never answers at all,
// anything else answers immediately.
type h2Shared struct {
	t         *testing.T
	srv       *httptest.Server
	transport *http.Transport

	onWire  chan struct{}
	release chan struct{}
	dials   atomic.Int64
}

func newH2Shared(t *testing.T) *h2Shared {
	t.Helper()
	h := &h2Shared{
		t: t,
		// Buffered so a handler reporting that it is on the wire never blocks on
		// a test that has not read the signal yet; no test parks more requests
		// than this.
		onWire:  make(chan struct{}, 8),
		release: make(chan struct{}),
	}
	h.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/park":
			// Headers first so the client's RoundTrip completes and the request
			// moves into the body-reading phase, which is where the connection
			// claim has to outlive RoundTrip.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			h.onWire <- struct{}{}
			select {
			case <-h.release:
				_, _ = io.WriteString(w, `{"parked":"ok"}`)
			case <-r.Context().Done():
			}
		case "/stall":
			h.onWire <- struct{}{}
			<-r.Context().Done()
		default:
			_, _ = io.WriteString(w, "{}")
		}
	}))
	h.srv.EnableHTTP2 = true
	h.srv.StartTLS()
	t.Cleanup(h.srv.Close)

	dialer := &net.Dialer{Timeout: time.Second}
	h.transport = newH2Transport(func(ctx context.Context, _, _ string) (net.Conn, error) {
		h.dials.Add(1)
		return dialer.DialContext(ctx, "tcp", h.srv.Listener.Addr().String())
	})
	return h
}

// client builds a Transport against the shared backend. Only the retry and
// timeout fields of cfg matter; the rest are filled in.
func (h *h2Shared) client(cfg Config) *Transport {
	return newH2Client(h.t, h.transport, cfg)
}

// get issues a request whose context cannot be cancelled by the test finishing,
// so a caller deadline never confounds what the transport itself decides.
func (h *h2Shared) get(tp *Transport, path string) (*http.Response, error) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.WithoutCancel(h.t.Context()), http.MethodGet, path, nil)
	require.NoError(h.t, err)
	return tp.Stream(req)
}

// getOK issues a request and requires it to succeed over HTTP/2, draining the
// body so the connection is returned to the pool.
func (h *h2Shared) getOK(tp *Transport, path string) {
	h.t.Helper()
	res, err := h.get(tp, path)
	require.NoError(h.t, err)
	require.Equal(h.t, 2, res.ProtoMajor, "these tests require HTTP/2 multiplexing")
	_, _ = io.Copy(io.Discard, res.Body)
	require.NoError(h.t, res.Body.Close())
}

// warmup establishes the single pooled connection the tests then share.
func (h *h2Shared) warmup(tp *Transport) {
	h.t.Helper()
	h.getOK(tp, "/warm")
	require.Equal(h.t, int64(1), h.dials.Load(), "warmup should establish exactly one connection")
}

// TestTimeoutDoesNotRetireConnInUseByAnotherRequest pins the sibling guard.
// HTTP/2 multiplexes, so closing the net.Conn a timeout stalled on fails every
// other stream on it. A request that is progressing normally must keep its
// connection even when a concurrent request times out.
//
// One Transport serves both requests, and the two deadlines are deliberately
// different: RequestTimeout is generous, while the stalling request is failed by
// the http.Transport's own ResponseHeaderTimeout. Without that split both
// requests would expire at the same instant and the sibling's fate would say
// nothing. The sibling has its headers already, so it is in the body-reading
// phase, which is exactly where a claim has to outlive RoundTrip.
func TestTimeoutDoesNotRetireConnInUseByAnotherRequest(t *testing.T) {
	t.Parallel()
	h := newH2Shared(t)
	h.transport.ResponseHeaderTimeout = 500 * time.Millisecond
	tp := h.client(Config{MaxRetries: 0, RequestTimeout: 30 * time.Second})
	h.warmup(tp)

	type parked struct {
		body string
		err  error
	}
	// The goroutine only reports; every assertion stays on the test goroutine.
	ctx := context.WithoutCancel(t.Context())
	sibling := make(chan parked, 1)
	reading := make(chan struct{})
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/park", nil)
		if err != nil {
			close(reading)
			sibling <- parked{err: err}
			return
		}
		res, err := tp.Stream(req)
		if err != nil {
			close(reading)
			sibling <- parked{err: err}
			return
		}
		close(reading) // headers are in: this request is now reading its body
		buf, readErr := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		if readErr != nil {
			sibling <- parked{err: readErr}
			return
		}
		sibling <- parked{body: string(buf), err: closeErr}
	}()
	<-h.onWire // the handler has flushed the sibling's headers
	<-reading  // and the sibling holds a stream while reading its body

	// Fails via ResponseHeaderTimeout, which reports net.Error.Timeout, so the
	// transport treats it as a timeout it generated.
	_, err := h.get(tp, "/stall") //nolint:bodyclose // error path: res is nil
	require.Error(t, err, "the stalled request must time out")

	h.getOK(tp, "/probe")
	require.Equal(t, int64(1), h.dials.Load(),
		"a timeout must not retire a connection another request is still using, dials=%d", h.dials.Load())

	close(h.release)
	got := <-sibling
	require.NoError(t, got.err,
		"a request progressing normally must survive a concurrent request's timeout")
	require.JSONEq(t, `{"parked":"ok"}`, got.body, "the sibling's body must arrive intact")
}

// TestTimeoutRetiresBlackHoledConn is the companion to the guard: a connection
// every request on it has timed out on must still be retired, so the fix for
// #1121 keeps working. The rows differ only in how the stalled requests arrive.
//
// These rows cover retirement end to end. They do not pin the drain semantics:
// whether overlapping traffic ever leaves the connection momentarily unused is a
// matter of wall-clock timing, so a retire-only-when-unused implementation can
// still pass here. TestAttemptConnsDrainsBeforeRetiring pins that property
// deterministically instead.
func TestTimeoutRetiresBlackHoledConn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// stalled is how many requests pile onto the dead connection.
		stalled int
		// staggered starts each request only once the previous one is on the
		// wire, so there is never a moment with nothing in flight.
		staggered bool
		// retries exercises the re-acquire that keeps a naive count above zero.
		retries int
	}{
		{name: "sole user", stalled: 1},
		{name: "concurrent arrivals", stalled: 2},
		{name: "staggered arrivals with retries", stalled: 4, staggered: true, retries: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newH2Shared(t)
			tp := h.client(Config{
				MaxRetries:           tt.retries,
				EnableRetryOnTimeout: tt.retries > 0,
				RequestTimeout:       500 * time.Millisecond,
			})
			h.warmup(tp)

			errs := make(chan error, tt.stalled)
			for range tt.stalled {
				go func() {
					_, err := h.get(tp, "/stall") //nolint:bodyclose // error path: res is nil
					errs <- err
				}()
				if tt.staggered {
					<-h.onWire // this one is on the wire before the next starts
				}
			}
			if !tt.staggered {
				for range tt.stalled {
					<-h.onWire // all streams open on the one connection
				}
			}
			for range tt.stalled {
				require.Error(t, <-errs, "each stalled request must time out")
			}

			h.getOK(tp, "/ok")
			require.Greater(t, h.dials.Load(), int64(1),
				"a black-holed connection must be retired, dials=%d", h.dials.Load())
		})
	}
}

// mapped reports whether conn still has a claim later arrivals would join.
func mapped(track *attemptConns, conn net.Conn) bool {
	_, ok := track.conns.Load(conn)
	return ok
}

// closeSpy is a net.Conn that records whether it was closed, so the claim tests
// can assert on the connection's fate rather than on attemptConns' bookkeeping.
type closeSpy struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeSpy) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *closeSpy) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9200} }

// TestAttemptConnsHoldsClaimThroughBody covers the reason the claim outlives
// RoundTrip: RoundTrip returns when the headers arrive, but the stream is still
// on the connection until the body is closed, so a timeout in that window must
// let the body finish before retiring the connection.
func TestAttemptConnsHoldsClaimThroughBody(t *testing.T) {
	t.Parallel()

	var track attemptConns
	conn := &closeSpy{}

	reader := track.acquire(conn)
	body := track.holdThroughBody(t.Context(), reader, io.NopCloser(strings.NewReader("payload")))

	// Another attempt times out on the same connection. The body still holds the
	// generation being drained, so the connection is marked, not closed.
	require.False(t, track.retire(track.acquire(conn)),
		"a connection still delivering a body must not be closed")
	require.False(t, conn.closed.Load(), "the connection must stay open while the body streams")

	// A request arriving now must not join the draining generation, or it could
	// postpone the close for as long as traffic keeps coming.
	late := track.acquire(conn)
	require.NotSame(t, reader.claim, late.claim, "later arrivals must join a fresh generation")
	track.release(late)

	buf, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "payload", string(buf), "body must stream through unchanged")

	// Closing the body completes the drain, which retires the connection.
	require.NoError(t, body.Close())
	require.True(t, conn.closed.Load(), "completing the drain must close the connection")
}

// TestAttemptConnsClaimEndsWithContext pins the bound on the body-held claim: a
// caller that never closes the body must not hold a connection open forever, so
// the claim also ends when the attempt context does.
func TestAttemptConnsClaimEndsWithContext(t *testing.T) {
	t.Parallel()

	var track attemptConns
	conn := &closeSpy{}
	ctx, cancel := context.WithCancel(t.Context())

	// Deliberately abandoned without Close: the context is what must release it.
	_ = track.holdThroughBody(ctx, track.acquire(conn), io.NopCloser(strings.NewReader("payload")))

	cancel()
	require.Eventually(t, func() bool {
		return !mapped(&track, conn)
	}, time.Second, 10*time.Millisecond,
		"an abandoned body must still release its claim when the context ends")
	require.False(t, conn.closed.Load(),
		"releasing a healthy connection must not close it")
}

// TestAttemptConnsDrainsBeforeRetiring covers when a timeout may close the
// connection. The rows differ only in who else is holding it at the time, which
// is the whole of the decision: a timeout waits for the attempts already there
// and ignores anything that arrives afterwards, so a connection carrying
// continuous traffic is still retired.
func TestAttemptConnsDrainsBeforeRetiring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// others is how many attempts hold the connection besides the one that
		// times out.
		others int
		// lateArrivals join after the timeout has marked the connection, so they
		// must neither postpone the close nor complete the drain themselves.
		lateArrivals int
	}{
		{name: "sole user retires immediately", others: 0},
		{name: "existing user drains first", others: 1},
		{name: "several existing users all drain first", others: 3},
		{name: "later arrivals do not extend the drain", others: 1, lateArrivals: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var track attemptConns
			conn := &closeSpy{}

			existing := make([]attemptClaim, tt.others)
			for i := range existing {
				existing[i] = track.acquire(conn)
			}

			closedByTimeout := track.retire(track.acquire(conn))
			require.Equal(t, tt.others == 0, closedByTimeout,
				"a timeout closes the connection only when nothing else holds it")
			require.Equal(t, tt.others == 0, conn.closed.Load(), "and the connection follows that decision")

			for i := range tt.lateArrivals {
				late := track.acquire(conn)
				require.NotSame(t, existing[0].claim, late.claim, "a late arrival joins the next generation")
				require.False(t, track.drop(late, false),
					"late arrival %d must not complete the drain", i)
			}

			// The connection is retired by whichever of the attempts present at
			// the mark finishes last, however much arrives in the meantime.
			for i, held := range existing {
				last := i == len(existing)-1
				require.Equal(t, last, track.drop(held, false),
					"only the last of the drain generation retires the connection")
			}
			require.True(t, conn.closed.Load(), "the drain must end with the connection closed")
			require.False(t, mapped(&track, conn), "a retired connection must not be retained")
		})
	}
}

// TestAttemptConnsGenerations covers the cases that do not share the drain shape:
// an empty claim, and reuse of a connection whose previous generation is gone.
func TestAttemptConnsEmptyClaimIsInert(t *testing.T) {
	t.Parallel()
	var track attemptConns
	require.False(t, track.drop(attemptClaim{}, false), "an empty claim is never closed")
	require.False(t, track.retire(attemptClaim{}), "an empty claim is never retired")
}

func TestAttemptConnsFinalizedGenerationNotReused(t *testing.T) {
	t.Parallel()
	var track attemptConns
	conn := &closeSpy{}

	first := track.acquire(conn)
	require.False(t, track.drop(first, false), "a healthy connection is not closed on release")
	require.False(t, conn.closed.Load())

	// The connection is live again later. Joining the finalized generation
	// would split the accounting, so a fresh one must be handed out.
	second := track.acquire(conn)
	require.NotSame(t, first.claim, second.claim, "a finalized generation must not be joined")
	require.True(t, track.retire(second), "the fresh generation retires on its own timeout")
	require.True(t, conn.closed.Load())
}

func TestAttemptConnsCountedIndependently(t *testing.T) {
	t.Parallel()
	var track attemptConns
	first := &closeSpy{}
	second := &closeSpy{}
	heldSecond := track.acquire(second)

	require.True(t, track.retire(track.acquire(first)), "first has no other user")
	require.True(t, first.closed.Load())
	require.False(t, second.closed.Load(), "retiring one connection must not close another")
	require.True(t, mapped(&track, second), "nor drop its claim")

	require.False(t, track.drop(heldSecond, false), "a healthy connection is not closed on release")
	require.False(t, mapped(&track, second), "an unused connection must not be retained")
}
