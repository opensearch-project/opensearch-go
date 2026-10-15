// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

func TestBulkIndexerObserverRecordsEvents(t *testing.T) {
	t.Parallel()

	o := NewBulkIndexerObserver()
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

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"requests ok", testutil.ToFloat64(o.requests.WithLabelValues("ok")), 1},
		{"requests error", testutil.ToFloat64(o.requests.WithLabelValues("error")), 1},
		{"request failure retried", testutil.ToFloat64(o.failures.WithLabelValues("request", "transport", "true")), 1},
		{"item failure retried", testutil.ToFloat64(o.failures.WithLabelValues("item", "rejected", "true")), 1},
		{"item failure final", testutil.ToFloat64(o.failures.WithLabelValues("item", "permanent", "false")), 1},
		{"retries by kind counts items, request scope", testutil.ToFloat64(o.retries.WithLabelValues("transport")), 3},
		{"retries by kind counts items, item scope", testutil.ToFloat64(o.retries.WithLabelValues("rejected")), 1},
		{"a final failure is not a retry", testutil.ToFloat64(o.retries.WithLabelValues("permanent")), 0},
		{"items succeeded", testutil.ToFloat64(o.items.WithLabelValues("succeeded")), 1},
		{"items failed", testutil.ToFloat64(o.items.WithLabelValues("failed")), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.InDelta(t, tt.want, tt.got, 0)
		})
	}

	require.Equal(t, 1, testutil.CollectAndCount(o.duration), "duration histogram")
	require.Equal(t, 1, testutil.CollectAndCount(o.bytes), "size histogram")
}

func TestBulkIndexerObserverRegister(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	require.NoError(t, NewBulkIndexerObserver().Register(reg))
	require.Error(t, NewBulkIndexerObserver().Register(reg), "the same metrics cannot be registered twice")
}

// TestBulkIndexerObserverWithIndexer drives a real BulkIndexer against a server
// that refuses the first bulk request and accepts the second, and checks the
// metrics that fall out.
func TestBulkIndexerObserverWithIndexer(t *testing.T) {
	t.Parallel()

	var bulks atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = io.WriteString(w, `{"version":{"number":"3.0.0","distribution":"opensearch"}}`)
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			_, _ = io.Copy(io.Discard, r.Body)
			if bulks.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":{"type":"es_rejected_execution_exception","reason":"full"},"status":429}`)
				return
			}
			_, _ = io.WriteString(w, `{"took":1,"errors":false,"items":[{"index":{"_id":"a","status":201}}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
		Addresses:    []string{srv.URL},
		DisableRetry: true,
	}})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	o := NewBulkIndexerObserver()
	bi, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		Observer:   o,
		RetryPolicy: opensearchutil.RetryPolicyFunc(func(
			attempt int, _ opensearchutil.FailureScope, _ opensearchutil.FailureKind, _ opensearchapi.BulkRespItem, _ error,
		) (time.Duration, bool) {
			return time.Nanosecond, attempt < 3
		}),
	})
	require.NoError(t, err)

	require.NoError(t, bi.Add(t.Context(), opensearchutil.BulkIndexerItem{
		Action: "index", DocumentID: "a", Body: strings.NewReader(`{"a":1}`),
		OnFailure: func(context.Context, opensearchutil.BulkIndexerItem, opensearchapi.BulkRespItem, error) {},
	}))
	require.NoError(t, bi.Close(t.Context()))

	require.InDelta(t, 1, testutil.ToFloat64(o.requests.WithLabelValues("error")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(o.requests.WithLabelValues("ok")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(o.failures.WithLabelValues("request", "rejected", "true")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(o.retries.WithLabelValues("rejected")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(o.items.WithLabelValues("succeeded")), 0)
	require.InDelta(t, 0, testutil.ToFloat64(o.items.WithLabelValues("failed")), 0)
}
