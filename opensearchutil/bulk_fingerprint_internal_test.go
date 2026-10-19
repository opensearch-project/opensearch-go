// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// bodyHash fingerprints an item by hashing its body. It deliberately leaves the
// body at its end, which the indexer must repair.
func bodyHash(_ context.Context, item BulkIndexerItem) (string, error) {
	body, err := io.ReadAll(item.Body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:4]), nil
}

func TestBulkIndexerDocumentID(t *testing.T) {
	t.Parallel()

	errFingerprint := errors.New("cannot fingerprint")

	retry := RetryPolicyFunc(func(
		attempt int, _ *BulkError,
	) (time.Duration, bool) {
		return 0, attempt < 2
	})

	tests := []struct {
		name          string
		policy        RetryPolicy
		fingerprint   DocumentFingerprinter
		item          BulkIndexerItem
		outcomes      []bulkOutcome
		wantSent      [][]string // action lines of each request
		wantFailErr   error      // OnError err, matched with errors.Is
		wantSucceeded []string   // ids passed to OnSuccess
		wantNotCalls  bool       // the fingerprinter must not have run
	}{
		{
			name:          "no id without a policy goes to the cluster as before",
			item:          BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:      []bulkOutcome{outcomeOK},
			wantSent:      [][]string{{"index:"}},
			wantSucceeded: []string{""},
		},
		{
			name:        "no id with a policy is refused and never sent",
			policy:      retry,
			item:        BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:    []bulkOutcome{outcomeOK},
			wantFailErr: ErrMissingDocumentID,
		},
		{
			name:          "the fingerprinter supplies an id without a policy",
			fingerprint:   bodyHash,
			item:          BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:      []bulkOutcome{outcomeOK},
			wantSent:      [][]string{{"index:015abd7f"}},
			wantSucceeded: []string{"015abd7f"},
		},
		{
			name:          "the fingerprinter satisfies a policy and the id survives a retry",
			policy:        retry,
			fingerprint:   bodyHash,
			item:          BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:      []bulkOutcome{outcomeTooManyRequests, outcomeOK},
			wantSent:      [][]string{{"index:015abd7f"}, {"index:015abd7f"}},
			wantSucceeded: []string{"015abd7f"},
		},
		{
			name:        "a fingerprinter error fails the item",
			fingerprint: func(context.Context, BulkIndexerItem) (string, error) { return "", errFingerprint },
			item:        BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:    []bulkOutcome{outcomeOK},
			wantFailErr: errFingerprint,
		},
		{
			name:        "an empty id from the fingerprinter is a missing id",
			fingerprint: func(context.Context, BulkIndexerItem) (string, error) { return "", nil },
			item:        BulkIndexerItem{Body: strings.NewReader(`{"a":1}`)},
			outcomes:    []bulkOutcome{outcomeOK},
			wantFailErr: ErrMissingDocumentID,
		},
		{
			name:          "an item with an id is not fingerprinted",
			policy:        retry,
			fingerprint:   func(context.Context, BulkIndexerItem) (string, error) { return "wrong", nil },
			item:          BulkIndexerItem{DocumentID: "mine", Body: strings.NewReader(`{"a":1}`)},
			outcomes:      []bulkOutcome{outcomeOK},
			wantSent:      [][]string{{"index:mine"}},
			wantSucceeded: []string{"mine"},
			wantNotCalls:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: tt.outcomes}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

			var (
				calls     atomic.Int64
				failErrs  []error
				succeeded []string
				onErrors  []error
				log       = &eventLog{}
			)
			cfg := BulkIndexerConfig{
				NumWorkers:       1,
				Client:           client,
				Observer:         log,
				RetryPolicy:      tt.policy,
				OnUnhandledError: func(_ context.Context, err error) { onErrors = append(onErrors, err) },
			}
			if tt.fingerprint != nil {
				cfg.Fingerprinter = func(ctx context.Context, item BulkIndexerItem) (string, error) {
					calls.Add(1)
					return tt.fingerprint(ctx, item)
				}
			}
			bi := newBulkIndexer(t, cfg)

			item := tt.item
			item.Action = "index"
			item.OnSuccess = func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
				succeeded = append(succeeded, item.DocumentID)
			}
			item.OnError = func(_ context.Context, _ BulkIndexerItem, err *BulkError) {
				failErrs = append(failErrs, err)
			}

			require.NoError(t, bi.Add(t.Context(), item), "a refused item is reported through OnError, not Add")
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, tt.wantSent, seq.requests(), "requests sent")
			require.Equal(t, tt.wantSucceeded, succeeded, "OnSuccess")
			if tt.wantFailErr == nil {
				require.Empty(t, failErrs)
			} else {
				require.Len(t, failErrs, 1, "OnError fires once")
				require.ErrorIs(t, failErrs[0], tt.wantFailErr)
				require.Equal(t, uint64(1), bi.Stats().NumFailed)
				require.Zero(t, bi.Stats().NumAdded, "a refused item is not added")
				require.Empty(t, onErrors, "an item with an OnError is not also reported to OnUnhandledError")
				require.Contains(t, log.lines[0], "item-failure scope=item a=0 kind=permanent")
			}
			if tt.wantNotCalls {
				require.Zero(t, calls.Load())
			}
			// Every request carries the body the caller supplied, whatever the
			// fingerprinter did to the reader.
			for _, body := range seq.bodies() {
				require.True(t, strings.HasSuffix(body, "{\"a\":1}\n"), "body %q", body)
			}
		})
	}
}

// TestBulkIndexerGeneratedIDRoutesLikeAnExplicitOne pins that the fingerprinter
// runs before the item is routed: identical documents get one id, so they reach
// one worker and share a request, as the same explicit id would.
func TestBulkIndexerGeneratedIDRoutesLikeAnExplicitOne(t *testing.T) {
	t.Parallel()

	const (
		numWorkers = 8
		duplicates = 5
	)

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)
	bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: numWorkers, Client: client, Fingerprinter: bodyHash})

	for range duplicates {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", Body: strings.NewReader(`{"a":1}`),
		}))
	}
	// Other documents, so a build routing everything to one worker cannot pass.
	for i := range numWorkers * 4 {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", Body: strings.NewReader(`{"n":` + strings.Repeat("1", i+1) + `}`),
		}))
	}
	require.NoError(t, bi.Close(t.Context()))

	carrying := 0
	for _, actions := range seq.requests() {
		hits := 0
		for _, a := range actions {
			if a == "index:015abd7f" {
				hits++
			}
		}
		if hits > 0 {
			carrying++
			require.Equal(t, duplicates, hits, "every copy rides one request")
		}
	}
	require.Equal(t, 1, carrying)
}

// TestBulkIndexerRejectedItemFallsBackToOnError pins that an item refused before
// it was queued, and which has no OnError to hear of it, is still reported.
func TestBulkIndexerRejectedItemFallsBackToOnError(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeOK}}
	client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

	var onErrors []error
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1,
		Client:     client,
		RetryPolicy: RetryPolicyFunc(func(
			int, *BulkError,
		) (time.Duration, bool) {
			return 0, false
		}),
		OnUnhandledError: func(_ context.Context, err error) { onErrors = append(onErrors, err) },
	})

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{Action: "index", Body: strings.NewReader(`{"a":1}`)}))
	require.NoError(t, bi.Close(t.Context()))

	require.Len(t, onErrors, 1)
	require.ErrorIs(t, onErrors[0], ErrMissingDocumentID)
	require.Empty(t, seq.requests())
}
