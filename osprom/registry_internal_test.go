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
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

// runRegistry starts reg.Run in the background and returns a stop func that
// closes the registry and waits for Run to return.
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

	promReg := prometheus.NewRegistry()
	ro := NewRequestObserver()
	reg, err := New(promReg, ro)
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

	// Stopping the registry drains the buffer, so the observation is recorded by
	// the time Close returns.
	stop()

	require.Equal(t, 1, testutil.CollectAndCount(ro.duration), "one duration observation")
	require.Equal(t, 1, testutil.CollectAndCount(ro.bytes), "one size observation")
	require.InDelta(t, 0.0, testutil.ToFloat64(reg.dropped), 0, "no drops on an unsaturated buffer")
}

func TestRegistryFanOutToMultipleObservers(t *testing.T) {
	promReg := prometheus.NewRegistry()
	ro := NewRequestObserver()
	counter := &countingObserver{}
	reg, err := New(promReg, ro, counter)
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

	require.Equal(t, 1, testutil.CollectAndCount(ro.duration), "prom observer saw the event")
	require.Equal(t, 1, counter.count(), "custom observer saw the same event")
}

func TestBufferSizeDefaultAndOverride(t *testing.T) {
	t.Run("default scales with GOMAXPROCS", func(t *testing.T) {
		reg, err := New(prometheus.NewRegistry())
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, defaultBufferSize(), cap(reg.ch))
		require.Positive(t, cap(reg.ch), "default buffer is never zero")
	})

	t.Run("WithBufferSize overrides", func(t *testing.T) {
		reg, err := NewWithOptions(prometheus.NewRegistry(), nil, []Option{WithBufferSize(7)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, 7, cap(reg.ch))
	})

	t.Run("non-positive override falls back to default", func(t *testing.T) {
		reg, err := NewWithOptions(prometheus.NewRegistry(), nil, []Option{WithBufferSize(0)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = reg.Close() })
		require.Equal(t, defaultBufferSize(), cap(reg.ch), "0 is ignored, default applies")
	})
}

func TestRegistryOverflowIncrementsDropped(t *testing.T) {
	promReg := prometheus.NewRegistry()
	reg, err := NewWithOptions(promReg, []Observer{NewRequestObserver()}, []Option{WithBufferSize(1)})
	require.NoError(t, err)

	// Do NOT run the dispatch loop: with a buffer of 1, the first event fills the
	// buffer and every subsequent enqueue drops.
	const n = 50
	for range n {
		reg.OnStreamResponse(context.Background(), opensearchtransport.StreamResponseEvent{
			ResponseEvent: opensearchtransport.ResponseEvent{
				Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
				StatusCode: http.StatusOK,
			},
			Duration: time.Millisecond,
		})
	}

	require.Positive(t, testutil.ToFloat64(reg.dropped), "a saturated buffer drops events")
	require.NoError(t, reg.Close())
}

func TestWithStatusClassifier(t *testing.T) {
	// The classifier is a RequestObserver option; verify the emitted "status"
	// label reflects the default vs a custom classifier end-to-end.
	tests := []struct {
		name       string
		opts       []RequestObserverOption
		code       int
		wantStatus string
	}{
		{name: "default 2xx", code: http.StatusOK, wantStatus: "2xx"},
		{name: "default 5xx", code: http.StatusServiceUnavailable, wantStatus: "5xx"},
		{name: "default error (no response)", code: 0, wantStatus: "error"},
		{
			name:       "custom collapses",
			opts:       []RequestObserverOption{WithStatusClassifier(func(code int) string { return "all" })},
			code:       http.StatusNotFound,
			wantStatus: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := NewRequestObserver(tt.opts...)
			ro.OnRequestResponse(&opensearchtransport.RequestResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
					StatusCode: tt.code,
				},
				Duration: time.Millisecond,
			})
			require.Equal(t, 1, testutil.CollectAndCount(ro.duration.MustCurryWith(
				prometheus.Labels{"method": http.MethodGet, "status": tt.wantStatus, "mode": "request"})),
				"a series with the expected status label was recorded")
		})
	}
}

func TestRequestFilterSkipsUnrecorded(t *testing.T) {
	promReg := prometheus.NewRegistry()
	ro := NewRequestObserver()
	// Record only 5xx; drop everything else before it crosses the channel.
	reg, err := NewWithOptions(promReg, []Observer{ro}, []Option{
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
	fire(http.StatusBadRequest)         // filtered out
	stop()

	require.Equal(t, 1, testutil.CollectAndCount(ro.duration), "only the 5xx event was recorded")
	require.InDelta(t, 0.0, testutil.ToFloat64(reg.dropped), 0, "filtered events are not counted as drops")
}

func TestOverflowHandlersFireWithMetric(t *testing.T) {
	// Both entry points share the drop path; each fires its own typed handler
	// in addition to the built-in counter. Drive both from one table.
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
			var calls, queue int
			var gotEvent bool
			// Buffer of 1 fills on the first event; do NOT run the dispatch loop,
			// so every subsequent enqueue drops.
			reg, err := NewWithOptions(prometheus.NewRegistry(), []Observer{NewRequestObserver()},
				append([]Option{WithBufferSize(1)}, tt.options(&calls, &queue, &gotEvent)...))
			require.NoError(t, err)
			t.Cleanup(func() { _ = reg.Close() })

			for range 10 {
				tt.fire(reg)
			}

			require.Positive(t, calls, "overflow handler fires on drop")
			require.LessOrEqual(t, queue, 1, "queueLen is at most the configured buffer size")
			require.True(t, gotEvent, "handler receives the dropped event pointer")
			require.Positive(t, testutil.ToFloat64(reg.dropped),
				"built-in counter ticks in addition to the handler")
		})
	}
}

func TestRegistryCloseIdempotent(t *testing.T) {
	reg, err := New(prometheus.NewRegistry())
	require.NoError(t, err)
	stop := runRegistry(t, reg)
	stop()
	require.NoError(t, reg.Close(), "second Close is a no-op")
}

func TestRegistryRunExitsOnContextCancel(t *testing.T) {
	reg, err := New(prometheus.NewRegistry())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var runErr error
	wg.Go(func() { runErr = reg.Run(ctx) })

	cancel()
	wg.Wait()
	require.ErrorIs(t, runErr, context.Canceled, "Run returns ctx.Err on cancellation")
}

func TestStatusClass(t *testing.T) {
	tests := []struct {
		name string
		code int
		want string
	}{
		{"no response", 0, "error"},
		{"200", 200, "2xx"},
		{"201", 201, "2xx"},
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

func TestRequestObserverREDCounters(t *testing.T) {
	// method, status, mode -> (wantRequests, wantErrors) after firing the events below.
	type want struct {
		method, status, mode string
		requests, errors     float64
	}
	events := []struct {
		mode   string // "request" or "stream"
		method string
		code   int
	}{
		{"request", http.MethodGet, http.StatusOK},
		{"request", http.MethodGet, http.StatusInternalServerError},
		{"request", http.MethodGet, http.StatusInternalServerError},
		{"stream", http.MethodPost, http.StatusOK},
		{"stream", http.MethodPost, http.StatusTooManyRequests},
	}
	wants := []want{
		{http.MethodGet, "2xx", "request", 1, 0},
		{http.MethodGet, "5xx", "request", 2, 2},
		{http.MethodPost, "2xx", "stream", 1, 0},
		{http.MethodPost, "4xx", "stream", 1, 1},
	}

	ro := NewRequestObserver()
	for _, e := range events {
		switch e.mode {
		case "request":
			ro.OnRequestResponse(&opensearchtransport.RequestResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: e.method},
					StatusCode: e.code,
				},
				Duration: time.Millisecond,
			})
		case "stream":
			ro.OnStreamResponse(&opensearchtransport.StreamResponseEvent{
				ResponseEvent: opensearchtransport.ResponseEvent{
					Request:    opensearchtransport.RequestEvent{Method: e.method},
					StatusCode: e.code,
				},
				Duration: time.Millisecond,
			})
		}
	}

	for _, w := range wants {
		t.Run(w.method+"_"+w.status+"_"+w.mode, func(t *testing.T) {
			require.InDelta(t, w.requests, testutil.ToFloat64(ro.requests.WithLabelValues(w.method, w.status, w.mode)), 0, "requests_total (rate)")
			require.InDelta(t, w.errors, testutil.ToFloat64(ro.errors.WithLabelValues(w.method, w.status, w.mode)), 0, "request_errors_total")
		})
	}
}

func TestPoolObserverUSE(t *testing.T) {
	po := NewPoolObserver()

	po.OnOverloadDetected(&opensearchtransport.ConnectionEvent{
		PoolName: "search", ActiveCount: 2, DeadCount: 1, StandbyCount: 0,
	})
	po.OnDemote(&opensearchtransport.ConnectionEvent{
		PoolName: "search", ActiveCount: 1, DeadCount: 2, StandbyCount: 0,
	})
	po.OnHealthCheckFail(&opensearchtransport.ConnectionEvent{PoolName: "search"})

	// Utilization reflects the most recent snapshot (from OnDemote).
	require.InDelta(t, 1.0, testutil.ToFloat64(po.connections.WithLabelValues("search", "active")), 0)
	require.InDelta(t, 2.0, testutil.ToFloat64(po.connections.WithLabelValues("search", "dead")), 0)
	// Saturation + errors.
	require.InDelta(t, 1.0, testutil.ToFloat64(po.overloaded.WithLabelValues("search")), 0)
	require.InDelta(t, 1.0, testutil.ToFloat64(po.demotions.WithLabelValues("search")), 0)
	require.InDelta(t, 1.0, testutil.ToFloat64(po.healthFails.WithLabelValues("search")), 0)
}

func TestRegistryForwardsLifecycleToSinks(t *testing.T) {
	promReg := prometheus.NewRegistry()
	po := NewPoolObserver()
	reg, err := New(promReg, po)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })

	// Lifecycle events are fanned out synchronously (not through the channel),
	// so no Run loop is needed to observe them.
	reg.OnDemote(opensearchtransport.ConnectionEvent{PoolName: "write", ActiveCount: 3, DeadCount: 1})
	require.InDelta(t, 1.0, testutil.ToFloat64(po.demotions.WithLabelValues("write")), 0)
	require.InDelta(t, 3.0, testutil.ToFloat64(po.connections.WithLabelValues("write", "active")), 0)
}

func TestSetObserversSwapsFanOut(t *testing.T) {
	promReg := prometheus.NewRegistry()
	before := &countingObserver{}
	reg, err := New(promReg, before)
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

	// Swap in a different observer; the old one must stop receiving events.
	after := &countingObserver{}
	reg.SetObservers(after)
	fire()
	require.Eventually(t, func() bool { return after.count() == 1 }, time.Second, 5*time.Millisecond)
	require.Equal(t, 1, before.count(), "swapped-out observer receives no further events")
}

// TestSetObserversRace exercises SetObservers concurrently with dispatch to
// prove the atomic swap is race-free (run with -race).
func TestSetObserversRace(t *testing.T) {
	reg, err := New(prometheus.NewRegistry())
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

func (o *countingObserver) OnRequestResponse(*opensearchtransport.RequestResponseEvent) {
	o.n.Add(1)
}

func (o *countingObserver) OnStreamResponse(*opensearchtransport.StreamResponseEvent) {
	o.n.Add(1)
}

func (o *countingObserver) count() int {
	return int(o.n.Load())
}
