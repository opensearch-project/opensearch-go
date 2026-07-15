// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// RequestObserver records request duration and response size as Prometheus
// histograms, labeled by method, status class, and mode (request vs stream).
// Wire it into a [Registry]; it satisfies [Observer].
type RequestObserver struct {
	BaseObserver

	duration *prometheus.HistogramVec
	bytes    *prometheus.HistogramVec
}

// RequestObserverOption configures a [RequestObserver].
type RequestObserverOption func(*requestObserverOptions)

type requestObserverOptions struct {
	durationBuckets []float64
	sizeBuckets     []float64
}

// WithDurationBuckets sets the histogram buckets for request duration in
// seconds. Defaults to [prometheus.DefBuckets].
func WithDurationBuckets(buckets []float64) RequestObserverOption {
	return func(o *requestObserverOptions) { o.durationBuckets = buckets }
}

// WithSizeBuckets sets the histogram buckets for response size in bytes.
// Defaults to exponential buckets from 64 B to ~64 MB.
func WithSizeBuckets(buckets []float64) RequestObserverOption {
	return func(o *requestObserverOptions) { o.sizeBuckets = buckets }
}

// NewRequestObserver returns a RequestObserver with the given options. Register
// it by wiring it into a [Registry]; do not register it directly.
func NewRequestObserver(opts ...RequestObserverOption) *RequestObserver {
	cfg := requestObserverOptions{
		durationBuckets: prometheus.DefBuckets,
		sizeBuckets:     prometheus.ExponentialBuckets(64, 4, 11), // 64 B .. ~64 MB
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &RequestObserver{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "opensearch",
			Subsystem: "client",
			Name:      "request_duration_seconds",
			Help:      "Duration of OpenSearch client requests, labeled by method, status class, and mode (request=full-read, stream=time-to-first-byte).",
			Buckets:   cfg.durationBuckets,
		}, []string{"method", "status", "mode"}),
		bytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "opensearch",
			Subsystem: "client",
			Name:      "response_size_bytes",
			Help:      "Size of buffered OpenSearch client response bodies, labeled by method and status class.",
			Buckets:   cfg.sizeBuckets,
		}, []string{"method", "status"}),
	}
}

// Register implements [Observer].
func (o *RequestObserver) Register(reg prometheus.Registerer) error {
	if err := reg.Register(o.duration); err != nil {
		return err
	}
	return reg.Register(o.bytes)
}

// OnRequest implements [Observer]. Response size is recorded only for buffered
// requests (ModeRequest), where the exact byte count is known.
func (o *RequestObserver) OnRequest(s RequestSample) {
	o.duration.WithLabelValues(s.Method, s.StatusClass, s.Mode.String()).Observe(s.Seconds)
	if s.Mode == ModeRequest {
		o.bytes.WithLabelValues(s.Method, s.StatusClass).Observe(float64(s.Bytes))
	}
}

// statusClass reduces an HTTP status code to a low-cardinality label: "2xx",
// "4xx", etc., or "error" when no response was received (status 0).
func statusClass(code int) string {
	if code == 0 {
		return "error"
	}
	if code < 100 || code >= 600 {
		return "unknown"
	}
	return strconv.Itoa(code/100) + "xx"
}
