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

//go:build integration

package opensearchutil_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v3/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v3/opensearchutil"
)

func TestBulkIndexerIntegration(t *testing.T) {
	testRecordCount := uint64(10000)
	ctx := context.Background()

	testCases := []struct {
		name                       string
		compressRequestBodyEnabled bool
		tests                      []struct {
			name       string
			action     string
			body       string
			numItems   uint64
			numIndexed uint64
			numCreated uint64
			numUpdated uint64
			numFailed  uint64
		}
	}{
		{
			name:                       "With body compression",
			compressRequestBodyEnabled: true,
			tests: []struct {
				name       string
				action     string
				body       string
				numItems   uint64
				numIndexed uint64
				numCreated uint64
				numUpdated uint64
				numFailed  uint64
			}{
				{
					name:       "Index",
					action:     "index",
					body:       `{"title":"bar"}`,
					numItems:   testRecordCount,
					numIndexed: testRecordCount,
					numCreated: 0,
					numUpdated: 0,
					numFailed:  0,
				},
				{
					name:       "Upsert",
					action:     "update",
					body:       `{"doc":{"title":"qwe"}, "doc_as_upsert": true}`,
					numItems:   testRecordCount,
					numIndexed: 0,
					numCreated: 0,
					numUpdated: testRecordCount,
					numFailed:  0,
				},
				{
					name:       "Create",
					action:     "create",
					body:       `{"title":"bar"}`,
					numItems:   testRecordCount,
					numIndexed: 0,
					numCreated: 0,
					numUpdated: 0,
					numFailed:  testRecordCount,
				},
			},
		},
		{
			name:                       "Without body compression",
			compressRequestBodyEnabled: false,
			tests: []struct {
				name       string
				action     string
				body       string
				numItems   uint64
				numIndexed uint64
				numCreated uint64
				numUpdated uint64
				numFailed  uint64
			}{
				{
					name:       "Index",
					action:     "index",
					body:       `{"title":"bar"}`,
					numItems:   testRecordCount,
					numIndexed: testRecordCount,
					numCreated: 0,
					numUpdated: 0,
					numFailed:  0,
				},
				{
					name:       "Upsert",
					action:     "update",
					body:       `{"doc":{"title":"qwe"}, "doc_as_upsert": true}`,
					numItems:   testRecordCount,
					numIndexed: 0,
					numCreated: 0,
					numUpdated: testRecordCount,
					numFailed:  0,
				},
				{
					name:       "Create",
					action:     "create",
					body:       `{"title":"bar"}`,
					numItems:   testRecordCount,
					numIndexed: 0,
					numCreated: 0,
					numUpdated: 0,
					numFailed:  testRecordCount,
				},
			},
		},
	}

	for _, c := range testCases {
		indexName := "test-bulk-integration"

		client, _ := opensearchapi.NewClient(
			opensearchapi.Config{
				Client: opensearch.Config{
					CompressRequestBody: c.compressRequestBodyEnabled,
					Logger:              &opensearchtransport.ColorLogger{Output: os.Stdout},
				},
			},
		)

		client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{Indices: []string{indexName}, Params: opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)}})
		client.Indices.Create(
			ctx,
			opensearchapi.IndicesCreateReq{
				Index:  indexName,
				Body:   strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0, "refresh_interval":"5s"}}`),
				Params: opensearchapi.IndicesCreateParams{WaitForActiveShards: "1"},
			},
		)

		for _, tt := range c.tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Run(c.name, func(t *testing.T) {
					bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
						Index:      indexName,
						Client:     client,
						ErrorTrace: true,
						Human:      true,
						Pretty:     true,
						// FlushBytes: 3e+6,
					})

					start := time.Now().UTC()

					for i := 1; i <= int(tt.numItems); i++ {
						err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
							Index:      indexName,
							Action:     tt.action,
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(tt.body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					if err := bi.Close(context.Background()); err != nil {
						t.Errorf("Unexpected error: %s", err)
					}

					stats := bi.Stats()

					if stats.NumAdded != tt.numItems {
						t.Errorf("Unexpected NumAdded: want=%d, got=%d", tt.numItems, stats.NumAdded)
					}

					if stats.NumIndexed != tt.numIndexed {
						t.Errorf("Unexpected NumIndexed: want=%d, got=%d", tt.numItems, stats.NumIndexed)
					}

					if stats.NumUpdated != tt.numUpdated {
						t.Errorf("Unexpected NumUpdated: want=%d, got=%d", tt.numUpdated, stats.NumUpdated)
					}

					if stats.NumCreated != tt.numCreated {
						t.Errorf("Unexpected NumCreated: want=%d, got=%d", tt.numCreated, stats.NumCreated)
					}

					if stats.NumFailed != tt.numFailed {
						t.Errorf("Unexpected NumFailed: want=0, got=%d", stats.NumFailed)
					}

					fmt.Printf("  Added %d documents to indexer. Succeeded: %d. Failed: %d. Requests: %d. Duration: %s (%.0f docs/sec)\n",
						stats.NumAdded,
						stats.NumFlushed,
						stats.NumFailed,
						stats.NumRequests,
						time.Since(start).Truncate(time.Millisecond),
						1000.0/float64(time.Since(start)/time.Millisecond)*float64(stats.NumFlushed))
				})

				t.Run("Multiple indices", func(t *testing.T) {
					bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
						Index:  "test-index-a",
						Client: client,
					})

					// Default index
					for i := 1; i <= 10; i++ {
						err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
							Action:     "index",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(tt.body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					// Index 1
					for i := 1; i <= 10; i++ {
						err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
							Action: "index",
							Index:  "test-index-b",
							Body:   strings.NewReader(tt.body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					// Index 2
					for i := 1; i <= 10; i++ {
						err := bi.Add(context.Background(), opensearchutil.BulkIndexerItem{
							Action: "index",
							Index:  "test-index-c",
							Body:   strings.NewReader(tt.body),
						})
						if err != nil {
							t.Fatalf("Unexpected error: %s", err)
						}
					}

					if err := bi.Close(context.Background()); err != nil {
						t.Errorf("Unexpected error: %s", err)
					}
					stats := bi.Stats()

					expectedIndexed := 10 + 10 + 10
					if stats.NumIndexed != uint64(expectedIndexed) {
						t.Errorf("Unexpected NumIndexed: want=%d, got=%d", expectedIndexed, stats.NumIndexed)
					}

					res, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{"test-index-a", "test-index-b", "test-index-c"}})
					if err != nil {
						t.Fatalf("Unexpected error: %s", err)
					}
					if res.StatusCode != 200 {
						t.Errorf("Expected indices to exist, but got a [%s] response", res.Status())
					}
				})
			})
		}
	}
}
