// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The server rejects refresh and wait_for_active_shards in an action line, so the
// bulk client writes neither, and does not count them when it sizes an item.
func TestBulkIndexerItemDeprecatedFieldsAreNotSent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item BulkIndexerItem
		want string
	}{
		{
			name: "refresh",
			item: BulkIndexerItem{Action: "index", DocumentID: "1", Refresh: new("wait_for")},
			want: `{"index":{"_id":"1"}}` + "\n",
		},
		{
			name: "wait_for_active_shards",
			item: BulkIndexerItem{Action: "index", DocumentID: "1", WaitForActiveShards: "all"},
			want: `{"index":{"_id":"1"}}` + "\n",
		},
		{
			name: "both, with a non-ASCII routing the encoder writes",
			item: BulkIndexerItem{
				Action: "create", DocumentID: "1", Routing: new("é"), Refresh: new("true"), WaitForActiveShards: 2,
			},
			want: `{"create":{"_id":"1","routing":"é"}}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newMetaEncoder().(*metaEncoder)
			line, err := e.encode(&tt.item)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(line))
		})
	}
}

func TestEstimateItemBytesIgnoresDeprecatedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item BulkIndexerItem
	}{
		{name: "refresh", item: BulkIndexerItem{Action: "index", DocumentID: "1", Refresh: new("wait_for")}},
		{name: "wait_for_active_shards", item: BulkIndexerItem{Action: "index", DocumentID: "1", WaitForActiveShards: "all"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plain := BulkIndexerItem{Action: tt.item.Action, DocumentID: tt.item.DocumentID}
			require.Equal(t, estimateItemBytesWithBody(plain, 0), estimateItemBytesWithBody(tt.item, 0))
		})
	}
}
