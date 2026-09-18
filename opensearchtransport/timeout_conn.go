// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
)

// panicOnInvariantViolation makes should-never-happen conditions fatal instead
// of self-healing. It is false in a shipped binary and set true by an init in
// the package's internal tests, so `go test` fails loudly on a defect while a
// production process logs, reconciles, and stays up. Mirrors the same switch in
// internal/ttlcache.
//
//nolint:gochecknoglobals // package-wide test switch, toggled only by an init in the internal tests
var panicOnInvariantViolation bool

// attemptConns tracks, per underlying net.Conn, which of this client's
// attempts are using it and whether a timeout has marked it for retirement.
//
// Close is not stream-scoped. HTTP/2 multiplexes many requests onto one
// net.Conn, so closing it fails every stream on it, not just the one that timed
// out. A timeout therefore does not close a connection other attempts are on.
// It marks the connection retiring and lets the attempts already there drain;
// the last of them closes it.
//
// Only the attempts holding the claim when the mark was set get to drain. Once
// marked, a claim refuses new joins, so the next arrival evicts it from the map
// and starts a fresh generation that cannot postpone the close. Waiting for the
// connection to fall idle would be an idle check, which #1121 rules out for
// exactly this reason: a hot HTTP/2 connection never becomes idle while requests
// keep arriving, so the stale connection would be kept forever.
//
// An attempt that arrives after the mark can still lose its stream when the
// drain completes. That connection has already been found unusable, so such an
// attempt was going to fail on it anyway.
//
// Keyed by net.Conn because that is the granularity Close acts on. Neither the
// logical Connection nor its per-pool inFlight counters work here: one
// Connection can be served by several net.Conns, and those counters are only
// maintained on the router path and are dropped before the response body is
// read.
//
// The bookkeeping is per-Client. Two Clients sharing one *http.Transport
// share its connection pool but not these claims, so neither sees the other's
// attempts and a timeout in one can still retire a connection the other is
// using. Retiring across that boundary would need state on the shared
// *http.Transport, which this package does not own.
//
// The zero value is ready to use. There is no mutex: the map is a sync.Map keyed
// by connection, and each generation's state is a counter and a flag, both
// atomic. An attempt joining an established generation pays a map load and a
// compare-and-swap; only a first arrival or a retiring generation costs more.
type attemptConns struct {
	// conns maps net.Conn to the *connClaim that later arrivals should join.
	conns sync.Map
}

// claimDead is the users value of a claim that has been finalized. It is a
// terminal state: the claim is on its way out of the map and must not be joined,
// so [connClaim.tryJoin] refuses it and the caller starts a fresh generation.
const claimDead = -1

// connClaim is the state shared by the attempts of one generation on one
// connection.
type connClaim struct {
	// users counts the attempts currently holding this claim, or claimDead once
	// the claim has been finalized.
	users atomic.Int64
	// retiring records that a timeout marked the connection for retirement.
	// Setting it closes the generation to new joins, so the attempts already
	// holding it are exactly the ones whose completion decides the close.
	retiring atomic.Bool
}

// tryJoin adds one user to the claim, reporting false when the caller must start
// from a fresh generation instead: either this one has been finalized, or it is
// retiring and admitting the caller would postpone the close.
//
// The retiring check is a filter, not a barrier. A caller that reads the flag
// just before a concurrent [attemptConns.retire] sets it still joins, and then
// counts as one of the attempts the close waits for. Excluding it would need
// users and retiring to move under one atomic word; the window is a few
// instructions wide and costs at most one attempt's worth of delay, so the
// close still lands within a generation.
func (c *connClaim) tryJoin() bool {
	for {
		if c.retiring.Load() {
			return false
		}
		users := c.users.Load()
		if users == claimDead {
			return false
		}
		if c.users.CompareAndSwap(users, users+1) {
			return true
		}
	}
}

// acquire records that an attempt is using conn and returns the claim it must
// hand back to [attemptConns.release] or [attemptConns.retire]. An attempt that
// arrives while conn is already retiring joins the next generation and so does
// not extend the drain; see [attemptConns].
func (a *attemptConns) acquire(conn net.Conn) attemptClaim {
	if conn == nil {
		return attemptClaim{}
	}
	for {
		claim, ok := a.load(conn)
		if claim.tryJoin() {
			return attemptClaim{conn: conn, claim: claim}
		}
		// The generation is retiring or already finalized. Evict it so the next
		// load creates a fresh one; its own holders keep it alive by pointer
		// until they drain.
		if ok {
			a.conns.CompareAndDelete(conn, claim)
		}
	}
}

// load returns the claim currently mapped for conn, creating one if absent. The
// bool reports whether the claim came from the map rather than being created by
// this call, which is what makes it safe to CompareAndDelete on a failed join.
func (a *attemptConns) load(conn net.Conn) (*connClaim, bool) {
	if existing, ok := a.conns.Load(conn); ok {
		return existing.(*connClaim), true
	}
	actual, loaded := a.conns.LoadOrStore(conn, &connClaim{})
	return actual.(*connClaim), loaded
}

// release drops one attempt's use of conn, closing it if that completed the
// drain of a connection already marked for retirement. Callers on the release
// path have nothing to do either way; [attemptConns.drop] reports whether the
// connection was closed.
func (a *attemptConns) release(held attemptClaim) {
	a.drop(held, false)
}

// retire drops one attempt's use of conn and marks it for retirement, because
// this attempt timed out on it. The connection closes once the attempts already
// on it have drained, which for a sole user is immediately. Reports whether conn
// was closed.
func (a *attemptConns) retire(held attemptClaim) bool {
	return a.drop(held, true)
}

// drop removes one claim on conn, marking it for retirement when markRetiring is
// set, and closes it when the drain is complete. Reports whether conn was
// closed.
//
// The close runs once the generation is finalized, so nothing can join it
// mid-close, and never under a lock: closing a *tls.Conn writes close_notify,
// which can block on the very stalled socket being retired.
func (a *attemptConns) drop(held attemptClaim, markRetiring bool) bool {
	if held.conn == nil || held.claim == nil {
		return false
	}

	if markRetiring {
		// Close the generation to new joins before dropping out of it, so the
		// attempts already here are exactly the ones the close waits for.
		held.claim.retiring.Store(true)
	}

	switch remaining := held.claim.users.Add(-1); {
	case remaining > 0:
		return false
	case remaining < 0:
		// Should never happen: acquire hands out one claim per successful
		// tryJoin and every path drops it exactly once. Self-heal by leaving the
		// connection alone; the accounting is already wrong, and closing on the
		// strength of it could take down live streams.
		if panicOnInvariantViolation {
			panic(fmt.Sprintf("attemptConns: connection claim released more than once (users=%d)", remaining))
		}
		if dl := loadDebugLogger(); dl != nil {
			dl.Logf("attemptConns: connection claim released more than once (users=%d), not retiring the connection\n", remaining)
		}
		return false
	}

	// Last of this generation out. Finalizing the claim is a single winner-takes-
	// all transition, so the caller that lands it is the only one that may close
	// the connection.
	if !held.claim.users.CompareAndSwap(0, claimDead) {
		// A joiner took the count back up between the decrement and here, so it
		// finishes the generation instead. Reachable while retiring too: tryJoin
		// admits a caller that read the flag just before it was set, and that
		// caller closes the connection on its own drop. See [connClaim.tryJoin].
		return false
	}
	// Drop the generation from the map if it is still there, so the map does not
	// grow one entry per connection the client ever dials. A retiring generation
	// an arrival already evicted is simply gone.
	a.conns.CompareAndDelete(held.conn, held.claim)

	if !held.claim.retiring.Load() {
		return false
	}
	if dl := loadDebugLogger(); dl != nil {
		dl.Logf("Closed retired HTTP connection: %q\n", held.conn.RemoteAddr())
	}
	closeRetiredConn(held.conn)
	return true
}

// attemptClaim identifies one attempt's hold on the connection it ran on. The
// zero value holds nothing, which is the case when connection tracing is off.
type attemptClaim struct {
	conn  net.Conn
	claim *connClaim
}

// holdThroughBody keeps this attempt's claim on conn alive while the caller
// reads the response body. RoundTrip returns once the headers arrive, but the
// stream stays on the connection until the body is closed, so releasing the
// claim early would let another attempt's timeout retire a connection that is
// still delivering bytes.
//
// The claim is dropped on whichever comes first, the body being closed or ctx
// ending. When a per-attempt timeout is configured ctx carries its deadline, so
// the claim cannot outlive it even if the caller abandons the body. Without one,
// closing the body is the only release, as the io.ReadCloser contract requires:
// a caller that abandons a body on a context that never ends keeps this entry,
// and the stream behind it, for the life of the client.
func (a *attemptConns) holdThroughBody(ctx context.Context, held attemptClaim, body io.ReadCloser) io.ReadCloser {
	if held.conn == nil {
		return body
	}
	release := sync.OnceFunc(func() { a.release(held) })
	stopOnCtx := context.AfterFunc(ctx, release)
	return &releaseOnCloseBody{ReadCloser: body, release: func() {
		stopOnCtx()
		release()
	}}
}

// claimRelease drops an attemptConns claim exactly once.
type claimRelease func()

// releaseOnCloseBody drops an attemptConns claim when the response body is
// closed. Read is inherited so the body streams unbuffered.
type releaseOnCloseBody struct {
	io.ReadCloser
	release claimRelease
}

// Close drops the connection claim, then closes the underlying body.
func (b *releaseOnCloseBody) Close() error {
	b.release()
	return b.ReadCloser.Close()
}

// attemptOutcome is what [attemptConns.settle] needs in order to decide the fate
// of the connection an attempt ran on.
type attemptOutcome struct {
	// held is the attempt's claim on the connection it ran on; its conn is nil
	// when tracing is off.
	held attemptClaim
	// connURL identifies the connection in debug logs.
	connURL string
	// err and res are what RoundTrip returned.
	err error
	res *http.Response
	// callerCancelled records that the caller's own context had already ended,
	// which is how a caller giving up is told apart from a timeout this client
	// generated.
	callerCancelled bool
}

// settle settles an attempt's claim on the connection it ran on, retiring that
// connection when this client's own timeout fired on it.
//
// context.DeadlineExceeded reports Timeout() == true, so a caller's own expiring
// deadline arrives here looking like a timeout. It implicates the caller rather
// than the connection, and it propagates into the attempt context, so
// out.callerCancelled means the cancellation came from above and the connection
// is left alone. If the caller's deadline expires between our timeout firing and
// that check the connection is spared, which is the safe direction.
//
// When a response body is still to be read the claim is handed to the body,
// because the stream stays on the connection until the body is closed;
// attemptCtx bounds how long that claim can be held.
func (a *attemptConns) settle(attemptCtx context.Context, out attemptOutcome) {
	if out.held.conn == nil {
		return
	}

	if out.err == nil && out.res != nil && out.res.Body != nil && out.res.Body != http.NoBody {
		out.res.Body = a.holdThroughBody(attemptCtx, out.held, out.res.Body)
		return
	}

	var netErr net.Error
	ourTimeout := out.err != nil && !out.callerCancelled &&
		errors.As(out.err, &netErr) && netErr.Timeout()
	if !ourTimeout {
		a.release(out.held)
		return
	}

	if !a.retire(out.held) {
		// Not closed by this attempt. Either other attempts are still on the
		// connection and the last of them closes it, or the accounting was
		// already broken, which drop reports separately.
		if dl := loadDebugLogger(); dl != nil {
			dl.Logf("Timed-out HTTP connection not closed by this attempt: %q\n", out.connURL)
		}
	}
}

// withAttemptConnTrace records the net.Conn selected for this RoundTrip into dst
// and registers the attempt with track, so the connection can be retired if this
// attempt times out on it.
//
// dst needs no synchronization. GotConn runs on the goroutine that called
// RoundTrip, before RoundTrip returns, on both protocols: HTTP/2 calls it from
// RoundTripOpt before handing the request to the ClientConn, and net/http calls
// it for HTTP/1 inside getConn ("Trace success but only for HTTP/1. HTTP/2
// calls trace.GotConn itself.").
//
// HTTP/2 may report more than one connection per outer RoundTrip, because
// RoundTripOpt retries internally on GOAWAY and REFUSED_STREAM and traces each
// connection it tries. Last write wins, which is what we want: the final
// connection is the one the request actually ran on. The connection we move off
// is released as we go, so an attempt holds exactly one claim.
//
// Existing ClientTrace hooks on ctx are preserved -- the previous GotConn, if
// any, still runs after dst is updated.
func withAttemptConnTrace(ctx context.Context, dst *attemptClaim, track *attemptConns) context.Context {
	trace := &httptrace.ClientTrace{}
	if prev := httptrace.ContextClientTrace(ctx); prev != nil {
		*trace = *prev
	}
	prevGot := trace.GotConn
	trace.GotConn = func(info httptrace.GotConnInfo) {
		if dst.conn != nil {
			track.release(*dst)
		}
		*dst = track.acquire(info.Conn)
		if prevGot != nil {
			prevGot(info)
		}
	}
	return httptrace.WithClientTrace(ctx, trace)
}

// closeRetiredConn closes a connection whose drain has completed. Canceling an
// HTTP/2 stream does not retire its ClientConn, so without this a timeout retry
// is multiplexed onto the same (possibly black-holed) connection and
// DialContext never runs. Closing the net.Conn forces the next RoundTrip to
// dial.
//
// This closes a connection httptrace declares off-limits: GotConnInfo.Conn is
// "owned by the http.Transport and should not be read, written or closed by
// users of ClientTrace". We do it because net/http exposes no sanctioned way to
// retire one specific pooled HTTP/2 connection. CloseIdleConnections skips a
// connection with live streams; Request.Close marks doNotReuse on whichever
// ClientConn the *next* request is assigned to, which need not be the stalled
// one; and golang.org/x/net/http2's ClientConnPool, the only API that can name
// a single connection, is deprecated and would make us own dialing, ALPN, and
// HTTP/1.1 fallback for all TLS traffic. If upstream ever provides a real way
// to retire a pooled connection, this whole file goes away.
//
// Only [attemptConns.drop] should call this: reaching it is what establishes
// that the connection was marked for retirement and has finished draining, and
// that conn is non-nil.
func closeRetiredConn(conn net.Conn) {
	_ = conn.Close()
}

// shouldTraceAttemptConn reports whether this client should record the
// underlying net.Conn on each attempt so a timeout can retire it. The
// default path (no per-attempt timeout, no retry-on-timeout) stays
// allocation-free.
func (c *Client) shouldTraceAttemptConn() bool {
	return c.requestTimeout > 0 || c.enableRetryOnTimeout
}
