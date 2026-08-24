// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel

import (
	"net/http"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// BenchmarkOnRequestResponse measures the hot-path cost of the observer entry
// point: copy into a pooled envelope and a non-blocking channel send. The
// dispatch goroutine drains concurrently, so the pool amortizes to zero
// steady-state allocations.
func BenchmarkOnRequestResponse(b *testing.B) {
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	reg, err := New(mp.Meter("bench"), NewRequestObserver())
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
	ctx := b.Context()
	for b.Loop() {
		reg.OnRequestResponse(ctx, ev)
	}
}
