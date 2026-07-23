// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// RequestObserver records the RED signals (Rate, Errors, Duration) for
// OpenSearch client requests as Prometheus metrics, labeled by method, status
// class, and mode (request vs stream). It is a deliberately low-cardinality
// default: it does not label by route, index, or node. To record those
// dimensions, implement [Observer] over your own pre-registered metrics. Wire it
// into a [Registry]; it satisfies [Observer].
type RequestObserver struct {
	BaseObserver

	statusClassifier StatusClassifier
	requests         *prometheus.CounterVec   // R: total requests
	errors           *prometheus.CounterVec   // E: error responses (4xx/5xx/transport error)
	duration         *prometheus.HistogramVec // D: request latency
	bytes            *prometheus.HistogramVec // response size
}

// StatusClassifier reduces an HTTP status code to a low-cardinality "status"
// label. It receives the response status code (0 when no response was received)
// and returns the label. The default classifier ([statusClass]) maps to the
// per-hundred bucket ("2xx", "3xx", "4xx", "5xx"), "error" (no response, code 0),
// or "unknown" (out of the 100..599 range). Override it with
// [WithStatusClassifier] to keep or collapse specific codes.
type StatusClassifier func(statusCode int) string

// RequestObserverOption configures a [RequestObserver].
type RequestObserverOption func(*requestObserverOptions)

type requestObserverOptions struct {
	durationBuckets  []float64
	sizeBuckets      []float64
	statusClassifier StatusClassifier
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

// WithStatusClassifier overrides how HTTP status codes are reduced to the
// "status" label. Defaults to [statusClass]. Use it to keep or collapse specific
// codes to control cardinality. See [StatusClassifier].
func WithStatusClassifier(fn StatusClassifier) RequestObserverOption {
	return func(o *requestObserverOptions) { o.statusClassifier = fn }
}

// NewRequestObserver returns a RequestObserver with the given options. Register
// it by wiring it into a [Registry]; do not register it directly.
func NewRequestObserver(opts ...RequestObserverOption) *RequestObserver {
	cfg := requestObserverOptions{
		durationBuckets:  prometheus.DefBuckets,
		sizeBuckets:      prometheus.ExponentialBuckets(64, 4, 11), // 64 B .. ~64 MB
		statusClassifier: statusClass,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.statusClassifier == nil {
		cfg.statusClassifier = statusClass
	}

	return &RequestObserver{
		statusClassifier: cfg.statusClassifier,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "requests_total",
			Help:      "Total OpenSearch client requests, labeled by method, status class, and mode (RED: rate).",
		}, []string{labelMethod, labelStatus, labelMode}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "request_errors_total",
			Help: "OpenSearch client requests that returned a 4xx/5xx status or a transport error, " +
				"labeled by method, status class, and mode (RED: errors).",
		}, []string{labelMethod, labelStatus, labelMode}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "request_duration_seconds",
			Help: "Duration of OpenSearch client requests, labeled by method, status class, " +
				"and mode (request=full-read, stream=time-to-first-byte) (RED: duration).",
			Buckets: cfg.durationBuckets,
		}, []string{labelMethod, labelStatus, labelMode}),
		bytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "response_size_bytes",
			Help:      "Size of buffered OpenSearch client response bodies, labeled by method and status class.",
			Buckets:   cfg.sizeBuckets,
		}, []string{labelMethod, labelStatus}),
	}
}

// Register implements [Observer].
func (o *RequestObserver) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{o.requests, o.errors, o.duration, o.bytes} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// isError reports whether a status code / transport error counts as a request
// error for the RED errors signal: any 4xx/5xx response, or a transport-level
// failure (status 0 with a non-nil error).
func isError(statusCode int, err error) bool {
	return err != nil || statusCode >= 400
}

// OnRequestResponse implements [Observer]. Records the request (rate), any error,
// the full-read duration, and the exact response-body size for a buffered
// request.
func (o *RequestObserver) OnRequestResponse(e *opensearchtransport.RequestResponseEvent) {
	status := o.statusClassifier(e.StatusCode)
	o.requests.WithLabelValues(e.Request.Method, status, modeRequestLabel).Inc()
	if isError(e.StatusCode, e.Err) {
		o.errors.WithLabelValues(e.Request.Method, status, modeRequestLabel).Inc()
	}
	o.duration.WithLabelValues(e.Request.Method, status, modeRequestLabel).Observe(e.Duration.Seconds())
	o.bytes.WithLabelValues(e.Request.Method, status).Observe(float64(e.ResponseBytes))
}

// OnStreamResponse implements [Observer]. Records the request (rate), any error,
// and the time-to-first-byte duration for a streaming request. Response size is
// not recorded: the streamed byte count is unknown until the caller reads the
// body.
func (o *RequestObserver) OnStreamResponse(e *opensearchtransport.StreamResponseEvent) {
	status := o.statusClassifier(e.StatusCode)
	o.requests.WithLabelValues(e.Request.Method, status, modeStreamLabel).Inc()
	if isError(e.StatusCode, e.Err) {
		o.errors.WithLabelValues(e.Request.Method, status, modeStreamLabel).Inc()
	}
	o.duration.WithLabelValues(e.Request.Method, status, modeStreamLabel).Observe(e.Duration.Seconds())
}

// statusClass reduces an HTTP status code to a low-cardinality label: "2xx",
// "4xx", etc., or "error" when no response was received (status 0).
func statusClass(code int) string {
	if code == 0 {
		return statusError
	}
	if code < 100 || code >= 600 {
		return "unknown"
	}
	return strconv.Itoa(code/100) + "xx"
}
