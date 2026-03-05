// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "sync"

// recordingObserver captures all ConnectionObserver events for test assertions.
// All methods are synchronous and safe for concurrent use.
type recordingObserver struct {
	BaseConnectionObserver

	mu          sync.Mutex
	events      map[string][]ConnectionEvent
	routeEvents []RouteEvent
}

func newRecordingObserver() *recordingObserver {
	return &recordingObserver{events: make(map[string][]ConnectionEvent)}
}

func (o *recordingObserver) record(kind string, event ConnectionEvent) {
	o.mu.Lock()
	o.events[kind] = append(o.events[kind], event)
	o.mu.Unlock()
}

func (o *recordingObserver) get(kind string) []ConnectionEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ConnectionEvent(nil), o.events[kind]...)
}

func (o *recordingObserver) count(kind string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.events[kind])
}

func (o *recordingObserver) getRouteEvents() []RouteEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]RouteEvent(nil), o.routeEvents...)
}

func (o *recordingObserver) OnPromote(e ConnectionEvent)          { o.record("promote", e) }
func (o *recordingObserver) OnDemote(e ConnectionEvent)           { o.record("demote", e) }
func (o *recordingObserver) OnOverloadDetected(e ConnectionEvent) { o.record("overload_detected", e) }
func (o *recordingObserver) OnOverloadCleared(e ConnectionEvent)  { o.record("overload_cleared", e) }
func (o *recordingObserver) OnDiscoveryAdd(e ConnectionEvent)     { o.record("discovery_add", e) }
func (o *recordingObserver) OnDiscoveryRemove(e ConnectionEvent)  { o.record("discovery_remove", e) }
func (o *recordingObserver) OnDiscoveryUnchanged(e ConnectionEvent) {
	o.record("discovery_unchanged", e)
}
func (o *recordingObserver) OnHealthCheckPass(e ConnectionEvent) { o.record("healthcheck_pass", e) }
func (o *recordingObserver) OnHealthCheckFail(e ConnectionEvent) { o.record("healthcheck_fail", e) }
func (o *recordingObserver) OnStandbyPromote(e ConnectionEvent)  { o.record("standby_promote", e) }
func (o *recordingObserver) OnStandbyDemote(e ConnectionEvent)   { o.record("standby_demote", e) }
func (o *recordingObserver) OnWarmupRequest(e ConnectionEvent)   { o.record("warmup_request", e) }
func (o *recordingObserver) OnRoute(e RouteEvent) {
	o.mu.Lock()
	o.routeEvents = append(o.routeEvents, e)
	o.mu.Unlock()
}
