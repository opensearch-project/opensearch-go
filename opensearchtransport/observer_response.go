// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "time"

// RequestEvent holds request-side facts known at or before the round trip.
// It is a flat value type: no slices or maps, so it does not escape to the heap
// when passed by value to an observer.
type RequestEvent struct {
	// Method is the HTTP method (e.g. "GET", "POST").
	Method string

	// Path is the raw request path, before any label reduction. Consumers
	// decide cardinality (e.g. templating "/logs/_doc/123" to "/logs/_doc/{id}").
	Path string

	// Index is the target index as understood by the routing layer, when known.
	// Empty when the request is not index-scoped or routing did not run.
	Index string

	// Host is the node actually contacted (scheme and host of the selected
	// backend, e.g. "http://node-1:9200").
	Host string

	// Attempt is the zero-based index of the final round-trip attempt for this
	// request. A request that succeeded without retry reports 0.
	Attempt int

	// RequestBytes is the request body size in bytes (req.ContentLength), or -1
	// when unknown.
	RequestBytes int64
}

// ResponseEvent holds response facts common to both the buffered and streaming
// entry points. It is timing- and size-agnostic on purpose: duration and byte
// fields live on the concrete RequestResponseEvent and StreamResponseEvent so
// their meaning is unambiguous from the type.
type ResponseEvent struct {
	// Request is the originating request snapshot.
	Request RequestEvent

	// StatusCode is the HTTP status code, or 0 when the round trip produced no
	// response (transport error).
	StatusCode int

	// Err is non-nil when the request failed at the transport layer.
	Err error
}

// RequestResponseEvent is fired by [Transport.Request] once per logical request
// after the response body has been fully read and buffered. Its Duration is the
// full time from request send until the entire body has been read, and its
// ResponseBytes is the exact number of bytes read.
type RequestResponseEvent struct {
	ResponseEvent

	// Duration is the elapsed time from request send until the entire response
	// body has been read and buffered.
	Duration time.Duration

	// ResponseBytes is the exact response body size in bytes, as measured by the
	// buffering read.
	ResponseBytes int64
}

// StreamResponseEvent is fired by [Transport.Stream] once per logical request,
// before the caller reads the body. Its Duration is time-to-first-byte and its
// ContentLength is the response's Content-Length header (-1 when chunked or
// otherwise unknown). The transport does not measure streamed bytes; a caller
// that needs an actual byte count must measure it while reading the body.
type StreamResponseEvent struct {
	ResponseEvent

	// Duration is time-to-first-byte: elapsed time from request send until the
	// response headers were received.
	Duration time.Duration

	// ContentLength is the response Content-Length header value, or -1 when the
	// length is unknown (chunked transfer, compressed, or absent).
	ContentLength int64
}
