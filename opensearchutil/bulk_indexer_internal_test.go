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
	"golang.org/x/sync/errgroup"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/errmask"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
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
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,`+
				`"version_type":"external","wait_for_active_shards":1}}`, testIndex) + "\n",
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
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,`+
				`"version_type":"external","wait_for_active_shards":"all"}}`, testIndex) + "\n",
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
				// The only error case expects a *json.UnsupportedValueError;
				// match the concrete type via a typed target so ErrorAs does
				// not receive a *error.
				var target *json.UnsupportedValueError
				require.ErrorAs(t, err, &target)
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
				t.Helper()
				var (
					wg        sync.WaitGroup
					countReqs int
					testfile  string
				)

				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						// The default router (on by default in v5) issues node
						// discovery requests; only count actual bulk requests so
						// the fixture sequence stays aligned.
						if request.URL.Path != "/_bulk" {
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
				t.Cleanup(func() { _ = client.Close() })

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
					wg.Go(func() {
						err := bi.Add(context.Background(), BulkIndexerItem{
							Action:     "foo",
							DocumentID: strconv.Itoa(i),
							Body:       strings.NewReader(fmt.Sprintf(`{"title":"foo-%d"}`, i)),
						})
						if err != nil {
							t.Errorf("Unexpected error: %s", err)
						}
					})
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
				t.Helper()
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
				t.Cleanup(func() { _ = client.Close() })

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
				t.Helper()
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
						RetryOnStatus: []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusTooManyRequests},
						RetryBackoff: func(i int) time.Duration {
							if testutil.IsDebugEnabled(t) {
								t.Logf("*** Retry #%d", i)
							}
							return time.Duration(i) * 100 * time.Millisecond
						},
						// Disable on-start discovery: the default (router-on)
						// config would issue node-discovery requests that hit the
						// mock concurrently with the bulk worker, racing on the
						// countReqs counter below.
						DiscoverNodesOnStart: func() *bool { b := false; return &b }(),
					},
				}
				if testutil.IsDebugEnabled(t) {
					cfg.Client.Logger = &opensearchtransport.ColorLogger{Output: os.Stdout}
				}
				client, _ := opensearchapi.NewClient(cfg)
				t.Cleanup(func() { _ = client.Close() })

				biCfg := BulkIndexerConfig{NumWorkers: 1, FlushBytes: 50, Client: client}
				if testutil.IsDebugEnabled(t) {
					biCfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, _ := NewBulkIndexer(biCfg)

				for i := 1; i <= 2; i++ {
					wg.Go(func() {
						err := bi.Add(context.Background(), BulkIndexerItem{
							Action: "foo",
							Body:   strings.NewReader(`{"title":"foo"}`),
						})
						if err != nil {
							t.Errorf("Unexpected error: %s", err)
						}
					})
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
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Add returns error on expired context",
			run: func(t *testing.T) {
				t.Helper()
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				t.Cleanup(func() { _ = client.Close() })
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
			name: "Add does not increment NumAdded when context is already cancelled",
			run: func(t *testing.T) {
				t.Helper()
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				t.Cleanup(func() { _ = client.Close() })
				bi, _ := NewBulkIndexer(BulkIndexerConfig{NumWorkers: 1, Client: client})

				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				// select{} chooses randomly between the two ready cases when the
				// queue still has room, so we cannot assert every Add fails. We can
				// assert the bookkeeping invariant: each Add ends up in exactly one
				// of NumAdded or BulkAddFailCount, never both, never neither.
				const numAttempts = 50
				var nilReturns, errReturns uint64
				for range numAttempts {
					if err := bi.Add(ctx, BulkIndexerItem{Action: "index", DocumentID: "cancelled"}); err == nil {
						nilReturns++
					} else {
						require.ErrorIs(t, err, context.Canceled)
						errReturns++
					}
				}
				require.NoError(t, bi.Close(context.Background()))

				stats := bi.Stats()
				require.Equal(t, nilReturns, stats.NumAdded, "NumAdded must equal the number of Add() calls that returned nil")
				require.Equal(t, errReturns, stats.BulkAddFailCount, "BulkAddFailCount must equal the number of Add() calls that returned ctx.Err()")
				require.Equal(t, uint64(numAttempts), stats.NumAdded+stats.BulkAddFailCount, "every Add() must be accounted for exactly once")
				require.Positive(t, errReturns, "at least one Add() should fail when context is already cancelled")
			},
		},
		{
			name: "Close returns error on cancelled context",
			run: func(t *testing.T) {
				t.Helper()
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				t.Cleanup(func() { _ = client.Close() })
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
		{
			name: "Close does not hang when construction context was cancelled",
			run: func(t *testing.T) {
				t.Helper()
				// The flusher goroutine exits when the construction context is
				// cancelled. A later Close must still return rather than block
				// forever signaling a flusher that already left. Regression for
				// the unbuffered done-channel deadlock.
				client, _ := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
				t.Cleanup(func() { _ = client.Close() })
				ctx, cancel := context.WithCancel(t.Context())
				bi, err := NewBulkIndexer(BulkIndexerConfig{
					NumWorkers: 1,
					Client:     client,
					Context:    ctx,
				})
				require.NoError(t, err)

				// Cancel the construction context and let the flusher observe it
				// and return (closing flusherDone).
				cancel()
				select {
				case <-bi.(*bulkIndexer).flusherDone:
				case <-time.After(time.Second):
					t.Fatal("flusher did not stop after construction context was cancelled")
				}

				done := make(chan error, 1)
				go func() { done <- bi.Close(t.Context()) }()
				select {
				case <-done:
					// Close returned; no deadlock.
				case <-time.After(5 * time.Second):
					t.Fatal("Close hung after construction context was cancelled")
				}
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
		name string
		run  func(t *testing.T)
	}{
		{
			name: "OnError called on transport failure",
			run: func(t *testing.T) {
				t.Helper()
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
				t.Cleanup(func() { _ = client.Close() })

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

				require.Error(t, indexerError, "expected indexer OnError to be called")
				require.Equal(t, 1, onErrorCount, "OnError call count")
			},
		},
		{
			// v5 reports *PartialBulkError when errors:true. flush already
			// skips handleBulkError and dispatches OnSuccess/OnFailure, but
			// used to return the partial error, so Close/auto-flush/worker.run
			// treated a mixed batch as a transport failure.
			name: "OnError is not called on partial bulk item failures",
			run: func(t *testing.T) {
				t.Helper()
				bodyContent, err := os.ReadFile("testdata/bulk_response_2.json")
				require.NoError(t, err)

				client, err := opensearchapi.NewClient(
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
						// Pin BulkItems unmasked so this test still hits
						// *PartialBulkError if the version default changes.
						Errors: errmask.New(),
					},
				)
				require.NoError(t, err)
				t.Cleanup(func() { _ = client.Close() })

				var (
					onErrorCount    int
					countSuccessful uint64
					countFailed     uint64
				)
				biCfg := BulkIndexerConfig{
					NumWorkers: 1,
					Client:     client,
					OnError: func(context.Context, error) {
						onErrorCount++
					},
				}
				if testutil.IsDebugEnabled(t) {
					biCfg.DebugLogger = log.New(os.Stdout, "", 0)
				}

				bi, err := NewBulkIndexer(biCfg)
				require.NoError(t, err)

				successFunc := func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) {
					atomic.AddUint64(&countSuccessful, 1)
				}
				failureFunc := func(_ context.Context, _ BulkIndexerItem, _ opensearchapi.BulkRespItem, err error) {
					require.NoError(t, err)
					atomic.AddUint64(&countFailed, 1)
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

				require.Equal(t, 0, onErrorCount, "OnError must not fire for per-item bulk failures")
				require.Equal(t, uint64(2), countSuccessful, "countSuccessful")
				require.Equal(t, uint64(2), countFailed, "countFailed")
			},
		},
		{
			name: "per-item OnSuccess and OnFailure",
			run: func(t *testing.T) {
				t.Helper()
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
				t.Cleanup(func() { _ = client.Close() })

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
				t.Helper()
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
				t.Cleanup(func() { _ = client.Close() })
				flushIndex := testutil.MustUniqueString(t, "test-flush")

				var flushEndCalled atomic.Bool
				bi, _ := NewBulkIndexer(BulkIndexerConfig{
					Client: client,
					Index:  flushIndex,
					OnFlushStart: func(ctx context.Context) context.Context {
						return context.WithValue(ctx, contextKey("flushing"), true)
					},
					OnFlushEnd: func(ctx context.Context) {
						if v, ok := ctx.Value(contextKey("flushing")).(bool); ok && v {
							flushEndCalled.Store(true)
						}
					},
				})

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "index",
					Body:   strings.NewReader(`{"title":"foo"}`),
				}))
				require.NoError(t, bi.Close(context.Background()))

				require.Equal(t, uint64(1), bi.Stats().NumAdded, "NumAdded")
				require.True(t, flushEndCalled.Load(), "OnFlushEnd should have been called with the context from OnFlushStart")
			},
		},
		{
			name: "per-item OnFailure on bulk request error",
			run: func(t *testing.T) {
				t.Helper()
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
				t.Cleanup(func() { _ = client.Close() })

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
				require.Len(t, idsFailureCount, int(numItems), "idsFailureCount length")

				for k, v := range idsFailureCount {
					_, ok := idsExpectedToFail[k]
					require.True(t, ok, "unexpected item ID failure: %v", k)
					delete(idsExpectedToFail, k)
					require.Equal(t, 1, v, "failure callback count for item %v", k)
				}
				require.Empty(t, idsExpectedToFail, "missing failure callbacks for item IDs")
			},
		},
		{
			name: "per-item OnFailure can read Error fields without nil checks",
			run: func(t *testing.T) {
				t.Helper()

				const bulkBody = `{
  "took": 1,
  "errors": true,
  "items": [
    {
      "create": {
        "_index": "i",
        "_id": "1",
        "status": 409,
        "error": {
          "type": "version_conflict_engine_exception",
          "reason": "already exists"
        }
      }
    },
    {"delete": {"_index": "i", "_id": "2", "status": 404, "result": "not_found"}}
  ]
}`
				client, _ := opensearchapi.NewClient(
					opensearchapi.Config{
						Client: opensearch.Config{
							Transport: &mockTransport{
								RoundTripFunc: func(request *http.Request) (*http.Response, error) {
									if request.URL.Path == "/" {
										return infoResponse()
									}
									return &http.Response{Body: io.NopCloser(strings.NewReader(bulkBody))}, nil
								},
							},
						},
					},
				)
				t.Cleanup(func() { _ = client.Close() })

				bi, _ := NewBulkIndexer(BulkIndexerConfig{NumWorkers: 1, Client: client})

				var (
					errorObjectFailure bool
					statusOnlyFailure  bool
				)
				readErrorFields := func(_ context.Context, _ BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
					require.NoError(t, err)
					require.NotNil(t, resp.Error)
					_ = resp.Error.Type
					_ = resp.Error.Reason
					if resp.Error.Type != "" {
						errorObjectFailure = true
					}
					if resp.Status == http.StatusNotFound {
						statusOnlyFailure = true
					}
				}

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "create", DocumentID: "1",
					Body: strings.NewReader(`{"title":"bar"}`), OnFailure: readErrorFields,
				}))
				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action: "delete", DocumentID: "2",
					Body: strings.NewReader(`{"title":"baz"}`), OnFailure: readErrorFields,
				}))
				require.NoError(t, bi.Close(context.Background()))

				require.True(t, errorObjectFailure, "expected error-object failure callback")
				require.True(t, statusOnlyFailure, "expected status-only failure callback")
			},
		},
		{
			name: "per-item OnFailure on writeMeta and writeBody errors",
			run: func(t *testing.T) {
				t.Helper()

				client, _ := opensearchapi.NewClient(opensearchapi.Config{
					Client: opensearch.Config{
						Transport: &mockTransport{
							RoundTripFunc: func(request *http.Request) (*http.Response, error) {
								if request.URL.Path == "/" {
									return infoResponse()
								}
								return &http.Response{Body: io.NopCloser(strings.NewReader(`{"items":[]}`))}, nil
							},
						},
					},
				})
				t.Cleanup(func() { _ = client.Close() })

				bi, _ := NewBulkIndexer(BulkIndexerConfig{NumWorkers: 1, Client: client})

				var (
					metaFailureCalled bool
					bodyFailureCalled bool
				)
				onFailure := func(_ context.Context, _ BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
					require.Error(t, err)
					require.NotNil(t, resp.Error)
					_ = resp.Error.Type
					_ = resp.Error.Reason
				}

				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action:              "index",
					DocumentID:          "meta-fail",
					WaitForActiveShards: math.NaN(),
					OnFailure: func(ctx context.Context, item BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
						onFailure(ctx, item, resp, err)
						metaFailureCalled = true
					},
				}))
				require.NoError(t, bi.Add(context.Background(), BulkIndexerItem{
					Action:     "index",
					DocumentID: "body-fail",
					Body:       failingReadBody{},
					OnFailure: func(ctx context.Context, item BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
						onFailure(ctx, item, resp, err)
						bodyFailureCalled = true
					},
				}))
				require.NoError(t, bi.Close(context.Background()))

				require.True(t, metaFailureCalled, "expected writeMeta OnFailure callback")
				require.True(t, bodyFailureCalled, "expected writeBody OnFailure callback")
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

type failingReadBody struct{}

func (failingReadBody) Read(_ []byte) (int, error) {
	return 0, errors.New("body read failed")
}

func (failingReadBody) Seek(int64, int) (int64, error) {
	return 0, nil
}

func int64Pointer(i int64) *int64 {
	return &i
}

func intPointer(i int) *int {
	return &i
}

func TestBulkIndexerOwnClientFlag(t *testing.T) {
	t.Run("implicit client is owned", func(t *testing.T) {
		bi, err := NewBulkIndexer(BulkIndexerConfig{})
		require.NoError(t, err)
		require.True(t, bi.(*bulkIndexer).implicitClient)
		require.NoError(t, bi.Close(context.Background()))
	})

	t.Run("supplied client is not owned", func(t *testing.T) {
		client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: &mockTransport{}}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client})
		require.NoError(t, err)
		require.False(t, bi.(*bulkIndexer).implicitClient)
		require.NoError(t, bi.Close(context.Background()))
	})

	t.Run("owned client is closed even when Close context is cancelled", func(t *testing.T) {
		// bulkIndexer.Close early-returns on a cancelled context; the owned
		// client must still be closed (via defer), or the leak this indexer
		// avoids would resurface on the cancel path. Observe the close through
		// the transport's CloseIdleConnections passthrough.
		tr := &closeRecordingTransport{}
		client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Transport: tr}})
		require.NoError(t, err)

		bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client})
		require.NoError(t, err)
		// Force ownership so Close treats this supplied client as implicitly
		// created (the real nil-client path also sets this).
		bi.(*bulkIndexer).implicitClient = true

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, bi.Close(ctx), context.Canceled)
		require.Positive(t, tr.idleClosed.Load(), "owned client must be closed on the cancelled-context path")
	})
}

// closeRecordingTransport is an http.RoundTripper whose CloseIdleConnections is
// invoked by opensearchtransport.Transport.Close, letting a test observe that
// the client was closed.
type closeRecordingTransport struct{ idleClosed atomic.Int32 }

func (t *closeRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return defaultRoundTripFunc(req)
}

func (t *closeRecordingTransport) CloseIdleConnections() { t.idleClosed.Add(1) }

func TestBulkIndexerQueueIndexPinsDocumentID(t *testing.T) {
	t.Parallel()

	// numWorkers is baked into wantIndex, so a change in shardhash.Hash or in
	// the modulo folding fails here instead of silently reshuffling documents.
	const numWorkers = 8

	tests := []struct {
		name       string
		documentID string
		wantIndex  int
	}{
		{name: "ascii", documentID: "user_123", wantIndex: 4},
		{name: "numeric", documentID: "42", wantIndex: 2},
		{name: "multibyte", documentID: "ünïcødé-🌍", wantIndex: 3},
		{name: "long", documentID: strings.Repeat("a", 512), wantIndex: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bi := &bulkIndexer{queues: make([]chan queueEntry, numWorkers)}
			for range 3 {
				require.Equal(t, tt.wantIndex, bi.queueIndex(BulkIndexerItem{DocumentID: tt.documentID}))
			}
		})
	}
}

func TestBulkIndexerQueueIndexRoundRobinsWithoutDocumentID(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 4
		rounds     = 3
	)

	bi := &bulkIndexer{queues: make([]chan queueEntry, numWorkers)}

	got := make([]int, numWorkers)
	for range numWorkers * rounds {
		got[bi.queueIndex(BulkIndexerItem{})]++
	}

	require.Equal(t, []int{rounds, rounds, rounds, rounds}, got)
}

func TestBulkIndexerAddDeliversToRoutedQueue(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 8
		numItems   = 10
		wantIndex  = 4 // shardhash.Hash("user_123") folded into numWorkers.
	)

	// Skip init so no worker drains the queues while the test inspects them.
	bi := &bulkIndexer{stats: &bulkIndexerStats{}, queues: make([]chan queueEntry, numWorkers)}
	for i := range bi.queues {
		bi.queues[i] = make(chan queueEntry, numItems)
	}

	for range numItems {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{Action: actionUpdate, DocumentID: "user_123"}))
	}

	for i, queue := range bi.queues {
		if i == wantIndex {
			require.Len(t, queue, numItems, "queue %d must hold every item for one document ID", i)
			continue
		}
		require.Empty(t, queue, "queue %d must stay empty", i)
	}
	require.Equal(t, uint64(numItems), bi.Stats().NumAdded)
}

// bulkDocumentIDs returns an "action:documentID" entry for each action line in
// a bulk request body, in the order the lines appear. Source lines are skipped:
// they either fail to decode into a bulkActionMetadata envelope or carry no key
// that names a bulk action.
func bulkDocumentIDs(body []byte) []string {
	var ids []string
	for line := range strings.SplitSeq(strings.TrimRight(string(body), "\n"), "\n") {
		var envelope map[string]bulkActionMetadata
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		for action, meta := range envelope {
			switch action {
			case actionIndex, actionCreate, actionUpdate, actionDelete:
				ids = append(ids, action+":"+meta.DocumentID)
			}
		}
	}
	return ids
}

func TestBulkIndexerKeepsDocumentActionsInOneRequest(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 8
		numFillers = numWorkers * 4
		hotDocID   = "user_123"
		hotActions = 3
		hotAction  = actionUpdate + ":" + hotDocID
		wantAdded  = numFillers + hotActions
	)

	var mu sync.Mutex
	// requests holds the action lines of each bulk request the indexer sent.
	var requests [][]string

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
		Transport: &mockTransport{RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "/_bulk") {
				return defaultRoundTripFunc(req)
			}
			// RoundTrip runs on a worker goroutine, so record the body and
			// leave every assertion to the test goroutine below.
			if body, readErr := io.ReadAll(req.Body); readErr == nil {
				mu.Lock()
				requests = append(requests, bulkDocumentIDs(body))
				mu.Unlock()
			}
			return defaultRoundTripFunc(req)
		}},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: numWorkers,
		Client:     client,
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	// Fill the other queues so a build that routes everything to one worker
	// cannot pass by accident.
	for i := range numFillers {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action:     actionIndex,
			DocumentID: "filler_" + strconv.Itoa(i),
			Body:       strings.NewReader(`{"a":1}`),
		}))
	}
	for range hotActions {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action:     actionUpdate,
			DocumentID: hotDocID,
			Body:       strings.NewReader(`{"doc":{"a":1}}`),
		}))
	}
	require.NoError(t, bi.Close(t.Context()))
	require.Equal(t, uint64(wantAdded), bi.Stats().NumAdded)

	mu.Lock()
	defer mu.Unlock()

	require.Equal(t, uint64(len(requests)), bi.Stats().NumRequests, "every bulk request must have been recorded")
	require.Greater(t, len(requests), 1, "fillers must spread over more than one worker")

	requestsCarryingHotDoc := 0
	for _, actions := range requests {
		hits := 0
		for _, action := range actions {
			if action == hotAction {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		requestsCarryingHotDoc++
		require.Equal(t, hotActions, hits, "one request must carry every action for %s", hotDocID)
	}
	require.Equal(t, 1, requestsCarryingHotDoc, "actions for %s must not be split across requests", hotDocID)
}

// bulkRecorder records the action lines of every bulk request an indexer sends.
type bulkRecorder struct {
	mu struct {
		sync.Mutex
		requests [][]string
	}
}

func (r *bulkRecorder) roundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/_bulk") {
		return defaultRoundTripFunc(req)
	}
	// RoundTrip runs on a worker goroutine, so record the body and leave every
	// assertion to the test goroutine.
	if body, err := io.ReadAll(req.Body); err == nil {
		r.mu.Lock()
		r.mu.requests = append(r.mu.requests, bulkDocumentIDs(body))
		r.mu.Unlock()
	}
	return defaultRoundTripFunc(req)
}

// takeActions returns every action line recorded so far and clears the record,
// so a caller can assert on one round of flushing at a time.
func (r *bulkRecorder) takeActions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var actions []string
	for _, request := range r.mu.requests {
		actions = append(actions, request...)
	}
	r.mu.requests = nil

	return actions
}

func newBulkTestClient(t *testing.T, roundTrip func(*http.Request) (*http.Response, error)) *opensearchapi.Client {
	t.Helper()

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
		Transport: &mockTransport{RoundTripFunc: roundTrip},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// flushContextKey types the value the Flush caller plants on its context, so
// the test can prove that context is what the bulk request ran under.
type flushContextKey struct{}

func TestBulkIndexerFlushRunsBulkRequestOnCallerContext(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 4
		wantValue  = "planted-by-flush-caller"
	)

	var flushed struct {
		sync.Mutex
		contextValues []any
	}

	var recorder bulkRecorder
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: numWorkers,
		Client:     newBulkTestClient(t, recorder.roundTrip),
		Index:      testutil.MustUniqueString(t, "test-index"),
		// OnFlushStart receives the context the flush is running under, which
		// is the same one the bulk request is issued with.
		OnFlushStart: func(ctx context.Context) context.Context {
			flushed.Lock()
			flushed.contextValues = append(flushed.contextValues, ctx.Value(flushContextKey{}))
			flushed.Unlock()

			return ctx
		},
	})
	require.NoError(t, err)

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     actionIndex,
		DocumentID: "doc_1",
		Body:       strings.NewReader(`{"a":1}`),
	}))

	require.NoError(t, bi.Flush(context.WithValue(t.Context(), flushContextKey{}, wantValue)))

	flushed.Lock()
	defer flushed.Unlock()

	// Exactly one element proves two things at once: the flush ran on the
	// caller's context rather than the worker's, and the workers holding
	// nothing did not fire the flush callbacks for a request they never sent.
	require.Equal(t, []any{wantValue}, flushed.contextValues)
}

func TestBulkIndexerFlushDrainsAndKeepsIndexerUsable(t *testing.T) {
	t.Parallel()

	const (
		numWorkers    = 4
		itemsPerRound = 12
		rounds        = 3
	)

	var recorder bulkRecorder
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: numWorkers,
		Client:     newBulkTestClient(t, recorder.roundTrip),
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	// The payload is far below the default FlushBytes (5MB) and each round
	// finishes well inside the default FlushInterval (30s), so neither
	// threshold can fire: every action the transport sees got there via Flush.
	for round := range rounds {
		want := make([]string, 0, itemsPerRound)
		for i := range itemsPerRound {
			documentID := fmt.Sprintf("doc_%d_%d", round, i)
			want = append(want, actionIndex+":"+documentID)
			require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: documentID,
				Body:       strings.NewReader(`{"a":1}`),
			}))
		}

		require.NoError(t, bi.Flush(t.Context()))
		require.ElementsMatch(t, want, recorder.takeActions(),
			"round %d: Flush must send exactly the items added since the previous Flush", round)
	}

	require.NoError(t, bi.Close(t.Context()))
	require.Equal(t, uint64(rounds*itemsPerRound), bi.Stats().NumAdded)
}

func TestBulkIndexerFlushReportsBulkFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// status and body describe the /_bulk response; transportErr instead
		// fails the round trip outright.
		status       int
		body         string
		transportErr error
		wantErr      bool
		wantFailed   uint64
	}{
		{
			name:       "http error status",
			status:     http.StatusInternalServerError,
			body:       `{"error":{"type":"illegal_state_exception"},"status":500}`,
			wantErr:    true,
			wantFailed: 1,
		},
		{
			name:         "transport error",
			transportErr: errors.New("dial tcp: connection refused"),
			wantErr:      true,
			wantFailed:   1,
		},
		{
			name:       "unparseable body",
			status:     http.StatusOK,
			body:       `{"items": not json`,
			wantErr:    true,
			wantFailed: 1,
		},
		{
			// The request landed; one document was rejected. That reaches the
			// caller through OnFailure, so the drain itself did not fail and
			// Flush must not report an error for it.
			name:   "per-item rejection is not a flush failure",
			status: http.StatusOK,
			body: `{"took":1,"errors":true,"items":[` +
				`{"index":{"_index":"i","_id":"doc_1","status":409,` +
				`"error":{"type":"version_conflict_engine_exception","reason":"conflict"}}}]}`,
			wantErr:    false,
			wantFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bi, err := NewBulkIndexer(BulkIndexerConfig{
				NumWorkers: 1,
				Client: newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
					if !strings.HasSuffix(req.URL.Path, "/_bulk") {
						return infoResponse()
					}
					if tt.transportErr != nil {
						return nil, tt.transportErr
					}
					return &http.Response{
						StatusCode: tt.status,
						Status:     http.StatusText(tt.status),
						Body:       io.NopCloser(strings.NewReader(tt.body)),
						Header:     http.Header{"Content-Type": []string{"application/json"}},
					}, nil
				}),
				Index: testutil.MustUniqueString(t, "test-index"),
			})
			require.NoError(t, err)

			require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "doc_1",
				Body:       strings.NewReader(`{"a":1}`),
			}))

			// A drain that did not land has to surface through Flush's return
			// value; the caller has no other way to learn it failed.
			flushErr := bi.Flush(t.Context())
			if tt.wantErr {
				require.Error(t, flushErr)
			} else {
				require.NoError(t, flushErr)
			}
			require.Equal(t, tt.wantFailed, bi.Stats().NumFailed)
			require.NoError(t, bi.Close(t.Context()))
		})
	}
}

func TestBulkIndexerFlushRejectsCancelledContext(t *testing.T) {
	t.Parallel()

	var recorder bulkRecorder
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: 2,
		Client:     newBulkTestClient(t, recorder.roundTrip),
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     actionIndex,
		DocumentID: "doc_1",
		Body:       strings.NewReader(`{"a":1}`),
	}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Flush checks the context before queueing any barrier, so a cancelled
	// context is a clean refusal rather than a partial drain.
	require.ErrorIs(t, bi.Flush(ctx), context.Canceled)
	require.Empty(t, recorder.takeActions(), "a refused Flush must not send anything")

	require.NoError(t, bi.Close(t.Context()))
}

// newParkedBulkTestClient returns a client that parks every bulk request until
// the test ends, plus a channel that receives once the first such request has
// arrived. Non-bulk requests are answered normally so the indexer still starts.
// A parked request holds the worker that issued it, so that worker stops
// draining its queue, which is how a test sequences Flush without sleeping.
func newParkedBulkTestClient(t *testing.T) (*opensearchapi.Client, <-chan struct{}) {
	t.Helper()

	inFlight := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	return newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return infoResponse()
		}
		select {
		case inFlight <- struct{}{}:
		default:
		}
		<-release

		return defaultRoundTripFunc(req)
	}), inFlight
}

func TestBulkIndexerFlushReturnsWhenContextCancelledMidDrain(t *testing.T) {
	t.Parallel()

	// The transport parks inside the bulk request until the test ends, so Flush
	// is provably still waiting for its barrier when the context dies. The
	// handshake travels on channels, so the test never sleeps to sequence this.
	client, inFlight := newParkedBulkTestClient(t)

	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     actionIndex,
		DocumentID: "doc_1",
		Body:       strings.NewReader(`{"a":1}`),
	}))

	ctx, cancel := context.WithCancel(t.Context())
	flushed := make(chan error, 1)
	go func() { flushed <- bi.Flush(ctx) }()

	<-inFlight
	cancel()

	select {
	case flushErr := <-flushed:
		require.ErrorIs(t, flushErr, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Flush hung after its context was cancelled mid-drain")
	}
}

func TestBulkIndexerFlushDoesNotHangWhenConstructionContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	var recorder bulkRecorder
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		Context:    ctx,
		NumWorkers: 2,
		Client:     newBulkTestClient(t, recorder.roundTrip),
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	cancel()
	<-bi.(*bulkIndexer).flusherDone

	// A live context, deliberately, rather than the cancelled one: the workers
	// are gone, so a Flush that only watched the caller's context would wait
	// forever for a barrier ack that can never arrive.
	done := make(chan error, 1)
	go func() { done <- bi.Flush(t.Context()) }()

	select {
	case flushErr := <-done:
		// A worker may still drain the barrier before it notices the
		// cancellation, so a nil error is legitimate here. What matters is
		// that Flush returned at all.
		if flushErr != nil {
			require.ErrorIs(t, flushErr, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Flush hung after construction context was cancelled")
	}
}

func TestBulkIndexerFlushReportsDeadWorkersWhenBarrierSendBlocks(t *testing.T) {
	t.Parallel()

	// A queue holds NumWorkers entries, so a single worker gives a queue of one
	// and the test can fill it. Parking the transport keeps that worker from
	// draining the queue, so the barrier send below has nowhere to go and the
	// construction context is the only case its select can take.
	client, inFlight := newParkedBulkTestClient(t)

	ctx, cancel := context.WithCancel(t.Context())
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		Context:    ctx,
		NumWorkers: 1,
		Client:     client,
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     actionIndex,
		DocumentID: "doc_1",
		Body:       strings.NewReader(`{"a":1}`),
	}))

	// Park the worker inside the bulk request this Flush drives. It stays
	// blocked for the rest of the test, so nothing consumes the queue again.
	parked := make(chan error, 1)
	go func() { parked <- bi.Flush(t.Context()) }()
	<-inFlight

	// The worker consumed the item and the barrier before parking, so the queue
	// is empty and this Add fills it to its capacity of one without blocking.
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action:     actionIndex,
		DocumentID: "doc_2",
		Body:       strings.NewReader(`{"b":2}`),
	}))

	cancel()

	// A live caller context, so the only error Flush can report is the dead
	// construction context. Without that case it would block on the full queue
	// until the caller's own context expired, which here is never.
	done := make(chan error, 1)
	go func() { done <- bi.Flush(t.Context()) }()

	select {
	case flushErr := <-done:
		require.ErrorIs(t, flushErr, context.Canceled)
		require.NoError(t, t.Context().Err(), "the caller's context must still be live, proving the error came from the construction context")
	case <-time.After(5 * time.Second):
		t.Fatal("Flush hung on a full queue after its workers were gone")
	}
}

func TestBulkIndexerFlushIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	const (
		numWorkers    = 4
		numAdders     = 4
		itemsPerAdder = 25
		numFlushers   = 3
		wantAdded     = numAdders * itemsPerAdder
	)

	var recorder bulkRecorder
	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: numWorkers,
		Client:     newBulkTestClient(t, recorder.roundTrip),
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	var g errgroup.Group
	for adder := range numAdders {
		g.Go(func() error {
			for i := range itemsPerAdder {
				if err := bi.Add(t.Context(), BulkIndexerItem{
					Action:     actionIndex,
					DocumentID: fmt.Sprintf("doc_%d_%d", adder, i),
					Body:       strings.NewReader(`{"a":1}`),
				}); err != nil {
					return err
				}
			}

			return nil
		})
	}
	for range numFlushers {
		g.Go(func() error { return bi.Flush(t.Context()) })
	}
	require.NoError(t, g.Wait())
	// Concurrent flushes make no promise about which items each one carried, so
	// the assertion is on the total after a final barrier: nothing added was
	// dropped, and nothing was sent twice.
	require.NoError(t, bi.Flush(t.Context()))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, uint64(wantAdded), bi.Stats().NumAdded)
	require.Len(t, recorder.takeActions(), wantAdded)
}
