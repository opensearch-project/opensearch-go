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

package opensearchutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

var infoBody = `{
  "version" : {
	"number" : "1.0.0",
	"distribution" : "opensearch"
  }
}`

var defaultRoundTripFunc = func(*http.Request) (*http.Response, error) {
	return &http.Response{Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

type mockTransport struct {
	RoundTripFunc func(*http.Request) (*http.Response, error)
}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.RoundTripFunc == nil {
		return defaultRoundTripFunc(req)
	}
	return t.RoundTripFunc(req)
}

func infoResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(infoBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func TestWriteMeta(t *testing.T) {
	testIndex := testutil.MustUniqueString(t, "test-index")

	type args struct {
		item BulkIndexerItem
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr error
	}{
		{
			name: "without _index and _id",
			args: args{BulkIndexerItem{Action: "index"}},
			want: `{"index":{}}` + "\n",
		},
		{
			name: "with _id",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "42",
			}},
			want: `{"index":{"_id":"42"}}` + "\n",
		},
		{
			name: "with _index",
			args: args{BulkIndexerItem{
				Action: "index",
				Index:  testIndex,
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s"}}`, testIndex) + "\n",
		},
		{
			name: "with _index and _id",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "42",
				Index:      testIndex,
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42"}}`, testIndex) + "\n",
		},
		{
			name: "with if_seq_no and if_primary_term",
			args: args{BulkIndexerItem{
				Action:        "index",
				DocumentID:    "42",
				Index:         testIndex,
				IfSeqNum:      int64Pointer(5),
				IfPrimaryTerm: int64Pointer(1),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","if_seq_no":5,"if_primary_term":1}}`, testIndex) + "\n",
		},
		{
			name: "with version and no document, if_seq_no, and if_primary_term",
			args: args{BulkIndexerItem{
				Action:  "index",
				Index:   testIndex,
				Version: int64Pointer(23),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s"}}`, testIndex) + "\n",
		},
		{
			name: "with version",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "42",
				Index:      testIndex,
				Version:    int64Pointer(24),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":24}}`, testIndex) + "\n",
		},
		{
			name: "with version and version_type",
			args: args{BulkIndexerItem{
				Action:      "index",
				DocumentID:  "42",
				Index:       testIndex,
				Version:     int64Pointer(25),
				VersionType: strPointer("external"),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,"version_type":"external"}}`, testIndex) + "\n",
		},
		{
			name: "wait_for_active_shards",
			args: args{BulkIndexerItem{
				Action:              "index",
				DocumentID:          "42",
				Index:               testIndex,
				Version:             int64Pointer(25),
				VersionType:         strPointer("external"),
				WaitForActiveShards: 1,
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,"version_type":"external","wait_for_active_shards":1}}`, testIndex) + "\n",
		},
		{
			name: "wait_for_active_shards, all",
			args: args{BulkIndexerItem{
				Action:              "index",
				DocumentID:          "42",
				Index:               testIndex,
				Version:             int64Pointer(25),
				VersionType:         strPointer("external"),
				WaitForActiveShards: "all",
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,"version_type":"external","wait_for_active_shards":"all"}}`, testIndex) + "\n",
		},
		{
			name: "with retry_on_conflict",
			args: args{BulkIndexerItem{
				Action:          "index",
				DocumentID:      "42",
				Index:           testIndex,
				Version:         int64Pointer(25),
				VersionType:     strPointer("external"),
				RetryOnConflict: intPointer(5),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,"version_type":"external","retry_on_conflict":5}}`, testIndex) + "\n",
		},
		{
			name: "_id with angle brackets is not HTML-escaped",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "prefix|<root_account>|suffix",
				Index:      testIndex,
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"prefix|<root_account>|suffix"}}`, testIndex) + "\n",
		},
		{
			name: "_id with ampersand is not HTML-escaped",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "foo&bar",
				Index:      testIndex,
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"foo&bar"}}`, testIndex) + "\n",
		},
		{
			name: "encode error from unsupported value",
			args: args{BulkIndexerItem{
				Action:              "index",
				DocumentID:          "1",
				WaitForActiveShards: math.NaN(),
			}},
			wantErr: &json.UnsupportedValueError{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bi := &bulkIndexer{
				metaPoolMaxBytes: defaultMetaBufferPoolMaxBytes,
				metaPool: sync.Pool{
					New: func() any { return new(bytes.Buffer) },
				},
			}
			w := &worker{
				bi:  bi,
				buf: bytes.NewBuffer(make([]byte, 0, 5e+6)),
			}
			err := w.writeMeta(tt.args.item)
			if tt.wantErr != nil {
				require.ErrorAs(t, err, &tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, w.buf.String())
		})
	}
}

func TestBulkIndexerLifecycle(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T) BulkIndexerStats
		want BulkIndexerStats
	}{
		{
			name: "3-batch sequential responses",
			run: func(t *testing.T) BulkIndexerStats {
				var (
					wg        sync.WaitGroup
					countReqs int
					testfile  string
				)

				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						if request.URL.Path == "/" {
							return infoResponse()
						}

						countReqs++
						switch countReqs {
						case 1:
							testfile = "testdata/bulk_response_1a.json"
						case 2:
							testfile = "testdata/bulk_response_1b.json"
						case 3:
							testfile = "testdata/bulk_response_1c.json"
						}
						bodyContent, _ := os.ReadFile(testfile)
						return &http.Response{Body: io.NopCloser(bytes.NewBuffer(bodyContent))}, nil
					},
				}}})

				cfg := BulkIndexerConfig{
					NumWorkers:    1,
					FlushBytes:    50,
					FlushInterval: time.Hour,
					Client:        client,
				}
				if testutil.IsDebugEnabled(t) {
					cfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, _ := NewBulkIndexer(cfg)

				for i := 1; i <= 6; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						err := bi.Add(context.Background(), BulkIndexerItem{
							Action:     "foo",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(fmt.Sprintf(`{"title":"foo-%d"}`, i)),
						})
						if err != nil {
							t.Errorf("Unexpected error: %s", err)
						}
					}(i)
				}
				wg.Wait()

				require.NoError(t, bi.Close(context.Background()))
				return bi.Stats()
			},
			want: BulkIndexerStats{
				NumAdded:    6,
				NumFlushed:  4,
				NumFailed:   2,
				NumIndexed:  1,
				NumCreated:  1,
				NumDeleted:  1,
				NumUpdated:  1,
				NumRequests: 3,
			},
		},
		{
			name: "automatic flush on interval",
			run: func(t *testing.T) BulkIndexerStats {
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						if request.URL.Path == "/" {
							return infoResponse()
						}
						return &http.Response{
							StatusCode: http.StatusOK,
							Status:     "200 OK",
							Body:       io.NopCloser(strings.NewReader(`{"items":[{"index": {}}]}`)),
						}, nil
					},
				}}})

				cfg := BulkIndexerConfig{
					NumWorkers:    1,
					Client:        client,
					FlushInterval: 50 * time.Millisecond,
				}
				if testutil.IsDebugEnabled(t) {
					cfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, _ := NewBulkIndexer(cfg)

				bi.Add(context.Background(),
					BulkIndexerItem{Action: "index", Body: strings.NewReader(`{"title":"foo"}`)})

				// Allow auto-flush to fire
				time.Sleep(250 * time.Millisecond)

				stats := bi.Stats()

				// Clear the timer before closing
				time.Sleep(200 * time.Millisecond)
				bi.Close(context.Background())

				return stats
			},
			want: BulkIndexerStats{
				NumAdded:    1,
				NumFlushed:  1,
				NumFailed:   0,
				NumIndexed:  1,
				NumRequests: 1,
			},
		},
		{
			name: "retry on 429 TooManyRequests",
			run: func(t *testing.T) BulkIndexerStats {
				var (
					wg        sync.WaitGroup
					countReqs int
				)

				cfg := opensearchapi.Config{
					Client: opensearch.Config{
						Transport: &mockTransport{
							RoundTripFunc: func(request *http.Request) (*http.Response, error) {
								if request.URL.Path == "/" {
									return infoResponse()
								}

								countReqs++
								if countReqs <= 4 {
									return &http.Response{
										StatusCode: http.StatusTooManyRequests,
										Status:     "429 TooManyRequests",
										Body:       io.NopCloser(strings.NewReader(`{"took":1}`)),
									}, nil
								}
								bodyContent, _ := os.ReadFile("testdata/bulk_response_1c.json")
								return &http.Response{
									StatusCode: http.StatusOK,
									Status:     "200 OK",
									Body:       io.NopCloser(bytes.NewBuffer(bodyContent)),
								}, nil
							},
						},

						MaxRetries:    5,
						RetryOnStatus: []int{502, 503, 504, 429},
						RetryBackoff: func(i int) time.Duration {
							if testutil.IsDebugEnabled(t) {
								t.Logf("*** Retry #%d", i)
							}
							return time.Duration(i) * 100 * time.Millisecond
						},
					},
				}
				if testutil.IsDebugEnabled(t) {
					cfg.Client.Logger = &opensearchtransport.ColorLogger{Output: os.Stdout}
				}
				client, _ := opensearchapi.NewClient(cfg)

				biCfg := BulkIndexerConfig{NumWorkers: 1, FlushBytes: 50, Client: client}
				if testutil.IsDebugEnabled(t) {
					biCfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, _ := NewBulkIndexer(biCfg)

				for i := 1; i <= 2; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						err := bi.Add(context.Background(), BulkIndexerItem{
							Action: "foo",
							Body:   strings.NewReader(`{"title":"foo"}`),
						})
						if err != nil {
							t.Errorf("Unexpected error: %s", err)
						}
					}()
				}
				wg.Wait()

				require.NoError(t, bi.Close(context.Background()))
				return bi.Stats()
			},
			want: BulkIndexerStats{
				NumAdded:    2,
				NumFlushed:  2,
				NumFailed:   0,
				NumDeleted:  1,
				NumUpdated:  1,
				NumRequests: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBulkIndexerContext(t *testing.T) {
	tests := []struct {
		name  string
		run   func(t *testing.T)
	}{
		{
			name: "Add returns error on expired context",
			run: func(t *testing.T) {
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				bi, _ := NewBulkIndexer(BulkIndexerConfig{NumWorkers: 1, Client: client})
				ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
				defer cancel()
				time.Sleep(100 * time.Millisecond)

				const numAttempts = 10
				errs := make([]error, 0, numAttempts)
				for range numAttempts {
					errs = append(errs, bi.Add(ctx, BulkIndexerItem{Action: "delete", DocumentID: "timeout"}))
				}
				require.NoError(t, bi.Close(context.Background()))

				var gotDeadline bool
				for _, err := range errs {
					if errors.Is(err, context.DeadlineExceeded) {
						gotDeadline = true
					}
				}
				require.True(t, gotDeadline, "expected at least one context.DeadlineExceeded in: %q", errs)
			},
		},
		{
			name: "Close returns error on cancelled context",
			run: func(t *testing.T) {
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				bi, _ := NewBulkIndexer(BulkIndexerConfig{
					NumWorkers: 1,
					FlushBytes: 1,
					Client:     client,
				})

				for range 10 {
					bi.Add(context.Background(), BulkIndexerItem{Action: "foo"})
				}

				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				require.Error(t, bi.Close(ctx))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func TestBulkIndexerCallbacks(t *testing.T) {
	tests := []struct {
		name  string
		run   func(t *testing.T)
	}{
		{
			name: "OnError called on transport failure",
			run: func(t *testing.T) {
				config := opensearchapi.Config{
					Client: opensearch.Config{
						Transport: &mockTransport{
							RoundTripFunc: func(request *http.Request) (*http.Response, error) {
								if request.URL.Path == "/" {
									return &http.Response{Body: io.NopCloser(strings.NewReader(infoBody))}, nil
								}
								return nil, fmt.Errorf("Mock transport error")
							},
						},
					},
				}
				if testutil.IsDebugEnabled(t) {
					config.Client.Logger = &opensearchtransport.ColorLogger{
						Output:             os.Stdout,
						EnableRequestBody:  true,
						EnableResponseBody: true,
					}
				}
				client, _ := opensearchapi.NewClient(config)

				var (
					indexerError error
					onErrorCount int
				)
				biCfg := BulkIndexerConfig{
					NumWorkers: 1,
					Client:     client,
					OnError: func(ctx context.Context, err error) {
						onErrorCount++
						indexerError = err
					},
				}
				if testutil.IsDebugEnabled(t) {
					biCfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, _ := NewBulkIndexer(biCfg)

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{Action: "foo"}))
				require.NoError(t, bi.Close(context.Background()))

				require.NotNil(t, indexerError, "expected indexer OnError to be called")
				require.Equal(t, 1, onErrorCount, "OnError call count")
			},
		},
		{
			name: "per-item OnSuccess and OnFailure",
			run: func(t *testing.T) {
				var (
					countSuccessful      uint64
					countFailed          uint64
					failedIDs            []string
					successfulItemBodies []string
					failedItemBodies     []string

					bodyFailureCount     = make(map[string]int)
					bodiesExpectedToFail = map[string]struct{}{
						`{"title":"bar"}`: {},
						`{"title":"baz"}`: {},
					}
				)

				bodyContent, _ := os.ReadFile("testdata/bulk_response_2.json")
				client, _ := opensearchapi.NewClient(
					opensearchapi.Config{
						Client: opensearch.Config{
							Transport: &mockTransport{
								RoundTripFunc: func(request *http.Request) (*http.Response, error) {
									if request.URL.Path == "/" {
										return infoResponse()
									}
									return &http.Response{Body: io.NopCloser(bytes.NewBuffer(bodyContent))}, nil
								},
							},
						},
					},
				)

				cfg := BulkIndexerConfig{NumWorkers: 1, Client: client}
				if testutil.IsDebugEnabled(t) {
					cfg.DebugLogger = log.New(os.Stdout, "", 0)
				}
				bi, _ := NewBulkIndexer(cfg)

				successFunc := func(ctx context.Context, item BulkIndexerItem, res opensearchapi.BulkRespItem) {
					atomic.AddUint64(&countSuccessful, 1)
					buf, err := io.ReadAll(item.Body)
					if err != nil {
						t.Fatalf("Unexpected error: %s", err)
					}
					successfulItemBodies = append(successfulItemBodies, string(buf))
				}

				failureFunc := func(ctx context.Context, item BulkIndexerItem, res opensearchapi.BulkRespItem, err error) {
					if err != nil {
						t.Fatalf("Unexpected error: %s", err)
					}
					buf, err := io.ReadAll(item.Body)
					if err != nil {
						t.Fatalf("Unexpected error: %s", err)
					}
					countFailed++
					failedIDs = append(failedIDs, item.DocumentID)
					failedItemBodies = append(failedItemBodies, string(buf))
					bodyFailureCount[string(buf)]++
				}

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "index", DocumentID: "1",
					Body: strings.NewReader(`{"title":"foo"}`), OnSuccess: successFunc, OnFailure: failureFunc,
				}))
				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "create", DocumentID: "1",
					Body: strings.NewReader(`{"title":"bar"}`), OnSuccess: successFunc, OnFailure: failureFunc,
				}))
				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "delete", DocumentID: "2",
					Body: strings.NewReader(`{"title":"baz"}`), OnSuccess: successFunc, OnFailure: failureFunc,
				}))
				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "update", DocumentID: "3",
					Body: strings.NewReader(`{"doc":{"title":"qux"}}`), OnSuccess: successFunc, OnFailure: failureFunc,
				}))

				require.NoError(t, bi.Close(context.Background()))

				stats := bi.Stats()

				require.Equal(t, uint64(4), stats.NumAdded, "NumAdded")
				require.Equal(t, uint64(2), stats.NumFailed, "NumFailed")
				require.Equal(t, uint64(2), stats.NumFlushed, "NumFlushed")
				require.Equal(t, uint64(1), stats.NumIndexed, "NumIndexed")
				require.Equal(t, uint64(1), stats.NumUpdated, "NumUpdated")
				require.Equal(t, uint64(2), countSuccessful, "countSuccessful")
				require.Equal(t, uint64(2), countFailed, "countFailed")

				require.Equal(t, stats.NumFailed, uint64(len(bodyFailureCount)), "bodyFailureCount length")
				for k, v := range bodyFailureCount {
					_, ok := bodiesExpectedToFail[k]
					require.True(t, ok, "unexpected item body failure: %v", k)
					delete(bodiesExpectedToFail, k)
					require.Equal(t, 1, v, "failure callback count for item %v", k)
				}
				require.Empty(t, bodiesExpectedToFail, "missing failure callbacks for item bodies")

				require.Equal(t, []string{"1", "2"}, failedIDs)
				require.Equal(t, []string{`{"title":"foo"}`, `{"doc":{"title":"qux"}}`}, successfulItemBodies)
				require.Equal(t, []string{`{"title":"bar"}`, `{"title":"baz"}`}, failedItemBodies)
			},
		},
		{
			name: "OnFlushStart and OnFlushEnd",
			run: func(t *testing.T) {
				type contextKey string
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						if request.URL.Path == "/" {
							return infoResponse()
						}
						return &http.Response{
							StatusCode: http.StatusOK,
							Status:     "200 OK",
							Body:       io.NopCloser(strings.NewReader(`{"items":[{"index":{}}]}`)),
						}, nil
					},
				}}})
				flushIndex := testutil.MustUniqueString(t, "test-flush")

				var flushEndCalled bool
				bi, _ := NewBulkIndexer(BulkIndexerConfig{
					Client: client,
					Index:  flushIndex,
					OnFlushStart: func(ctx context.Context) context.Context {
						return context.WithValue(ctx, contextKey("flushing"), true)
					},
					OnFlushEnd: func(ctx context.Context) {
						if v, ok := ctx.Value(contextKey("flushing")).(bool); ok && v {
							flushEndCalled = true
						}
					},
				})

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "index",
					Body:   strings.NewReader(`{"title":"foo"}`),
				}))
				require.NoError(t, bi.Close(context.Background()))

				require.Equal(t, uint64(1), bi.Stats().NumAdded, "NumAdded")
				require.True(t, flushEndCalled, "OnFlushEnd should have been called with the context from OnFlushStart")
			},
		},
		{
			name: "per-item OnFailure on bulk request error",
			run: func(t *testing.T) {
				var (
					numItems          uint64 = 5
					idsExpectedToFail        = make(map[string]struct{}, numItems)
					idsFailureCount          = make(map[string]int)

					onErrorCallCount uint64
					wg               sync.WaitGroup
				)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				client, _ := opensearchapi.NewClient(opensearchapi.Config{
					Client: opensearch.Config{
						Transport: &mockTransport{
							RoundTripFunc: func(request *http.Request) (*http.Response, error) {
								if request.URL.Path == "/" {
									return infoResponse()
								}
								return nil, errors.New("simulated bulk request error")
							},
						},
					},
					})


				bi, _ := NewBulkIndexer(BulkIndexerConfig{
					NumWorkers: 1,
					FlushBytes: 1,
					Client:     client,
					OnError: func(ctx context.Context, err error) {
						onErrorCallCount++
						if err.Error() != "flush: simulated bulk request error" {
							t.Errorf("Unexpected error: %v", err)
						}
					},
				})

				wg.Add(int(numItems))
				for i := 0; i < int(numItems); i++ {
					id := fmt.Sprintf("id_%d", i)
					idsExpectedToFail[id] = struct{}{}
					require.NoError(t, bi.Add(ctx, BulkIndexerItem{
						Action:     "index",
						DocumentID: id,
						Body:       strings.NewReader(fmt.Sprintf(`{"title":"doc_%d"}`, i)),
						OnFailure: func(ctx context.Context, item BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
							if err.Error() != "flush: simulated bulk request error" {
								t.Errorf("Unexpected error in OnFailure: %v", err)
							}
							idsFailureCount[item.DocumentID]++
							wg.Done()
						},
					}))
				}

				require.NoError(t, bi.Close(ctx))
				wg.Wait()

				stats := bi.Stats()

				require.Equal(t, numItems, onErrorCallCount, "OnError call count")
				require.Equal(t, numItems, stats.NumFailed, "NumFailed")
				require.Equal(t, int(numItems), len(idsFailureCount), "idsFailureCount length")

				for k, v := range idsFailureCount {
					_, ok := idsExpectedToFail[k]
					require.True(t, ok, "unexpected item ID failure: %v", k)
					delete(idsExpectedToFail, k)
					require.Equal(t, 1, v, "failure callback count for item %v", k)
				}
				require.Empty(t, idsExpectedToFail, "missing failure callbacks for item IDs")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func strPointer(s string) *string {
	return &s
}

func int64Pointer(i int64) *int64 {
	return &i
}

func intPointer(i int) *int {
	return &i
}
