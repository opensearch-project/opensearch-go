// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/debuglog"
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
	t.Cleanup(func() { _ = tp.Close() })

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

// debugRecord is one emitted lifecycle message plus the fields chained onto
// it before Msg, so a test can assert either the message text alone or that
// the chain reached the logger intact.
type debugRecord struct {
	msg    string
	fields string // "key=val" pairs in call order, space-separated
}

// captureDebugLogger records the lifecycle messages a Registry emits.
type captureDebugLogger struct {
	// mu guards records, appended to by every worker goroutine the Registry runs.
	mu struct {
		sync.Mutex
		records []debugRecord
	}
}

func (c *captureDebugLogger) Debug() debuglog.Event {
	return &captureEvent{c: c}
}

func (c *captureDebugLogger) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	msgs := make([]string, 0, len(c.mu.records))
	for _, r := range c.mu.records {
		msgs = append(msgs, r.msg)
	}
	return msgs
}

// fields returns the rendered field chain of the first record whose message
// equals msg, or "" if no such record was captured.
func (c *captureDebugLogger) fields(msg string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.mu.records {
		if r.msg == msg {
			return r.fields
		}
	}
	return ""
}

// captureEvent accumulates one record's chained fields. It belongs to the
// single caller building the chain, so it needs no lock of its own; only the
// append to the shared captureDebugLogger in Msg takes the mutex, which is
// what makes concurrent Debug() calls from the registry's workers safe.
type captureEvent struct {
	c      *captureDebugLogger
	fields []string
}

func (e *captureEvent) add(kv string) debuglog.Event {
	e.fields = append(e.fields, kv)
	return e
}

func (e *captureEvent) Str(key, val string) debuglog.Event { return e.add(key + "=" + val) }
func (e *captureEvent) Strs(key string, val []string) debuglog.Event {
	return e.add(key + "=" + strings.Join(val, ","))
}

func (e *captureEvent) Int(key string, val int) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%d", key, val))
}

func (e *captureEvent) Int32(key string, val int32) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%d", key, val))
}

func (e *captureEvent) Int64(key string, val int64) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%d", key, val))
}

func (e *captureEvent) Uint32(key string, val uint32) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%d", key, val))
}

func (e *captureEvent) Float64(key string, val float64) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%v", key, val))
}

func (e *captureEvent) Dur(key string, val time.Duration) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%s", key, val))
}

func (e *captureEvent) Time(key string, val time.Time) debuglog.Event {
	return e.add(fmt.Sprintf("%s=%s", key, val))
}

func (e *captureEvent) Stringer(key string, val fmt.Stringer) debuglog.Event {
	return e.add(key + "=" + debuglog.StringerText(val))
}

func (e *captureEvent) Err(err error) debuglog.Event {
	return e.add(fmt.Sprintf("err=%v", err))
}

func (e *captureEvent) Msg(msg string) {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	e.c.mu.records = append(e.c.mu.records, debugRecord{msg: msg, fields: strings.Join(e.fields, " ")})
}

// TestLoggerCapturesChainedFields pins that the fields chained before Msg
// reach the logger, not just the final message text. The emitting sites
// build a multi-field chain before calling Msg; a chain that forgot Msg
// would silently log nothing, and the compiler cannot catch that.
func TestLoggerCapturesChainedFields(t *testing.T) {
	t.Parallel()

	explicit := &captureDebugLogger{}
	reg, err := NewWithOptions(prometheus.NewRegistry(), nil, []Option{WithLogger(explicit)})
	require.NoError(t, err)
	require.NotPanics(t, runRegistry(t, reg))

	fields := explicit.fields("osprom registry running")
	require.Contains(t, fields, "observers=0")
	require.Contains(t, fields, "buffer_size=")
	require.Contains(t, fields, "workers=")
}

// TestWithLogger covers both explicit-logger paths: any debuglog.Logger
// receives the lifecycle records, not only a purpose-built adapter, and a
// nil one silences them rather than panicking.
//
// Two captures, not one. With a single sink the client's own debug records land
// beside the registry's, and an assertion on that sink passes even when the
// registry's messages never arrive. The global capture also pins that WithLogger
// is what routes these records: a nil logger has to silence them, not fall back
// to the installed default.
//
// Not parallel: installing the process-global debug logger mutates process
// state that the other logger tests in this file read.
func TestWithLogger(t *testing.T) {
	tests := []struct {
		name   string
		logger func(explicit *captureDebugLogger) debuglog.Logger
		want   []string
	}{
		{
			name:   "any Logger receives lifecycle records",
			logger: func(explicit *captureDebugLogger) debuglog.Logger { return explicit },
			want:   []string{"osprom registry running", "osprom registry stopped"},
		},
		{
			name:   "nil logger silences lifecycle records",
			logger: func(*captureDebugLogger) debuglog.Logger { return nil },
			// Empty rather than nil: messages sizes its result from the records it
			// walks, so no records yields a non-nil slice of none.
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			explicit, global := &captureDebugLogger{}, &captureDebugLogger{}

			// Installs global as the process-global debug logger, which is the
			// default WithLogger has to override in both rows.
			client, err := opensearch.NewClient(opensearch.Config{
				Addresses:   []string{"http://localhost:9200"},
				DebugLogger: global,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			reg, err := NewWithOptions(prometheus.NewRegistry(), nil, []Option{WithLogger(tt.logger(explicit))})
			require.NoError(t, err)

			// A nil logger would panic either inside Run's goroutine, which takes
			// the test binary down, or on Close, which fails here.
			require.NotPanics(t, runRegistry(t, reg))

			require.Equal(t, tt.want, explicit.messages())
			require.NotContains(t, global.messages(), "osprom registry running")
			require.NotContains(t, global.messages(), "osprom registry stopped")
		})
	}
}

// TestDefaultLoggerResolvesPerMessage pins that the default resolves the
// process-global logger at each message rather than once at construction.
//
// Both halves matter. A Registry is built before the client that installs the
// logger, since the client takes the Registry as its Observer, so reading the
// global at construction would capture nil and silence these messages forever.
// And because the last client constructed wins, swapping the global between two
// messages has to redirect the second one: a resolve-once-lazily implementation
// would send both to the first logger.
func TestDefaultLoggerResolvesPerMessage(t *testing.T) {
	first, second := &captureDebugLogger{}, &captureDebugLogger{}

	reg, err := NewWithOptions(prometheus.NewRegistry(), nil, nil) // no WithLogger: takes the default
	require.NoError(t, err)

	// Installs first as the process-global debug logger.
	c1, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: first,
		Observer:    reg,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })

	var wg sync.WaitGroup
	wg.Go(func() { _ = reg.Run(t.Context()) })

	require.Eventually(t, func() bool {
		return slices.Contains(first.messages(), "osprom registry running")
	}, time.Second, 10*time.Millisecond, "startup message never reached the installed logger")

	// Replaces the process-global logger: the last client constructed wins.
	c2, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	require.NoError(t, reg.Close())
	wg.Wait()

	require.Contains(t, second.messages(), "osprom registry stopped",
		"shutdown message did not follow the swapped global")
	require.NotContains(t, first.messages(), "osprom registry stopped",
		"shutdown message went to the logger installed at construction time")
}
