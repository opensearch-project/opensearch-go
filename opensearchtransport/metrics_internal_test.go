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

package opensearchtransport

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"
)

func TestMetrics(t *testing.T) {
	t.Run("Metrics()", func(t *testing.T) {
		tp, _ := New(
			Config{
				URLs: []*url.URL{
					{Scheme: "http", Host: "foo1"},
					{Scheme: "http", Host: "foo2"},
					{Scheme: "http", Host: "foo3"},
				},
				DisableRetry:  true,
				EnableMetrics: true,
			},
		)

		tp.metrics.requests.Store(3)
		tp.metrics.failures.Store(4)
		tp.metrics.mu.Lock()
		tp.metrics.mu.responses[200] = 1
		tp.metrics.mu.responses[404] = 2
		tp.metrics.mu.Unlock()

		req, _ := http.NewRequest(http.MethodHead, "/", nil)
		resp, err := tp.Perform(req)
		if err == nil {
			defer resp.Body.Close()
		}

		m, err := tp.Metrics()
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		fmt.Println(m)

		if m.Requests != 4 {
			t.Errorf("Unexpected output, want=4, got=%d", m.Requests)
		}
		if m.Failures != 5 {
			t.Errorf("Unexpected output, want=5, got=%d", m.Failures)
		}
		if len(m.Responses) != 2 {
			t.Errorf("Unexpected output: %+v", m.Responses)
		}
		if len(m.Connections) != 3 {
			t.Errorf("Unexpected output: %+v", m.Connections)
		}
	})

	t.Run("Metrics() when not enabled", func(t *testing.T) {
		tp, _ := New(Config{})

		_, err := tp.Metrics()
		if err == nil {
			t.Fatalf("Expected error, got: %v", err)
		}
	})

	t.Run("String()", func(t *testing.T) {
		var m ConnectionMetric

		m = ConnectionMetric{URL: "http://foo1"}

		if m.String() != "{http://foo1}" {
			t.Errorf("Unexpected output: %s", m)
		}

		tt, _ := time.Parse(time.RFC3339, "2010-11-11T11:00:00Z")
		m = ConnectionMetric{
			URL:       "http://foo2",
			IsDead:    true,
			Failures:  123,
			DeadSince: &tt,
		}

		match, err := regexp.MatchString(
			`{http://foo2 dead=true failures=123 dead_since=Nov 11 \d+:00:00}`,
			m.String(),
		)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if !match {
			t.Errorf("Unexpected output: %s", m)
		}
	})

	t.Run("incrementResponse method", func(t *testing.T) {
		m := &metrics{
			mu: struct {
				sync.RWMutex
				responses map[int]int
			}{
				responses: make(map[int]int),
			},
		}

		// Test incrementResponse method directly
		m.incrementResponse(200)
		m.incrementResponse(404)
		m.incrementResponse(200) // increment same code again

		m.mu.RLock()
		if m.mu.responses[200] != 2 {
			t.Errorf("Expected 2 responses for status 200, got %d", m.mu.responses[200])
		}
		if m.mu.responses[404] != 1 {
			t.Errorf("Expected 1 response for status 404, got %d", m.mu.responses[404])
		}
		m.mu.RUnlock()
	})
}
