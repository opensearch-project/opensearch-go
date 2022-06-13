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

package opensearchapi_test

import (
	"context"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// TODO: Refactor into a shared mock/testing package

var (
	defaultResponse = &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       ioutil.NopCloser(strings.NewReader("MOCK")),
	}
	defaultRoundTripFn = func(*http.Request) (*http.Response, error) { return defaultResponse, nil }
	errorRoundTripFn   = func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/" {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       ioutil.NopCloser(strings.NewReader("{}")),
			}, nil
		}
		return &http.Response{
			Header:     http.Header{},
			StatusCode: 400,
			Body: ioutil.NopCloser(strings.NewReader(`
					{ "error" : {
					    "root_cause" : [
					      {
					        "type" : "parsing_exception",
					        "reason" : "no [query] registered for [foo]",
					        "line" : 1,
					        "col" : 22
					      }
					    ],
					    "type" : "parsing_exception",
					    "reason" : "no [query] registered for [foo]",
					    "line" : 1,
					    "col" : 22
					  },
					  "status" : 400
					}`)),
		}, nil
	}
)

type FakeTransport struct {
	RoundTripFn func(*http.Request) (*http.Response, error)
}

func (t *FakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.RoundTripFn(req)
}

func newFakeClient(b *testing.B) *opensearch.Client {
	cfg := opensearch.Config{Transport: &FakeTransport{RoundTripFn: defaultRoundTripFn}}
	client, err := opensearch.NewClient(cfg)

	if err != nil {
		b.Fatalf("Unexpected error when creating a client: %s", err)
	}

	return client
}

func newFakeClientWithError(b *testing.B) *opensearch.Client {
	cfg := opensearch.Config{Transport: &FakeTransport{RoundTripFn: errorRoundTripFn}}
	client, err := opensearch.NewClient(cfg)

	if err != nil {
		b.Fatalf("Unexpected error when creating a client: %s", err)
	}

	return client
}

func BenchmarkAPI(b *testing.B) {
	var client = newFakeClient(b)
	var fakeClientWithError = newFakeClientWithError(b)

	b.Run("client.Info()                      ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.Info(); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Info() WithContext          ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.Info(client.Info.WithContext(context.Background())); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("InfoRequest{}.Do()                 ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := opensearchapi.InfoRequest{}
			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Cluster.Health()            ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := client.Cluster.Health(); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Cluster.Health() With()     ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := client.Cluster.Health(
				client.Cluster.Health.WithContext(context.Background()),
				client.Cluster.Health.WithLevel("indices"),
				client.Cluster.Health.WithPretty(),
			)

			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("ClusterHealthRequest{}.Do()        ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := opensearchapi.ClusterHealthRequest{}
			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("ClusterHealthRequest{...}.Do()     ", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := opensearchapi.ClusterHealthRequest{Level: "indices", Pretty: true}
			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Index() With()             ", func(b *testing.B) {
		indx := "test"
		body := strings.NewReader(`{"title" : "Test"}`)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := client.Index(
				indx,
				body,
				client.Index.WithDocumentID("1"),
				client.Index.WithRefresh("true"),
				client.Index.WithContext(context.Background()),
				client.Index.WithRefresh("true"),
				client.Index.WithPretty(),
				client.Index.WithTimeout(100),
			)

			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("IndexRequest{...}.Do()             ", func(b *testing.B) {
		b.ResetTimer()
		var body strings.Builder
		for i := 0; i < b.N; i++ {
			docID := strconv.FormatInt(int64(i), 10)
			body.Reset()
			body.WriteString(`{"foo" : "bar `)
			body.WriteString(docID)
			body.WriteString(`	" }`)

			req := opensearchapi.IndexRequest{
				Index:      "test",
				DocumentID: docID,
				Body:       strings.NewReader(body.String()),
				Refresh:    "true",
				Pretty:     true,
				Timeout:    100,
			}

			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("IndexRequest{...}.Do() reused      ", func(b *testing.B) {
		b.ResetTimer()
		var body strings.Builder

		req := opensearchapi.IndexRequest{}
		req.Index = "test"
		req.Refresh = "true"
		req.Pretty = true
		req.Timeout = 100

		for i := 0; i < b.N; i++ {
			docID := strconv.FormatInt(int64(i), 10)
			body.Reset()
			body.WriteString(`{"foo" : "bar `)
			body.WriteString(docID)
			body.WriteString(`	" }`)
			req.DocumentID = docID
			req.Body = strings.NewReader(body.String())

			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Search()                   ", func(b *testing.B) {
		body := strings.NewReader(`{"query" : { "match_all" : {} } }`)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := client.Search(client.Search.WithContext(context.Background()), client.Search.WithBody(body))

			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Search() with error        ", func(b *testing.B) {
		body := strings.NewReader(`{}`)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := fakeClientWithError.Search(fakeClientWithError.Search.WithContext(context.Background()), fakeClientWithError.Search.WithBody(body))

			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("SearchRequest{...}.Do()            ", func(b *testing.B) {
		body := strings.NewReader(`{"query" : { "match_all" : {} } }`)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := opensearchapi.SearchRequest{Body: body}
			if _, err := req.Do(context.Background(), client); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("SearchRequest{...}.Do() with error ", func(b *testing.B) {
		body := strings.NewReader(`{}`)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := opensearchapi.SearchRequest{Body: body}
			if _, err := req.Do(context.Background(), fakeClientWithError); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})
}
