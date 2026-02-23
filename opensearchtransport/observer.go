// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"sync/atomic"
	"time"
)

// ConnectionEvent is a point-in-time snapshot of a connection captured at the
// moment a lifecycle event occurs. All fields are safe to retain after the
// callback returns.
type ConnectionEvent struct {
	// URL is the connection's address (e.g. "http://127.0.0.1:9200").
	URL string

	// ID is the node's unique identifier (populated after discovery).
	ID string

	// Name is the node's human-readable name (populated after discovery).
	Name string

	// Roles lists the node's roles (e.g. ["data", "ingest"]).
	Roles []string

	// Version is the node's OpenSearch version string.
	Version string

	// PoolName identifies which pool dispatched this event (e.g. "roundrobin", "role:data").
	PoolName string

	// Failures is the cumulative failure count at the time of the event.
	Failures int64

	// ActiveCount is the number of active connections in the pool at the time of the event.
	ActiveCount int

	// DeadCount is the number of dead connections in the pool at the time of the event.
	DeadCount int

	// StandbyCount is the number of standby connections in the pool at the time of the event.
	StandbyCount int

	// State is the connection's packed state snapshot at the time of the event.
	// Use its methods to query lifecycle phase and warmup progress.
	State ConnState

	// Weight is the connection's round-robin weight at the time of the event.
	// Default 1; higher for nodes with more cores in heterogeneous clusters.
	Weight int

	// Error is non-nil only for OnHealthCheckFail events.
	Error error

	// Timestamp is when the event was created.
	Timestamp time.Time
}

// ConnectionObserver receives notifications about connection lifecycle events.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// Methods should return quickly to avoid blocking transport operations.
//
// Observer methods may be called while internal locks are held. Implementations
// must NOT call back into the Client, connection pool, or any method that
// acquires transport-internal locks. Violating this contract will cause a
// deadlock.
type ConnectionObserver interface { //nolint:interfacebloat // lifecycle observer needs all 12 event methods
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
	// to the ready list after passing health checks.
	OnStandbyPromote(event ConnectionEvent)

	// OnStandbyDemote is called when a ready connection is moved to the
	// standby list (during rotation or discovery overflow).
	OnStandbyDemote(event ConnectionEvent)

	// OnWarmupRequest is called when a request is about to be served by a
	// connection going through warmup. The State field carries the connection's
	// packed state after the warmup round was advanced; use
	// State.IsWarmingUp() to detect whether warmup just completed (false)
	// or is still in progress (true), and State.WarmupRoundsRemaining() /
	// State.WarmupSkipRemaining() to inspect progress.
	OnWarmupRequest(event ConnectionEvent)
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

// Compile-time check that BaseConnectionObserver implements ConnectionObserver.
var _ ConnectionObserver = (*BaseConnectionObserver)(nil)

// newConnectionEvent builds a ConnectionEvent snapshot from a Connection.
// This reads only immutable fields (URL, ID, Name, Roles, Version) and atomic
// counters (failures, state), so it is safe to call while holding locks.
func newConnectionEvent(poolName string, c *Connection, activeCount, deadCount int) ConnectionEvent {
	event := ConnectionEvent{
		URL:         c.URL.String(),
		ID:          c.ID,
		Name:        c.Name,
		Version:     c.Version,
		PoolName:    poolName,
		Failures:    c.failures.Load(),
		State:       ConnState{packed: c.state.Load()},
		Weight:      c.effectiveWeight(),
		ActiveCount: activeCount,
		DeadCount:   deadCount,
		Timestamp:   time.Now().UTC(),
	}
	if len(c.Roles) > 0 {
		event.Roles = c.Roles.toSlice()
	}
	return event
}

// newConnectionEventWithStandby builds a ConnectionEvent snapshot that includes standby count.
func newConnectionEventWithStandby(poolName string, c *Connection, activeCount, deadCount, standbyCount int) ConnectionEvent {
	event := newConnectionEvent(poolName, c, activeCount, deadCount)
	event.StandbyCount = standbyCount
	return event
}

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
