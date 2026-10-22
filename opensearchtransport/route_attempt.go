// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "context"

// routeAttempt is what the transport's retry loop knows about one request's failed
// attempts: the nodes it sent the request to and how many times. The routers read
// it when the request is routed again, through the request's context.
//
// A retry is routed to the best candidate the request has not tried in the current
// pass over its candidate set. Once every candidate has been tried, a new pass
// begins over the candidate set routing yields then, which may be the same nodes
// or others, and each candidate's score is multiplied by one more than the times
// the request has tried it, so a node that keeps refusing ranks lower each pass.
//
// It is created at the first failed attempt and used by one request's retry loop
// alone, one attempt at a time, so it needs no lock. A request that succeeds at
// once never allocates one.
type routeAttempt struct {
	tried []triedConn
}

// triedConn is one node a request was sent to: how many times, and whether in the
// current pass.
type triedConn struct {
	conn   *Connection
	tries  int
	inPass bool
}

// routeAttemptKey is the context key for a request's routeAttempt.
type routeAttemptKey struct{}

// withRouteAttempt returns ctx carrying a, unless ctx already carries it.
func withRouteAttempt(ctx context.Context, a *routeAttempt) context.Context {
	if routeAttemptFrom(ctx) == a {
		return ctx
	}

	return context.WithValue(ctx, routeAttemptKey{}, a)
}

// routeAttemptFrom returns the request's routeAttempt, or nil before its first
// failed attempt.
func routeAttemptFrom(ctx context.Context) *routeAttempt {
	a, _ := ctx.Value(routeAttemptKey{}).(*routeAttempt)

	return a
}

// record notes a failed attempt sent to conn.
func (a *routeAttempt) record(conn *Connection) {
	for i := range a.tried {
		if t := &a.tried[i]; t.conn == conn {
			t.tries++
			t.inPass = true

			return
		}
	}
	a.tried = append(a.tried, triedConn{conn: conn, tries: 1, inPass: true})
}

// lookup returns how many times the request has tried conn, and whether it tried
// conn in the current pass.
func (a *routeAttempt) lookup(conn *Connection) (int, bool) {
	if a == nil {
		return 0, false
	}
	for _, t := range a.tried {
		if t.conn == conn {
			return t.tries, t.inPass
		}
	}

	return 0, false
}

// newPass begins a new pass: every candidate may be tried again. The counts of
// tries are kept.
func (a *routeAttempt) newPass() {
	for i := range a.tried {
		a.tried[i].inPass = false
	}
}
