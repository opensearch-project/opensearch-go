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
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func newWarmMigrationColdFunc(t Transport) WarmMigrationCold {
	return func(o ...func(*WarmMigrationColdRequest)) (*Response, error) {
		var r = WarmMigrationColdRequest{}
		for _, f := range o {
			f(&r)
		}
		return r.Do(r.ctx, t)
	}
}

// ----- API Definition -------------------------------------------------------

// WarmMigrationCold run migration from cold to warm storage.
type WarmMigrationCold func(o ...func(*WarmMigrationColdRequest)) (*Response, error)

// WarmMigrationColdRequest configures the WarmMigrationCold API request.
type WarmMigrationColdRequest struct {
	Body io.Reader

	Index           string
	StartTime       *time.Time
	EndTime         *time.Time
	ExpandWildcards string
	Format          string
	H               []string
	Help            *bool
	Local           *bool
	S               []string
	V               *bool

	Pretty     bool
	Human      bool
	ErrorTrace bool
	FilterPath []string

	Header http.Header

	ctx context.Context
}

// Do executes the request and returns response or error.
func (r WarmMigrationColdRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	var (
		method string
		path   strings.Builder
		params map[string]string
	)

	method = "POST"

	path.Grow(1 + len("_ultrawarm") + 1 + len("migration") + 1 + len(r.Index) + 1 + len("_cold"))
	path.WriteString("/")
	path.WriteString("_ultrawarm")
	path.WriteString("/")
	path.WriteString("migration")
	path.WriteString("/")
	path.WriteString(r.Index)
	path.WriteString("/")
	path.WriteString("_cold")

	params = make(map[string]string)

	if r.ExpandWildcards != "" {
		params["expand_wildcards"] = r.ExpandWildcards
	}

	if r.Format != "" {
		params["format"] = r.Format
	}

	if len(r.H) > 0 {
		params["h"] = strings.Join(r.H, ",")
	}

	if r.Help != nil {
		params["help"] = strconv.FormatBool(*r.Help)
	}

	if r.Local != nil {
		params["local"] = strconv.FormatBool(*r.Local)
	}

	if len(r.S) > 0 {
		params["s"] = strings.Join(r.S, ",")
	}

	if r.V != nil {
		params["v"] = strconv.FormatBool(*r.V)
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

	req, err := newRequest(method, path.String(), r.Body)
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

	if r.Body != nil {
		req.Header[headerContentType] = headerContentTypeJSON
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

	return &response, response.Err()
}

// WithContext sets the request context.
func (f WarmMigrationCold) WithContext(v context.Context) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.ctx = v
	}
}

// WithExpandWildcards - whether to expand wildcard expression to concrete indices that are open, closed or both..
func (f WarmMigrationCold) WithExpandWildcards(v string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.ExpandWildcards = v
	}
}

// WithFormat - a short version of the accept header, e.g. json, yaml.
func (f WarmMigrationCold) WithFormat(v string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.Format = v
	}
}

// WithH - comma-separated list of column names to display.
func (f WarmMigrationCold) WithH(v ...string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.H = v
	}
}

// WithHelp - return help information.
func (f WarmMigrationCold) WithHelp(v bool) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.Help = &v
	}
}

// WithLocal - return local information, do not retrieve the state from cluster-manager node (default: false).
func (f WarmMigrationCold) WithLocal(v bool) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.Local = &v
	}
}

// WithS - comma-separated list of column names or column aliases to sort by.
func (f WarmMigrationCold) WithS(v ...string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.S = v
	}
}

// WithV - verbose mode. display column headers.
func (f WarmMigrationCold) WithV(v bool) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.V = &v
	}
}

// WithPretty makes the response body pretty-printed.
func (f WarmMigrationCold) WithPretty() func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.Pretty = true
	}
}

// WithHuman makes statistical values human-readable.
func (f WarmMigrationCold) WithHuman() func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.Human = true
	}
}

// WithErrorTrace includes the stack trace for errors in the response body.
func (f WarmMigrationCold) WithErrorTrace() func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.ErrorTrace = true
	}
}

// WithFilterPath filters the properties of the response body.
func (f WarmMigrationCold) WithFilterPath(v ...string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		r.FilterPath = v
	}
}

// WithHeader adds the headers to the HTTP request.
func (f WarmMigrationCold) WithHeader(h map[string]string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		for k, v := range h {
			r.Header.Add(k, v)
		}
	}
}

// WithOpaqueID adds the X-Opaque-Id header to the HTTP request.
func (f WarmMigrationCold) WithOpaqueID(s string) func(*WarmMigrationColdRequest) {
	return func(r *WarmMigrationColdRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("X-Opaque-Id", s)
	}
}
