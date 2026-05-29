// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v4/internal/errmask"
)

func ptrMask(m errmask.ErrorMask) *errmask.ErrorMask { return &m }

func TestResolveErrorMask(t *testing.T) {
	tests := []struct {
		name string
		env  *string // nil = unset, non-nil = set to this value
		cfg  Config
		want errmask.ErrorMask
	}{
		{
			name: "v4 default nil config masks everything",
			env:  nil,
			cfg:  Config{},
			want: errmask.All,
		},
		{
			name: "explicit Empty pointer reports everything",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.Empty)},
			want: errmask.Empty,
		},
		{
			name: "explicit All pointer masks everything",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.All,
		},
		{
			name: "explicit single-bit mask honored",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems,
		},
		{
			name: "env adds bit on top of cfg base",
			env:  strPtr("+search_shards"),
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems | errmask.SearchShards,
		},
		{
			name: "env clears bit from cfg base",
			env:  strPtr("-bulk_items"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.All &^ errmask.BulkItems,
		},
		{
			name: "env empty token unmasks everything",
			env:  strPtr("empty"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.Empty,
		},
		{
			name: "env none alias unmasks everything",
			env:  strPtr("none"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.Empty,
		},
		{
			name: "env composite resets and sets",
			env:  strPtr("empty,+write_shards"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.WriteShards,
		},
		{
			name: "env unknown tokens silently dropped",
			env:  strPtr("garbage"),
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems,
		},
		{
			name: "env empty string falls through to base",
			env:  strPtr(""),
			cfg:  Config{Errors: ptrMask(errmask.SearchShards)},
			want: errmask.SearchShards,
		},
		{
			name: "pascal case rejected as unknown tokens",
			env:  strPtr("+BulkItems,+SearchShards"),
			cfg:  Config{Errors: ptrMask(errmask.Empty)},
			want: errmask.Empty, // both tokens fall through to unknown; mask unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != nil {
				t.Setenv(envvars.ErrorMask, *tt.env)
			}

			got := resolveErrorMask(tt.cfg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func strPtr(s string) *string { return &s }
