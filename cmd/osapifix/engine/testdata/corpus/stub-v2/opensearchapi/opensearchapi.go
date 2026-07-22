// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearchapi is a minimal stand-in for opensearch-go/v2's
// opensearchapi package. It carries only the seed-op shapes the rewrite touches:
// the API method set with Ping and Indices.Exists as functional-options funcs,
// their request types, and the raw Response with the methods the rewrite either
// keeps, rewrites (Status), or markers (String, Warnings, HasWarnings). See the
// sibling opensearch.go for why this stub exists.
package opensearchapi

import (
	"context"
	"io"
	"net/http"
)

// Transport is the sink Do writes through; the root Client satisfies it.
type Transport interface {
	Perform(*http.Request) (*http.Response, error)
}

// Response is the v2 raw response. The v3 hop keeps Body/StatusCode/IsError,
// rewrites Status() to an http.StatusText form, and markers the rest.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

func (r *Response) String() string      { return "" }
func (r *Response) Status() string      { return "" }
func (r *Response) IsError() bool       { return false }
func (r *Response) Warnings() []string  { return nil }
func (r *Response) HasWarnings() bool   { return false }

// Ping is the functional-options request func for the ping API.
type Ping func(o ...func(*PingRequest)) (*Response, error)

// PingRequest configures a Ping call.
type PingRequest struct{}

func (f Ping) WithContext(v context.Context) func(*PingRequest) { return func(*PingRequest) {} }
func (f Ping) WithPretty() func(*PingRequest)                   { return func(*PingRequest) {} }
func (f Ping) WithHuman() func(*PingRequest)                    { return func(*PingRequest) {} }
func (f Ping) WithErrorTrace() func(*PingRequest)               { return func(*PingRequest) {} }
func (f Ping) WithFilterPath(v ...string) func(*PingRequest)    { return func(*PingRequest) {} }

// IndicesExists is the functional-options request func for the indices-exists API.
type IndicesExists func(index []string, o ...func(*IndicesExistsRequest)) (*Response, error)

// IndicesExistsRequest configures an Indices.Exists call.
type IndicesExistsRequest struct{}

func (f IndicesExists) WithContext(v context.Context) func(*IndicesExistsRequest) {
	return func(*IndicesExistsRequest) {}
}
func (f IndicesExists) WithLocal(v bool) func(*IndicesExistsRequest) {
	return func(*IndicesExistsRequest) {}
}
func (f IndicesExists) WithFilterPath(v ...string) func(*IndicesExistsRequest) {
	return func(*IndicesExistsRequest) {}
}

// Indices groups the index-scoped APIs. Only Exists is needed for the seed set.
type Indices struct {
	Exists IndicesExists
}

// API is the method set embedded by the root Client. v2's real API has 51
// method fields; the stub carries the two seed ops plus the Indices group.
type API struct {
	Ping    Ping
	Indices *Indices
}

// New builds an empty API. The real one wires each field to a transport.
func New() *API {
	return &API{Indices: &Indices{}}
}

// BulkRequest is an idiom-1 (function API) request type. It is removed outright
// in v3, so any reference to it must surface as a removed-type MANUAL line.
type BulkRequest struct {
	Index string
	Body  io.Reader
}

// Do executes the request against a Transport, returning the raw Response. This
// is the idiom-1 call shape opensearchapi.<X>Request{...}.Do(ctx, client).
func (r BulkRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	return &Response{}, nil
}

