// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport_test

import (
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// benchMetricsTransport builds a multi-server, routed transport so Metrics()
// exercises the full detailed path.
func benchMetricsTransport(b *testing.B) *opensearchtransport.Client {
	b.Helper()

	router, err := opensearchtransport.NewDefaultRouter()
	if err != nil {
		b.Fatalf("Unexpected error building router: %q", err)
	}

	tp, err := opensearchtransport.New(opensearchtransport.Config{
		URLs: []*url.URL{
			{Scheme: "http", Host: "foo1"},
			{Scheme: "http", Host: "foo2"},
			{Scheme: "http", Host: "foo3"},
			{Scheme: "http", Host: "foo4"},
			{Scheme: "http", Host: "foo5"},
		},
		Transport:     newFakeTransport(b),
		Router:        router,
		EnableMetrics: true,
		// Disable the load-shedding poller so it doesn't touch the fake
		// transport mid-measurement and skew results.
		NodeStatsInterval: -1,
	})
	if err != nil {
		b.Fatalf("Unexpected error: %q", err)
	}
	b.Cleanup(func() { _ = tp.Close() })
	return tp
}

// BenchmarkMetrics measures the single-threaded cost of one full snapshot.
func BenchmarkMetrics(b *testing.B) {
	tp := benchMetricsTransport(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tp.Metrics(); err != nil {
			b.Fatalf("Unexpected error: %q", err)
		}
	}
}

// BenchmarkMetricsParallel measures concurrent Metrics() calls, isolating
// reader-vs-reader contention on the snapshot locks.
func BenchmarkMetricsParallel(b *testing.B) {
	tp := benchMetricsTransport(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := tp.Metrics(); err != nil {
				b.Fatalf("Unexpected error: %q", err)
			}
		}
	})
}

// BenchmarkMetricsUnderLoad measures Metrics() while concurrent Stream()
// traffic mutates connection state -- the reader-vs-writer contention case the
// lock-free timestamp conversion targets.
func BenchmarkMetricsUnderLoad(b *testing.B) {
	tp := benchMetricsTransport(b)

	var stop atomic.Bool
	var wg sync.WaitGroup
	const loadGoroutines = 8
	wg.Add(loadGoroutines)
	for range loadGoroutines {
		go func() {
			defer wg.Done()
			for !stop.Load() {
				req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
				res, err := tp.Stream(req)
				if err == nil {
					res.Body.Close()
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tp.Metrics(); err != nil {
			b.Fatalf("Unexpected error: %q", err)
		}
	}
	b.StopTimer()

	stop.Store(true)
	wg.Wait()
}
