// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom

import (
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// BenchmarkOnRequestResponse measures the hot-path cost of the observer entry
// point: copy into a pooled envelope and a non-blocking channel send. The
// dispatch goroutine drains concurrently, so the pool amortizes to zero
// steady-state allocations.
func BenchmarkOnRequestResponse(b *testing.B) {
	reg, err := New(prometheus.NewRegistry(), 1024, NewRequestObserver())
	if err != nil {
		b.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = reg.Run(b.Context())
	}()
	b.Cleanup(func() {
		_ = reg.Close()
		<-stopped
	})

	ev := opensearchtransport.RequestResponseEvent{
		ResponseEvent: opensearchtransport.ResponseEvent{
			Request:    opensearchtransport.RequestEvent{Method: http.MethodGet},
			StatusCode: http.StatusOK,
		},
		Duration:      2 * time.Millisecond,
		ResponseBytes: 256,
	}

	b.ReportAllocs()
	for b.Loop() {
		reg.OnRequestResponse(ev)
	}
}
