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

package opensearchutil_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

var mockResponseBody = `{
  "took": 30,
  "errors": false,
  "items": [
    {
      "index": {
        "_index": "bench-index",
        "_id": "1",
        "_version": 1,
        "result": "created",
        "_shards": { "total": 2, "successful": 1, "failed": 0 },
        "status": 201,
        "_seq_no": 0,
        "_primary_term": 1
      }
    }
  ]
}`

type mockTransp struct{}

func (t *mockTransp) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{Body: io.NopCloser(strings.NewReader(mockResponseBody))}, nil // 1x alloc
}

func BenchmarkBulkIndexer(b *testing.B) {
	b.ReportAllocs()

	b.Run("Basic", func(b *testing.B) {
		client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransp{}}})
		b.Cleanup(func() { _ = client.Close() })
		bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
			Client:     client,
			FlushBytes: 1024,
		})
		defer bi.Close(b.Context())

		docID := make([]byte, 0, 16)
		var docIDBuf bytes.Buffer
		docIDBuf.Grow(cap(docID))

		for i := 0; b.Loop(); i++ {
			docID = strconv.AppendInt(docID, int64(i), 10)
			docIDBuf.Write(docID)
			bi.Add(b.Context(), opensearchutil.BulkIndexerItem{
				Action:     "index",
				DocumentID: docIDBuf.String(),                  // 1x alloc
				Body:       strings.NewReader(`{"foo":"bar"}`), // 1x alloc
			})
			docID = docID[:0]
			docIDBuf.Reset()
		}
	})

	// Documents of a few KiB: sent from a copy of each, and sent from the caller's
	// own memory.
	doc := []byte(`{"foo":"` + strings.Repeat("bar", 1400) + `"}`) // About 4 KiB.

	for _, lend := range []bool{false, true} {
		name := "Copied4KiB"
		if lend {
			name = "Lent4KiB"
		}

		b.Run(name, func(b *testing.B) {
			client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransp{}}})
			b.Cleanup(func() { _ = client.Close() })
			bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{Client: client, FlushBytes: 1 << 20})
			defer bi.Close(b.Context())

			b.SetBytes(int64(len(doc)))

			for i := 0; b.Loop(); i++ {
				item := opensearchutil.BulkIndexerItem{Action: "index", DocumentID: strconv.Itoa(i)}
				if lend {
					_ = opensearchutil.AddZeroCopy(b.Context(), bi, opensearchutil.BulkIndexerZeroCopyItem{BulkIndexerItem: item, Data: doc})

					continue
				}

				item.Body = bytes.NewReader(doc)
				_ = bi.Add(b.Context(), item)
			}
		})
	}
}

// echoTransp answers a bulk request with one created item for every action in it,
// as a cluster does, so that the indexer parses a response that matches its batch
// and runs a callback for each item. mockTransp answers every request with one item,
// which a batch of more than one never matches.
type echoTransp struct{}

func (t *echoTransp) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		// Not a bulk request: the transport's own discovery and health checks.
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}

	var resp strings.Builder
	resp.WriteString(`{"took":1,"errors":false,"items":[`)
	for i, n := 0, bytes.Count(body, []byte(`{"index":`)); i < n; i++ {
		if i > 0 {
			resp.WriteByte(',')
		}
		resp.WriteString(`{"index":{"_index":"bench-index","_id":"1","_version":1,"result":"created","status":201}}`)
	}
	resp.WriteString(`]}`)

	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(resp.String()))}, nil
}

// BenchmarkBulkIndexerRoundTrip measures an item from Add to its OnSuccess callback,
// flushing every batch of items, against a transport that answers with one result per
// action. Run it with -benchmem, and with -memprofile to see where the allocations are.
func BenchmarkBulkIndexerRoundTrip(b *testing.B) {
	const batch = 1000

	client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &echoTransp{}}})
	b.Cleanup(func() { _ = client.Close() })
	bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{Client: client, FlushBytes: 1 << 20})
	defer bi.Close(b.Context())

	var done atomic.Int64
	onSuccess := func(context.Context, opensearchutil.BulkIndexerItem, opensearchapi.BulkRespItem) { done.Add(1) }
	body := []byte(`{"foo":"bar"}`)

	b.ReportAllocs()

	for i := 0; b.Loop(); i++ {
		_ = bi.Add(b.Context(), opensearchutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: strconv.Itoa(i),
			Body:       bytes.NewReader(body),
			OnSuccess:  onSuccess,
		})

		if i%batch == batch-1 {
			_ = bi.Flush(b.Context())
		}
	}
	_ = bi.Flush(b.Context())

	if got := done.Load(); got != int64(b.N) {
		b.Fatalf("%d callbacks for %d items", got, b.N)
	}
}
