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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/errmask"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

// stubTransport returns a RoundTripFunc that maps URL.Path to a JSON
// body. Anything else 404s.
func stubTransport(t *testing.T, byPath map[string]string) http.RoundTripper {
	t.Helper()
	return mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		body, ok := byPath[req.URL.Path]
		if !ok {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"error":"unmapped path"}`)),
			}, nil
		}
		hdr := http.Header{}
		hdr.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     hdr,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
}

// newClient builds a client with the given mask. nil mask uses the
// version's default; a non-nil mask is honored verbatim.
func newClient(t *testing.T, mask *errmask.ErrorMask, byPath map[string]string) *opensearchapi.Client {
	t.Helper()
	c, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
			Transport: stubTransport(t, byPath),
		},
		Errors: mask,
	})
	require.NoError(t, err)
	return c
}

func ptrMask(m errmask.ErrorMask) *errmask.ErrorMask { return &m }

// ---------------------------------------------------------------------------
// Stubbed response bodies (one per partial-failure shape).
// ---------------------------------------------------------------------------

const bulkPartialBody = `{
  "took": 1,
  "errors": true,
  "items": [
    {"index": {"_index": "i", "_id": "1", "status": 201}},
    {"index": {"_index": "i", "_id": "2", "status": 400, "error": {"type": "mapper_parsing_exception", "reason": "boom"}}}
  ]
}`

const searchShardFailureBody = `{
  "took": 5,
  "timed_out": false,
  "_shards": {"total": 5, "successful": 3, "skipped": 0, "failed": 2,
    "failures": [{"shard": 0, "index": "i", "node": "n", "reason": {"type": "x", "reason": "boom"}}]
  },
  "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}
}`

const writeShardFailureBody = `{
  "_index": "i", "_id": "1", "_version": 1, "result": "created",
  "_shards": {"total": 2, "successful": 1, "failed": 1},
  "_seq_no": 0, "_primary_term": 1
}`

const msearchShardFailureBody = `{
  "took": 1,
  "responses": [
    {"took": 1, "_shards": {"total": 5, "successful": 5, "failed": 0, "skipped": 0},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 200},
    {"took": 1, "_shards": {"total": 5, "successful": 3, "failed": 2, "skipped": 0,
       "failures": [{"shard": 0, "index": "i", "node": "n", "reason": {"type": "x", "reason": "boom"}}]},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 200}
  ]
}`

const msearchSubQueryErrorBody = `{
  "took": 1,
  "responses": [
    {"took": 1, "_shards": {"total": 5, "successful": 5, "failed": 0, "skipped": 0},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 200},
    {"took": 0, "_shards": {"total": 0, "successful": 0, "failed": 0, "skipped": 0},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 400,
      "error": {"type": "parsing_exception", "reason": "unknown query [bogus]"}}
  ]
}`

// ---------------------------------------------------------------------------
// Single-op dispatch table.
//
// Covers operations whose partial-failure detection produces exactly one
// error type per dispatch. Each row supplies the path being mocked, the
// body served on that path, the dispatch closure, the bit that masks
// the error, and the assertion that runs on a successfully-detected
// error.
// ---------------------------------------------------------------------------

type singleOpCase struct {
	name     string
	path     string
	body     string
	maskBit  errmask.ErrorMask
	dispatch func(context.Context, *opensearchapi.Client) (any, error)
	assert   func(t *testing.T, err error)
}

func TestSingleOpDispatch(t *testing.T) {
	t.Parallel()

	cases := []singleOpCase{
		{
			name:    "Bulk -> PartialBulkError",
			path:    "/_bulk",
			body:    bulkPartialBody,
			maskBit: errmask.BulkItems,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Bulk(ctx, opensearchapi.BulkReq{
					Body: strings.NewReader(`{"index":{"_index":"i","_id":"1"}}` + "\n" +
						`{"x":1}` + "\n"),
				})
			},
			assert: func(t *testing.T, err error) {
				var bErr *opensearchapi.PartialBulkError
				require.True(t, errors.As(err, &bErr), "expected PartialBulkError, got %T: %v", err, err)
				require.Len(t, bErr.FailedItems, 1)
				require.Equal(t, 1, bErr.SucceededCount)
			},
		},
		{
			name:    "Search -> PartialSearchError",
			path:    "/_search",
			body:    searchShardFailureBody,
			maskBit: errmask.SearchShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Search(ctx, nil)
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.PartialSearchError
				require.True(t, errors.As(err, &sErr), "expected PartialSearchError, got %T: %v", err, err)
				require.Equal(t, 2, sErr.FailedShards)
				require.Equal(t, 5, sErr.TotalShards)
			},
		},
		{
			name:    "Scroll.Get -> PartialSearchError",
			path:    "/_search/scroll",
			body:    searchShardFailureBody,
			maskBit: errmask.SearchShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Scroll.Get(ctx, opensearchapi.ScrollGetReq{ScrollID: "abc"})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.PartialSearchError
				require.True(t, errors.As(err, &sErr), "expected PartialSearchError, got %T: %v", err, err)
				require.Equal(t, 2, sErr.FailedShards)
			},
		},
		{
			name:    "SearchTemplate -> PartialSearchError",
			path:    "/_search/template",
			body:    searchShardFailureBody,
			maskBit: errmask.SearchShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.SearchTemplate(ctx, opensearchapi.SearchTemplateReq{
					Body: strings.NewReader(`{"id":"q","params":{}}`),
				})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.PartialSearchError
				require.True(t, errors.As(err, &sErr), "expected PartialSearchError, got %T: %v", err, err)
				require.Equal(t, 2, sErr.FailedShards)
			},
		},
		{
			name:    "Index -> ShardFailureError",
			path:    "/i/_doc/1",
			body:    writeShardFailureBody,
			maskBit: errmask.WriteShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Index(ctx, opensearchapi.IndexReq{
					Index:      "i",
					DocumentID: "1",
					Body:       strings.NewReader(`{"x":1}`),
				})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.ShardFailureError
				require.True(t, errors.As(err, &sErr), "expected ShardFailureError, got %T: %v", err, err)
				require.Equal(t, opensearchapi.OperationIndex, sErr.Operation)
				require.Equal(t, 1, sErr.FailedShards)
			},
		},
		{
			name:    "Document.Create -> ShardFailureError",
			path:    "/i/_create/1",
			body:    writeShardFailureBody,
			maskBit: errmask.WriteShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Document.Create(ctx, opensearchapi.DocumentCreateReq{
					Index:      "i",
					DocumentID: "1",
					Body:       strings.NewReader(`{"x":1}`),
				})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.ShardFailureError
				require.True(t, errors.As(err, &sErr), "expected ShardFailureError, got %T: %v", err, err)
				require.Equal(t, opensearchapi.OperationCreate, sErr.Operation)
			},
		},
		{
			name:    "Document.Delete -> ShardFailureError",
			path:    "/i/_doc/1",
			body:    writeShardFailureBody,
			maskBit: errmask.WriteShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{
					Index:      "i",
					DocumentID: "1",
				})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.ShardFailureError
				require.True(t, errors.As(err, &sErr), "expected ShardFailureError, got %T: %v", err, err)
				require.Equal(t, opensearchapi.OperationDelete, sErr.Operation)
			},
		},
		{
			name:    "Update -> ShardFailureError",
			path:    "/i/_update/1",
			body:    writeShardFailureBody,
			maskBit: errmask.WriteShards,
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.Update(ctx, opensearchapi.UpdateReq{
					Index:      "i",
					DocumentID: "1",
					Body:       strings.NewReader(`{"doc":{"x":1}}`),
				})
			},
			assert: func(t *testing.T, err error) {
				var sErr *opensearchapi.ShardFailureError
				require.True(t, errors.As(err, &sErr), "expected ShardFailureError, got %T: %v", err, err)
				require.Equal(t, opensearchapi.OperationUpdate, sErr.Operation)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/reported", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.Empty), map[string]string{tc.path: tc.body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NotNil(t, resp)
			tc.assert(t, err)
		})

		t.Run(tc.name+"/masked", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(tc.maskBit), map[string]string{tc.path: tc.body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NoError(t, err)
			require.NotNil(t, resp)
		})
	}
}

// ---------------------------------------------------------------------------
// MSearch shard-aggregation table.
//
// MSearch / MSearchTemplate aggregate _shards.failed across responses
// and surface them as PartialSearchError. This branch is gated on
// errmask.SearchShards (the per-shard envelope semantics), distinct
// from the per-sub-response Error inspection below.
// ---------------------------------------------------------------------------

type msearchShardCase struct {
	name     string
	path     string
	dispatch func(context.Context, *opensearchapi.Client) (any, error)
}

func TestMSearchShardAggregation(t *testing.T) {
	t.Parallel()

	body := msearchShardFailureBody
	cases := []msearchShardCase{
		{
			name: "MSearch",
			path: "/_msearch",
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.MSearch(ctx, opensearchapi.MSearchReq{
					Body: strings.NewReader(`{}` + "\n" + `{"query":{"match_all":{}}}` + "\n"),
				})
			},
		},
		{
			name: "MSearchTemplate",
			path: "/_msearch/template",
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.MSearchTemplate(ctx, opensearchapi.MSearchTemplateReq{
					Body: strings.NewReader(`{}` + "\n" + `{"id":"q","params":{}}` + "\n"),
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/reported", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.Empty), map[string]string{tc.path: body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NotNil(t, resp)
			var sErr *opensearchapi.PartialSearchError
			require.True(t, errors.As(err, &sErr), "expected PartialSearchError, got %T: %v", err, err)
			require.Equal(t, 2, sErr.FailedShards)
			require.Equal(t, 10, sErr.TotalShards)
		})

		t.Run(tc.name+"/masked", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.SearchShards), map[string]string{tc.path: body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NoError(t, err)
			require.NotNil(t, resp)
		})
	}
}

// ---------------------------------------------------------------------------
// MSearch per-sub-response Error inspection table.
//
// MSearch / MSearchTemplate also surface per-sub-response Error objects
// when a query fails before it can execute (parse error, etc.). This
// branch is gated on errmask.MultiSearchItems and produces
// MultiSearchItemError. SearchShards is masked in these test rows so
// only the per-item branch is exercised.
// ---------------------------------------------------------------------------

func TestMSearchPerItemError(t *testing.T) {
	t.Parallel()

	body := msearchSubQueryErrorBody
	cases := []msearchShardCase{
		{
			name: "MSearch",
			path: "/_msearch",
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.MSearch(ctx, opensearchapi.MSearchReq{
					Body: strings.NewReader(`{}` + "\n" + `{"query":{"match_all":{}}}` + "\n"),
				})
			},
		},
		{
			name: "MSearchTemplate",
			path: "/_msearch/template",
			dispatch: func(ctx context.Context, c *opensearchapi.Client) (any, error) {
				return c.MSearchTemplate(ctx, opensearchapi.MSearchTemplateReq{
					Body: strings.NewReader(`{}` + "\n" + `{"id":"q","params":{}}` + "\n"),
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/reported", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.SearchShards), map[string]string{tc.path: body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NotNil(t, resp)
			var iErr *opensearchapi.MultiSearchItemError
			require.True(t, errors.As(err, &iErr), "expected MultiSearchItemError, got %T: %v", err, err)
			require.Len(t, iErr.Items, 1)
			require.Equal(t, 1, iErr.SucceededCount)
			require.Equal(t, 1, iErr.Items[0].Index)
			require.Equal(t, 400, iErr.Items[0].Status)
			require.NotNil(t, iErr.Items[0].Error)
			require.Equal(t, "parsing_exception", iErr.Items[0].Error.Type)
		})

		t.Run(tc.name+"/masked", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(errmask.SearchShards|errmask.MultiSearchItems), map[string]string{tc.path: body})
			resp, err := tc.dispatch(context.Background(), c)
			require.NoError(t, err)
			require.NotNil(t, resp)
		})
	}
}

// ---------------------------------------------------------------------------
// MSearch multi-error collapse rule.
//
// When BOTH SearchShards aggregation and MultiSearchItems detection fire
// on the same response, the dispatch wraps both sub-errors in
// *MSearchErrors / *MSearchTemplateErrors. Single-sub-error responses
// return the bare sub-error (no wrapper). Caller errors.As against
// either the per-op type or a concrete sub-error works in both cases.
// ---------------------------------------------------------------------------

const msearchBothFailureBody = `{
  "took": 1,
  "responses": [
    {"took": 1, "_shards": {"total": 5, "successful": 3, "failed": 2, "skipped": 0,
       "failures": [{"shard": 0, "index": "i", "node": "n", "reason": {"type": "x", "reason": "boom"}}]},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 200},
    {"took": 0, "_shards": {"total": 0, "successful": 0, "failed": 0, "skipped": 0},
      "hits": {"total": {"value": 0, "relation": "eq"}, "max_score": null, "hits": []}, "timed_out": false, "status": 400,
      "error": {"type": "parsing_exception", "reason": "unknown query [bogus]"}}
  ]
}`

func TestMSearchMultiErrorCollapse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		mask              errmask.ErrorMask
		wantPerOpWrap     bool // true expects *MSearchErrors wrap
		wantPartialSearch bool // true expects PartialSearchError reachable via errors.As
		wantMultiItem     bool // true expects MultiSearchItemError reachable via errors.As
		wantNilErr        bool
		wantUnwrapLen     int // expected len(MSearchErrors.Unwrap()); 0 means N/A
	}{
		{
			name:              "both fire -> wrapped in MSearchErrors",
			mask:              errmask.Empty,
			wantPerOpWrap:     true,
			wantPartialSearch: true,
			wantMultiItem:     true,
			wantUnwrapLen:     2,
		},
		{
			name:              "only SearchShards fires -> bare *PartialSearchError",
			mask:              errmask.MultiSearchItems,
			wantPerOpWrap:     false,
			wantPartialSearch: true,
			wantMultiItem:     false,
		},
		{
			name:              "only MultiSearchItems fires -> bare *MultiSearchItemError",
			mask:              errmask.SearchShards,
			wantPerOpWrap:     false,
			wantPartialSearch: false,
			wantMultiItem:     true,
		},
		{
			name:       "both masked -> nil err",
			mask:       errmask.SearchShards | errmask.MultiSearchItems,
			wantNilErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, ptrMask(tc.mask), map[string]string{"/_msearch": msearchBothFailureBody})
			_, err := c.MSearch(context.Background(), opensearchapi.MSearchReq{
				Body: strings.NewReader(`{}` + "\n" + `{"query":{"match_all":{}}}` + "\n"),
			})

			if tc.wantNilErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)

			var mErrs *opensearchapi.MSearchErrors
			gotPerOp := errors.As(err, &mErrs)
			require.Equal(t, tc.wantPerOpWrap, gotPerOp,
				"per-op wrap presence: got %v want %v (err type %T)", gotPerOp, tc.wantPerOpWrap, err)
			if tc.wantUnwrapLen > 0 {
				require.Len(t, mErrs.Unwrap(), tc.wantUnwrapLen)
			}

			var pSearch *opensearchapi.PartialSearchError
			require.Equal(t, tc.wantPartialSearch, errors.As(err, &pSearch),
				"PartialSearchError reachable via errors.As")

			var mItem *opensearchapi.MultiSearchItemError
			require.Equal(t, tc.wantMultiItem, errors.As(err, &mItem),
				"MultiSearchItemError reachable via errors.As")
		})
	}
}
