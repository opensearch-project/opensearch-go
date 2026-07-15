// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/errmask"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// TestResolveErrorMask verifies the v4->v5 error-handling toggle end to end:
// how Config.Errors and the OPENSEARCH_GO_ERROR_MASK env var resolve to the
// effective mask. The v5 default flip (nil => Empty, report every category)
// is the behavioral change the osapifix v4->v5 hop flags as a semantic
// followup; this exercises the actual resolver those callers must migrate to.
func TestResolveErrorMask(t *testing.T) {
	tests := []struct {
		name string
		cfg  *errmask.ErrorMask
		env  string // "" means leave OPENSEARCH_GO_ERROR_MASK unset
		want errmask.ErrorMask
	}{
		{
			name: "nil is v5 default Empty (report every category)",
			cfg:  nil,
			want: errmask.Empty,
		},
		{
			name: "explicit All masks everything (v4-shaped silence)",
			cfg:  errmask.New(errmask.All),
			want: errmask.All,
		},
		{
			name: "explicit New() reports every category",
			cfg:  errmask.New(),
			want: errmask.Empty,
		},
		{
			name: "selective mask honored verbatim",
			cfg:  errmask.New(errmask.SearchShards | errmask.MultiSearchItems),
			want: errmask.SearchShards | errmask.MultiSearchItems,
		},
		{
			name: "env all overrides nil default",
			cfg:  nil,
			env:  "all",
			want: errmask.All,
		},
		{
			name: "env empty (canonical v5-default token) reports every category",
			cfg:  errmask.New(errmask.All),
			env:  "empty",
			want: errmask.Empty,
		},
		{
			name: "env none overrides explicit All",
			cfg:  errmask.New(errmask.All),
			env:  "none",
			want: errmask.Empty,
		},
		{
			name: "env +all,-bulk_items (release-note transition recipe)",
			cfg:  nil,
			env:  "+all,-bulk_items",
			want: errmask.All &^ errmask.BulkItems,
		},
		{
			name: "env +/- tokens layer onto the cfg base",
			cfg:  errmask.New(errmask.All),
			env:  "-bulk_items",
			want: errmask.All &^ errmask.BulkItems,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(envvars.ErrorMask, tt.env)
			}
			got := resolveErrorMask(Config{Errors: tt.cfg})
			require.Equal(t, tt.want, got)
		})
	}
}
