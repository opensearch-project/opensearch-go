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
	ctx := b.Context()
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: "node-1:9200", Path: "/idx/_search"},
	}
	sr := streamResult{
		escapedPath: "/idx/_search",
		routeName:   "search",
		index:       "idx",
		poolName:    "search",
		hostPort:    "http://node-1:9200",
	}

	b.Run("start", func(b *testing.B) {
		b.ReportAllocs()
		ev := RequestEvent{Method: http.MethodGet, Path: "/idx/_search", RouteName: "search", Index: "idx"}
		for b.Loop() {
			_ = obs.OnRequestStart(ctx, ev)
		}
	})

	b.Run("stream", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			obs.OnStreamResponse(ctx, StreamResponseEvent{
				ResponseEvent: ResponseEvent{
					Request:    newRequestEvent(req, sr),
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
			obs.OnRequestResponse(ctx, RequestResponseEvent{
				ResponseEvent: ResponseEvent{
					Request:    newRequestEvent(req, sr),
					StatusCode: http.StatusOK,
				},
				Duration:      2 * time.Millisecond,
				ResponseBytes: 256,
			})
		}
	})
}
