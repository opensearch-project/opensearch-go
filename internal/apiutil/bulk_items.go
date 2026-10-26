// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil

import "context"

// bulkItemsKey is the context key for WithBulkItems.
type bulkItemsKey struct{}

// WithBulkItems returns ctx saying that the bulk request sent with it carries n
// actions, so that its response is decoded into Items sized for them. The bulk
// indexer, which knows the count, says it this way because the generated
// DocumentClient.Bulk takes nothing else that could carry it.
func WithBulkItems(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, bulkItemsKey{}, n)
}

// BulkItems returns the count WithBulkItems gave ctx, or 0.
func BulkItems(ctx context.Context) int {
	n, _ := ctx.Value(bulkItemsKey{}).(int)

	return n
}
