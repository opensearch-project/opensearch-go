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
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestBulkIndexerCloseReportsAnEndedContextToOnError: when the context handed to
// Close ends, Close returns its error and tells OnError once, as it does for the
// failures it finds while draining.
func TestBulkIndexerCloseReportsAnEndedContextToOnError(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

	var reported struct {
		sync.Mutex
		errs []error
	}
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		OnError: func(_ context.Context, err error) {
			reported.Lock()
			defer reported.Unlock()

			reported.errs = append(reported.errs, err)
		},
	})

	f := newCallbackCounts(1)
	require.NoError(t, bi.Add(t.Context(), f.item("a")))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, bi.Close(ctx), context.Canceled)

	reported.Lock()
	defer reported.Unlock()

	require.Len(t, reported.errs, 1, "the ended context is reported once")
	require.ErrorIs(t, reported.errs[0], context.Canceled)
}

func TestIsServed(t *testing.T) {
	t.Parallel()

	ok := &opensearchapi.BulkRespItem{Status: http.StatusCreated}
	rejected := &opensearchapi.BulkRespItem{Status: http.StatusTooManyRequests}

	tests := []struct {
		name string
		resp *opensearchapi.BulkResp
		want bool
	}{
		{name: "no response", resp: nil, want: false},
		{name: "no items", resp: &opensearchapi.BulkResp{}, want: false},
		{
			name: "every item refused",
			resp: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{{Index: rejected}, {Create: rejected}}},
			want: false,
		},
		{
			name: "one item indexed among refused ones",
			resp: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{{Index: rejected}, {Delete: ok}}},
			want: true,
		},
		{
			name: "an item with no result at all",
			resp: &opensearchapi.BulkResp{Items: []opensearchapi.BulkItem{{}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, isServed(tt.resp))
		})
	}
}

func TestIsTooLarge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp *opensearchapi.BulkResp
		err  error
		want bool
	}{
		{name: "nothing wrong", want: false},
		{name: "an unrelated error", err: errors.New("boom"), want: false},
		{name: "a 413 in a struct error", err: &opensearch.StructError{Status: http.StatusRequestEntityTooLarge}, want: true},
		{name: "a 413 in a string error", err: &opensearch.StringError{Status: http.StatusRequestEntityTooLarge}, want: true},
		{name: "a 429 in a struct error", err: &opensearch.StructError{Status: http.StatusTooManyRequests}, want: false},
		{name: "a 429 in a string error", err: &opensearch.StringError{Status: http.StatusTooManyRequests}, want: false},
		{
			name: "a 413 wrapped",
			err:  errors.Join(errors.New("bulk"), &opensearch.StructError{Status: http.StatusRequestEntityTooLarge}),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, isTooLarge(tt.resp, tt.err))
		})
	}
}

// rereadFails is a body that reads once and then cannot be read again, as a
// document backed by a connection that was closed after the first pass.
type rereadFails struct {
	*strings.Reader
	read, rewound bool
}

var errBodyGone = errors.New("body gone")

func (b *rereadFails) Read(p []byte) (int, error) {
	if b.rewound {
		return 0, errBodyGone
	}
	b.read = true

	return b.Reader.Read(p)
}

// Seek breaks the body when it is rewound after a pass: the indexer rewinds to 0 before
// the first read, which leaves a body that has not been read as it was.
func (b *rereadFails) Seek(offset int64, whence int) (int64, error) {
	b.rewound = b.rewound || b.read

	return b.Reader.Seek(offset, whence)
}

// TestBulkIndexerRetryFailsAnItemWhoseBodyCannotBeReadAgain: a retry serializes its
// items afresh. An item whose body fails on that second pass is final at once, with
// that error, and is not sent; with nothing else left to resend, no second request
// goes out.
func TestBulkIndexerRetryFailsAnItemWhoseBodyCannotBeReadAgain(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeTooManyRequests, outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		RetryPolicy: RetryPolicyFunc(func(
			int, FailureScope, FailureKind, opensearchapi.BulkRespItem, error,
		) (time.Duration, bool) {
			return 0, true
		}),
	})

	f := newCallbackCounts(1)
	item := f.item("a")
	item.Body = &rereadFails{Reader: strings.NewReader(`{"a":1}`)}
	require.NoError(t, bi.Add(t.Context(), item))
	require.NoError(t, bi.Close(t.Context()))

	require.EqualValues(t, 1, f.failed.Load())
	require.Zero(t, f.succeeded.Load())
	require.ErrorIs(t, <-f.errs, errBodyGone)
	require.Len(t, seq.requests(), 1, "the item that cannot be rebuilt is not sent again")
}
