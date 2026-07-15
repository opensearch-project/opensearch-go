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
	reg, err := New(promReg, 16, ro)
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
	require.Equal(t, 0.0, testutil.ToFloat64(reg.dropped), "no drops on an unsaturated buffer")
}

func TestRegistryFanOutToMultipleObservers(t *testing.T) {
	promReg := prometheus.NewRegistry()
	ro := NewRequestObserver()
	counter := &countingObserver{}
	reg, err := New(promReg, 16, ro, counter)
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

	require.Equal(t, 1, testutil.CollectAndCount(ro.duration), "prom observer saw the event")
	require.Equal(t, 1, counter.count(), "custom observer saw the same event")
}

func TestRegistryOverflowIncrementsDropped(t *testing.T) {
	promReg := prometheus.NewRegistry()
	reg, err := New(promReg, 1, NewRequestObserver())
	require.NoError(t, err)

	// Do NOT run the dispatch loop: with a buffer of 1, the first event fills the
	// buffer and every subsequent enqueue drops.
	const n = 50
	for range n {
		reg.OnStreamResponse(opensearchtransport.StreamResponseEvent{
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

func TestRegistryCloseIdempotent(t *testing.T) {
	reg, err := New(prometheus.NewRegistry(), 4)
	require.NoError(t, err)
	stop := runRegistry(t, reg)
	stop()
	require.NoError(t, reg.Close(), "second Close is a no-op")
}

func TestRegistryRunExitsOnContextCancel(t *testing.T) {
	reg, err := New(prometheus.NewRegistry(), 4)
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
	cases := map[int]string{
		0: "error", 200: "2xx", 201: "2xx", 404: "4xx", 503: "5xx", 700: "unknown",
	}
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

func (o *countingObserver) OnRequest(RequestSample) {
	o.mu.Lock()
	o.n++
	o.mu.Unlock()
}

func (o *countingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}
