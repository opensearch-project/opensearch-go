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

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// Attribute names specific to the bulk indexer.
const (
	attrResult   = "result"
	attrScope    = "scope"
	attrKind     = "kind"
	attrRetrying = "retrying"
	attrOutcome  = "outcome"

	resultOK    = "ok"
	resultError = "error"

	outcomeSucceeded = "succeeded"
	outcomeFailed    = "failed"
)

// BulkIndexerObserver records what an [opensearchutil.BulkIndexer] does as
// OpenTelemetry metrics. It implements [opensearchutil.BulkIndexerObserver], so
// set it as the indexer's Observer; it is independent of [Registry] and of the
// transport. Every attribute is low-cardinality (result, scope, kind, retrying,
// outcome); none carries an index name or a document id.
//
//   - Rate and errors of the bulk requests: a counter by result (ok when the
//     cluster answered, error when the whole request failed), plus histograms of
//     request duration and body size.
//   - Failures: a counter by scope (request or item), kind (rejected,
//     unavailable, transport, permanent) and whether the retry policy retried it.
//   - Retries: a counter of items sent again, by kind.
//   - Items: a counter of items that reached a final state, by outcome.
//
// Each call is recorded on the context the indexer's flush runs on, so a tracing
// SDK can attach the flush's span as an exemplar. The methods run on the
// indexer's worker goroutines and only record instruments, which do not block.
type BulkIndexerObserver struct {
	opensearchutil.BaseBulkIndexerObserver

	requests metric.Int64Counter     // bulk requests by result
	duration metric.Float64Histogram // bulk request latency
	bytes    metric.Int64Histogram   // bulk request body size
	failures metric.Int64Counter     // failed attempts by scope, kind, retrying
	retries  metric.Int64Counter     // items sent again, by kind
	items    metric.Int64Counter     // items settled, by outcome
}

// NewBulkIndexerObserver returns a BulkIndexerObserver. Call
// [BulkIndexerObserver.Register] with a meter to create its instruments, then
// set it as [opensearchutil.BulkIndexerConfig].Observer.
func NewBulkIndexerObserver() *BulkIndexerObserver {
	return &BulkIndexerObserver{}
}

// Register creates the observer's instruments from meter.
func (o *BulkIndexerObserver) Register(meter metric.Meter) error {
	var err error
	if o.requests, err = meter.Int64Counter(
		instrumentPrefix+"bulk.requests",
		metric.WithDescription("Bulk requests the indexer sent, attributed ok when the cluster answered and error when the whole "+
			"request failed (RED: rate, errors)."),
		metric.WithUnit("{request}"),
	); err != nil {
		return err
	}
	if o.duration, err = meter.Float64Histogram(
		instrumentPrefix+"bulk.request.duration",
		metric.WithDescription("Duration of bulk requests the indexer sent (RED: duration)."),
		metric.WithUnit("s"),
	); err != nil {
		return err
	}
	if o.bytes, err = meter.Int64Histogram(
		instrumentPrefix+"bulk.request.size",
		metric.WithDescription("Body size of bulk requests the indexer sent."),
		metric.WithUnit("By"),
	); err != nil {
		return err
	}
	if o.failures, err = meter.Int64Counter(
		instrumentPrefix+"bulk.failures",
		metric.WithDescription("Failed attempts of a bulk request or of an item, by scope, failure kind, and whether the retry "+
			"policy retried it."),
		metric.WithUnit("{failure}"),
	); err != nil {
		return err
	}
	if o.retries, err = meter.Int64Counter(
		instrumentPrefix+"bulk.retries",
		metric.WithDescription("Items the indexer sent again after a failure, by failure kind."),
		metric.WithUnit("{item}"),
	); err != nil {
		return err
	}
	o.items, err = meter.Int64Counter(
		instrumentPrefix+"bulk.items",
		metric.WithDescription("Items that reached a final state, by outcome."),
		metric.WithUnit("{item}"),
	)
	return err
}

// OnRequestEnd implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnRequestEnd(ctx context.Context, e opensearchutil.BulkRequestEvent) {
	result := resultOK
	if e.Err != nil {
		result = resultError
	}
	o.requests.Add(ctx, 1, metric.WithAttributes(attribute.String(attrResult, result)))
	o.duration.Record(ctx, e.Duration.Seconds())
	o.bytes.Record(ctx, int64(e.Bytes))
}

// OnRequestFailure implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnRequestFailure(ctx context.Context, e opensearchutil.BulkFailureEvent) {
	o.recordFailure(ctx, e)
}

// OnItemFailure implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnItemFailure(ctx context.Context, e opensearchutil.BulkFailureEvent) {
	o.recordFailure(ctx, e)
}

func (o *BulkIndexerObserver) recordFailure(ctx context.Context, e opensearchutil.BulkFailureEvent) {
	o.failures.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrScope, e.Scope.String()),
		attribute.String(attrKind, e.Kind.String()),
		attribute.String(attrRetrying, strconv.FormatBool(e.Retrying)),
	))
	if e.Retrying {
		o.retries.Add(ctx, int64(e.Items), metric.WithAttributes(attribute.String(attrKind, e.Kind.String())))
	}
}

// OnItemSettled implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnItemSettled(ctx context.Context, e opensearchutil.BulkItemEvent) {
	outcome := outcomeSucceeded
	if e.Failed {
		outcome = outcomeFailed
	}
	o.items.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOutcome, outcome)))
}
