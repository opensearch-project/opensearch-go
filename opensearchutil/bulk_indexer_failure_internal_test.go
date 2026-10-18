// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// This file pins what a BulkIndexer does when a bulk request does not go
// cleanly, and what happens to the requests that follow. It describes the
// behavior as it is, so a change to it -- a retry policy, a failure
// classification -- has to change a row here on purpose.

// jsonResponse builds a response carrying body as JSON with the given status.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// bulkItemsJSON renders one bulk response item per action line, each with the
// given status and, for a status of 300 or more, the given error type.
func bulkItemsJSON(actions []string, status int, errType string) string {
	items := make([]string, len(actions))
	for i, action := range actions {
		op, id, _ := strings.Cut(action, ":")
		errField := ""
		if errType != "" {
			errField = fmt.Sprintf(`,"error":{"type":%q,"reason":"simulated"}`, errType)
		}
		items[i] = fmt.Sprintf(`{%q:{"_id":%q,"status":%d%s}}`, op, id, status, errField)
	}

	return `{"took":1,"errors":` + fmt.Sprint(errType != "") + `,"items":[` + strings.Join(items, ",") + `]}`
}

// bulkOutcome decides the response to one bulk request, given the action lines
// the request carried.
type bulkOutcome func(actions []string) (*http.Response, error)

var (
	// outcomeOK accepts every action.
	outcomeOK bulkOutcome = func(actions []string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, bulkItemsJSON(actions, http.StatusCreated, "")), nil
	}
	// outcomeConnectionError fails the request before any response arrives.
	outcomeConnectionError bulkOutcome = func([]string) (*http.Response, error) {
		return nil, errors.New("simulated bulk request error")
	}
	// outcomeTooManyRequests answers the whole request with a 429, as a cluster
	// does when its write queue is full.
	outcomeTooManyRequests bulkOutcome = func([]string) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests,
			`{"error":{"type":"es_rejected_execution_exception","reason":"simulated"},"status":429}`), nil
	}
	// outcomeUnavailable answers the whole request with a 503.
	outcomeUnavailable bulkOutcome = func([]string) (*http.Response, error) {
		return jsonResponse(http.StatusServiceUnavailable,
			`{"error":{"type":"unavailable_shards_exception","reason":"simulated"},"status":503}`), nil
	}
	// outcomeEveryItemRejected accepts the request and rejects every action in it.
	outcomeEveryItemRejected bulkOutcome = func(actions []string) (*http.Response, error) {
		return jsonResponse(http.StatusOK,
			bulkItemsJSON(actions, http.StatusTooManyRequests, "es_rejected_execution_exception")), nil
	}
	// outcomeExtraItem answers 200 with one more result than the request had actions.
	outcomeExtraItem bulkOutcome = func(actions []string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, bulkItemsJSON(append(slices.Clip(actions), "index:extra"),
			http.StatusCreated, "")), nil
	}
	// outcomeMissingItem answers 200 with one result fewer than the request had actions.
	outcomeMissingItem bulkOutcome = func(actions []string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, bulkItemsJSON(actions[:len(actions)-1], http.StatusCreated, "")), nil
	}
)

// bulkSequence answers the info request and then each bulk request in turn with
// the outcome at that position, repeating the last outcome once they run out.
// It records the action lines of every bulk request, in arrival order.
type bulkSequence struct {
	outcomes []bulkOutcome

	mu struct {
		sync.Mutex
		requests [][]string
		bodies   []string
	}
}

func (s *bulkSequence) roundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/" {
		return infoResponse()
	}
	// The client also runs node discovery, whose requests carry no body.
	if !strings.HasSuffix(req.URL.Path, "/_bulk") {
		return defaultRoundTripFunc(req)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	actions := bulkDocumentIDs(body)

	s.mu.Lock()
	n := min(len(s.mu.requests), len(s.outcomes)-1)
	s.mu.requests = append(s.mu.requests, actions)
	s.mu.bodies = append(s.mu.bodies, string(body))
	s.mu.Unlock()

	return s.outcomes[n](actions)
}

// bodies returns the raw body of each bulk request seen so far.
func (s *bulkSequence) bodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.mu.bodies...)
}

// requests returns the action lines of each bulk request seen so far.
func (s *bulkSequence) requests() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([][]string(nil), s.mu.requests...)
}

// withoutTransportRetry turns off the transport's own retry, which would
// otherwise resend a bulk request that drew a 502, 503 or 504 before the indexer
// ever saw the failure.
func withoutTransportRetry(cfg *opensearchapi.Config) {
	cfg.Client.DisableRetry = true
}

// failureCallback is what one OnFailure call reported, reduced to the parts that
// tell the failure kinds apart.
type failureCallback struct {
	ID        string
	HasErr    bool   // err was non-nil
	ErrPrefix string // "flush: " for an error that wraps a failed request
	Status    int    // the response item's status; 0 when no response item exists
	ErrorType string // the response item's error type; empty when it carries none
}

// TestBulkIndexerFailureKinds pins the arguments OnFailure receives for each way
// an item can fail. Per-item rejection is told apart from the others by a nil
// err. The other three kinds -- a request that failed, a response that could not
// be paired with the items, and a document that could not be serialized -- all
// carry a non-nil err and an empty response item, so they differ only in the
// text of err. A refused request and one that never reached the cluster are the
// same kind here, even though only one of them can have been partly applied.
//
// The transport's own retry is switched off so each outcome reaches the indexer
// as written; TestBulkIndexerTransportRetryIsBelowTheIndexer covers it.
func TestBulkIndexerFailureKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   bulkOutcome
		item      BulkIndexerItem
		want      failureCallback
		wantErrIn string // substring of err; empty when err is nil
		wantOnErr int    // OnError calls
	}{
		{
			name:    "item rejected by the cluster",
			outcome: outcomeEveryItemRejected,
			item:    BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want: failureCallback{
				ID: "doc", Status: http.StatusTooManyRequests, ErrorType: "es_rejected_execution_exception",
			},
		},
		{
			name:      "connection error",
			outcome:   outcomeConnectionError,
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want:      failureCallback{ID: "doc", HasErr: true, ErrPrefix: "flush: "},
			wantErrIn: "simulated bulk request error",
			wantOnErr: 1,
		},
		{
			name:      "whole request refused with 429",
			outcome:   outcomeTooManyRequests,
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want:      failureCallback{ID: "doc", HasErr: true, ErrPrefix: "flush: "},
			wantErrIn: "es_rejected_execution_exception",
			wantOnErr: 1,
		},
		{
			name:      "whole request refused with 503",
			outcome:   outcomeUnavailable,
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want:      failureCallback{ID: "doc", HasErr: true, ErrPrefix: "flush: "},
			wantErrIn: "unavailable_shards_exception",
			wantOnErr: 1,
		},
		{
			name:      "response cannot be paired with the items",
			outcome:   outcomeExtraItem,
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want:      failureCallback{ID: "doc", HasErr: true, ErrPrefix: "flush: "},
			wantErrIn: "bulk response has 2 items, indexer serialized 1",
			wantOnErr: 1,
		},
		{
			name:      "response holds fewer results than the items",
			outcome:   outcomeMissingItem,
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
			want:      failureCallback{ID: "doc", HasErr: true, ErrPrefix: "flush: "},
			wantErrIn: "bulk response has 0 items, indexer serialized 1",
			wantOnErr: 1,
		},
		{
			name:    "document cannot be serialized",
			outcome: outcomeOK,
			// NaN cannot be serialized into the action metadata line. The
			// failure is reported when the item is written, so no request is
			// made for it and the flush has nothing to fail.
			item:      BulkIndexerItem{Action: "index", DocumentID: "doc", WaitForActiveShards: math.NaN()},
			want:      failureCallback{ID: "doc", HasErr: true},
			wantErrIn: "json: unsupported value: NaN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.outcome}}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

			var (
				failures []failureCallback
				errs     []string
				onErrors []string
			)
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1,
				Client:     client,
				OnError:    func(_ context.Context, err error) { onErrors = append(onErrors, err.Error()) },
			})

			item := tt.item
			item.OnFailure = func(_ context.Context, item BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
				got := failureCallback{ID: item.DocumentID, HasErr: err != nil, Status: resp.Status}
				if err != nil {
					errs = append(errs, err.Error())
					if strings.HasPrefix(err.Error(), "flush: ") {
						got.ErrPrefix = "flush: "
					}
				}
				// The indexer fills Error in, so the callback can read it unchecked.
				if resp.Error == nil {
					t.Errorf("OnFailure got a nil Error for %q", item.DocumentID)
				} else {
					got.ErrorType = resp.Error.Type
				}
				failures = append(failures, got)
			}

			// Close drains the buffer and reports a failed drain to OnError, but
			// returns nil: a failed request is not a failed Close.
			require.NoError(t, bi.Add(t.Context(), item))
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, []failureCallback{tt.want}, failures)
			if tt.wantErrIn != "" {
				require.Len(t, errs, 1)
				require.Contains(t, errs[0], tt.wantErrIn)
			}
			require.Len(t, onErrors, tt.wantOnErr, "OnError")
			require.Equal(t, uint64(1), bi.Stats().NumFailed)
		})
	}
}

// TestBulkIndexerRequestsAfterFailure pins what follows a bulk request that did
// not go cleanly: nothing is retried, the failed batch is dropped from the
// buffer, and items added afterwards go out in a request of their own and
// succeed. The indexer carries no state from one flush to the next.
func TestBulkIndexerRequestsAfterFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		first     bulkOutcome
		wantFlush bool // the first Flush returns an error
	}{
		{name: "connection error", first: outcomeConnectionError, wantFlush: true},
		{name: "whole request refused with 429", first: outcomeTooManyRequests, wantFlush: true},
		{name: "whole request refused with 503", first: outcomeUnavailable, wantFlush: true},
		{name: "response cannot be paired with the items", first: outcomeExtraItem, wantFlush: true},
		{name: "response holds fewer results than the items", first: outcomeMissingItem, wantFlush: true},
		// Every rejected item has an OnFailure, so the drain itself succeeded.
		{name: "every item rejected", first: outcomeEveryItemRejected, wantFlush: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.first, outcomeOK}}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

			var (
				failed    []string
				succeeded []string
				onErrors  []string
			)
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1,
				Client:     client,
				OnError:    func(_ context.Context, err error) { onErrors = append(onErrors, err.Error()) },
			})

			add := func(id string) {
				require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
					Action:     "index",
					DocumentID: id,
					Body:       strings.NewReader(`{"a":1}`),
					OnSuccess: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
						succeeded = append(succeeded, item.DocumentID)
					},
					OnFailure: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem, _ error) {
						failed = append(failed, item.DocumentID)
					},
				}))
			}

			add("a")
			add("b")
			if tt.wantFlush {
				require.Error(t, bi.Flush(t.Context()), "first Flush")
			} else {
				require.NoError(t, bi.Flush(t.Context()), "first Flush")
			}

			add("c")
			require.NoError(t, bi.Flush(t.Context()), "second Flush")
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, [][]string{
				{"index:a", "index:b"},
				{"index:c"},
			}, seq.requests(), "the failed batch is sent once and not again with the next")
			require.Equal(t, []string{"a", "b"}, failed, "OnFailure fires once per item of the failed batch")
			require.Equal(t, []string{"c"}, succeeded, "an item added after the failure succeeds")
			require.Empty(t, onErrors, "an explicit Flush reports through its return value, not OnError")

			require.Equal(t, BulkIndexerStats{
				NumAdded:          3,
				NumUngroupedItems: 3, // No index, so no shard.
				NumFailed:         2,
				NumFlushed:        1,
				NumIndexed:        1,
				NumRequests:       2,
			}, withoutLoad(bi.Stats()))
		})
	}
}

// TestBulkIndexerCallbacksRunOnWorker pins that item callbacks run on the
// goroutine that sends the buffer, before the flush that triggered them returns,
// and with the scheduler's lock free. A callback that blocks therefore stalls its
// buffer -- and any Flush or Close waiting on it, and the shard's next buffer --
// but not the rest of the indexer.
func TestBulkIndexerCallbacksRunOnWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome bulkOutcome
		item    BulkIndexerItem
	}{
		{
			name:    "OnSuccess",
			outcome: outcomeOK,
			item:    BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
		},
		{
			name:    "OnFailure for a rejected item",
			outcome: outcomeEveryItemRejected,
			item:    BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
		},
		{
			name:    "OnFailure for a failed request",
			outcome: outcomeConnectionError,
			item:    BulkIndexerItem{Action: "index", DocumentID: "doc", Body: strings.NewReader(`{"a":1}`)},
		},
		{
			name:    "OnFailure for an unserializable document",
			outcome: outcomeOK,
			item:    BulkIndexerItem{Action: "index", DocumentID: "doc", WaitForActiveShards: math.NaN()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.outcome}}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems)
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1,
				Client:     client,
			})
			sched := bi.(*bulkIndexer).sched

			// Only the goroutine sending the buffer appends, and the test reads
			// after Flush has returned, so no lock of its own is needed.
			var calls []string
			// record fails the test when the scheduler's lock was held while the
			// callback ran, which would stall every Add behind a slow callback;
			// TryLock only succeeds on a lock nobody holds.
			record := func(name string) {
				if !sched.TryLock() {
					t.Errorf("%s ran with the scheduler lock held", name)
				} else {
					sched.Unlock()
				}
				calls = append(calls, name)
			}

			item := tt.item
			item.OnSuccess = func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem) { record("success") }
			item.OnFailure = func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem, error) { record("failure") }

			require.NoError(t, bi.Add(t.Context(), item))
			// Flush's error is the point of other tests; here only the ordering
			// matters, so a failed drain is expected for some rows.
			_ = bi.Flush(t.Context())
			require.Len(t, calls, 1, "the callback must have run by the time Flush returns")
			require.NoError(t, bi.Close(t.Context()))
		})
	}
}

// TestBulkIndexerTransportRetryIsBelowTheIndexer pins where the only retry in the
// write path lives. The indexer never resends a batch, but the transport does by
// default: a bulk request that draws a 502, 503 or 504 is sent again with the
// same body, and the indexer sees the final answer as one request. A 429 is not
// in the default retry set, so it reaches the indexer on the first attempt.
func TestBulkIndexerTransportRetryIsBelowTheIndexer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		outcomes     []bulkOutcome
		wantAttempts [][]string
		wantFailed   []string
		wantOK       []string
	}{
		{
			name:         "503 then success is one request to the indexer",
			outcomes:     []bulkOutcome{outcomeUnavailable, outcomeOK},
			wantAttempts: [][]string{{"index:a"}, {"index:a"}},
			wantOK:       []string{"a"},
		},
		{
			name:         "429 is not retried",
			outcomes:     []bulkOutcome{outcomeTooManyRequests, outcomeOK},
			wantAttempts: [][]string{{"index:a"}},
			wantFailed:   []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: tt.outcomes}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems)

			var failed, succeeded []string
			bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: client})

			require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
				Action:     "index",
				DocumentID: "a",
				Body:       strings.NewReader(`{"a":1}`),
				OnSuccess: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
					succeeded = append(succeeded, item.DocumentID)
				},
				OnFailure: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem, _ error) {
					failed = append(failed, item.DocumentID)
				},
			}))
			_ = bi.Flush(t.Context())
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, tt.wantAttempts, seq.requests(), "requests that reached the transport")
			require.Equal(t, tt.wantFailed, failed, "OnFailure")
			require.Equal(t, tt.wantOK, succeeded, "OnSuccess")
			require.Equal(t, uint64(1), bi.Stats().NumRequests, "the indexer counts the batch once")
		})
	}
}
