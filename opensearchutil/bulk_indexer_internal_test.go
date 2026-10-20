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
	"math"
	"net/http"
	"os"
	"slices"
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

// bulkOKResponse answers a bulk request that carried n actions with a success
// for each, as the cluster does; a response with fewer results than actions is
// one the indexer rejects.
func bulkOKResponse(n int) *http.Response {
	items := strings.TrimSuffix(strings.Repeat(`{"index":{"status":200}},`, n), ",")

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"errors":false,"items":[` + items + `]}`)),
	}
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
				IfSeqNum:      new(int64(5)),
				IfPrimaryTerm: new(int64(1)),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","if_seq_no":5,"if_primary_term":1}}`, testIndex) + "\n",
		},
		{
			name: "with version and no document, if_seq_no, and if_primary_term",
			args: args{BulkIndexerItem{
				Action:  "index",
				Index:   testIndex,
				Version: new(int64(23)),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s"}}`, testIndex) + "\n",
		},
		{
			name: "with version",
			args: args{BulkIndexerItem{
				Action:     "index",
				DocumentID: "42",
				Index:      testIndex,
				Version:    new(int64(24)),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":24}}`, testIndex) + "\n",
		},
		{
			name: "with version and version_type",
			args: args{BulkIndexerItem{
				Action:      "index",
				DocumentID:  "42",
				Index:       testIndex,
				Version:     new(int64(25)),
				VersionType: new("external"),
			}},
			want: fmt.Sprintf(`{"index":{"_index":"%s","_id":"42","version":25,"version_type":"external"}}`, testIndex) + "\n",
		},
		{
			name: "wait_for_active_shards",
			args: args{BulkIndexerItem{
				Action:              "index",
				DocumentID:          "42",
				Index:               testIndex,
				Version:             new(int64(25)),
				VersionType:         new("external"),
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
				Version:             new(int64(25)),
				VersionType:         new("external"),
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
				Version:         new(int64(25)),
				VersionType:     new("external"),
				RetryOnConflict: new(5),
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
				metaPool:         sync.Pool{New: newMetaEncoder},
			}
			w := &worker{
				bi:  bi,
				buf: newChunkBuffer(newChunkPool(defaultChunkSize)),
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
					Sizing:        BulkSizing{Shard: ShardFixed, Node: NodeFixed}, // FlushBytes is the batch size, not a ceiling.
					FlushInterval: time.Hour,
					Client:        client,
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
				NumAdded:          6,
				NumUngroupedItems: 6, // No geometry: nothing is placed in a shard group.
				NumByteFlushes:    3, // FlushBytes is small here, so each of the 3 requests was a full buffer.
				NumFlushed:        4,
				NumFailed:         2,
				NumIndexed:        1,
				NumCreated:        1,
				NumDeleted:        1,
				NumUpdated:        1,
				NumRequests:       3,
				Errors:            errorsAt(LayerItem, KindPermanent, 2),
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
				NumAdded:          1,
				NumUngroupedItems: 1,
				NumTimerFlushes:   1, // The flush interval sent it.
				NumFlushed:        1,
				NumFailed:         0,
				NumIndexed:        1,
				NumRequests:       1,
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
						DiscoverNodesOnStart: new(false),
					},
				}
				if testutil.IsDebugEnabled(t) {
					cfg.Client.Logger = &opensearchtransport.ColorLogger{Output: os.Stdout}
				}
				client, _ := opensearchapi.NewClient(cfg)
				t.Cleanup(func() { _ = client.Close() })

				biCfg := BulkIndexerConfig{NumWorkers: 1, FlushBytes: 50, Sizing: BulkSizing{Shard: ShardFixed, Node: NodeFixed}, Client: client}
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
				NumAdded:          2,
				NumUngroupedItems: 2,
				NumByteFlushes:    1,
				NumFlushed:        2,
				NumFailed:         0,
				NumDeleted:        1,
				NumUpdated:        1,
				NumRequests:       1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			require.Equal(t, tt.want, withoutLoad(got))
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

// bulkRoundTrip answers every request a bulk test makes: the client's info
// request at "/", then each bulk request.
type bulkRoundTrip func(*http.Request) (*http.Response, error)

// respondWith answers the info request, then every bulk request with body.
func respondWith(body string) bulkRoundTrip {
	return withInfo(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
}

// failWith answers the info request, then fails every bulk request with err.
func failWith(err error) bulkRoundTrip {
	return withInfo(func(*http.Request) (*http.Response, error) {
		return nil, err
	})
}

// withInfo answers the info request at "/" and leaves every other request to
// bulk, so a case only has to describe the bulk response.
func withInfo(bulk bulkRoundTrip) bulkRoundTrip {
	return func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/" {
			return infoResponse()
		}
		return bulk(request)
	}
}

// unmaskedBulkItems pins BulkItems unmasked, so a bulk response with
// errors:true surfaces as a *PartialBulkError whatever the version default is.
func unmaskedBulkItems(cfg *opensearchapi.Config) {
	cfg.Errors = errmask.New()
}

// maskedBulkItems masks BulkItems, so a bulk response with errors:true reports
// no error at all and the per-item results are the only report of a rejection.
func maskedBulkItems(cfg *opensearchapi.Config) {
	cfg.Errors = errmask.New(errmask.BulkItems)
}

// newBulkIndexer builds an indexer on client, attaching a debug logger when
// debugging is on.
//
// A cfg with NumWorkers set to 1 puts every item and flush callback on one
// goroutine, which is what lets the tests below record what a callback saw by
// appending to a plain slice, with no synchronizing of their own.
func newBulkIndexer(t *testing.T, cfg BulkIndexerConfig) BulkIndexer {
	t.Helper()

	bi, err := NewBulkIndexer(cfg)
	require.NoError(t, err)

	return bi
}

// testItemCallback is what one item's OnSuccess or OnError callback reported. A
// rejection the cluster made carries a status, and an error type when the
// response item named one; a rejection the indexer made while writing the
// request carries an error instead.
type testItemCallback struct {
	ID        string
	Body      string
	Err       string
	ErrorType string
	Reason    string
	Status    int
}

// TestBulkIndexerPerItemCallbacks covers a mixed batch: the response reports
// errors:true, so two items succeed and two fail. The failed items reach
// OnError and the request itself is not a failure, so OnUnhandledError stays silent.
// Whether the bulk call reports the rejections as a *PartialBulkError or masks
// them away makes no difference to what the callbacks see.
func TestBulkIndexerPerItemCallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		errMask func(*opensearchapi.Config)
	}{
		{name: "partial bulk error surfaced", errMask: unmaskedBulkItems},
		{name: "partial bulk error masked", errMask: maskedBulkItems},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile("testdata/bulk_response_2.json")
			require.NoError(t, err)

			client := newBulkTestClient(t, respondWith(string(body)), tt.errMask)

			var (
				onErrorCount int
				succeeded    []testItemCallback
				failed       []testItemCallback
			)
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers:       1,
				Client:           client,
				OnUnhandledError: func(context.Context, error) { onErrorCount++ },
			})

			onSuccess := func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
				succeeded = append(succeeded, testItemCallback{ID: item.DocumentID, Body: readItemBody(t, item)})
			}
			// t.Errorf rather than require, per the note on readItemBody.
			onFailure := func(_ context.Context, item BulkIndexerItem, err *BulkError) {
				if err.Err != nil {
					t.Errorf("OnError got a cause beyond the item's response for %q: %s", item.DocumentID, err.Err)
				}
				failed = append(failed, testItemCallback{ID: item.DocumentID, Body: readItemBody(t, item)})
			}

			for _, item := range []BulkIndexerItem{
				{Action: "index", DocumentID: "1", Body: strings.NewReader(`{"title":"foo"}`)},
				{Action: "create", DocumentID: "1", Body: strings.NewReader(`{"title":"bar"}`)},
				{Action: "delete", DocumentID: "2", Body: strings.NewReader(`{"title":"baz"}`)},
				{Action: "update", DocumentID: "3", Body: strings.NewReader(`{"doc":{"title":"qux"}}`)},
			} {
				item.OnSuccess, item.OnError = onSuccess, onFailure
				require.NoError(t, bi.Add(t.Context(), item))
			}
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, BulkIndexerStats{
				NumAdded:          4,
				NumUngroupedItems: 4,
				NumFlushed:        2,
				NumFailed:         2,
				NumIndexed:        1,
				NumUpdated:        1,
				// Indexer flushes only; the client's info request is not counted.
				NumRequests:     1,
				NumCloseFlushes: 1,
				Errors:          errorsAt(LayerItem, KindPermanent, 2),
			}, withoutLoad(bi.Stats()))

			require.Equal(t, []testItemCallback{
				{ID: "1", Body: `{"title":"foo"}`},
				{ID: "3", Body: `{"doc":{"title":"qux"}}`},
			}, succeeded)
			require.Equal(t, []testItemCallback{
				{ID: "1", Body: `{"title":"bar"}`},
				{ID: "2", Body: `{"title":"baz"}`},
			}, failed)
			require.Equal(t, 0, onErrorCount, "OnUnhandledError must not fire for per-item bulk failures")
		})
	}
}

// TestBulkIndexerOnErrorForUnhandledPartialFailure covers a mixed batch whose
// rejected items carry no OnError of their own: each such failure is joined
// into the error OnUnhandledError receives, so a per-item rejection is never swallowed.
// The two failures in the fixture exercise both shapes -- a rejection with an
// error object (create "1") and a status-only rejection (delete "2").
func TestBulkIndexerOnErrorForUnhandledPartialFailure(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/bulk_response_2.json")
	require.NoError(t, err)

	client := newBulkTestClient(t, respondWith(string(body)), unmaskedBulkItems)

	var (
		onErrorCount int
		onErrorErr   error
	)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		OnUnhandledError: func(_ context.Context, err error) {
			onErrorCount++
			onErrorErr = err
		},
	})

	// No OnError on any item, so both rejections fall back to OnUnhandledError.
	for _, item := range []BulkIndexerItem{
		{Action: "index", DocumentID: "1", Body: strings.NewReader(`{"title":"foo"}`)},
		{Action: "create", DocumentID: "1", Body: strings.NewReader(`{"title":"bar"}`)},
		{Action: "delete", DocumentID: "2", Body: strings.NewReader(`{"title":"baz"}`)},
		{Action: "update", DocumentID: "3", Body: strings.NewReader(`{"doc":{"title":"qux"}}`)},
	} {
		require.NoError(t, bi.Add(t.Context(), item))
	}
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, uint64(2), bi.Stats().NumFailed, "both rejected items are counted")
	require.Equal(t, 1, onErrorCount, "OnUnhandledError fires once for the flush carrying both unhandled rejections")
	require.ErrorContains(t, onErrorErr, `"create" "1" rejected: "version_conflict_engine_exception"`)
	require.ErrorContains(t, onErrorErr, `"delete" "2" rejected: status 404`)
}

// TestBulkIndexerFlushCallbacks checks that the context OnFlushStart returns is
// the one OnFlushEnd is handed and the one the bulk request runs under, and that
// the request goes to the configured index.
func TestBulkIndexerFlushCallbacks(t *testing.T) {
	t.Parallel()

	const wantValue = "flushing"
	index := testutil.MustUniqueString(t, "test-flush")

	// Recorded from the bulk request itself, so a flush that drops the
	// OnFlushStart context or targets another index fails.
	var requests struct {
		sync.Mutex
		contextValues []any
		paths         []string
	}
	client := newBulkTestClient(t, withInfo(func(request *http.Request) (*http.Response, error) {
		// The client also discovers nodes, which carries neither the flush
		// context nor the index, so record the bulk requests alone.
		if strings.HasSuffix(request.URL.Path, "/_bulk") {
			requests.Lock()
			requests.contextValues = append(requests.contextValues, request.Context().Value(flushContextKey{}))
			requests.paths = append(requests.paths, request.URL.Path)
			requests.Unlock()
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"items":[{"index":{}}]}`)),
		}, nil
	}))

	var flushEndValues []any
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		Index:      index,
		OnFlushStart: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, flushContextKey{}, wantValue)
		},
		OnFlushEnd: func(ctx context.Context) {
			flushEndValues = append(flushEndValues, ctx.Value(flushContextKey{}))
		},
	})

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: "index",
		Body:   strings.NewReader(`{"title":"foo"}`),
	}))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, []any{wantValue}, flushEndValues, "OnFlushEnd context value")

	requests.Lock()
	defer requests.Unlock()
	require.Equal(t, []any{wantValue}, requests.contextValues, "bulk request context value")
	require.Equal(t, []string{"/" + index + "/_bulk"}, requests.paths, "bulk request path")
}

// TestBulkIndexerOnErrorForARequestError covers a bulk request that never
// landed: every buffered item fails with the request error, and OnUnhandledError reports
// it once per flush.
func TestBulkIndexerOnErrorForARequestError(t *testing.T) {
	t.Parallel()

	// opensearchtransport wraps the request error with `"METHOD" "url": `
	// context whose URL varies per request, so the OnError and OnUnhandledError errors
	// are matched on the flush prefix and the underlying cause rather than
	// compared to an exact string.
	const (
		wantErrPrefix = "flush: "
		wantErrSuffix = ": simulated bulk request error"
	)

	// Each row pairs the item to add with what its OnError callback must
	// report. want is written out rather than derived from add, so a callback
	// handed the wrong document, or an ID paired with another document's body,
	// fails the comparison instead of agreeing with its own input. The error is
	// checked separately, below, since its wrapped URL varies per request.
	items := []struct {
		add  BulkIndexerItem
		want testItemCallback
	}{
		{
			add:  BulkIndexerItem{Action: "index", DocumentID: "id_0", Body: strings.NewReader(`{"title":"doc_0"}`)},
			want: testItemCallback{ID: "id_0", Body: `{"title":"doc_0"}`},
		},
		{
			add:  BulkIndexerItem{Action: "index", DocumentID: "id_1", Body: strings.NewReader(`{"title":"doc_1"}`)},
			want: testItemCallback{ID: "id_1", Body: `{"title":"doc_1"}`},
		},
		{
			add:  BulkIndexerItem{Action: "index", DocumentID: "id_2", Body: strings.NewReader(`{"title":"doc_2"}`)},
			want: testItemCallback{ID: "id_2", Body: `{"title":"doc_2"}`},
		},
		{
			add:  BulkIndexerItem{Action: "index", DocumentID: "id_3", Body: strings.NewReader(`{"title":"doc_3"}`)},
			want: testItemCallback{ID: "id_3", Body: `{"title":"doc_3"}`},
		},
		{
			add:  BulkIndexerItem{Action: "index", DocumentID: "id_4", Body: strings.NewReader(`{"title":"doc_4"}`)},
			want: testItemCallback{ID: "id_4", Body: `{"title":"doc_4"}`},
		},
	}

	client := newBulkTestClient(t, failWith(errors.New("simulated bulk request error")))

	var (
		onErrorCount int
		failedItems  []testItemCallback
		failedErrs   []string
		onErrorErrs  []string
	)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		// One item per flush, so each item fails in its own request.
		FlushBytes: 1,
		Client:     client,
		OnUnhandledError: func(_ context.Context, err error) {
			onErrorCount++
			onErrorErrs = append(onErrorErrs, err.Error())
		},
	})

	// Every added item must reach OnError exactly once, in the order it was
	// added, carrying the request error.
	wantItems := make([]testItemCallback, 0, len(items))
	for _, item := range items {
		wantItems = append(wantItems, item.want)

		add := item.add
		add.OnError = func(_ context.Context, item BulkIndexerItem, err *BulkError) {
			failedItems = append(failedItems, testItemCallback{
				ID:   item.DocumentID,
				Body: readItemBody(t, item),
			})
			failedErrs = append(failedErrs, err.Error())
		}
		require.NoError(t, bi.Add(t.Context(), add))
	}
	require.NoError(t, bi.Close(t.Context()))

	numItems := len(wantItems)
	require.Equal(t, numItems, onErrorCount, "OnUnhandledError call count")
	require.Equal(t, uint64(numItems), bi.Stats().NumFailed, "NumFailed")
	require.Equal(t, wantItems, failedItems, "every added item must fail exactly once")
	for _, got := range slices.Concat(failedErrs, onErrorErrs) {
		require.Truef(t, strings.HasPrefix(got, wantErrPrefix), "error %q should start with %q", got, wantErrPrefix)
		require.Truef(t, strings.HasSuffix(got, wantErrSuffix), "error %q should end with %q", got, wantErrSuffix)
	}
}

// TestBulkIndexerOnErrorDetail covers what OnError reports for a rejected
// item, whether the cluster rejected it or the indexer rejected it while writing
// the request. In both cases the response item's Error field is readable without
// a nil check, because the indexer fills it in for an item that has none.
func TestBulkIndexerOnErrorDetail(t *testing.T) {
	t.Parallel()

	// Item 1 is rejected with an error object, item 2 with a status alone.
	const rejectedByCluster = `{
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

	tests := []struct {
		name     string
		bulkResp string
		items    []BulkIndexerItem
		want     []testItemCallback
	}{
		{
			name:     "rejected by the cluster",
			bulkResp: rejectedByCluster,
			items: []BulkIndexerItem{
				{Action: "create", DocumentID: "1", Body: strings.NewReader(`{"title":"bar"}`)},
				{Action: "delete", DocumentID: "2", Body: strings.NewReader(`{"title":"baz"}`)},
			},
			want: []testItemCallback{
				{
					ID:        "1",
					ErrorType: "version_conflict_engine_exception",
					Reason:    "already exists",
					Status:    http.StatusConflict,
				},
				// Status-only rejection: no error object on the wire, so the
				// filled-in Error carries no type or reason.
				{ID: "2", Status: http.StatusNotFound},
			},
		},
		{
			name: "rejected while writing the request",
			// Carries no items, and neither rejected document is in it. Still
			// sent: body-fail's metadata line was already in the buffer when its
			// body failed to read, so the drain on Close ships that line alone.
			bulkResp: `{"items":[]}`,
			items: []BulkIndexerItem{
				// NaN cannot be serialized into the action metadata line.
				{Action: "index", DocumentID: "meta-fail", WaitForActiveShards: math.NaN()},
				{Action: "index", DocumentID: "body-fail", Body: &failingBody{}},
			},
			// No response item exists for either, so neither carries a status.
			want: []testItemCallback{
				{ID: "meta-fail", Err: "json: unsupported value: NaN"},
				{ID: "body-fail", Err: "body read failed"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newBulkTestClient(t, respondWith(tt.bulkResp))
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1,
				Client:     client,
			})

			// A slice rather than a map keyed by ID, so a second callback for one
			// item shows up as an extra element instead of overwriting the first.
			var rejected []testItemCallback
			// t.Errorf rather than require, per the note on readItemBody.
			record := func(_ context.Context, item BulkIndexerItem, err *BulkError) {
				got := testItemCallback{ID: item.DocumentID, Status: err.Resp.Status}
				if err.Err != nil {
					got.Err = err.Err.Error()
				}
				// A status-only rejection, such as a 404 delete, has no error object.
				if err.Resp.Error != nil {
					got.ErrorType = err.Resp.Error.Type
					if err.Resp.Error.Reason != nil {
						got.Reason = *err.Resp.Error.Reason
					}
				}
				rejected = append(rejected, got)
			}

			for _, item := range tt.items {
				item.OnError = record
				require.NoError(t, bi.Add(t.Context(), item))
			}
			require.NoError(t, bi.Close(t.Context()))

			require.ElementsMatch(t, tt.want, rejected)
		})
	}
}

// TestBulkIndexerOnErrorOnWriteErrors pins which write failures reach the
// indexer's OnUnhandledError. Both failures below reject one item and both reach its
// OnError, but only a body that cannot be read is also reported to OnUnhandledError; a
// metadata line that cannot be serialized is not.
func TestBulkIndexerOnErrorOnWriteErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		item         BulkIndexerItem
		wantOnErrors []string
		wantOnError  testItemCallback
	}{
		{
			name: "unserializable metadata is not reported to OnUnhandledError",
			// NaN cannot be serialized into the action metadata line.
			item:         BulkIndexerItem{Action: "index", DocumentID: "meta-fail", WaitForActiveShards: math.NaN()},
			wantOnErrors: nil,
			wantOnError:  testItemCallback{ID: "meta-fail", Err: "json: unsupported value: NaN"},
		},
		{
			name:         "unreadable body is reported to OnUnhandledError",
			item:         BulkIndexerItem{Action: "index", DocumentID: "body-fail", Body: &failingBody{}},
			wantOnErrors: []string{"body read failed"},
			wantOnError:  testItemCallback{ID: "body-fail", Err: "body read failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newBulkTestClient(t, respondWith(`{"items":[]}`))

			var (
				onErrorErrs []string
				rejected    []testItemCallback
			)
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1,
				Client:     client,
				OnUnhandledError: func(_ context.Context, err error) {
					onErrorErrs = append(onErrorErrs, err.Error())
				},
			})

			item := tt.item
			item.OnError = func(_ context.Context, item BulkIndexerItem, err *BulkError) {
				rejected = append(rejected, testItemCallback{ID: item.DocumentID, Err: err.Error()})
			}
			require.NoError(t, bi.Add(t.Context(), item))
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, []testItemCallback{tt.wantOnError}, rejected, "OnError")
			require.Equal(t, tt.wantOnErrors, onErrorErrs, "OnUnhandledError")
		})
	}
}

// readItemBody reads the item body a callback received. The indexer rewinds the
// body before sending, so a callback sees it from the start and a case can assert
// on which document reached which callback.
//
// Item callbacks run on a worker goroutine, so every check inside one reports
// with t.Errorf: require's FailNow may only be called from the goroutine running
// the test. What a callback saw is asserted with require after Close, once the
// workers have joined. This helper follows that rule too, returning the read
// failure in place of the body so it shows up in the caller's comparison.
func readItemBody(t *testing.T, item BulkIndexerItem) string {
	t.Helper()

	buf, err := io.ReadAll(item.Body)
	if err != nil {
		t.Errorf("reading item %q body: %s", item.DocumentID, err)
		return fmt.Sprintf("<unread body for %q: %s>", item.DocumentID, err)
	}

	return string(buf)
}

// bodyFailure names which io.ReadSeeker method a failingBody fails.
type bodyFailure int

const (
	// failOnRead fails the Read that follows the payload, so ReadFrom returns
	// an error after len(data) bytes reached the bulk buffer. Empty data fails
	// the first Read, having written nothing.
	failOnRead bodyFailure = iota
	// failOnSeek serves the whole payload, then fails the rewind the indexer
	// performs so the caller-supplied body stays reusable.
	failOnSeek
)

// failingBody is an item body whose serialization fails; the fail field
// selects which step (see bodyFailure).
type failingBody struct {
	data []byte
	fail bodyFailure
}

func (b *failingBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		if b.fail == failOnRead {
			return 0, errors.New("body read failed")
		}

		return 0, io.EOF
	}

	n := copy(p, b.data)
	b.data = b.data[n:]

	return n, nil
}

func (b *failingBody) Seek(int64, int) (int64, error) {
	if b.fail == failOnSeek {
		return 0, errors.New("body seek failed")
	}

	return 0, nil
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
		ids := bulkDocumentIDs(body)
		r.mu.Lock()
		r.mu.requests = append(r.mu.requests, ids)
		r.mu.Unlock()

		return bulkOKResponse(len(ids)), nil
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

func newBulkTestClient(
	t *testing.T,
	roundTrip func(*http.Request) (*http.Response, error),
	opts ...func(*opensearchapi.Config),
) *opensearchapi.Client {
	t.Helper()

	cfg := opensearchapi.Config{Client: opensearch.Config{
		Transport: &mockTransport{RoundTripFunc: roundTrip},
	}}
	for _, opt := range opts {
		opt(&cfg)
	}

	client, err := opensearchapi.NewClient(cfg)
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
		// handlePerItem attaches an OnError to the item, so a per-item
		// rejection is handled there rather than surfacing through Flush.
		handlePerItem bool
		wantErr       bool
		wantFailed    uint64
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
			// The request landed; one document was rejected and the item has an
			// OnError, so the rejection reaches the caller there. The drain
			// itself did not fail, so Flush must not report an error for it. An
			// item without an OnError instead surfaces through Flush -- see
			// TestBulkIndexerOnErrorForUnhandledPartialFailure.
			name:   "per-item rejection is not a flush failure",
			status: http.StatusOK,
			body: `{"took":1,"errors":true,"items":[` +
				`{"index":{"_index":"i","_id":"doc_1","status":409,` +
				`"error":{"type":"version_conflict_engine_exception","reason":"conflict"}}}]}`,
			handlePerItem: true,
			wantErr:       false,
			wantFailed:    1,
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

			item := BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "doc_1",
				Body:       strings.NewReader(`{"a":1}`),
			}
			if tt.handlePerItem {
				item.OnError = func(context.Context, BulkIndexerItem, *BulkError) {}
			}
			require.NoError(t, bi.Add(t.Context(), item))

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

func newTestWorker() *worker {
	bi := &bulkIndexer{
		stats:            &bulkIndexerStats{},
		now:              time.Now,
		metaPoolMaxBytes: defaultMetaBufferPoolMaxBytes,
		metaPool:         sync.Pool{New: newMetaEncoder},
	}
	return &worker{
		bi:  bi,
		buf: newChunkBuffer(newChunkPool(defaultChunkSize)),
	}
}

func TestWorkerWriteItemRollsBackBufferOnError(t *testing.T) {
	t.Parallel()

	const goodBody = `{"a":1}`
	wantGood := `{"index":{"_id":"good"}}` + "\n" + goodBody + "\n"

	tests := []struct {
		name    string
		item    BulkIndexerItem
		wantErr string
	}{
		{
			name: "writeBody read error with 0 bytes",
			item: BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "bad-read",
				Body:       &failingBody{},
			},
			wantErr: "body read failed",
		},
		{
			name: "writeBody read error after partial body",
			item: BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "bad-partial",
				Body:       &failingBody{data: []byte(`{"partial":true}`)},
			},
			wantErr: "body read failed",
		},
		{
			name: "writeBody seek error after copying body",
			item: BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "bad-seek",
				Body:       &failingBody{data: []byte(`{"seek":true}`), fail: failOnSeek},
			},
			wantErr: "body seek failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newTestWorker()
			require.NoError(t, w.writeItem(t.Context(), BulkIndexerItem{
				Action:     actionIndex,
				DocumentID: "good",
				Body:       strings.NewReader(goodBody),
			}))
			require.Equal(t, wantGood, w.buf.String())

			err := w.writeItem(t.Context(), tt.item)
			require.ErrorContains(t, err, tt.wantErr)
			require.Equal(t, wantGood, w.buf.String(), "failed item must not leave an orphan action line")
		})
	}
}

func TestBulkIndexerDoesNotFlushOrphanActionOnBodyError(t *testing.T) {
	t.Parallel()

	var mu struct {
		sync.Mutex
		bodies  []string
		success []string
		failed  []string
	}

	client := newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return infoResponse()
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		actions := bulkDocumentIDs(body)
		items := make([]string, len(actions))
		for i, action := range actions {
			id := strings.TrimPrefix(action, actionIndex+":")
			items[i] = fmt.Sprintf(`{"index":{"_id":%q,"status":201,"result":"created"}}`, id)
		}

		mu.Lock()
		mu.bodies = append(mu.bodies, string(body))
		mu.Unlock()

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"took":1,"errors":false,"items":[` + strings.Join(items, ",") + `]}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})

	bi, err := NewBulkIndexer(BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		Index:      testutil.MustUniqueString(t, "test-index"),
	})
	require.NoError(t, err)

	onSuccess := func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
		mu.Lock()
		mu.success = append(mu.success, item.DocumentID)
		mu.Unlock()
	}
	onFailure := func(_ context.Context, item BulkIndexerItem, err *BulkError) {
		require.Error(t, err)
		mu.Lock()
		mu.failed = append(mu.failed, item.DocumentID)
		mu.Unlock()
	}

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: actionIndex, DocumentID: "good-1",
		Body: strings.NewReader(`{"title":"ok"}`), OnSuccess: onSuccess, OnError: onFailure,
	}))
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: actionIndex, DocumentID: "bad",
		Body: &failingBody{}, OnSuccess: onSuccess, OnError: onFailure,
	}))
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: actionIndex, DocumentID: "good-2",
		Body: strings.NewReader(`{"title":"ok"}`), OnSuccess: onSuccess, OnError: onFailure,
	}))
	require.NoError(t, bi.Flush(t.Context()))
	require.NoError(t, bi.Close(t.Context()))

	mu.Lock()
	defer mu.Unlock()

	require.Equal(t, []string{"bad"}, mu.failed)
	require.Equal(t, []string{"good-1", "good-2"}, mu.success)
	require.Len(t, mu.bodies, 1)
	require.NotContains(t, mu.bodies[0], `"_id":"bad"`)
	require.Contains(t, mu.bodies[0], `"_id":"good-1"`)
	require.Contains(t, mu.bodies[0], `"_id":"good-2"`)

	stats := bi.Stats()
	require.Equal(t, uint64(3), stats.NumAdded)
	require.Equal(t, uint64(1), stats.NumFailed)
	require.Equal(t, uint64(2), stats.NumFlushed)
	require.Equal(t, uint64(2), stats.NumIndexed)
}

func TestWorkerFlushGuardsMismatchedResponseCount(t *testing.T) {
	t.Parallel()

	const responseN = 2

	itemsJSON := make([]string, responseN)
	for i := range responseN {
		itemsJSON[i] = fmt.Sprintf(`{"index":{"_id":"%d","status":201,"result":"created"}}`, i)
	}
	payload := `{"took":1,"errors":false,"items":[` + strings.Join(itemsJSON, ",") + `]}`

	client := newBulkTestClient(t, func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/_bulk") {
			return infoResponse()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})

	var failed atomic.Uint64
	w := newTestWorker()
	w.bi.config.Client = client
	w.items = []BulkIndexerItem{{
		Action:     actionIndex,
		DocumentID: "0",
		OnError: func(context.Context, BulkIndexerItem, *BulkError) {
			failed.Add(1)
		},
	}}
	_, err := w.buf.WriteString(`{"index":{"_id":"0"}}` + "\n{\"a\":1}\n")
	require.NoError(t, err)

	err = w.flush(t.Context())
	require.ErrorContains(t, err, "bulk response has 2 items, indexer serialized 1")
	require.Equal(t, uint64(1), failed.Load())
	require.Equal(t, uint64(1), w.bi.stats.numFailed.Load())
	require.Equal(t, 0, w.buf.Len())
	require.Empty(t, w.items)
}

// referenceMeta writes the action line as the indexer did before metaEncoder: a fresh
// encoder and a map of the metadata for every item.
func referenceMeta(t *testing.T, item BulkIndexerItem) string {
	t.Helper()

	meta := bulkActionMetadata{
		Index: item.Index, DocumentID: item.DocumentID, Version: item.Version, VersionType: item.VersionType,
		Routing: item.Routing, IfPrimaryTerm: item.IfPrimaryTerm, IfSeqNum: item.IfSeqNum,
		WaitForActiveShards: item.WaitForActiveShards, Refresh: item.Refresh, RequireAlias: item.RequireAlias,
		RetryOnConflict: item.RetryOnConflict,
	}
	if meta.DocumentID == "" {
		meta.Version, meta.VersionType = nil, nil
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(map[string]bulkActionMetadata{item.Action: meta}))

	return buf.String()
}

// TestMetaEncoderReuse writes the action line for a series of items with one
// metaEncoder, as the pool does, and checks each line against the one the indexer used
// to write: byte for byte, and with nothing of an earlier item's fields, key or pointers
// left in the encoder.
func TestMetaEncoderReuse(t *testing.T) {
	t.Parallel()

	routing := "r-1"
	version := int64(3)
	versionType := "external"
	seqNo, primaryTerm := int64(7), int64(2)
	refresh, requireAlias, retries := "wait_for", true, 4

	tests := []struct {
		name string
		item BulkIndexerItem
	}{
		{name: "an index with every field", item: BulkIndexerItem{
			Action: "index", Index: "i", DocumentID: "d", Routing: &routing, Version: &version, VersionType: &versionType,
			IfSeqNum: &seqNo, IfPrimaryTerm: &primaryTerm, Refresh: &refresh, RequireAlias: &requireAlias,
			RetryOnConflict: &retries, WaitForActiveShards: "all",
		}},
		{name: "a bare delete after a full index", item: BulkIndexerItem{Action: "delete", DocumentID: "x"}},
		{name: "a create with no fields at all", item: BulkIndexerItem{Action: "create"}},
		{name: "an update with a version and no id", item: BulkIndexerItem{Action: "update", Version: &version, VersionType: &versionType}},
		{name: "characters that HTML escaping would change", item: BulkIndexerItem{Action: "index", DocumentID: "a<b>&c"}},
		{name: "a quote and a newline are escaped", item: BulkIndexerItem{Action: "update", DocumentID: "a\"b\nc"}},
		{name: "invalid UTF-8", item: BulkIndexerItem{Action: "index", DocumentID: "a\xffb"}},
		{name: "an action the bulk API does not have", item: BulkIndexerItem{Action: "custom", DocumentID: "1"}},
		{name: "an empty action", item: BulkIndexerItem{DocumentID: "1"}},
		{name: "an action name that needs escaping", item: BulkIndexerItem{Action: "in\"dex", DocumentID: "1"}},
		{name: "an index again, after the others", item: BulkIndexerItem{Action: "index", Index: "i2", DocumentID: "d2"}},
	}

	e := newMetaEncoder().(*metaEncoder) // One encoder for every case, in order.
	for _, tt := range tests {
		line, err := e.encode(&tt.item)
		require.NoError(t, err, tt.name)
		require.Equal(t, referenceMeta(t, tt.item), string(line), tt.name)
		require.Equal(t, actionLine{}, e.line, "%s: the line is cleared", tt.name)
		require.Equal(t, bulkActionMetadata{}, e.meta, "%s: the metadata is cleared", tt.name)
		require.Empty(t, e.entry, "%s: the map is cleared", tt.name)
	}
}

func TestMetaEncoderFailure(t *testing.T) {
	t.Parallel()

	e := newMetaEncoder().(*metaEncoder)

	for _, action := range []string{"index", "custom"} {
		_, err := e.encode(&BulkIndexerItem{Action: action, DocumentID: "1", WaitForActiveShards: math.NaN()})

		var target *json.UnsupportedValueError
		require.ErrorAs(t, err, &target, action)
	}
}

// TestBulkIndexerOrderKey: items that share an order key are written in the order
// they were added. The key is the document id, else the item's routing, else the
// indexer's routing; an item with none has no order to keep.
func TestBulkIndexerOrderKey(t *testing.T) {
	t.Parallel()

	itemRouting, empty := "item-routing", ""

	tests := []struct {
		name          string
		item          BulkIndexerItem
		configRouting string
		want          string
	}{
		{name: "document id", item: BulkIndexerItem{DocumentID: "doc-1"}, want: "doc-1"},
		{
			name: "document id outranks both routings",
			item: BulkIndexerItem{DocumentID: "doc-1", Routing: &itemRouting}, configRouting: "config-routing", want: "doc-1",
		},
		{name: "item routing", item: BulkIndexerItem{Routing: &itemRouting}, want: "item-routing"},
		{
			name: "item routing outranks the indexer's",
			item: BulkIndexerItem{Routing: &itemRouting}, configRouting: "config-routing", want: "item-routing",
		},
		{
			name: "an empty item routing falls through to the indexer's",
			item: BulkIndexerItem{Routing: &empty}, configRouting: "config-routing", want: "config-routing",
		},
		{name: "indexer routing", configRouting: "config-routing", want: "config-routing"},
		{name: "nothing to order by", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bi := &bulkIndexer{config: BulkIndexerConfig{Routing: tt.configRouting}}

			require.Equal(t, tt.want, bi.orderKey(tt.item))
		})
	}
}

// seekScript is a body with no Len whose Seek fails on the call numbered failAt
// (1-based), or never when failAt is 0.
type seekScript struct {
	io.ReadSeeker

	failAt int
	calls  int
}

var errSeek = errors.New("seek failed")

func (s *seekScript) Seek(offset int64, whence int) (int64, error) {
	s.calls++
	if s.calls == s.failAt {
		return 0, errSeek
	}

	return s.ReadSeeker.Seek(offset, whence)
}

func TestBodyLen(t *testing.T) {
	t.Parallel()

	t.Run("no body is empty", func(t *testing.T) {
		t.Parallel()

		require.Zero(t, bodyLen(nil))
	})

	t.Run("a reader that knows its length is not touched", func(t *testing.T) {
		t.Parallel()

		r := strings.NewReader("0123456789")
		_, err := r.Seek(4, io.SeekStart)
		require.NoError(t, err)

		require.EqualValues(t, 6, bodyLen(r), "the unread bytes")
		require.Equal(t, 6, r.Len(), "the position is where it was")
	})

	t.Run("any other seeker is measured from its position and left there", func(t *testing.T) {
		t.Parallel()

		s := &seekScript{ReadSeeker: strings.NewReader("0123456789")}
		_, err := s.Seek(4, io.SeekStart)
		require.NoError(t, err)
		s.calls = 0

		require.EqualValues(t, 6, bodyLen(s))

		rest, err := io.ReadAll(s.ReadSeeker)
		require.NoError(t, err)
		require.Equal(t, "456789", string(rest), "the position is where it was")
	})

	tests := []struct {
		name   string
		failAt int
	}{
		{name: "the position cannot be read", failAt: 1},
		{name: "the end cannot be reached", failAt: 2},
		{name: "the position cannot be restored", failAt: 3},
	}

	for _, tt := range tests {
		t.Run("0 when "+tt.name, func(t *testing.T) {
			t.Parallel()

			s := &seekScript{ReadSeeker: strings.NewReader("0123456789"), failAt: tt.failAt}

			require.Zero(t, bodyLen(s))
		})
	}
}

func TestBulkIndexerWorkersDone(t *testing.T) {
	t.Parallel()

	t.Run("an indexer without a construction context never signals", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, (&bulkIndexer{}).workersDone())
	})

	t.Run("the construction context ends the workers", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		bi := &bulkIndexer{config: BulkIndexerConfig{Context: ctx}}
		done := bi.workersDone()

		select {
		case <-done:
			t.Fatal("workersDone closed before the context ended")
		default:
		}

		cancel()
		receive(t, done)
	})
}

func TestIsThrottled(t *testing.T) {
	t.Parallel()

	withItems := func(statuses ...int) *opensearchapi.BulkResp {
		blk := &opensearchapi.BulkResp{}
		for _, status := range statuses {
			blk.Items = append(blk.Items, opensearchapi.BulkItem{Index: &opensearchapi.BulkRespItem{Status: status}})
		}

		return blk
	}

	tests := []struct {
		name string
		blk  *opensearchapi.BulkResp
		err  error
		want bool
	}{
		{name: "no response and no error", want: false},
		{name: "no response and a plain error", err: errors.New("connection reset"), want: false},
		{name: "a structured 429", err: &opensearch.StructError{Status: http.StatusTooManyRequests}, want: true},
		{
			name: "a wrapped structured 429",
			err:  fmt.Errorf("bulk: %w", &opensearch.StructError{Status: http.StatusTooManyRequests}),
			want: true,
		},
		{name: "a structured 400", err: &opensearch.StructError{Status: http.StatusBadRequest}, want: false},
		{name: "a string 429", err: &opensearch.StringError{Status: http.StatusTooManyRequests}, want: true},
		{
			name: "a wrapped string 429",
			err:  fmt.Errorf("bulk: %w", &opensearch.StringError{Status: http.StatusTooManyRequests}),
			want: true,
		},
		{name: "a string 500", err: &opensearch.StringError{Status: http.StatusInternalServerError}, want: false},
		{name: "an item rejected with 429", blk: withItems(http.StatusCreated, http.StatusTooManyRequests), want: true},
		{name: "items that all succeeded", blk: withItems(http.StatusCreated, http.StatusOK), want: false},
		{name: "items rejected for their own sake", blk: withItems(http.StatusConflict, http.StatusBadRequest), want: false},
		{name: "a response without items", blk: &opensearchapi.BulkResp{}, want: false},
		{
			name: "429 on a delete item",
			blk: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{
				{Delete: &opensearchapi.BulkRespItem{Status: http.StatusTooManyRequests}},
			}},
			want: true,
		},
		{
			name: "429 on an update item",
			blk: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{
				{Update: &opensearchapi.BulkRespItem{Status: http.StatusTooManyRequests}},
			}},
			want: true,
		},
		{
			name: "429 on a create item",
			blk: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{
				{Create: &opensearchapi.BulkRespItem{Status: http.StatusTooManyRequests}},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, isThrottled(tt.blk, tt.err))
		})
	}
}

func BenchmarkWriteMeta(b *testing.B) {
	bi := &bulkIndexer{
		metaPoolMaxBytes: defaultMetaBufferPoolMaxBytes,
		metaPool:         sync.Pool{New: newMetaEncoder},
	}
	w := &worker{bi: bi, buf: newChunkBuffer(newChunkPool(defaultChunkSize))}
	item := BulkIndexerItem{Action: "index", Index: "bench-index", DocumentID: "doc-000123"}

	b.ReportAllocs()

	for b.Loop() {
		if err := w.writeMeta(item); err != nil {
			b.Fatal(err)
		}
		w.buf.Reset()
	}
}
