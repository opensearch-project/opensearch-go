// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// PoolObserver records the USE signals (Utilization, Saturation, Errors) for the
// transport's connection pool as OpenTelemetry metrics, attributed by pool. It
// observes connection-lifecycle events rather than requests:
//
//   - Utilization: an observable gauge of active / dead / standby connection
//     counts, updated from each lifecycle event's pool snapshot.
//   - Saturation: a counter of overload-detected events (a node shed load
//     because its resource usage exceeded thresholds).
//   - Errors: a counter of demotions (a ready connection went dead on failure)
//     and a counter of health-check failures.
//
// Wire it into a [Registry] alongside [RequestObserver] for full RED+USE
// coverage; it satisfies [Observer].
type PoolObserver struct {
	BaseObserver

	overloaded  metric.Int64Counter // S: overload-detected events by pool
	demotions   metric.Int64Counter // E: ready->dead demotions by pool
	healthFails metric.Int64Counter // E: health-check failures by pool

	connections metric.Int64ObservableGauge // U: active/dead/standby by pool and state

	mu    sync.Mutex
	gauge map[poolState]int64 // last-known counts, read by the gauge callback
}

// poolState keys the utilization gauge by pool name and connection state.
type poolState struct {
	pool  string
	state string
}

// NewPoolObserver returns a PoolObserver. Its instruments are created when it is
// wired into a [Registry] via [New]; do not create them directly.
func NewPoolObserver() *PoolObserver {
	return &PoolObserver{gauge: make(map[poolState]int64)}
}

// Register implements [Observer].
func (o *PoolObserver) Register(meter metric.Meter) error {
	var err error
	if o.overloaded, err = meter.Int64Counter(
		instrumentPrefix+"pool.overloaded",
		metric.WithDescription("Connections demoted because the node's resource usage exceeded thresholds (USE: saturation)."),
		metric.WithUnit("{connection}"),
	); err != nil {
		return err
	}
	if o.demotions, err = meter.Int64Counter(
		instrumentPrefix+"pool.demotions",
		metric.WithDescription("Ready connections demoted to dead on request failure (USE: errors)."),
		metric.WithUnit("{connection}"),
	); err != nil {
		return err
	}
	if o.healthFails, err = meter.Int64Counter(
		instrumentPrefix+"pool.health_check_failures",
		metric.WithDescription("Connection health-check failures (USE: errors)."),
		metric.WithUnit("{failure}"),
	); err != nil {
		return err
	}
	// Utilization is an async gauge: the callback reports the last-known snapshot
	// recorded by the lifecycle hooks. This matches OTel's pull model and avoids
	// synchronous gauge Set calls on the transport goroutines.
	o.connections, err = meter.Int64ObservableGauge(
		instrumentPrefix+"pool.connections",
		metric.WithDescription("Connections in the transport pool by state, attributed by pool (USE: utilization)."),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(o.observeConnections),
	)
	return err
}

// observeConnections is the async-gauge callback: it reports every pool/state
// count captured by the lifecycle hooks.
func (o *PoolObserver) observeConnections(_ context.Context, obs metric.Int64Observer) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for k, v := range o.gauge {
		obs.Observe(v, metric.WithAttributes(
			attribute.String(attrPool, k.pool),
			attribute.String(attrState, k.state),
		))
	}
	return nil
}

// updateUtilization stores the active/dead/standby counts from a lifecycle
// event's pool snapshot for the next gauge callback.
func (o *PoolObserver) updateUtilization(e *opensearchtransport.ConnectionEvent) {
	o.mu.Lock()
	o.gauge[poolState{e.PoolName, "active"}] = int64(e.ActiveCount)
	o.gauge[poolState{e.PoolName, "dead"}] = int64(e.DeadCount)
	o.gauge[poolState{e.PoolName, "standby"}] = int64(e.StandbyCount)
	o.mu.Unlock()
}

// OnPromote implements [Observer]: a resurrection changes pool composition.
func (o *PoolObserver) OnPromote(_ context.Context, e *opensearchtransport.ConnectionEvent) {
	o.updateUtilization(e)
}

// OnDemote implements [Observer]: a failure moved a connection to dead.
func (o *PoolObserver) OnDemote(ctx context.Context, e *opensearchtransport.ConnectionEvent) {
	o.demotions.Add(ctx, 1, metric.WithAttributes(attribute.String(attrPool, e.PoolName)))
	o.updateUtilization(e)
}

// OnOverloadDetected implements [Observer]: the node shed load (saturation).
func (o *PoolObserver) OnOverloadDetected(ctx context.Context, e *opensearchtransport.ConnectionEvent) {
	o.overloaded.Add(ctx, 1, metric.WithAttributes(attribute.String(attrPool, e.PoolName)))
	o.updateUtilization(e)
}

// OnOverloadCleared implements [Observer]: resource usage normalized.
func (o *PoolObserver) OnOverloadCleared(_ context.Context, e *opensearchtransport.ConnectionEvent) {
	o.updateUtilization(e)
}

// OnHealthCheckFail implements [Observer].
func (o *PoolObserver) OnHealthCheckFail(ctx context.Context, e *opensearchtransport.ConnectionEvent) {
	o.healthFails.Add(ctx, 1, metric.WithAttributes(attribute.String(attrPool, e.PoolName)))
}
