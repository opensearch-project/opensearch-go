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

package opensearch_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type FakeTransport struct {
	Response *http.Response
}

func (t *FakeTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	response := t.Response
	response.Body = io.NopCloser(strings.NewReader(`{
		  "name" : "es1",
		  "cluster_name" : "opensearch-go",
		  "cluster_uuid" : "clusteruuid",
		  "version" : {
			"number" : "2.7.0",
			"distribution" : "opensearch",
			"build_type" : "tar",
			"build_hash" : "b7a6e09e492b1e965d827525f7863b366ef0e304",
			"build_date" : "2023-04-27T21:43:09.523336706Z",
			"build_snapshot" : false,
			"lucene_version" : "9.5.0",
			"minimum_wire_compatibility_version" : "7.10.0",
			"minimum_index_compatibility_version" : "7.0.0"
		  }
		}`))
	return t.Response, nil
}

func newFakeTransport(_ *testing.B, resp http.Response) *FakeTransport {
	return &FakeTransport{
		Response: &resp,
	}
}

func BenchmarkClient(b *testing.B) {
	defaultResponse := http.Response{
		Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
		StatusCode:    http.StatusOK,
		ContentLength: 2,
		Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
		Body:          io.NopCloser(strings.NewReader(`{}`)),
	}

	b.ReportAllocs()

	b.Run("Create client with defaults", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := opensearch.NewClient(opensearch.Config{Transport: newFakeTransport(b, defaultResponse)})
			if err != nil {
				b.Fatalf("Unexpected error when creating a client: %s", err)
			}
		}
	})
}

func BenchmarkClientAPI(b *testing.B) {
	infoResponse := http.Response{
		Status:     fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
		StatusCode: http.StatusOK,
		Header:     http.Header(map[string][]string{"Content-Type": {"application/json"}}),
	}

	b.ReportAllocs()

	ctx := context.Background()

	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				Addresses: []string{"http://localhost:9200"},
				Transport: newFakeTransport(b, infoResponse),
			},
		},
	)
	if err != nil {
		b.Fatalf("ERROR: %s", err)
	}

	b.Run("InfoRequest{}.Do()", func(b *testing.B) {
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			if _, _, err := client.Info(ctx, nil); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Index()", func(b *testing.B) {
		b.ResetTimer()
		var body strings.Builder

		for i := 0; i < b.N; i++ {
			docID := strconv.FormatInt(int64(i), 10)

			body.Reset()
			body.WriteString(`{"foo" : "bar `)
			body.WriteString(docID)
			body.WriteString(`	" }`)

			req := opensearchapi.IndexReq{
				Index:      "test",
				DocumentID: docID,
				Body:       strings.NewReader(body.String()),
				Params: opensearchapi.IndexParams{
					Refresh: "true",
					Pretty:  true,
					Timeout: 100,
				},
			}

			_, _, err := client.Index(ctx, req)
			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Search()", func(b *testing.B) {
		b.ResetTimer()

		body := `{"foo" : "bar"}`

		req := &opensearchapi.SearchReq{
			Indices: []string{"test"},
			Body:    strings.NewReader(body),
			Params: opensearchapi.SearchParams{
				Size:    opensearchapi.ToPointer(25),
				Pretty:  true,
				Timeout: 100,
			},
		}

		for i := 0; i < b.N; i++ {
			_, _, err := client.Search(ctx, req)
			if err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})

	b.Run("client.Bulk()", func(b *testing.B) {
		b.ResetTimer()
		var body strings.Builder

		for i := 0; i < b.N; i++ {
			docID := strconv.FormatInt(int64(i), 10)

			body.Reset()
			body.WriteString(`{"index" : { "_index" : "test", "_type" : "_doc", "_id" : "` + docID + `" }}`)
			body.WriteString(`{"foo" : "bar `)
			body.WriteString(docID)
			body.WriteString(`	" }`)

			req := opensearchapi.BulkReq{
				Body: strings.NewReader(body.String()),
				Params: opensearchapi.BulkParams{
					Refresh: "true",
					Pretty:  true,
					Timeout: 100,
				},
			}
			if _, _, err := client.Bulk(ctx, req); err != nil {
				b.Errorf("Unexpected error when getting a response: %s", err)
			}
		}
	})
}
