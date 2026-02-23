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

func TestIndicesValidateQueryParams_get(t *testing.T) {
	type fields struct {
		AllowNoIndices    *bool
		AllShards         *bool
		Analyzer          string
		AnalyzeWildcard   *bool
		DefaultOperator   string
		Df                string
		ExpandWildcards   string
		Explain           *bool
		IgnoreUnavailable *bool
		Lenient           *bool
		Query             string
		Rewrite           *bool
		Pretty            bool
		Human             bool
		ErrorTrace        bool
		FilterPath        []string
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
				AllowNoIndices:    ToPointer(true),
				AllShards:         ToPointer(true),
				Analyzer:          "standard",
				AnalyzeWildcard:   ToPointer(true),
				DefaultOperator:   "AND",
				Df:                "message",
				ExpandWildcards:   "open,closed",
				Explain:           ToPointer(true),
				IgnoreUnavailable: ToPointer(true),
				Lenient:           ToPointer(true),
				Query:             "test query",
				Rewrite:           ToPointer(true),
				Pretty:            true,
				Human:             true,
				ErrorTrace:        true,
				FilterPath:        []string{"hits", "took"},
			},
			want: map[string]string{
				"allow_no_indices":   "true",
				"all_shards":         "true",
				"analyzer":           "standard",
				"analyze_wildcard":   "true",
				"default_operator":   "AND",
				"df":                 "message",
				"expand_wildcards":   "open,closed",
				"explain":            "true",
				"ignore_unavailable": "true",
				"lenient":            "true",
				"q":                  "test query",
				"rewrite":            "true",
				"pretty":             "true",
				"human":              "true",
				"error_trace":        "true",
				"filter_path":        "hits,took",
			},
		},
		{
			name: "boolean params false",
			fields: fields{
				AllowNoIndices:    ToPointer(false),
				AllShards:         ToPointer(false),
				AnalyzeWildcard:   ToPointer(false),
				Explain:           ToPointer(false),
				IgnoreUnavailable: ToPointer(false),
				Lenient:           ToPointer(false),
				Rewrite:           ToPointer(false),
			},
			want: map[string]string{
				"allow_no_indices":   "false",
				"all_shards":         "false",
				"analyze_wildcard":   "false",
				"explain":            "false",
				"ignore_unavailable": "false",
				"lenient":            "false",
				"rewrite":            "false",
			},
		},
		{
			name: "only string params",
			fields: fields{
				Analyzer:        "whitespace",
				DefaultOperator: "OR",
				Df:              "content",
				ExpandWildcards: "all",
				Query:           "status:active",
			},
			want: map[string]string{
				"analyzer":         "whitespace",
				"default_operator": "OR",
				"df":               "content",
				"expand_wildcards": "all",
				"q":                "status:active",
			},
		},
		{
			name: "filter path with multiple values",
			fields: fields{
				FilterPath: []string{"valid", "explanations", "shards"},
			},
			want: map[string]string{
				"filter_path": "valid,explanations,shards",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := IndicesValidateQueryParams{
				AllowNoIndices:    tt.fields.AllowNoIndices,
				AllShards:         tt.fields.AllShards,
				Analyzer:          tt.fields.Analyzer,
				AnalyzeWildcard:   tt.fields.AnalyzeWildcard,
				DefaultOperator:   tt.fields.DefaultOperator,
				Df:                tt.fields.Df,
				ExpandWildcards:   tt.fields.ExpandWildcards,
				Explain:           tt.fields.Explain,
				IgnoreUnavailable: tt.fields.IgnoreUnavailable,
				Lenient:           tt.fields.Lenient,
				Query:             tt.fields.Query,
				Rewrite:           tt.fields.Rewrite,
				Pretty:            tt.fields.Pretty,
				Human:             tt.fields.Human,
				ErrorTrace:        tt.fields.ErrorTrace,
				FilterPath:        tt.fields.FilterPath,
			}
			got := r.get()
			assert.Equal(t, tt.want, got)
		})
	}
}
