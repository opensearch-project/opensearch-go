// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

// newTestMeter returns a Meter backed by a ManualReader plus a collect func that
// gathers the current metrics for assertions.
func newTestMeter(t *testing.T) (*metric.MeterProvider, func() metricdata.ResourceMetrics) {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	return mp, func() metricdata.ResourceMetrics {
		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(context.Background(), &rm))
		return rm
	}
}

// metricNames flattens the collected metric names for presence assertions.
func metricNames(rm metricdata.ResourceMetrics) []string {
	var names []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names = append(names, m.Name)
		}
	}
	return names
}

func runRegistry(t *testing.T, reg *Registry) func() {
	t.Helper()
	var wg sync.WaitGroup
	wg.Go(func() { _ = reg.Run(context.Background()) })
	return func() {
		require.NoError(t, reg.Close())
		wg.Wait()
	}
}

func TestRegistryRecordsRequestResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(ts.Close)

	mp, collect := newTestMeter(t)
	reg, err := New(mp.Meter("test"), 16, NewRequestObserver())
	require.NoError(t, err)
	stop := runRegistry(t, reg)

	tp, err := opensearchtransport.New(opensearchtransport.Config{
		URLs:     []*url.URL{mustURL(t, ts.URL)},
		Observer: reg,
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	res, err := tp.Request(req)
	require.NoError(t, err)
	_ = res.Body.Close()

	stop() // drains buffered events before returning

	names := metricNames(collect())
	require.Contains(t, names, "opensearch.client.request.duration")
	require.Contains(t, names, "opensearch.client.response.size")
}

func TestRegistryFanOutToMultipleObservers(t *testing.T) {
	mp, collect := newTestMeter(t)
	counter := &countingObserver{}
	reg, err := New(mp.Meter("test"), 16, NewRequestObserver(), counter)
	require.NoError(t, err)
	stop := runRegistry(t, reg)

	reg.OnRequestResponse(opensearchtransport.RequestResponseEvent{
		ResponseEvent: opensearchtransport.ResponseEvent{
			Request:    opensearchtransport.RequestEvent{Method: http.MethodPost},
			StatusCode: http.StatusOK,
		},
		Duration:      3 * time.Millisecond,
		ResponseBytes: 128,
	})
	stop()

	require.Contains(t, metricNames(collect()), "opensearch.client.request.duration")
	require.Equal(t, 1, counter.count(), "custom observer saw the same event")
}

func TestRegistryOverflowDrops(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := New(mp.Meter("test"), 1, NewRequestObserver())
	require.NoError(t, err)

	// Do not run the dispatch loop: with a buffer of 1, subsequent enqueues drop.
	require.NotPanics(t, func() {
		for range 50 {
			reg.OnStreamResponse(opensearchtransport.StreamResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
					StatusCode: http.StatusOK,
				},
				Duration: time.Millisecond,
			})
		}
	})
	require.NoError(t, reg.Close())
}

func TestRegistryCloseIdempotent(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := New(mp.Meter("test"), 4)
	require.NoError(t, err)
	stop := runRegistry(t, reg)
	stop()
	require.NoError(t, reg.Close(), "second Close is a no-op")
}

func TestRegistryRunExitsOnContextCancel(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := New(mp.Meter("test"), 4)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var runErr error
	wg.Go(func() { runErr = reg.Run(ctx) })
	cancel()
	wg.Wait()
	require.ErrorIs(t, runErr, context.Canceled)
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{0: "error", 200: "2xx", 404: "4xx", 503: "5xx", 700: "unknown"}
	for code, want := range cases {
		require.Equal(t, want, statusClass(code))
	}
}

// countingObserver is a minimal custom Observer proving arbitrary bundles can be
// wired into a Registry. Its counter is mutex-guarded for the dispatch goroutine.
type countingObserver struct {
	BaseObserver
	mu sync.Mutex
	n  int
}

func (o *countingObserver) OnRequest(context.Context, RequestSample) {
	o.mu.Lock()
	o.n++
	o.mu.Unlock()
}

func (o *countingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}
