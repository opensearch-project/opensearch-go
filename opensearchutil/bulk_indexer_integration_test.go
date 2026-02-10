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

//go:build integration && (core || opensearchutil)

package opensearchutil_test

import (
	"context"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	osapitest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestBulkIndexerIntegration(t *testing.T) {
	testRecordCount := uint64(10000)
	ctx := t.Context()

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
		indexName := testutil.MustUniqueString(t, "test-bulk-integration")

		client, _ := opensearchapi.NewClient(
			opensearchapi.Config{
				Client: opensearch.Config{
					CompressRequestBody: c.compressRequestBodyEnabled,
				},
			},
		)

		config, err := osapitest.ClientConfig()
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if config != nil {
			config.Client.CompressRequestBody = c.compressRequestBodyEnabled
			// Only enable verbose logging if DEBUG=true
			if tptestutil.IsDebugEnabled(t) {
				config.Client.Logger = &opensearchtransport.ColorLogger{Output: os.Stdout}
			}
			client, _ = opensearchapi.NewClient(*config)
		}

		client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
			Indices: []string{indexName},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		})
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
					// Pass client to avoid internal client creation that schedules node discovery
					bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
						Index:      indexName,
						Client:     client,
						ErrorTrace: true,
						Human:      true,
						Pretty:     true,
						// FlushBytes: 3e+6,
					})

					start := time.Now().UTC()

					// Validate numItems is within safe int range
					if tt.numItems > math.MaxInt {
						t.Fatalf("numItems too large: %d", tt.numItems)
					}

					// Track failures for debugging
					var failureCount int
					var firstFailureErr error

					for i := 1; i <= int(tt.numItems); i++ { // #nosec G115 -- checked bounds above
						err := bi.Add(ctx, opensearchutil.BulkIndexerItem{
							Index:      indexName,
							Action:     tt.action,
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(tt.body),
							OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem) {
								if tptestutil.IsDebugEnabled(t) {
									t.Logf("[%s] SUCCESS: id=%s, index=%s, status=%d", tt.action, item.DocumentID, resp.Index, resp.Status)
								}
							},
							OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
								failureCount++
								if firstFailureErr == nil {
									firstFailureErr = err
								}
								// Only log individual failures if unexpected or in debug mode
								if (tt.numFailed == 0 || tptestutil.IsDebugEnabled(t)) && failureCount <= 5 {
									t.Logf("[%s] FAILURE: id=%s, index=%s, status=%d, err=%v, resp.Error=%+v",
										tt.action, item.DocumentID, resp.Index, resp.Status, err, resp.Error)
								}
							},
						})
						if err != nil {
							t.Fatalf("Unexpected error adding item: %s", err)
						}
					}

					if err := bi.Close(ctx); err != nil {
						t.Errorf("Unexpected error closing bulk indexer: %v", err)
					}

					stats := bi.Stats()

					// Only log failure summary if count doesn't match expectations
					if stats.NumFailed != tt.numFailed {
						t.Logf("Unexpected failure count for %s: want=%d, got=%d, first error: %v",
							tt.action, tt.numFailed, stats.NumFailed, firstFailureErr)
					}

					if stats.NumAdded != tt.numItems {
						t.Errorf("Unexpected NumAdded: want=%d, got=%d", tt.numItems, stats.NumAdded)
					}

					if stats.NumIndexed != tt.numIndexed {
						t.Errorf("Unexpected NumIndexed: want=%d, got=%d", tt.numIndexed, stats.NumIndexed)
					}

					if stats.NumUpdated != tt.numUpdated {
						t.Errorf("Unexpected NumUpdated: want=%d, got=%d", tt.numUpdated, stats.NumUpdated)
					}

					if stats.NumCreated != tt.numCreated {
						t.Errorf("Unexpected NumCreated: want=%d, got=%d", tt.numCreated, stats.NumCreated)
					}

					if stats.NumFailed != tt.numFailed {
						t.Errorf("Unexpected NumFailed: want=%d, got=%d", tt.numFailed, stats.NumFailed)
					}

					if tptestutil.IsDebugEnabled(t) {
						t.Logf("  Added %d documents to indexer. Succeeded: %d. Failed: %d. Requests: %d. Duration: %s (%.0f docs/sec)",
							stats.NumAdded,
							stats.NumFlushed,
							stats.NumFailed,
							stats.NumRequests,
							time.Since(start).Truncate(time.Millisecond),
							1000.0/float64(time.Since(start)/time.Millisecond)*float64(stats.NumFlushed))
					}
				})

				t.Run("Multiple indices", func(t *testing.T) {
					var failureCount int
					var firstFailureErr error

					bi, _ := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
						Index:  "test-index-a",
						Client: client,
						OnError: func(ctx context.Context, err error) {
							t.Logf("BulkIndexer error: %v", err)
						},
					})

					// Default index
					for i := 1; i <= 10; i++ {
						err := bi.Add(ctx, opensearchutil.BulkIndexerItem{
							Action:     "index",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(tt.body),
							OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem) {
								if tptestutil.IsDebugEnabled(t) {
									t.Logf("[Multiple indices] SUCCESS: id=%s, index=%s", item.DocumentID, resp.Index)
								}
							},
							OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
								failureCount++
								if firstFailureErr == nil {
									firstFailureErr = err
								}
								t.Logf("[Multiple indices] FAILURE: id=%s, index=%s, status=%d, err=%v, resp.Error=%+v",
									item.DocumentID, resp.Index, resp.Status, err, resp.Error)
							},
						})
						if err != nil {
							t.Fatalf("Unexpected error adding to default index: %v", err)
						}
					}

					// Index 1
					for i := 1; i <= 10; i++ {
						err := bi.Add(ctx, opensearchutil.BulkIndexerItem{
							Action: "index",
							Index:  "test-index-b",
							Body:   strings.NewReader(tt.body),
							OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem) {
								if tptestutil.IsDebugEnabled(t) {
									t.Logf("[Multiple indices] SUCCESS: index=%s", resp.Index)
								}
							},
							OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
								failureCount++
								if firstFailureErr == nil {
									firstFailureErr = err
								}
								t.Logf("[Multiple indices] FAILURE: index=%s, status=%d, err=%v, resp.Error=%+v",
									resp.Index, resp.Status, err, resp.Error)
							},
						})
						if err != nil {
							t.Fatalf("Unexpected error adding to test-index-b: %v", err)
						}
					}

					// Index 2
					for i := 1; i <= 10; i++ {
						err := bi.Add(ctx, opensearchutil.BulkIndexerItem{
							Action: "index",
							Index:  "test-index-c",
							Body:   strings.NewReader(tt.body),
							OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem) {
								if tptestutil.IsDebugEnabled(t) {
									t.Logf("[Multiple indices] SUCCESS: index=%s", resp.Index)
								}
							},
							OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
								failureCount++
								if firstFailureErr == nil {
									firstFailureErr = err
								}
								t.Logf("[Multiple indices] FAILURE: index=%s, status=%d, err=%v, resp.Error=%+v",
									resp.Index, resp.Status, err, resp.Error)
							},
						})
						if err != nil {
							t.Fatalf("Unexpected error adding to test-index-c: %v", err)
						}
					}

					if err := bi.Close(ctx); err != nil {
						t.Errorf("Unexpected error closing bulk indexer: %v", err)
					}
					stats := bi.Stats()

					if failureCount > 0 {
						t.Logf("[Multiple indices] Total failures: %d, first error: %v", failureCount, firstFailureErr)
					}

					expectedIndexed := 10 + 10 + 10
					// #nosec G115 -- small constant value, no overflow risk
					if stats.NumIndexed != uint64(expectedIndexed) {
						t.Errorf("Unexpected NumIndexed: want=%d, got=%d", expectedIndexed, stats.NumIndexed)
					}

					t.Logf("[Multiple indices] About to check indices existence...")
					res, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{
						Indices: []string{"test-index-a", "test-index-b", "test-index-c"},
					})
					if err != nil {
						t.Fatalf("Unexpected error checking indices: %v", err)
					}
					if res.StatusCode != http.StatusOK {
						t.Errorf("Expected indices to exist, but got a [%s] response", res.Status())
					}
				})
			})
		}
	}
}
