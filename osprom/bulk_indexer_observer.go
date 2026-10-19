// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"context"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// Metric label names specific to the bulk indexer.
const (
	labelResult   = "result"
	labelScope    = "scope"
	labelKind     = "kind"
	labelRetrying = "retrying"
	labelOutcome  = "outcome"

	resultOK    = "ok"
	resultError = "error"

	outcomeSucceeded = "succeeded"
	outcomeFailed    = "failed"
)

// BulkIndexerObserver records what an [opensearchutil.BulkIndexer] does as
// Prometheus metrics. It implements [opensearchutil.BulkIndexerObserver], so set
// it as the indexer's Observer; it is independent of [Registry] and of the
// transport. Every label is low-cardinality (result, scope, kind, retrying,
// outcome); none carries an index name or a document id.
//
//   - Rate and errors of the bulk requests: a counter by result (ok when the
//     cluster answered, error when the whole request failed), plus histograms of
//     request duration and body size.
//   - Failures: a counter by scope (request or item), kind (rejected,
//     unavailable, transport, permanent) and whether the retry policy retried it.
//   - Retries: a counter of items sent again, by kind.
//   - Items: a counter of items that reached a final state, by outcome, and a
//     histogram of their document sizes (bulk_item_size_bytes, in bytes, from
//     [opensearchutil.BulkItemEvent].Bytes: the document alone, without the action
//     line). It has no labels, so one series gives the average (sum over count) and,
//     in the upper buckets, the outliers.
//
// Its methods run on the indexer's worker goroutines and only update
// Prometheus collectors, which do not block.
type BulkIndexerObserver struct {
	opensearchutil.BaseBulkIndexerObserver

	requests *prometheus.CounterVec // bulk requests by result
	duration prometheus.Histogram   // bulk request latency
	bytes    prometheus.Histogram   // bulk request body size
	failures *prometheus.CounterVec // failed attempts by scope, kind, retrying
	retries  *prometheus.CounterVec // items sent again, by kind
	items    *prometheus.CounterVec // items settled, by outcome
	itemSize prometheus.Histogram   // document size of items settled
}

// NewBulkIndexerObserver returns a BulkIndexerObserver. Call
// [BulkIndexerObserver.Register] to add its collectors to a registry, then set
// it as [opensearchutil.BulkIndexerConfig].Observer.
func NewBulkIndexerObserver() *BulkIndexerObserver {
	return &BulkIndexerObserver{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_requests_total",
			Help: "Bulk requests the indexer sent, labeled ok when the cluster answered and error when the whole request " +
				"failed (RED: rate, errors).",
		}, []string{labelResult}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_request_duration_seconds",
			Help:      "Duration of bulk requests the indexer sent (RED: duration).",
			Buckets:   prometheus.DefBuckets,
		}),
		bytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_request_size_bytes",
			Help:      "Body size of bulk requests the indexer sent.",
			Buckets:   prometheus.ExponentialBuckets(1<<10, 4, 9), // 1 KiB .. 64 MiB
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_failures_total",
			Help:      "Failed attempts of a bulk request or of an item, by scope, failure kind, and whether the retry policy retried it.",
		}, []string{labelScope, labelKind, labelRetrying}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_retries_total",
			Help:      "Items the indexer sent again after a failure, by failure kind.",
		}, []string{labelKind}),
		items: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_items_total",
			Help:      "Items that reached a final state, by outcome.",
		}, []string{labelOutcome}),
		itemSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "bulk_item_size_bytes",
			Help: "Size of the document of each item that reached a final state, in bytes, excluding the action line. " +
				"Sum over count is the average document size; the upper buckets show outliers.",
			Buckets: []float64{100, 512, 1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20, 10 << 20},
		}),
	}
}

// Register adds the observer's collectors to reg.
func (o *BulkIndexerObserver) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{o.requests, o.duration, o.bytes, o.failures, o.retries, o.items, o.itemSize} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// OnRequestEnd implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnRequestEnd(_ context.Context, e opensearchutil.BulkRequestEvent) {
	result := resultOK
	if e.Err != nil {
		result = resultError
	}
	o.requests.WithLabelValues(result).Inc()
	o.duration.Observe(e.Duration.Seconds())
	o.bytes.Observe(float64(e.Bytes))
}

// OnRequestFailure implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnRequestFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	o.recordFailure(e)
}

// OnItemFailure implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnItemFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	o.recordFailure(e)
}

func (o *BulkIndexerObserver) recordFailure(e opensearchutil.BulkFailureEvent) {
	o.failures.WithLabelValues(e.Scope.String(), e.Kind.String(), strconv.FormatBool(e.Retrying)).Inc()
	if e.Retrying {
		o.retries.WithLabelValues(e.Kind.String()).Add(float64(e.Items))
	}
}

// OnItemSettled implements [opensearchutil.BulkIndexerObserver].
func (o *BulkIndexerObserver) OnItemSettled(_ context.Context, e opensearchutil.BulkItemEvent) {
	outcome := outcomeSucceeded
	if e.Failed {
		outcome = outcomeFailed
	}
	o.items.WithLabelValues(outcome).Inc()
	o.itemSize.Observe(float64(e.Bytes))
}
