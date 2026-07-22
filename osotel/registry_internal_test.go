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
	"sync/atomic"
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
	reg, err := New(mp.Meter("test"), NewRequestObserver())
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
	reg, err := New(mp.Meter("test"), NewRequestObserver(), counter)
	require.NoError(t, err)
	stop := runRegistry(t, reg)

	reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
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

func TestBufferSizeDefaultAndOverride(t *testing.T) {
	mp, _ := newTestMeter(t)

	t.Run("default scales with GOMAXPROCS", func(t *testing.T) {
		reg, err := New(mp.Meter("test"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, defaultBufferSize(), cap(reg.ch))
		require.Positive(t, cap(reg.ch))
	})

	t.Run("WithBufferSize overrides", func(t *testing.T) {
		reg, err := NewWithOptions(mp.Meter("test"), nil, []Option{WithBufferSize(7)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, 7, cap(reg.ch))
	})

	t.Run("non-positive override falls back to default", func(t *testing.T) {
		reg, err := NewWithOptions(mp.Meter("test"), nil, []Option{WithBufferSize(0)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, defaultBufferSize(), cap(reg.ch))
	})
}

func TestRegistryOverflowDrops(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := NewWithOptions(mp.Meter("test"), []Observer{NewRequestObserver()}, []Option{WithBufferSize(1)})
	require.NoError(t, err)

	// Do not run the dispatch loop: with a buffer of 1, subsequent enqueues drop.
	require.NotPanics(t, func() {
		for range 50 {
			reg.OnStreamResponse(context.Background(), opensearchtransport.StreamResponseEvent{
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

func TestWithStatusClassifier(t *testing.T) {
	// The classifier is a RequestObserver option; verify the emitted "status"
	// attribute on the requests counter reflects the default vs a custom
	// classifier.
	tests := []struct {
		name       string
		opts       []RequestObserverOption
		code       int
		wantStatus string
	}{
		{name: "default 2xx", code: http.StatusOK, wantStatus: "2xx"},
		{name: "default 5xx", code: http.StatusServiceUnavailable, wantStatus: "5xx"},
		{name: "default error", code: 0, wantStatus: "error"},
		{
			name:       "custom collapses",
			opts:       []RequestObserverOption{WithStatusClassifier(func(int) string { return "all" })},
			code:       http.StatusNotFound,
			wantStatus: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp, collect := newTestMeter(t)
			ro := NewRequestObserver(tt.opts...)
			reg, err := New(mp.Meter("test"), ro)
			require.NoError(t, err)
			stop := runRegistry(t, reg)

			reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
					StatusCode: tt.code,
				},
				Duration: time.Millisecond,
			})
			stop()

			require.Equal(t, tt.wantStatus, counterStatusAttr(t, collect(), "opensearch.client.requests"),
				"requests counter carries the classified status attribute")
		})
	}
}

// counterStatusAttr returns the "status" attribute of the first data point of
// the named Int64 sum metric.
func counterStatusAttr(t *testing.T, rm metricdata.ResourceMetrics, name string) string {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %s is an Int64 sum", name)
			require.NotEmpty(t, sum.DataPoints, "metric %s has a data point", name)
			v, ok := sum.DataPoints[0].Attributes.Value(attrStatus)
			require.True(t, ok, "data point has a status attribute")
			return v.AsString()
		}
	}
	t.Fatalf("metric %s not found", name)
	return ""
}

func TestRequestObserverREDInstruments(t *testing.T) {
	mp, collect := newTestMeter(t)
	ro := NewRequestObserver()
	reg, err := New(mp.Meter("test"), ro)
	require.NoError(t, err)
	stop := runRegistry(t, reg)

	// One success and two failures.
	for _, code := range []int{http.StatusOK, http.StatusInternalServerError, http.StatusInternalServerError} {
		reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
			ResponseEvent: opensearchtransport.ResponseEvent{
				Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
				StatusCode: code,
			},
			Duration: time.Millisecond,
		})
	}
	stop()

	names := metricNames(collect())
	require.Contains(t, names, "opensearch.client.requests")       // R
	require.Contains(t, names, "opensearch.client.request.errors") // E
	require.Contains(t, names, "opensearch.client.request.duration")
	require.Contains(t, names, "opensearch.client.response.size")
}

func TestPoolObserverUSEInstruments(t *testing.T) {
	mp, collect := newTestMeter(t)
	po := NewPoolObserver()
	reg, err := New(mp.Meter("test"), po)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })

	// Lifecycle events are fanned out synchronously; no Run loop needed.
	reg.OnOverloadDetected(opensearchtransport.ConnectionEvent{PoolName: "search", ActiveCount: 2, DeadCount: 1})
	reg.OnDemote(opensearchtransport.ConnectionEvent{PoolName: "search", ActiveCount: 1, DeadCount: 2})
	reg.OnHealthCheckFail(opensearchtransport.ConnectionEvent{PoolName: "search"})

	names := metricNames(collect())
	require.Contains(t, names, "opensearch.client.pool.connections")           // U
	require.Contains(t, names, "opensearch.client.pool.overloaded")            // S
	require.Contains(t, names, "opensearch.client.pool.demotions")             // E
	require.Contains(t, names, "opensearch.client.pool.health_check_failures") // E
}

func TestRequestFilterSkipsUnrecorded(t *testing.T) {
	mp, collect := newTestMeter(t)
	ro := NewRequestObserver()
	reg, err := NewWithOptions(mp.Meter("test"), []Observer{ro}, []Option{
		WithRequestFilter(func(e *opensearchtransport.RequestResponseEvent) bool {
			return e.StatusCode >= 500
		}),
	})
	require.NoError(t, err)
	stop := runRegistry(t, reg)

	fire := func(code int) {
		reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
			ResponseEvent: opensearchtransport.ResponseEvent{
				Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
				StatusCode: code,
			},
			Duration: time.Millisecond,
		})
	}
	fire(http.StatusOK)                 // filtered out
	fire(http.StatusServiceUnavailable) // recorded
	stop()

	// Only the 5xx crossed the channel: the requests counter has a single point.
	require.Equal(t, "5xx", counterStatusAttr(t, collect(), "opensearch.client.requests"),
		"only the 5xx request was recorded")
}

func TestOverflowHandlersFire(t *testing.T) {
	// Both entry points share the drop path; each fires its own typed handler.
	tests := []struct {
		name    string
		options func(calls, queue *int, gotEvent *bool) []Option
		fire    func(reg *Registry)
	}{
		{
			name: "request",
			options: func(calls, queue *int, gotEvent *bool) []Option {
				return []Option{WithOverflowHandler(func(q int, d *opensearchtransport.RequestResponseEvent) {
					*calls++
					*queue = q
					*gotEvent = d != nil
				})}
			},
			fire: func(reg *Registry) {
				reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
					ResponseEvent: opensearchtransport.ResponseEvent{
						Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
						StatusCode: http.StatusOK,
					},
					Duration: time.Millisecond,
				})
			},
		},
		{
			name: "stream",
			options: func(calls, queue *int, gotEvent *bool) []Option {
				return []Option{WithStreamOverflowHandler(func(q int, d *opensearchtransport.StreamResponseEvent) {
					*calls++
					*queue = q
					*gotEvent = d != nil
				})}
			},
			fire: func(reg *Registry) {
				reg.OnStreamResponse(context.Background(), opensearchtransport.StreamResponseEvent{
					ResponseEvent: opensearchtransport.ResponseEvent{
						Request:    opensearchtransport.RequestEvent{Method: http.MethodPost},
						StatusCode: http.StatusOK,
					},
					Duration: time.Millisecond,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp, _ := newTestMeter(t)
			var calls, queue int
			var gotEvent bool
			// Buffer of 1 fills on the first event; do NOT run the dispatch loop.
			reg, err := NewWithOptions(mp.Meter("test"), []Observer{NewRequestObserver()},
				append([]Option{WithBufferSize(1)}, tt.options(&calls, &queue, &gotEvent)...))
			require.NoError(t, err)
			t.Cleanup(func() { _ = reg.Close() })

			for range 10 {
				tt.fire(reg)
			}

			require.Positive(t, calls, "overflow handler fires on drop")
			require.LessOrEqual(t, queue, 1, "queueLen is at most the configured buffer size")
			require.True(t, gotEvent, "handler receives the dropped event pointer")
		})
	}
}

func TestRegistryRunExitsOnContextCancel(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := New(mp.Meter("test"))
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
	tests := []struct {
		name string
		code int
		want string
	}{
		{"no response", 0, "error"},
		{"200", 200, "2xx"},
		{"404", 404, "4xx"},
		{"503", 503, "5xx"},
		{"out of range", 700, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, statusClass(tt.code))
		})
	}
}

func TestSetObserversSwapsFanOut(t *testing.T) {
	mp, _ := newTestMeter(t)
	before := &countingObserver{}
	reg, err := New(mp.Meter("test"), before)
	require.NoError(t, err)
	stop := runRegistry(t, reg)
	t.Cleanup(stop)

	fire := func() {
		reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
			ResponseEvent: opensearchtransport.ResponseEvent{
				Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
				StatusCode: http.StatusOK,
			},
			Duration: time.Millisecond,
		})
	}

	fire()
	require.Eventually(t, func() bool { return before.count() == 1 }, time.Second, 5*time.Millisecond)

	after := &countingObserver{}
	reg.SetObservers(after)
	fire()
	require.Eventually(t, func() bool { return after.count() == 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, 1, before.count(), "swapped-out observer receives no further events")
}

// TestSetObserversRace exercises SetObservers concurrently with dispatch to
// prove the atomic swap is race-free (run with -race).
func TestSetObserversRace(t *testing.T) {
	mp, _ := newTestMeter(t)
	reg, err := New(mp.Meter("test"))
	require.NoError(t, err)
	stop := runRegistry(t, reg)
	t.Cleanup(stop)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			reg.SetObservers(&countingObserver{})
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			reg.OnRequestResponse(context.Background(), opensearchtransport.RequestResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
					StatusCode: http.StatusOK,
				},
				Duration: time.Millisecond,
			})
		}
	}()
	wg.Wait()
}

// countingObserver is a minimal custom Observer proving arbitrary sinks can be
// wired into a Registry. Its counter is atomic for the concurrent dispatch
// workers.
type countingObserver struct {
	BaseObserver
	n atomic.Int64
}

func (o *countingObserver) OnRequestResponse(context.Context, *opensearchtransport.RequestResponseEvent) {
	o.n.Add(1)
}

func (o *countingObserver) OnStreamResponse(context.Context, *opensearchtransport.StreamResponseEvent) {
	o.n.Add(1)
}

func (o *countingObserver) count() int {
	return int(o.n.Load())
}
