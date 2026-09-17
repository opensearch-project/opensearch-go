// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net"
	"net/http/httptrace"
)

// withAttemptConnTrace records the net.Conn selected for this RoundTrip into
// dst, so a timed-out attempt can retire the connection it stalled on.
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
// connection is the one the request actually ran on.
//
// Existing ClientTrace hooks on ctx are preserved -- the previous GotConn, if
// any, still runs after dst is updated.
func withAttemptConnTrace(ctx context.Context, dst *net.Conn) context.Context {
	trace := &httptrace.ClientTrace{}
	if prev := httptrace.ContextClientTrace(ctx); prev != nil {
		*trace = *prev
	}
	prevGot := trace.GotConn
	trace.GotConn = func(info httptrace.GotConnInfo) {
		*dst = info.Conn
		if prevGot != nil {
			prevGot(info)
		}
	}
	return httptrace.WithClientTrace(ctx, trace)
}

// closeTimedOutConn retires the connection an attempt stalled on. Canceling an
// HTTP/2 stream does not retire its ClientConn, so without this a timeout retry
// is multiplexed onto the same (possibly black-holed) connection and
// DialContext never runs. Closing the net.Conn forces the next RoundTrip to
// dial. A nil conn is a no-op.
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
// Callers must only invoke this for a timeout they generated themselves; see
// the call site in stream for why caller cancellation must not reach here.
func closeTimedOutConn(conn net.Conn) {
	if conn == nil {
		return
	}
	_ = conn.Close()
}

// shouldTraceAttemptConn reports whether this transport should record the
// underlying net.Conn on each attempt so a timeout can retire it. The
// default path (no per-attempt timeout, no retry-on-timeout) stays
// allocation-free.
func (c *Transport) shouldTraceAttemptConn() bool {
	return c.requestTimeout > 0 || c.enableRetryOnTimeout
}
