// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/errmask"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// TestRespHelperMethods covers the per-Resp wrapper-helper methods
// emitted by [PartialFailureFragment] in cmd/osgen, plus the
// PartialFailures(mask) aggregator. These are codegen output, so
// regression coverage protects the template + applies/RenderMethod
// hookup.
//
// We construct Resp values directly (no HTTP mock) since the methods
// operate on already-decoded data; the dispatch path is exercised by
// the regenerated handlers under TestDispatch_*.
//
// Each row's `assert` closure does the per-Resp inspection: callers
// vary in the helper they invoke (BulkItemFailures vs SearchShard
// Failures vs ...), what fields the typed-error exposes, and which
// mask bit gates them. The aggregator-gating check is uniform: build
// once, assert helper output, assert PartialFailures(Empty) length,
// assert PartialFailures(maskBit) is empty.
func TestRespHelperMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "BulkResp.BulkItemFailures",
			assert: func(t *testing.T) {
				t.Helper()
				id1, id2 := "1", "2"
				errType := "mapper_parsing_exception"
				errReason := "boom"
				r := &opensearchapi.BulkResp{
					Errors: true,
					Items: []opensearchapi.BulkItem{
						{Index: &opensearchapi.BulkRespItem{ID: &id1, Status: 201}},
						{Index: &opensearchapi.BulkRespItem{
							ID:     &id2,
							Status: 400,
							Error:  &opensearchapi.ErrorCause{Type: errType, Reason: &errReason},
						}},
					},
				}
				e := r.BulkItemFailures()
				require.NotNil(t, e)
				require.Len(t, e.FailedItems, 1)
				require.Equal(t, 1, e.SucceededCount)

				require.Len(t, r.PartialFailures(errmask.Empty), 1)
				require.Empty(t, r.PartialFailures(errmask.BulkItems))
			},
		},
		{
			name: "SearchResp.SearchShardFailures",
			assert: func(t *testing.T) {
				t.Helper()
				shardReason := "boom"
				r := &opensearchapi.SearchResp{
					SearchResult: opensearchapi.SearchResult{
						Shards: opensearchapi.ShardStatistics{
							Total: 5, Successful: 3, Failed: 2,
							Failures: []opensearchapi.ShardSearchFailure{
								{Reason: opensearchapi.ErrorCause{Type: "x", Reason: &shardReason}},
							},
						},
					},
				}
				e := r.SearchShardFailures()
				require.NotNil(t, e)
				require.Equal(t, 2, e.FailedShards)
				require.Equal(t, 5, e.TotalShards)

				require.Len(t, r.PartialFailures(errmask.Empty), 1)
				require.Empty(t, r.PartialFailures(errmask.SearchShards))
			},
		},
		{
			name: "IndexResp.WriteShardFailures",
			assert: func(t *testing.T) {
				t.Helper()
				r := &opensearchapi.IndexResp{
					Shards: opensearchapi.ShardStatistics{Total: 2, Successful: 1, Failed: 1},
				}
				e := r.WriteShardFailures()
				require.NotNil(t, e)
				require.Equal(t, opensearchapi.OperationIndex, e.Operation)
				require.Equal(t, 1, e.FailedShards)
				require.Equal(t, 2, e.TotalShards)

				require.Len(t, r.PartialFailures(errmask.Empty), 1)
				require.Empty(t, r.PartialFailures(errmask.WriteShards))
			},
		},
		{
			name: "MSearchResp aggregator fires both wrappers (union dispatch)",
			assert: func(t *testing.T) {
				t.Helper()
				shardReason := "shard boom"
				itemReason := "unknown query"
				r := &opensearchapi.MSearchResp{
					Responses: []opensearchapi.MSearchMultiSearchResultResponsesItem{
						opensearchapi.NewMSearchMultiSearchResultResponsesItemFromMSearchMultiSearchItem(
							opensearchapi.MSearchMultiSearchItem{
								SearchResult: opensearchapi.SearchResult{
									Shards: opensearchapi.ShardStatistics{
										Total: 5, Successful: 3, Failed: 2,
										Failures: []opensearchapi.ShardSearchFailure{
											{Reason: opensearchapi.ErrorCause{Type: "x", Reason: &shardReason}},
										},
									},
								},
							},
						),
						opensearchapi.NewMSearchMultiSearchResultResponsesItemFromErrorRespBase(
							opensearchapi.ErrorRespBase{
								Status: 400,
								Error:  opensearchapi.ErrorCause{Type: "parsing_exception", Reason: &itemReason},
							},
						),
					},
				}

				shardErr := r.SearchShardFailures()
				require.NotNil(t, shardErr)
				require.Equal(t, 2, shardErr.FailedShards)

				itemErr := r.MultiSearchItemFailures()
				require.NotNil(t, itemErr)
				require.Len(t, itemErr.Items, 1)
				require.Equal(t, 1, itemErr.SucceededCount)
				require.Equal(t, 1, itemErr.Items[0].Index) // sub-response position

				// Both bits unmasked -> 2 sub-errors.
				require.Len(t, r.PartialFailures(errmask.Empty), 2)

				// Mask one bit -> the other still fires.
				got := r.PartialFailures(errmask.SearchShards)
				require.Len(t, got, 1)
				var multiItemErr *opensearchapi.MultiSearchItemError
				require.ErrorAs(t, got[0], &multiItemErr)

				got = r.PartialFailures(errmask.MultiSearchItems)
				require.Len(t, got, 1)
				var shardErrSub *opensearchapi.PartialSearchError
				require.ErrorAs(t, got[0], &shardErrSub)

				// Both masked -> aggregator returns nothing.
				require.Empty(t, r.PartialFailures(errmask.SearchShards|errmask.MultiSearchItems))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.assert(t)
		})
	}
}

// TestNilRespHelperMethods confirms the per-wrapper helpers return nil
// (no panic) when called on a nil receiver. The dispatch collapse rule
// normally prevents this, but defensive code paths and tests may
// exercise the helpers directly.
func TestNilRespHelperMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func() any
	}{
		{"nil BulkResp", func() any { var r *opensearchapi.BulkResp; return r.BulkItemFailures() }},
		{"nil SearchResp", func() any { var r *opensearchapi.SearchResp; return r.SearchShardFailures() }},
		{"nil IndexResp", func() any { var r *opensearchapi.IndexResp; return r.WriteShardFailures() }},
		{"nil DeleteResp", func() any { var r *opensearchapi.DeleteResp; return r.WriteShardFailures() }},
		{"nil UpdateResp", func() any { var r *opensearchapi.UpdateResp; return r.WriteShardFailures() }},
		{"nil MSearchResp shard helper", func() any { var r *opensearchapi.MSearchResp; return r.SearchShardFailures() }},
		{"nil MSearchResp item helper", func() any { var r *opensearchapi.MSearchResp; return r.MultiSearchItemFailures() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.call()
			require.True(t, isTypedNil(got), "expected typed nil, got %T (%v)", got, got)
		})
	}
}

// TestPackageErrorsHelper covers the opensearchapi.Errors(err) package
// helper in v5preview: a non-nil err returns a single-element slice
// (or the unwrapped slice for a per-op multi-error wrapper); nil
// returns nil.
func TestPackageErrorsHelper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantLen int
		// assertElems runs after the length check; nil to skip.
		assertElems func(t *testing.T, got []error)
	}{
		{
			name:    "nil err returns nil",
			err:     nil,
			wantLen: 0,
		},
		{
			name:    "non-partial err returns single-element slice",
			err:     errors.New("boom"),
			wantLen: 1,
		},
		{
			name:    "single sub-error returns single-element slice",
			err:     &opensearchapi.PartialBulkError{},
			wantLen: 1,
			assertElems: func(t *testing.T, got []error) {
				t.Helper()
				var target *opensearchapi.PartialBulkError
				require.ErrorAs(t, got[0], &target)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := opensearchapi.Errors(tt.err)
			require.Len(t, got, tt.wantLen)
			if tt.assertElems != nil {
				tt.assertElems(t, got)
			}
		})
	}
}

// isTypedNil reports whether v is a non-nil interface containing a
// typed nil pointer. Used by TestNilRespHelperMethods.
func isTypedNil(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case *opensearchapi.PartialBulkError:
		return x == nil
	case *opensearchapi.PartialSearchError:
		return x == nil
	case *opensearchapi.ShardFailureError:
		return x == nil
	case *opensearchapi.MultiSearchItemError:
		return x == nil
	}
	return false
}
