// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/errmask"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// TestRespHelperMethods exercises the per-wrapper helper methods directly
// on Resp values, plus the PartialFailures(mask) aggregator's mask-gating
// behavior. The dispatch error path is covered by TestSingleOpDispatch
// in partial_failure_test.go; this test focuses on the methods themselves
// so callers can introspect a Resp without going through the dispatch
// error.
func TestRespHelperMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		body    string
		fetch   func(*opensearchapi.Client, context.Context) (any, error)
		// assertHelper inspects the per-wrapper method + PartialFailures
		// aggregator on the returned Resp. resp is the typed *Resp; mask
		// is whatever was configured on the client.
		assertHelper func(t *testing.T, resp any, mask errmask.ErrorMask)
	}{
		{
			name: "BulkResp.BulkItemFailures",
			path: "/_bulk",
			body: bulkPartialBody,
			fetch: func(c *opensearchapi.Client, ctx context.Context) (any, error) {
				return c.Bulk(ctx, opensearchapi.BulkReq{})
			},
			assertHelper: func(t *testing.T, resp any, mask errmask.ErrorMask) {
				r := resp.(*opensearchapi.BulkResp)
				e := r.BulkItemFailures()
				require.NotNil(t, e)
				require.Len(t, e.FailedItems, 1)
				require.Equal(t, 1, e.SucceededCount)

				// PartialFailures gates on mask: BulkItems unmasked
				// returns the helper's value; BulkItems masked drops it.
				gotUnmasked := r.PartialFailures(errmask.Empty)
				require.Len(t, gotUnmasked, 1)
				require.IsType(t, &opensearchapi.PartialBulkError{}, gotUnmasked[0])

				gotMasked := r.PartialFailures(errmask.BulkItems)
				require.Empty(t, gotMasked)
			},
		},
		{
			name: "SearchResp.SearchShardFailures",
			path: "/_search",
			body: searchShardFailureBody,
			fetch: func(c *opensearchapi.Client, ctx context.Context) (any, error) {
				return c.Search(ctx, nil)
			},
			assertHelper: func(t *testing.T, resp any, mask errmask.ErrorMask) {
				r := resp.(*opensearchapi.SearchResp)
				e := r.SearchShardFailures()
				require.NotNil(t, e)
				require.Equal(t, 2, e.FailedShards)
				require.Equal(t, 5, e.TotalShards)

				gotUnmasked := r.PartialFailures(errmask.Empty)
				require.Len(t, gotUnmasked, 1)

				gotMasked := r.PartialFailures(errmask.SearchShards)
				require.Empty(t, gotMasked)
			},
		},
		{
			name: "IndexResp.WriteShardFailures",
			path: "/i/_doc",
			body: writeShardFailureBody,
			fetch: func(c *opensearchapi.Client, ctx context.Context) (any, error) {
				return c.Index(ctx, opensearchapi.IndexReq{Index: "i"})
			},
			assertHelper: func(t *testing.T, resp any, mask errmask.ErrorMask) {
				r := resp.(*opensearchapi.IndexResp)
				e := r.WriteShardFailures()
				require.NotNil(t, e)
				require.Equal(t, opensearchapi.OperationIndex, e.Operation)
				require.Equal(t, 1, e.FailedShards)
				require.Equal(t, 2, e.TotalShards)

				gotUnmasked := r.PartialFailures(errmask.Empty)
				require.Len(t, gotUnmasked, 1)

				gotMasked := r.PartialFailures(errmask.WriteShards)
				require.Empty(t, gotMasked)
			},
		},
		{
			// MSearch returns two helpers + a PartialFailures aggregator
			// that can yield 2 sub-errors when both wrapper bits fire.
			name: "MSearchResp.SearchShardFailures + MultiSearchItemFailures",
			path: "/_msearch",
			body: msearchSubQueryErrorBody, // shard-failure-free but has per-item Error
			fetch: func(c *opensearchapi.Client, ctx context.Context) (any, error) {
				return c.MSearch(ctx, opensearchapi.MSearchReq{})
			},
			assertHelper: func(t *testing.T, resp any, mask errmask.ErrorMask) {
				r := resp.(*opensearchapi.MSearchResp)
				require.Nil(t, r.SearchShardFailures(), "no shard failures in fixture")
				e := r.MultiSearchItemFailures()
				require.NotNil(t, e)
				require.Len(t, e.Items, 1)
				require.Equal(t, 1, e.SucceededCount)

				// Aggregator: with everything reported, only MultiSearchItems fires.
				gotUnmasked := r.PartialFailures(errmask.Empty)
				require.Len(t, gotUnmasked, 1)
				require.IsType(t, &opensearchapi.MultiSearchItemError{}, gotUnmasked[0])

				// Mask MultiSearchItems -> aggregator returns nothing.
				gotMasked := r.PartialFailures(errmask.MultiSearchItems)
				require.Empty(t, gotMasked)
			},
		},
		{
			name: "MSearchResp aggregator fires both wrappers",
			path: "/_msearch",
			body: msearchBothFailuresBody,
			fetch: func(c *opensearchapi.Client, ctx context.Context) (any, error) {
				return c.MSearch(ctx, opensearchapi.MSearchReq{})
			},
			assertHelper: func(t *testing.T, resp any, mask errmask.ErrorMask) {
				r := resp.(*opensearchapi.MSearchResp)
				// Both per-wrapper helpers return non-nil.
				require.NotNil(t, r.SearchShardFailures())
				require.NotNil(t, r.MultiSearchItemFailures())

				// Aggregator returns both sub-errors when nothing is masked.
				got := r.PartialFailures(errmask.Empty)
				require.Len(t, got, 2)

				// Masking one bit drops just that sub-error.
				got = r.PartialFailures(errmask.SearchShards)
				require.Len(t, got, 1)
				require.IsType(t, &opensearchapi.MultiSearchItemError{}, got[0])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.All), map[string]string{tt.path: tt.body})
			resp, _ := tt.fetch(c, context.Background())
			tt.assertHelper(t, resp, errmask.All)
		})
	}
}

// TestNilRespHelperMethods confirms the per-wrapper helpers return nil
// (no panic) when called on a nil receiver. The collapse rule on the
// dispatch path normally prevents this, but library code may still
// exercise the helpers in defensive code paths.
func TestNilRespHelperMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func() any
	}{
		{"nil BulkResp", func() any { var r *opensearchapi.BulkResp; return r.BulkItemFailures() }},
		{"nil SearchResp", func() any { var r *opensearchapi.SearchResp; return r.SearchShardFailures() }},
		{"nil ScrollGetResp", func() any { var r *opensearchapi.ScrollGetResp; return r.SearchShardFailures() }},
		{"nil SearchTemplateResp", func() any { var r *opensearchapi.SearchTemplateResp; return r.SearchShardFailures() }},
		{"nil MSearchResp shard helper", func() any { var r *opensearchapi.MSearchResp; return r.SearchShardFailures() }},
		{"nil MSearchResp item helper", func() any { var r *opensearchapi.MSearchResp; return r.MultiSearchItemFailures() }},
		{"nil MSearchTemplateResp shard helper", func() any { var r *opensearchapi.MSearchTemplateResp; return r.SearchShardFailures() }},
		{"nil MSearchTemplateResp item helper", func() any { var r *opensearchapi.MSearchTemplateResp; return r.MultiSearchItemFailures() }},
		{"nil IndexResp", func() any { var r *opensearchapi.IndexResp; return r.WriteShardFailures() }},
		{"nil DocumentCreateResp", func() any { var r *opensearchapi.DocumentCreateResp; return r.WriteShardFailures() }},
		{"nil DocumentDeleteResp", func() any { var r *opensearchapi.DocumentDeleteResp; return r.WriteShardFailures() }},
		{"nil UpdateResp", func() any { var r *opensearchapi.UpdateResp; return r.WriteShardFailures() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.call()
			// Method returns *T; on nil receiver we want nil, no panic.
			// Use reflect-free check: typed nil pointer in interface.
			require.True(t, isTypedNil(got), "expected typed nil, got %T (%v)", got, got)
		})
	}
}

// TestPackageErrorsHelper covers the opensearchapi.Errors(err) package
// helper that flattens single- and multi-error returns into a uniform
// slice for caller-side switch/default dispatch.
func TestPackageErrorsHelper(t *testing.T) {
	t.Parallel()

	// Pre-build a real *MSearchErrors (multi-wrapper aggregate) by
	// dispatching against a body that fires both shard-aggregation and
	// per-item-error wrappers.
	c := newClient(t, ptrMask(errmask.Empty), map[string]string{"/_msearch": msearchBothFailuresBody})
	_, multiErr := c.MSearch(context.Background(), opensearchapi.MSearchReq{})
	require.Error(t, multiErr)
	var msErr *opensearchapi.MSearchErrors
	require.True(t, errors.As(multiErr, &msErr), "fixture must yield *MSearchErrors")

	tests := []struct {
		name    string
		err     error
		wantLen int
		// wantTypes is the expected concrete types of each element, in
		// the same order opensearchapi.Errors returns them.
		wantTypes []any
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
			// Concrete type is whatever errors.New returned; we only
			// care that it's wrapped as a single element here.
		},
		{
			name:    "single sub-error returns single-element slice",
			err:     &opensearchapi.PartialBulkError{},
			wantLen: 1,
			wantTypes: []any{
				&opensearchapi.PartialBulkError{},
			},
		},
		{
			name:    "MSearchErrors aggregate flattens into multi-element slice",
			err:     multiErr,
			wantLen: 2,
			wantTypes: []any{
				&opensearchapi.PartialSearchError{},
				&opensearchapi.MultiSearchItemError{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := opensearchapi.Errors(tt.err)
			require.Len(t, got, tt.wantLen)
			for i, want := range tt.wantTypes {
				require.IsType(t, want, got[i], "element %d", i)
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

// msearchBothFailuresBody combines shard failures (in sub-response 0)
// and a fully-failed sub-response (sub-response 1 with Error set), so
// MSearchResp's PartialFailures(mask=Empty) returns 2 sub-errors.
const msearchBothFailuresBody = `{
  "took": 1,
  "responses": [
    {"took": 1, "_shards": {"total": 5, "successful": 3, "failed": 2, "skipped": 0,
       "failures": [{"shard": 0, "index": "i", "node": "n", "reason": {"type": "x", "reason": "shard boom"}}]},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 200},
    {"took": 0, "_shards": {"total": 0, "successful": 0, "failed": 0, "skipped": 0},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 400,
      "error": {"type": "parsing_exception", "reason": "unknown query [bogus]"}}
  ]
}`
