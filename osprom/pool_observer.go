// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// PoolObserver records the USE signals (Utilization, Saturation, Errors) for the
// transport's connection pool as Prometheus metrics, labeled by pool. It
// observes connection-lifecycle events rather than requests:
//
//   - Utilization: gauges of active / dead / standby connection counts, updated
//     from each lifecycle event's pool snapshot.
//   - Saturation: a counter of overload-detected events (a node shed load
//     because its resource usage exceeded thresholds).
//   - Errors: a counter of demotions (a ready connection went dead on failure)
//     and a counter of health-check failures.
//
// Wire it into a [Registry] alongside [RequestObserver] for full RED+USE
// coverage; it satisfies [Observer].
type PoolObserver struct {
	BaseObserver

	connections *prometheus.GaugeVec   // U: active/dead/standby by pool and state
	overloaded  *prometheus.CounterVec // S: overload-detected events by pool
	demotions   *prometheus.CounterVec // E: ready->dead demotions by pool
	healthFails *prometheus.CounterVec // E: health-check failures by pool
}

// NewPoolObserver returns a PoolObserver. Register it by wiring it into a
// [Registry]; do not register it directly.
func NewPoolObserver() *PoolObserver {
	return &PoolObserver{
		connections: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "pool_connections",
			Help:      "Connections in the transport pool by state, labeled by pool (USE: utilization).",
		}, []string{labelPool, labelState}),
		overloaded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "pool_overloaded_total",
			Help:      "Connections demoted because the node's resource usage exceeded thresholds, labeled by pool (USE: saturation).",
		}, []string{labelPool}),
		demotions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "pool_demotions_total",
			Help:      "Ready connections demoted to dead on request failure, labeled by pool (USE: errors).",
		}, []string{labelPool}),
		healthFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "pool_health_check_failures_total",
			Help:      "Connection health-check failures, labeled by pool (USE: errors).",
		}, []string{labelPool}),
	}
}

// Register implements [Observer].
func (o *PoolObserver) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{o.connections, o.overloaded, o.demotions, o.healthFails} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// updateUtilization refreshes the active/dead/standby gauges from a lifecycle
// event's pool snapshot.
func (o *PoolObserver) updateUtilization(e *opensearchtransport.ConnectionEvent) {
	o.connections.WithLabelValues(e.PoolName, "active").Set(float64(e.ActiveCount))
	o.connections.WithLabelValues(e.PoolName, "dead").Set(float64(e.DeadCount))
	o.connections.WithLabelValues(e.PoolName, "standby").Set(float64(e.StandbyCount))
}

// OnPromote implements [Observer]: a resurrection changes pool composition.
func (o *PoolObserver) OnPromote(e *opensearchtransport.ConnectionEvent) {
	o.updateUtilization(e)
}

// OnDemote implements [Observer]: a failure moved a connection to dead.
func (o *PoolObserver) OnDemote(e *opensearchtransport.ConnectionEvent) {
	o.demotions.WithLabelValues(e.PoolName).Inc()
	o.updateUtilization(e)
}

// OnOverloadDetected implements [Observer]: the node shed load (saturation).
func (o *PoolObserver) OnOverloadDetected(e *opensearchtransport.ConnectionEvent) {
	o.overloaded.WithLabelValues(e.PoolName).Inc()
	o.updateUtilization(e)
}

// OnOverloadCleared implements [Observer]: resource usage normalized.
func (o *PoolObserver) OnOverloadCleared(e *opensearchtransport.ConnectionEvent) {
	o.updateUtilization(e)
}

// OnHealthCheckFail implements [Observer].
func (o *PoolObserver) OnHealthCheckFail(e *opensearchtransport.ConnectionEvent) {
	o.healthFails.WithLabelValues(e.PoolName).Inc()
}
