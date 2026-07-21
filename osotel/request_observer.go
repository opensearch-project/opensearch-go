// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// RequestObserver records the RED signals (Rate, Errors, Duration) for
// OpenSearch client requests as OpenTelemetry metrics, attributed by method,
// status class, and mode (request vs stream). It is a deliberately
// low-cardinality default: it does not attribute by route, index, or node. To
// record those dimensions, implement [Observer] over your own instruments. Wire
// it into a [Registry]; it satisfies [Observer].
type RequestObserver struct {
	BaseObserver

	statusClassifier StatusClassifier
	requests         metric.Int64Counter     // R: total requests
	errors           metric.Int64Counter     // E: error responses (4xx/5xx/transport error)
	duration         metric.Float64Histogram // D: request latency
	bytes            metric.Int64Histogram   // response size
}

// StatusClassifier reduces an HTTP status code to a low-cardinality "status"
// attribute. It receives the response status code (0 when no response was
// received) and returns the label. The default classifier ([statusClass]) maps
// to the per-hundred bucket ("2xx", "3xx", "4xx", "5xx"), "error" (no response,
// code 0), or "unknown" (out of the 100..599 range). Override it with
// [WithStatusClassifier] to keep or collapse specific codes.
type StatusClassifier func(statusCode int) string

// RequestObserverOption configures a [RequestObserver].
type RequestObserverOption func(*requestObserverOptions)

type requestObserverOptions struct {
	statusClassifier StatusClassifier
}

// WithStatusClassifier overrides how HTTP status codes are reduced to the
// "status" attribute. Defaults to [statusClass]. Use it to keep or collapse
// specific codes to control cardinality. See [StatusClassifier].
func WithStatusClassifier(fn StatusClassifier) RequestObserverOption {
	return func(o *requestObserverOptions) { o.statusClassifier = fn }
}

// NewRequestObserver returns a RequestObserver with the given options. Its
// instruments are created when it is wired into a [Registry] via [New]; do not
// create them directly.
func NewRequestObserver(opts ...RequestObserverOption) *RequestObserver {
	cfg := requestObserverOptions{statusClassifier: statusClass}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.statusClassifier == nil {
		cfg.statusClassifier = statusClass
	}
	return &RequestObserver{statusClassifier: cfg.statusClassifier}
}

// Register implements [Observer].
func (o *RequestObserver) Register(meter metric.Meter) error {
	var err error
	if o.requests, err = meter.Int64Counter(
		instrumentPrefix+"requests",
		metric.WithDescription("Total OpenSearch client requests, attributed by method, status class, and mode (RED: rate)."),
		metric.WithUnit("{request}"),
	); err != nil {
		return err
	}
	if o.errors, err = meter.Int64Counter(
		instrumentPrefix+"request.errors",
		metric.WithDescription("OpenSearch client requests that returned a 4xx/5xx status or a transport error (RED: errors)."),
		metric.WithUnit("{request}"),
	); err != nil {
		return err
	}
	if o.duration, err = meter.Float64Histogram(
		instrumentPrefix+"request.duration",
		metric.WithDescription("Duration of OpenSearch client requests (request=full-read, stream=time-to-first-byte) (RED: duration)."),
		metric.WithUnit("s"),
	); err != nil {
		return err
	}
	o.bytes, err = meter.Int64Histogram(
		instrumentPrefix+"response.size",
		metric.WithDescription("Size of buffered OpenSearch client response bodies."),
		metric.WithUnit("By"),
	)
	return err
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
func (o *RequestObserver) OnRequestResponse(ctx context.Context, e *opensearchtransport.RequestResponseEvent) {
	status := o.statusClassifier(e.StatusCode)
	attrs := metric.WithAttributes(
		attribute.String(attrMethod, e.Request.Method),
		attribute.String(attrStatus, status),
		attribute.String(attrMode, modeRequestLabel),
	)
	o.requests.Add(ctx, 1, attrs)
	if isError(e.StatusCode, e.Err) {
		o.errors.Add(ctx, 1, attrs)
	}
	o.duration.Record(ctx, e.Duration.Seconds(), attrs)
	o.bytes.Record(ctx, e.ResponseBytes, metric.WithAttributes(
		attribute.String(attrMethod, e.Request.Method),
		attribute.String(attrStatus, status),
	))
}

// OnStreamResponse implements [Observer]. Records the request (rate), any error,
// and the time-to-first-byte duration for a streaming request. Response size is
// not recorded: the streamed byte count is unknown until the caller reads the
// body.
func (o *RequestObserver) OnStreamResponse(ctx context.Context, e *opensearchtransport.StreamResponseEvent) {
	status := o.statusClassifier(e.StatusCode)
	attrs := metric.WithAttributes(
		attribute.String(attrMethod, e.Request.Method),
		attribute.String(attrStatus, status),
		attribute.String(attrMode, modeStreamLabel),
	)
	o.requests.Add(ctx, 1, attrs)
	if isError(e.StatusCode, e.Err) {
		o.errors.Add(ctx, 1, attrs)
	}
	o.duration.Record(ctx, e.Duration.Seconds(), attrs)
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
