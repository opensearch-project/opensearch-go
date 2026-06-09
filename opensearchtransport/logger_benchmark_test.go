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
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// BenchmarkTransportLogger measures the per-Perform cost when a logger is
// attached. The transport client is constructed once per sub-benchmark and
// closed via b.Cleanup so we don't pay New() and don't leak background
// goroutines across iterations.
func BenchmarkTransportLogger(b *testing.B) {
	b.ReportAllocs()

	run := func(b *testing.B, cfg opensearchtransport.Config, readBody bool) {
		b.Helper()
		cfg.URLs = []*url.URL{{Scheme: "http", Host: "foo"}}
		cfg.Transport = newFakeTransport(b)
		// Disable the load-shedding poller; it isn't part of the steady-state
		// per-request hot path and its tick rate would otherwise bleed into
		// the measurement at this benchmark's iteration count.
		cfg.NodeStatsInterval = -1

		tp, err := opensearchtransport.New(cfg)
		if err != nil {
			b.Fatalf("Unexpected error: %q", err)
		}
		b.Cleanup(func() { _ = tp.Close() })

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			res, err := tp.Perform(req)
			if err != nil {
				b.Fatalf("Unexpected error: %q", err)
			}
			if readBody {
				body, err := io.ReadAll(res.Body)
				if err != nil {
					b.Fatalf("Error reading response body: %q", err)
				}
				if len(body) < 13 {
					b.Errorf("Error reading response body bytes, want=13, got=%d", len(body))
				}
			}
			res.Body.Close()
		}
	}

	b.Run("Text", func(b *testing.B) {
		run(b, opensearchtransport.Config{
			Logger: &opensearchtransport.TextLogger{Output: io.Discard},
		}, false)
	})

	b.Run("Text-Body", func(b *testing.B) {
		run(b, opensearchtransport.Config{
			Logger: &opensearchtransport.TextLogger{
				Output:             io.Discard,
				EnableRequestBody:  true,
				EnableResponseBody: true,
			},
		}, true)
	})

	b.Run("JSON", func(b *testing.B) {
		run(b, opensearchtransport.Config{
			Logger: &opensearchtransport.JSONLogger{Output: io.Discard},
		}, false)
	})

	b.Run("JSON-Body", func(b *testing.B) {
		run(b, opensearchtransport.Config{
			Logger: &opensearchtransport.JSONLogger{
				Output:             io.Discard,
				EnableRequestBody:  true,
				EnableResponseBody: true,
			},
		}, false)
	})
}
