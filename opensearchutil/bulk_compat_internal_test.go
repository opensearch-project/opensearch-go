// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// v5Call is what a v5.0 OnFailure callback was handed.
type v5Call struct {
	resp opensearchapi.BulkRespItem
	err  error
}

// TestBulkIndexerItemOnFailureCompat: an item that sets only the deprecated OnFailure
// still hears of its failure, with the arguments v5.0 gave it: a nil error and the
// response for an item the cluster rejected, the cause for a request that failed, and
// always a non-nil resp.Error.
func TestBulkIndexerItemOnFailureCompat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outcome    bulkOutcome
		wantNilErr bool
		wantStatus int
	}{
		{
			name: "an item the cluster rejected", outcome: outcomeItems(map[string]int{"a": http.StatusConflict}),
			wantNilErr: true, wantStatus: http.StatusConflict,
		},
		{name: "a request with no answer", outcome: outcomeConnectionError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seq := &bulkSequence{outcomes: []bulkOutcome{tt.outcome}}
			bi := newBulkIndexer(t, BulkIndexerConfig{
				NumWorkers: 1, Client: newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry),
			})

			var calls []v5Call
			require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
				Action: actionIndex, DocumentID: "a", Body: seqBody(1),
				OnFailure: func(_ context.Context, _ BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
					calls = append(calls, v5Call{resp: resp, err: err})
				},
			}))
			require.NoError(t, bi.Close(t.Context()))

			require.Len(t, calls, 1)
			require.Equal(t, tt.wantNilErr, calls[0].err == nil)
			require.Equal(t, tt.wantStatus, calls[0].resp.Status)
			require.NotNil(t, calls[0].resp.Error, "v5.0 always filled resp.Error in")
		})
	}
}

// TestBulkIndexerItemOnErrorWinsOverOnFailure: with both callbacks set, only OnError runs.
func TestBulkIndexerItemOnErrorWinsOverOnFailure(t *testing.T) {
	t.Parallel()

	seq := &bulkSequence{outcomes: []bulkOutcome{outcomeItems(map[string]int{"a": http.StatusConflict})}}
	bi := newBulkIndexer(t, BulkIndexerConfig{
		NumWorkers: 1, Client: newBulkTestClient(t, seq.roundTrip, unmaskedBulkItems, withoutTransportRetry),
	})

	var onError, onFailure int
	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Action: actionIndex, DocumentID: "a", Body: seqBody(1),
		OnError:   func(context.Context, BulkIndexerItem, *BulkError) { onError++ },
		OnFailure: func(context.Context, BulkIndexerItem, opensearchapi.BulkRespItem, error) { onFailure++ },
	}))
	require.NoError(t, bi.Close(t.Context()))

	require.Equal(t, 1, onError)
	require.Zero(t, onFailure)
}

// TestBulkIndexerConfigOnErrorCompat: the deprecated config OnError receives what
// OnUnhandledError would, and OnUnhandledError wins when both are set.
func TestBulkIndexerConfigOnErrorCompat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setUnhandled  bool
		wantUnhandled int
		wantOnError   int
	}{
		{name: "only the deprecated OnError", wantOnError: 1},
		{name: "both set", setUnhandled: true, wantUnhandled: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var unhandled, onError int
			cfg := BulkIndexerConfig{
				NumWorkers: 1,
				Client: newBulkTestClient(t, (&bulkSequence{outcomes: []bulkOutcome{outcomeItems(map[string]int{"a": http.StatusConflict})}}).roundTrip,
					unmaskedBulkItems, withoutTransportRetry),
				OnError: func(context.Context, error) { onError++ },
			}
			if tt.setUnhandled {
				cfg.OnUnhandledError = func(context.Context, error) { unhandled++ }
			}
			bi := newBulkIndexer(t, cfg)

			// No callback of its own, so its rejection is unhandled.
			require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{Action: actionIndex, DocumentID: "a", Body: seqBody(1)}))
			require.NoError(t, bi.Close(t.Context()))

			require.Equal(t, tt.wantUnhandled, unhandled)
			require.Equal(t, tt.wantOnError, onError)
		})
	}
}
