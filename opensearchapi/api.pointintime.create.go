// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func newPointInTimeCreateFunc(t Transport) PointInTimeCreate {
	return func(o ...func(*PointInTimeCreateRequest)) (*Response, error) {
		var r = PointInTimeCreateRequest{}
		for _, f := range o {
			f(&r)
		}
		return r.Do(r.ctx, t)
	}
}

// ----- API Definition -------------------------------------------------------

// Point In Time ets you run different queries against a dataset that is fixed in time.
type PointInTimeCreate func(o ...func(*PointInTimeCreateRequest)) (*Response, error)

// PointInTimeRequest configures the Point In Time API request.
type PointInTimeCreateRequest struct {
	Indices []string

	KeepAlive               time.Duration
	Preference              string
	Routing                 string
	ExpandWildcards         string
	AllowPartialPitCreation bool

	Pretty     bool
	Human      bool
	ErrorTrace bool
	FilterPath []string

	Header http.Header

	ctx context.Context
}

// Do executes the request and returns response or error.
func (r PointInTimeCreateRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	var (
		path   strings.Builder
		params map[string]string
	)
	method := "POST"

	path.Grow(1 + len(strings.Join(r.Indices, ",")) + len("/_search/point_in_time"))
	path.WriteString("/")
	path.WriteString(strings.Join(r.Indices, ","))
	path.WriteString("/_search/point_in_time")

	params = make(map[string]string)

	if r.KeepAlive != 0 {
		params["keep_alive"] = formatDuration(r.KeepAlive)
	}

	if r.Preference != "" {
		params["preference"] = r.Preference
	}

	if r.Routing != "" {
		params["routing"] = r.Routing
	}

	if r.ExpandWildcards != "" {
		params["expand_wildcards"] = r.ExpandWildcards
	}

	if r.AllowPartialPitCreation {
		params["allow_partial_pit_creation"] = "true"
	}

	if r.Pretty {
		params["pretty"] = "true"
	}

	if r.Human {
		params["human"] = "true"
	}

	if r.ErrorTrace {
		params["error_trace"] = "true"
	}

	if len(r.FilterPath) > 0 {
		params["filter_path"] = strings.Join(r.FilterPath, ",")
	}

	req, err := newRequest(method, path.String(), nil)
	if err != nil {
		return nil, err
	}

	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	if len(r.Header) > 0 {
		if len(req.Header) == 0 {
			req.Header = r.Header
		} else {
			for k, vv := range r.Header {
				for _, v := range vv {
					req.Header.Add(k, v)
				}
			}
		}
	}

	if ctx != nil {
		req = req.WithContext(ctx)
	}

	res, err := transport.Perform(req)
	if err != nil {
		return nil, err
	}

	response := Response{
		StatusCode: res.StatusCode,
		Body:       res.Body,
		Header:     res.Header,
	}

	return &response, nil
}

// WithContext sets the request context.
func (f PointInTimeCreate) WithContext(v context.Context) func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.ctx = v
	}
}

// WithKeepAlive - specify the amount of time to keep the PIT.
func (f PointInTimeCreate) WithKeepAlive(v time.Duration) func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.KeepAlive = v
	}
}

// WithPretty makes the response body pretty-printed.
func (f PointInTimeCreate) WithPretty() func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.Pretty = true
	}
}

// WithHuman makes statistical values human-readable.
func (f PointInTimeCreate) WithHuman() func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.Human = true
	}
}

// WithErrorTrace includes the stack trace for errors in the response body.
func (f PointInTimeCreate) WithErrorTrace() func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.ErrorTrace = true
	}
}

// WithFilterPath filters the properties of the response body.
func (f PointInTimeCreate) WithFilterPath(v ...string) func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		r.FilterPath = v
	}
}

// WithHeader adds the headers to the HTTP request.
func (f PointInTimeCreate) WithHeader(h map[string]string) func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		for k, v := range h {
			r.Header.Add(k, v)
		}
	}
}

// WithOpaqueID adds the X-Opaque-Id header to the HTTP request.
func (f PointInTimeCreate) WithOpaqueID(s string) func(*PointInTimeCreateRequest) {
	return func(r *PointInTimeCreateRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("X-Opaque-Id", s)
	}
}
