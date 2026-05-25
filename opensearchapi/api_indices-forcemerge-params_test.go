// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

//nolint:testpackage // to test unexported get() method
package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndicesForcemergeParams_get(t *testing.T) {
	type fields struct {
		AllowNoIndices     *bool
		ExpandWildcards    string
		Flush              *bool
		IgnoreUnavailable  *bool
		MaxNumSegments     *int
		OnlyExpungeDeletes *bool
		WaitForCompletion  *bool
		Pretty             bool
		Human              bool
		ErrorTrace         bool
		FilterPath         []string
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string]string
	}{
		{
			name:   "empty params",
			fields: fields{},
			want:   map[string]string{},
		},
		{
			name: "all params set",
			fields: fields{
				AllowNoIndices:     ToPointer(true),
				ExpandWildcards:    "open,closed",
				Flush:              ToPointer(true),
				IgnoreUnavailable:  ToPointer(true),
				MaxNumSegments:     ToPointer(1),
				OnlyExpungeDeletes: ToPointer(true),
				WaitForCompletion:  ToPointer(true),
				Pretty:             true,
				Human:              true,
				ErrorTrace:         true,
				FilterPath:         []string{"_shards.total", "_shards.successful"},
			},
			want: map[string]string{
				"allow_no_indices":     "true",
				"expand_wildcards":     "open,closed",
				"flush":                "true",
				"ignore_unavailable":   "true",
				"max_num_segments":     "1",
				"only_expunge_deletes": "true",
				"wait_for_completion":  "true",
				"pretty":               "true",
				"human":                "true",
				"error_trace":          "true",
				"filter_path":          "_shards.total,_shards.successful",
			},
		},
		{
			name: "boolean params false",
			fields: fields{
				AllowNoIndices:     ToPointer(false),
				Flush:              ToPointer(false),
				IgnoreUnavailable:  ToPointer(false),
				OnlyExpungeDeletes: ToPointer(false),
				WaitForCompletion:  ToPointer(false),
			},
			want: map[string]string{
				"allow_no_indices":     "false",
				"flush":                "false",
				"ignore_unavailable":   "false",
				"only_expunge_deletes": "false",
				"wait_for_completion":  "false",
			},
		},
		{
			name: "async force-merge (wait_for_completion=false only)",
			fields: fields{
				WaitForCompletion: ToPointer(false),
			},
			want: map[string]string{
				"wait_for_completion": "false",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := IndicesForcemergeParams{
				AllowNoIndices:     tt.fields.AllowNoIndices,
				ExpandWildcards:    tt.fields.ExpandWildcards,
				Flush:              tt.fields.Flush,
				IgnoreUnavailable:  tt.fields.IgnoreUnavailable,
				MaxNumSegments:     tt.fields.MaxNumSegments,
				OnlyExpungeDeletes: tt.fields.OnlyExpungeDeletes,
				WaitForCompletion:  tt.fields.WaitForCompletion,
				Pretty:             tt.fields.Pretty,
				Human:              tt.fields.Human,
				ErrorTrace:         tt.fields.ErrorTrace,
				FilterPath:         tt.fields.FilterPath,
			}
			got := r.get()
			assert.Equal(t, tt.want, got)
		})
	}
}
