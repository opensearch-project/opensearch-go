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
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func newColdSearchFunc(t Transport) ColdSearch {
	return func(o ...func(*ColdSearchRequest)) (*Response, error) {
		var r = ColdSearchRequest{}
		for _, f := range o {
			f(&r)
		}
		return r.Do(r.ctx, t)
	}
}

// ----- API Definition -------------------------------------------------------

// ColdSearch shows list of indices storing in cold storage.
type ColdSearch func(o ...func(*ColdSearchRequest)) (*Response, error)

// ColdSearchRequest configures the ColdSearch API request.
type ColdSearchRequest struct {
	Name []string

	Body io.Reader

	IndexPattern    string
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
func (r ColdSearchRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	var (
		method string
		path   strings.Builder
		params map[string]string
	)

	method = "GET"

	path.Grow(1 + len("_cold") + 1 + len("indices") + 1 + len("_search"))
	path.WriteString("/")
	path.WriteString("_cold")
	path.WriteString("/")
	path.WriteString("indices")
	path.WriteString("/")
	path.WriteString("_search")

	params = make(map[string]string)

	if r.IndexPattern != "" {
		params["filters"] = fmt.Sprintf("{index_pattern:\"%s\"}", r.IndexPattern)
	}

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
func (f ColdSearch) WithContext(v context.Context) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.ctx = v
	}
}

// WithName - a list of alias names to return.
func (f ColdSearch) WithName(v ...string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Name = v
	}
}

// WithExpandWildcards - whether to expand wildcard expression to concrete indices that are open, closed or both..
func (f ColdSearch) WithExpandWildcards(v string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.ExpandWildcards = v
	}
}

// WithFormat - a short version of the accept header, e.g. json, yaml.
func (f ColdSearch) WithFormat(v string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Format = v
	}
}

// WithH - comma-separated list of column names to display.
func (f ColdSearch) WithH(v ...string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.H = v
	}
}

// WithHelp - return help information.
func (f ColdSearch) WithHelp(v bool) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Help = &v
	}
}

// WithLocal - return local information, do not retrieve the state from cluster-manager node (default: false).
func (f ColdSearch) WithLocal(v bool) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Local = &v
	}
}

// WithS - comma-separated list of column names or column aliases to sort by.
func (f ColdSearch) WithS(v ...string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.S = v
	}
}

// WithV - verbose mode. display column headers.
func (f ColdSearch) WithV(v bool) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.V = &v
	}
}

// WithPretty makes the response body pretty-printed.
func (f ColdSearch) WithPretty() func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Pretty = true
	}
}

// WithHuman makes statistical values human-readable.
func (f ColdSearch) WithHuman() func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.Human = true
	}
}

// WithErrorTrace includes the stack trace for errors in the response body.
func (f ColdSearch) WithErrorTrace() func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.ErrorTrace = true
	}
}

// WithFilterPath filters the properties of the response body.
func (f ColdSearch) WithFilterPath(v ...string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		r.FilterPath = v
	}
}

// WithHeader adds the headers to the HTTP request.
func (f ColdSearch) WithHeader(h map[string]string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		for k, v := range h {
			r.Header.Add(k, v)
		}
	}
}

// WithOpaqueID adds the X-Opaque-Id header to the HTTP request.
func (f ColdSearch) WithOpaqueID(s string) func(*ColdSearchRequest) {
	return func(r *ColdSearchRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("X-Opaque-Id", s)
	}
}
