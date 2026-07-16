// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// BenchmarkResponseEventFire proves that constructing a response event and
// firing it at a no-op observer allocates nothing: the events are flat value
// types passed by value, so they do not escape to the heap on the fire path.
func BenchmarkResponseEventFire(b *testing.B) {
	var obs BaseConnectionObserver
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: "node-1:9200", Path: "/idx/_search"},
	}

	b.Run("stream", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			obs.OnStreamResponse(StreamResponseEvent{
				ResponseEvent: ResponseEvent{
					Request:    newRequestEvent(req, 0),
					StatusCode: http.StatusOK,
				},
				Duration:      2 * time.Millisecond,
				ContentLength: 256,
			})
		}
	})

	b.Run("request", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			obs.OnRequestResponse(RequestResponseEvent{
				ResponseEvent: ResponseEvent{
					Request:    newRequestEvent(req, 0),
					StatusCode: http.StatusOK,
				},
				Duration:      2 * time.Millisecond,
				ResponseBytes: 256,
			})
		}
	})
}
