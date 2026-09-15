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
	"sync"
)

// attemptConn holds the net.Conn httptrace.GotConn reports for one RoundTrip.
// GotConn may run on a different goroutine than RoundTrip's caller (HTTP/2),
// so the slot is mutex-guarded.
type attemptConn struct {
	mu   sync.Mutex
	conn net.Conn
}

func (a *attemptConn) set(c net.Conn) {
	a.mu.Lock()
	a.conn = c
	a.mu.Unlock()
}

// close retires the captured connection. Canceling an HTTP/2 stream does not
// retire the ClientConn, so a timeout retry would otherwise be multiplexed
// onto the same (possibly black-holed) connection. Closing the net.Conn
// forces the next RoundTrip to dial. A nil or empty slot is a no-op.
func (a *attemptConn) close() {
	a.mu.Lock()
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.Close()
}

// withAttemptConnTrace records the net.Conn selected for this RoundTrip into
// a. Existing ClientTrace hooks on ctx are preserved: the previous GotConn,
// if any, still runs after the slot is updated.
func withAttemptConnTrace(ctx context.Context, a *attemptConn) context.Context {
	trace := &httptrace.ClientTrace{}
	if prev := httptrace.ContextClientTrace(ctx); prev != nil {
		*trace = *prev
	}
	prevGot := trace.GotConn
	trace.GotConn = func(info httptrace.GotConnInfo) {
		a.set(info.Conn)
		if prevGot != nil {
			prevGot(info)
		}
	}
	return httptrace.WithClientTrace(ctx, trace)
}

// shouldTraceAttemptConn reports whether this transport should record the
// underlying net.Conn on each attempt so a timeout can retire it. The
// default path (no per-attempt timeout, no retry-on-timeout) stays
// allocation-free.
func (c *Transport) shouldTraceAttemptConn() bool {
	return c.requestTimeout > 0 || c.enableRetryOnTimeout
}
