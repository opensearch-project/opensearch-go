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

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

type FakeTransport struct {
	FakeResponse *http.Response
}

func (t *FakeTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return t.FakeResponse, nil
}

func newFakeTransport(_ *testing.B) *FakeTransport {
	return &FakeTransport{
		FakeResponse: &http.Response{
			Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
			StatusCode:    http.StatusOK,
			ContentLength: 13,
			Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
			Body:          io.NopCloser(strings.NewReader(`{"foo":"bar"}`)),
		},
	}
}

func BenchmarkTransport(b *testing.B) {
	b.ReportAllocs()

	b.Run("Defaults", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			tp, _ := opensearchtransport.New(opensearchtransport.Config{
				URLs:      []*url.URL{{Scheme: "http", Host: "foo"}},
				Transport: newFakeTransport(b),
			})

			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			res, err := tp.Perform(req)
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}
			defer res.Body.Close()
		}
	})

	b.Run("Headers", func(b *testing.B) {
		hdr := http.Header{}
		hdr.Set("Accept", "application/yaml")

		for i := 0; i < b.N; i++ {
			tp, _ := opensearchtransport.New(opensearchtransport.Config{
				URLs:      []*url.URL{{Scheme: "http", Host: "foo"}},
				Header:    hdr,
				Transport: newFakeTransport(b),
			})

			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			res, err := tp.Perform(req)
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}
			defer res.Body.Close()
		}
	})
}
