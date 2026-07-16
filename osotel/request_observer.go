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
)

// RequestObserver records request duration and response size as OpenTelemetry
// histograms, attributed by method, status class, and mode (request vs stream).
// Wire it into a [Registry]; it satisfies [Observer].
type RequestObserver struct {
	BaseObserver

	duration metric.Float64Histogram
	bytes    metric.Int64Histogram
}

// NewRequestObserver returns a RequestObserver. Its instruments are created when
// it is wired into a [Registry] via [New]; do not create them directly.
func NewRequestObserver() *RequestObserver {
	return &RequestObserver{}
}

// Register implements [Observer].
func (o *RequestObserver) Register(meter metric.Meter) error {
	var err error
	o.duration, err = meter.Float64Histogram(
		"opensearch.client.request.duration",
		metric.WithDescription("Duration of OpenSearch client requests (request=full-read, stream=time-to-first-byte)."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	o.bytes, err = meter.Int64Histogram(
		"opensearch.client.response.size",
		metric.WithDescription("Size of buffered OpenSearch client response bodies."),
		metric.WithUnit("By"),
	)
	return err
}

// OnRequest implements [Observer]. Response size is recorded only for buffered
// requests (ModeRequest), where the exact byte count is known.
func (o *RequestObserver) OnRequest(ctx context.Context, s RequestSample) {
	o.duration.Record(ctx, s.Seconds, metric.WithAttributes(
		attribute.String("method", s.Method),
		attribute.String("status", s.StatusClass),
		attribute.String("mode", s.Mode.String()),
	))
	if s.Mode == ModeRequest {
		o.bytes.Record(ctx, s.Bytes, metric.WithAttributes(
			attribute.String("method", s.Method),
			attribute.String("status", s.StatusClass),
		))
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
