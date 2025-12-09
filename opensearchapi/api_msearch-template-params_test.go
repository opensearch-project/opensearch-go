// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

//nolint:testpackage // to test unexported get() method
package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMSearchTemplateParams_get(t *testing.T) {
	type fields struct {
		AllowNoIndices        *bool
		CcsMinimizeRoundtrips *bool
		ExpandWildcards       string
		IgnoreUnavailable     *bool
		MaxConcurrentSearches *int
		RestTotalHitsAsInt    *bool
		SearchType            string
		TypedKeys             *bool
		Pretty                bool
		Human                 bool
		ErrorTrace            bool
		FilterPath            []string
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
			name: "all params",
			fields: fields{
				AllowNoIndices:        ToPointer(true),
				CcsMinimizeRoundtrips: ToPointer(true),
				ExpandWildcards:       "open,hidden",
				IgnoreUnavailable:     ToPointer(true),
				MaxConcurrentSearches: ToPointer(10),
				RestTotalHitsAsInt:    ToPointer(true),
				SearchType:            "query_then_fetch",
				TypedKeys:             ToPointer(true),
				Pretty:                true,
				Human:                 true,
				ErrorTrace:            true,
				FilterPath:            []string{"took", "responses"},
			},
			want: map[string]string{
				"allow_no_indices":        "true",
				"ccs_minimize_roundtrips": "true",
				"expand_wildcards":        "open,hidden",
				"ignore_unavailable":      "true",
				"max_concurrent_searches": "10",
				"rest_total_hits_as_int":  "true",
				"search_type":             "query_then_fetch",
				"typed_keys":              "true",
				"pretty":                  "true",
				"human":                   "true",
				"error_trace":             "true",
				"filter_path":             "took,responses",
			},
		},
		{
			name: "ignore_unavailable false",
			fields: fields{
				IgnoreUnavailable: ToPointer(false),
				AllowNoIndices:    ToPointer(false),
			},
			want: map[string]string{
				"ignore_unavailable": "false",
				"allow_no_indices":   "false",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := MSearchTemplateParams{
				AllowNoIndices:        tt.fields.AllowNoIndices,
				CcsMinimizeRoundtrips: tt.fields.CcsMinimizeRoundtrips,
				ExpandWildcards:       tt.fields.ExpandWildcards,
				IgnoreUnavailable:     tt.fields.IgnoreUnavailable,
				MaxConcurrentSearches: tt.fields.MaxConcurrentSearches,
				RestTotalHitsAsInt:    tt.fields.RestTotalHitsAsInt,
				SearchType:            tt.fields.SearchType,
				TypedKeys:             tt.fields.TypedKeys,
				Pretty:                tt.fields.Pretty,
				Human:                 tt.fields.Human,
				ErrorTrace:            tt.fields.ErrorTrace,
				FilterPath:            tt.fields.FilterPath,
			}
			assert.Equalf(t, tt.want, r.get(), "get()")
		})
	}
}
