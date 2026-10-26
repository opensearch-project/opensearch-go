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

// interceptBulk is the hook DocumentClient.Bulk calls first. It never answers
// the call itself: it returns the response the answer is decoded into, with room
// in Items for the actions the request carries when apiutil.WithBulkItems gave
// ctx their count, so that decoding the answer does not grow Items by copying,
// and nil otherwise.
//
//nolint:unparam // The generated dispatch calls every interceptor with this signature.
func (c *Client) interceptBulk(ctx context.Context, _ BulkReq) (*BulkResp, bool, error) {
	n := apiutil.BulkItems(ctx)
	if n <= 0 {
		return nil, false, nil
	}

	return &BulkResp{Items: make([]BulkItem, 0, n)}, false, nil
}
