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

// seedBulk is the seeder DocumentClient.Bulk calls before it sends the request.
// It returns the response the answer is decoded into, with room in Items for the
// actions the request carries when apiutil.WithBulkItems gave ctx their count, so
// that decoding the answer does not grow Items by copying, and nil otherwise.
//
// The first unused element of Items marks the seed: its Index is &bulkSeedMark.
// The decoder takes the mark out before it decodes there, and unseed drops a
// seed nothing decoded into.
func (c *Client) seedBulk(ctx context.Context, _ BulkReq) *BulkResp {
	n := apiutil.BulkItems(ctx)
	if n <= 0 {
		return nil
	}
	items := make([]BulkItem, 0, n)
	items[:1][0].Index = &bulkSeedMark

	return &BulkResp{Items: items}
}

// bulkSeedMark is what the Index of a seed's first unused element points at; see
// seedBulk. Only its address is used.
//
//nolint:gochecknoglobals // An address to compare against, never written.
var bulkSeedMark BulkRespItem

// takeBulkSeed reports whether items, an empty Items, is a seed's, and clears its
// mark.
func takeBulkSeed(items []BulkItem) bool {
	if len(items) != 0 || cap(items) == 0 {
		return false
	}
	first := &items[:1][0]
	if first.Index != &bulkSeedMark {
		return false
	}
	*first = BulkItem{}

	return true
}

// unseed drops a seed the answer did not decode into, such as for a failed
// request or a body without items, so that Items is nil, as it is in a response
// that was not seeded.
func (r *BulkResp) unseed() {
	if takeBulkSeed(r.Items) {
		r.Items = nil
	}
}
