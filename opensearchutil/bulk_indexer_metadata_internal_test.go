// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// streamPosition is the kind of value a consumer attaches as Metadata.
type streamPosition struct {
	Topic  string
	Offset int64
}

// TestBulkIndexerItemMetadata checks that Metadata reaches whichever callback
// the item gets, unchanged, and that it is not part of the request.
func TestBulkIndexerItemMetadata(t *testing.T) {
	t.Parallel()

	const wantBody = "{\"index\":{\"_id\":\"doc\"}}\n{\"a\":1}\n"

	position := streamPosition{Topic: "metadata-marker-topic", Offset: 4102}

	tests := []struct {
		name           string
		outcomes       []bulkOutcome
		policy         *recordingPolicy
		unserializable bool // add an item that cannot be serialized
		wantOK         bool // OnSuccess is called, otherwise OnError
		wantBodies     []string
	}{
		{name: "accepted", outcomes: []bulkOutcome{outcomeOK}, wantOK: true, wantBodies: []string{wantBody}},
		{
			name:       "rejected by the cluster",
			outcomes:   []bulkOutcome{outcomeEveryItemRejected},
			wantBodies: []string{wantBody},
		},
		{
			name:       "request failed",
			outcomes:   []bulkOutcome{outcomeConnectionError},
			wantBodies: []string{wantBody},
		},
		{
			name:           "cannot be serialized",
			outcomes:       []bulkOutcome{outcomeOK},
			unserializable: true,
		},
		{
			name:       "accepted after a retry",
			outcomes:   []bulkOutcome{outcomeTooManyRequests, outcomeOK},
			policy:     &recordingPolicy{decide: func(int, ErrorKind) (time.Duration, bool) { return 0, true }},
			wantOK:     true,
			wantBodies: []string{wantBody, wantBody},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: tt.outcomes}
			client := newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry)

			cfg := BulkIndexerConfig{NumWorkers: 1, Client: client}
			if tt.policy != nil {
				cfg.RetryPolicy = tt.policy
			}
			bi := newBulkIndexer(t, cfg)

			var succeeded, failed []any
			item := BulkIndexerItem{
				Action:     "index",
				DocumentID: "doc",
				Body:       strings.NewReader(`{"a":1}`),
				Metadata:   position,
				OnSuccess: func(_ context.Context, item BulkIndexerItem, _ opensearchapi.BulkRespItem) {
					succeeded = append(succeeded, item.Metadata)
				},
				OnError: func(_ context.Context, item BulkIndexerItem, _ *BulkError) {
					failed = append(failed, item.Metadata)
				},
			}
			if tt.unserializable {
				item.WaitForActiveShards = math.NaN()
			}

			require.NoError(t, bi.Add(t.Context(), item))
			require.NoError(t, bi.Close(t.Context()))

			if tt.wantOK {
				require.Equal(t, []any{position}, succeeded, "Metadata in OnSuccess")
				require.Empty(t, failed)
			} else {
				require.Equal(t, []any{position}, failed, "Metadata in OnError")
				require.Empty(t, succeeded)
			}

			// The whole body is compared, so a Metadata field serialized
			// anywhere in it, under any name, would fail here.
			require.Equal(t, tt.wantBodies, seq.bodies(), "request bodies")
		})
	}
}
