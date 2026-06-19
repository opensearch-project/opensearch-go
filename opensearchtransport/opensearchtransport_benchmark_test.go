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

//go:build !integration

package opensearchtransport_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// FakeTransport is a test http.RoundTripper that returns a fresh response per
// call. We must not share the Body across iterations: a strings.Reader is
// stateful, and after one Perform drains it the next iteration sees EOF.
type FakeTransport struct{}

func (t *FakeTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
		StatusCode:    http.StatusOK,
		ContentLength: 13,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(`{"foo":"bar"}`)),
	}, nil
}

func newFakeTransport(_ *testing.B) *FakeTransport { return &FakeTransport{} }

// BenchmarkTransport measures the per-Perform cost on a steady-state client.
// opensearchtransport.New is hoisted out of the loop because it's a one-time
// cost in real usage and on this branch it spawns long-lived health-check
// goroutines that would otherwise leak across iterations.
func BenchmarkTransport(b *testing.B) {
	b.ReportAllocs()

	b.Run("Defaults", func(b *testing.B) {
		tp, err := opensearchtransport.New(opensearchtransport.Config{
			URLs:      []*url.URL{{Scheme: "http", Host: "foo"}},
			Transport: newFakeTransport(b),
			// Disable the load-shedding poller; it touches the fake transport
			// every few seconds and would skew the steady-state measurement.
			NodeStatsInterval: -1,
		})
		if err != nil {
			b.Fatalf("Unexpected error: %q", err)
		}
		b.Cleanup(func() { _ = tp.Close() })

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			res, err := tp.Stream(req)
			if err != nil {
				b.Fatalf("Unexpected error: %q", err)
			}
			res.Body.Close()
		}
	})

	b.Run("Headers", func(b *testing.B) {
		hdr := http.Header{}
		hdr.Set("Accept", "application/yaml")

		tp, err := opensearchtransport.New(opensearchtransport.Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			Header:            hdr,
			Transport:         newFakeTransport(b),
			NodeStatsInterval: -1,
		})
		if err != nil {
			b.Fatalf("Unexpected error: %q", err)
		}
		b.Cleanup(func() { _ = tp.Close() })

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			res, err := tp.Stream(req)
			if err != nil {
				b.Fatalf("Unexpected error: %q", err)
			}
			res.Body.Close()
		}
	})
}
