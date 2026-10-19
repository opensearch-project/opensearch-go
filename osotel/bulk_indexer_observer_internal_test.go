// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// counterValue returns the value of the Int64 sum named name whose data point
// carries every attribute in want, or 0 when none does.
func counterValue(t *testing.T, rm metricdata.ResourceMetrics, name string, want map[string]string) int64 {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %q is an Int64 sum", name)
			for _, dp := range sum.DataPoints {
				match := true
				for k, v := range want {
					got, found := dp.Attributes.Value(attribute.Key(k))
					if !found || got.AsString() != v {
						match = false
						break
					}
				}
				if match {
					return dp.Value
				}
			}
		}
	}

	return 0
}

func TestBulkIndexerObserverRecordsEvents(t *testing.T) {
	t.Parallel()

	mp, collect := newTestMeter(t)
	o := NewBulkIndexerObserver()
	require.NoError(t, o.Register(mp.Meter("test")))
	ctx := t.Context()

	o.OnRequestEnd(ctx, opensearchutil.BulkRequestEvent{Items: 3, Bytes: 2048, Duration: 250 * time.Millisecond})
	o.OnRequestEnd(ctx, opensearchutil.BulkRequestEvent{Items: 3, Bytes: 2048, Err: io.ErrUnexpectedEOF})
	o.OnRequestFailure(ctx, opensearchutil.BulkFailureEvent{
		Scope: opensearchutil.ScopeRequest, Kind: opensearchutil.FailureTransport, Items: 3, Retrying: true,
	})
	o.OnItemFailure(ctx, opensearchutil.BulkFailureEvent{
		Scope: opensearchutil.ScopeItem, Kind: opensearchutil.FailureRejected, Items: 1, Retrying: true,
	})
	o.OnItemFailure(ctx, opensearchutil.BulkFailureEvent{
		Scope: opensearchutil.ScopeItem, Kind: opensearchutil.FailurePermanent, Items: 1,
	})
	o.OnItemSettled(ctx, opensearchutil.BulkItemEvent{})
	o.OnItemSettled(ctx, opensearchutil.BulkItemEvent{Failed: true})
	o.OnItemSettled(ctx, opensearchutil.BulkItemEvent{Failed: true})

	rm := collect()
	const failures = "opensearch.client.bulk.failures"
	tests := []struct {
		name   string
		metric string
		attrs  map[string]string
		want   int64
	}{
		{"requests ok", "opensearch.client.bulk.requests", map[string]string{"result": "ok"}, 1},
		{"requests error", "opensearch.client.bulk.requests", map[string]string{"result": "error"}, 1},
		{"request failure retried", failures, map[string]string{"scope": "request", "kind": "transport", "retrying": "true"}, 1},
		{"item failure retried", failures, map[string]string{"scope": "item", "kind": "rejected", "retrying": "true"}, 1},
		{"item failure final", failures, map[string]string{"scope": "item", "kind": "permanent", "retrying": "false"}, 1},
		{"retries count items, request scope", "opensearch.client.bulk.retries", map[string]string{"kind": "transport"}, 3},
		{"retries count items, item scope", "opensearch.client.bulk.retries", map[string]string{"kind": "rejected"}, 1},
		{"a final failure is not a retry", "opensearch.client.bulk.retries", map[string]string{"kind": "permanent"}, 0},
		{"items succeeded", "opensearch.client.bulk.items", map[string]string{"outcome": "succeeded"}, 1},
		{"items failed", "opensearch.client.bulk.items", map[string]string{"outcome": "failed"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, counterValue(t, rm, tt.metric, tt.attrs))
		})
	}

	require.Contains(t, metricNames(rm), "opensearch.client.bulk.request.duration")
	require.Contains(t, metricNames(rm), "opensearch.client.bulk.request.size")
}

func TestBulkIndexerObserverRegisterTwice(t *testing.T) {
	t.Parallel()

	mp, _ := newTestMeter(t)
	o := NewBulkIndexerObserver()
	require.NoError(t, o.Register(mp.Meter("test")))
	require.NoError(t, o.Register(mp.Meter("test")), "OpenTelemetry returns the same instruments on a repeat registration")
}
