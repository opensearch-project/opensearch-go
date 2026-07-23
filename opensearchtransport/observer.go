// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"sync/atomic"
)

// ConnectionObserver receives notifications about connection lifecycle events
// and routing decisions.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// Methods should return quickly to avoid blocking transport operations.
//
// Observer methods may be called while internal locks are held. Implementations
// must NOT call back into the Transport, connection pool, or any method that
// acquires transport-internal locks. Violating this contract will cause a
// deadlock.
type ConnectionObserver interface { //nolint:interfacebloat // lifecycle + routing + resolver observer needs all 15 event methods
	// OnPromote is called when a dead connection becomes ready
	// (successful resurrection after health check).
	OnPromote(event ConnectionEvent)

	// OnDemote is called when a ready connection becomes dead
	// (request failure).
	OnDemote(event ConnectionEvent)

	// OnOverloadDetected is called when a ready connection is demoted
	// because the node's resource usage exceeds configured thresholds.
	OnOverloadDetected(event ConnectionEvent)

	// OnOverloadCleared is called when an overload-demoted connection
	// is promoted back to ready after resource usage returns to normal.
	OnOverloadCleared(event ConnectionEvent)

	// OnDiscoveryAdd is called when node discovery finds a new node
	// not previously in the connection pool.
	OnDiscoveryAdd(event ConnectionEvent)

	// OnDiscoveryRemove is called when node discovery determines a node
	// is no longer part of the cluster.
	OnDiscoveryRemove(event ConnectionEvent)

	// OnDiscoveryUnchanged is called for each existing node that remains
	// in the pool after a discovery update.
	OnDiscoveryUnchanged(event ConnectionEvent)

	// OnHealthCheckPass is called when a connection passes a health check.
	OnHealthCheckPass(event ConnectionEvent)

	// OnHealthCheckFail is called when a connection fails a health check.
	// The Error field of ConnectionEvent contains the failure reason.
	OnHealthCheckFail(event ConnectionEvent)

	// OnStandbyPromote is called when a standby connection is promoted
	// to the active partition after passing consecutive health checks.
	OnStandbyPromote(event ConnectionEvent)

	// OnStandbyDemote is called when an active connection is moved to the
	// standby partition (during rotation or discovery overflow).
	OnStandbyDemote(event ConnectionEvent)

	// OnWarmupRequest is called when a request is about to be served by a
	// connection going through warmup. The State field carries the connection's
	// packed state after the warmup round was advanced; use
	// State.IsWarmingUp() to detect whether warmup just completed (false)
	// or is still in progress (true), and State.WarmupRoundsRemaining() /
	// State.WarmupSkipRemaining() to inspect progress.
	OnWarmupRequest(event ConnectionEvent)

	// OnRoute is called after the router selects a node for a
	// request. The event contains the full scoring breakdown for all
	// candidates considered during the routing decision.
	//
	// The event's Candidates slice is backed by a pooled array. A synchronous
	// observer that finishes within the call needs to do nothing: the array is
	// reclaimed automatically once OnRoute returns. An observer that retains the
	// event past the call -- for example by sending it to another goroutine over
	// a channel -- must call [RouteEvent.Retain] synchronously inside OnRoute,
	// then [RouteEvent.Release] exactly once when it is done reading Candidates.
	// To keep the candidates without managing the lifecycle, copy them with
	// slices.Clone(event.Candidates).
	//
	// The scalar RouteEvent fields (IndexName, Selected, TargetShard, ...) are
	// values and remain valid after the array is reclaimed.
	OnRoute(event RouteEvent)

	// OnShardMapInvalidation is called when a routing failure flags a
	// connection's shard placement as stale. The connection is excluded
	// from routing candidates until a /_cat/shards refresh.
	OnShardMapInvalidation(event ShardMapInvalidationEvent)

	// OnAddressRewrite is called when an AddressResolverFunc rewrites a
	// node's URL during discovery. The event contains the original and
	// rewritten URLs.
	OnAddressRewrite(event AddressRewriteEvent)

	// OnRequestStart is called once per logical request, before the first round
	// trip, with the request's context and a snapshot of the identity known up
	// front (Method, Path, RouteName, Index; Host/Attempt are not yet known and
	// are zero). It returns the context used for the remainder of the request and
	// passed back to the attempt and response hooks, so a tracer can open a span
	// here, carry it in the returned context, and close it when the response
	// event fires. Return ctx unchanged to opt out; the returned context must be
	// derived from ctx (or be ctx itself). It is only called when an observer is
	// registered.
	OnRequestStart(ctx context.Context, event RequestEvent) context.Context

	// OnAttemptStart is called before each round-trip attempt (attempt is
	// zero-based) with the current request context. The returned context scopes
	// that single attempt, letting a tracer open a child span per attempt. Return
	// ctx unchanged to opt out.
	OnAttemptStart(ctx context.Context, attempt int) context.Context

	// OnAttemptEnd is called after each round-trip attempt returns, with the
	// attempt's context, its zero-based index, the HTTP status code (0 on
	// transport error), and the attempt error (nil on success). It closes any
	// per-attempt span opened by OnAttemptStart.
	OnAttemptEnd(ctx context.Context, attempt int, statusCode int, err error)

	// OnRequestResponse is called once per logical request by
	// [Transport.Request] after the response body has been read and buffered.
	// The event carries the full-read Duration and the exact ResponseBytes. ctx
	// is the request context returned by OnRequestStart.
	OnRequestResponse(ctx context.Context, event RequestResponseEvent)

	// OnStreamResponse is called once per logical request by [Transport.Stream]
	// at round-trip return, before the caller reads the body. The event carries
	// the time-to-first-byte Duration and the Content-Length header
	// (ContentLength), not a measured byte count. ctx is the request context
	// returned by OnRequestStart.
	OnStreamResponse(ctx context.Context, event StreamResponseEvent)
}

// BaseConnectionObserver is an embeddable no-op implementation of
// ConnectionObserver. Embed it in your own struct and override only the
// methods you care about.
//
//	type myObserver struct {
//	    opensearchtransport.BaseConnectionObserver
//	}
//
//	func (o *myObserver) OnDemote(event opensearchtransport.ConnectionEvent) {
//	    log.Printf("connection dead: %s", event.URL)
//	}
type BaseConnectionObserver struct{}

// OnPromote implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnPromote(ConnectionEvent) {}

// OnDemote implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnDemote(ConnectionEvent) {}

// OnOverloadDetected implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnOverloadDetected(ConnectionEvent) {}

// OnOverloadCleared implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnOverloadCleared(ConnectionEvent) {}

// OnDiscoveryAdd implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnDiscoveryAdd(ConnectionEvent) {}

// OnDiscoveryRemove implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnDiscoveryRemove(ConnectionEvent) {}

// OnDiscoveryUnchanged implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnDiscoveryUnchanged(ConnectionEvent) {}

// OnHealthCheckPass implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnHealthCheckPass(ConnectionEvent) {}

// OnHealthCheckFail implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnHealthCheckFail(ConnectionEvent) {}

// OnStandbyPromote implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnStandbyPromote(ConnectionEvent) {}

// OnStandbyDemote implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnStandbyDemote(ConnectionEvent) {}

// OnWarmupRequest implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnWarmupRequest(ConnectionEvent) {}

// OnRoute implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnRoute(RouteEvent) {}

// OnShardMapInvalidation implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnShardMapInvalidation(ShardMapInvalidationEvent) {}

// OnAddressRewrite implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnAddressRewrite(AddressRewriteEvent) {}

// OnRequestStart implements ConnectionObserver (no-op; returns ctx unchanged).
func (BaseConnectionObserver) OnRequestStart(ctx context.Context, event RequestEvent) context.Context {
	_ = event
	return ctx
}

// OnAttemptStart implements ConnectionObserver (no-op; returns ctx unchanged).
func (BaseConnectionObserver) OnAttemptStart(ctx context.Context, attempt int) context.Context {
	_ = attempt
	return ctx
}

// OnAttemptEnd implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnAttemptEnd(ctx context.Context, attempt int, statusCode int, err error) {
	//nolint:dogsled // names document the no-op signature for future overriders
	_, _, _, _ = ctx, attempt, statusCode, err
}

// OnRequestResponse implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnRequestResponse(ctx context.Context, event RequestResponseEvent) {
	_, _ = ctx, event
}

// OnStreamResponse implements ConnectionObserver (no-op).
func (BaseConnectionObserver) OnStreamResponse(ctx context.Context, event StreamResponseEvent) {
	_, _ = ctx, event
}

// Compile-time check that BaseConnectionObserver implements ConnectionObserver.
var _ ConnectionObserver = (*BaseConnectionObserver)(nil)

// observerFromAtomic loads a ConnectionObserver from an atomic pointer.
// Returns nil if the pointer is nil or stores nil.
func observerFromAtomic(p *atomic.Pointer[ConnectionObserver]) ConnectionObserver {
	if p == nil {
		return nil
	}
	ptr := p.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}
