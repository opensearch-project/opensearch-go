// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
)

// allocBulkResp is the response allocator DocumentClient.Bulk calls before it
// sends the request. It returns the response the answer is decoded into, with
// room in Items for the actions the request carries when apiutil.WithBulkItems
// gave ctx their count, so that decoding the answer does not grow Items by
// copying, and nil otherwise.
//
// The first unused element of Items marks the preallocated response: its Index
// is &bulkAllocMark. The decoder takes the mark out before it decodes there, and
// dropPrealloc drops a preallocated response nothing decoded into.
func (c *Client) allocBulkResp(ctx context.Context, _ BulkReq) *BulkResp {
	n := apiutil.BulkItems(ctx)
	if n <= 0 {
		return nil
	}
	items := make([]BulkItem, 0, n)
	items[:1][0].Index = &bulkAllocMark

	return &BulkResp{Items: items}
}

// bulkAllocMark is what the Index of a preallocated response's first unused
// element points at; see allocBulkResp. Only its address is used.
//
//nolint:gochecknoglobals // An address to compare against, never written.
var bulkAllocMark BulkRespItem

// takeBulkAllocMark reports whether items, an empty Items, is a preallocated
// response's, and clears its mark.
func takeBulkAllocMark(items []BulkItem) bool {
	if len(items) != 0 || cap(items) == 0 {
		return false
	}
	first := &items[:1][0]
	if first.Index != &bulkAllocMark {
		return false
	}
	*first = BulkItem{}

	return true
}

// dropPrealloc drops a preallocated response the answer did not decode into,
// such as for a failed request or a body without items, so that Items is nil, as
// it is in a response that was not preallocated.
func (r *BulkResp) dropPrealloc() {
	if takeBulkAllocMark(r.Items) {
		r.Items = nil
	}
}
