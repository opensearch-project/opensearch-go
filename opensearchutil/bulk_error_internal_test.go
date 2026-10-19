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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestBulkErrorIs: a BulkError matches the sentinel of its layer and of its kind,
// and what its cause matches, and nothing else.
func TestBulkErrorIs(t *testing.T) {
	t.Parallel()

	layers := []error{ErrItemLayer, ErrRequestLayer, ErrIndexLayer, ErrIndexerLayer}
	kinds := []error{ErrRejected, ErrUnavailable, ErrTransport, ErrPermanent, ErrIndexerCanceled}
	cause := errors.New("the cause")

	tests := []struct {
		name      string
		err       *BulkError
		wantLayer error
		wantKind  error
	}{
		{
			name:      "an item the cluster rejected",
			err:       &BulkError{Layer: LayerItem, Kind: KindPermanent},
			wantLayer: ErrItemLayer,
			wantKind:  ErrPermanent,
		},
		{
			name:      "a request refused with a 429",
			err:       &BulkError{Layer: LayerRequest, Kind: KindRejected, Err: cause},
			wantLayer: ErrRequestLayer,
			wantKind:  ErrRejected,
		},
		{
			name:      "an unavailable request",
			err:       &BulkError{Layer: LayerRequest, Kind: KindUnavailable},
			wantLayer: ErrRequestLayer,
			wantKind:  ErrUnavailable,
		},
		{
			name:      "a request with no answer",
			err:       &BulkError{Layer: LayerRequest, Kind: KindTransport},
			wantLayer: ErrRequestLayer,
			wantKind:  ErrTransport,
		},
		{
			name:      "an index whose shard map changed",
			err:       &BulkError{Layer: LayerIndex, Kind: KindPermanent},
			wantLayer: ErrIndexLayer,
			wantKind:  ErrPermanent,
		},
		{
			name:      "a canceled indexer",
			err:       &BulkError{Layer: LayerIndexer, Kind: KindCanceled},
			wantLayer: ErrIndexerLayer,
			wantKind:  ErrIndexerCanceled,
		},
		{name: "the zero value matches nothing", err: &BulkError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, l := range layers {
				require.Equal(t, errors.Is(l, tt.wantLayer), errors.Is(tt.err, l), "layer %v", l)
			}
			for _, k := range kinds {
				require.Equal(t, errors.Is(k, tt.wantKind), errors.Is(tt.err, k), "kind %v", k)
			}
			require.Equal(t, tt.err.Err != nil, errors.Is(tt.err, cause), "the cause")
		})
	}
}

// TestBulkErrorMessage: the message is the cause's, or for an item the cluster
// rejected, the action, the document ID and the cluster's reason.
func TestBulkErrorMessage(t *testing.T) {
	t.Parallel()

	id, reason := "doc-1", "failed to parse"
	tests := []struct {
		name string
		err  *BulkError
		want string
	}{
		{name: "a cause", err: &BulkError{Layer: LayerRequest, Err: errors.New("connection reset")}, want: "connection reset"},
		{
			name: "a rejection with a reason",
			err: &BulkError{Layer: LayerItem, Status: http.StatusBadRequest, action: actionIndex, Resp: opensearchapi.BulkRespItem{
				ID: &id, Status: http.StatusBadRequest, Error: &opensearchapi.ErrorCause{Type: "mapper_parsing_exception", Reason: &reason},
			}},
			want: `"index" "doc-1" rejected: "mapper_parsing_exception": "failed to parse"`,
		},
		{
			name: "a rejection with a status alone",
			err: &BulkError{
				Layer: LayerItem, Status: http.StatusNotFound, action: actionDelete,
				Resp: opensearchapi.BulkRespItem{ID: &id, Status: http.StatusNotFound},
			},
			want: `"delete" "doc-1" rejected: status 404`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}

// TestUnsentError: an item that is never sent is classified by why.
func TestUnsentError(t *testing.T) {
	t.Parallel()

	item := BulkIndexerItem{Action: actionIndex, DocumentID: "doc"}
	tests := []struct {
		name      string
		cause     error
		wantLayer ErrorLayer
		wantKind  ErrorKind
	}{
		{name: "the indexer was canceled", cause: ErrIndexerCanceled, wantLayer: LayerIndexer, wantKind: KindCanceled},
		{
			name:      "the index's shard map changed",
			cause:     fmt.Errorf("%w: index %q", ErrShardMapChanged, "idx"),
			wantLayer: LayerIndex,
			wantKind:  KindPermanent,
		},
		{name: "the item has no ID", cause: ErrMissingDocumentID, wantLayer: LayerItem, wantKind: KindPermanent},
		{name: "the body cannot be read", cause: errors.New("read: broken pipe"), wantLayer: LayerItem, wantKind: KindPermanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := unsentError(item, tt.cause)
			require.Equal(t, tt.wantLayer, err.Layer)
			require.Equal(t, tt.wantKind, err.Kind)
			require.ErrorIs(t, err, tt.cause, "the cause stays reachable")
		})
	}
}

// TestBulkIndexerOnErrorMatchesTheSentinels: what an item's OnError receives can be
// told apart with errors.Is, whichever layer failed.
func TestBulkIndexerOnErrorMatchesTheSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   bulkOutcome
		wantLayer error
		wantKind  error
	}{
		{
			name:      "an item the cluster rejected",
			outcome:   outcomeItems(map[string]int{"a": http.StatusConflict}),
			wantLayer: ErrItemLayer,
			wantKind:  ErrPermanent,
		},
		{
			name:      "an item refused for a full write queue",
			outcome:   outcomeItems(map[string]int{"a": http.StatusTooManyRequests}),
			wantLayer: ErrItemLayer,
			wantKind:  ErrRejected,
		},
		{name: "a request refused with a 429", outcome: outcomeTooManyRequests, wantLayer: ErrRequestLayer, wantKind: ErrRejected},
		{name: "a request that got no answer", outcome: outcomeConnectionError, wantLayer: ErrRequestLayer, wantKind: ErrTransport},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := runRetry(t, inherit, []bulkOutcome{tt.outcome}, &recordingPolicy{decide: neverRetry}, "a")
			require.Len(t, run.failErrs, 1)
			require.ErrorIs(t, run.failErrs[0], tt.wantLayer)
			require.ErrorIs(t, run.failErrs[0], tt.wantKind)
		})
	}

	t.Run("a canceled indexer", func(t *testing.T) {
		t.Parallel()

		bi := newBulkIndexer(t, BulkIndexerConfig{NumWorkers: 1, Client: newBulkTestClient(t, defaultRoundTripFunc)})
		bi.Cancel()

		var got *BulkError
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: actionIndex, DocumentID: "a",
			OnError: func(_ context.Context, _ BulkIndexerItem, err *BulkError) { got = err },
		}))
		require.NoError(t, bi.Close(t.Context()))

		require.ErrorIs(t, got, ErrIndexerLayer)
		require.ErrorIs(t, got, ErrIndexerCanceled)
		require.ErrorIs(t, got, context.Canceled)
	})
}
