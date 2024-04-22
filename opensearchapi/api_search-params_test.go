// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.
//
//go:build !integration

//nolint:testpackage // to test unexported get() method
package opensearchapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSearchParams_get(t *testing.T) {
	type fields struct {
		AllowNoIndices             *bool
		AllowPartialSearchResults  *bool
		Analyzer                   string
		AnalyzeWildcard            *bool
		BatchedReduceSize          *int
		CcsMinimizeRoundtrips      *bool
		DefaultOperator            string
		Df                         string
		DocvalueFields             []string
		ExpandWildcards            string
		Explain                    *bool
		From                       *int
		IgnoreThrottled            *bool
		IgnoreUnavailable          *bool
		Lenient                    *bool
		MaxConcurrentShardRequests *int
		MinCompatibleShardNode     string
		Preference                 string
		PreFilterShardSize         *int
		Query                      string
		RequestCache               *bool
		RestTotalHitsAsInt         *bool
		Routing                    []string
		Scroll                     time.Duration
		SearchPipeline             string
		SearchType                 string
		SeqNoPrimaryTerm           *bool
		Size                       *int
		Sort                       []string
		Source                     interface{}
		SourceExcludes             []string
		SourceIncludes             []string
		Stats                      []string
		StoredFields               []string
		SuggestField               string
		SuggestMode                string
		SuggestSize                *int
		SuggestText                string
		TerminateAfter             *int
		Timeout                    time.Duration
		TrackScores                *bool
		TrackTotalHits             interface{}
		TypedKeys                  *bool
		Version                    *bool
		Pretty                     bool
		Human                      bool
		ErrorTrace                 bool
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string]string
	}{
		{
			name:   "Not specifying any parameter should result in an empty parameter map",
			fields: fields{},
			want:   map[string]string{},
		},
		{
			name: "Test search params are assigned and in correct format",
			fields: fields{
				AllowNoIndices:             ToPointer(true),
				AllowPartialSearchResults:  ToPointer(true),
				AnalyzeWildcard:            ToPointer(true),
				Analyzer:                   "default",
				BatchedReduceSize:          ToPointer(30),
				CcsMinimizeRoundtrips:      ToPointer(true),
				DefaultOperator:            "OR",
				Df:                         "cake",
				DocvalueFields:             []string{"title", "date"},
				ErrorTrace:                 false,
				ExpandWildcards:            "open,hidden",
				Explain:                    ToPointer(true),
				From:                       ToPointer(30),
				Human:                      false,
				IgnoreThrottled:            ToPointer(true),
				IgnoreUnavailable:          ToPointer(true),
				Lenient:                    ToPointer(true),
				MaxConcurrentShardRequests: ToPointer(100),
				MinCompatibleShardNode:     "one",
				PreFilterShardSize:         ToPointer(128),
				Preference:                 "_local",
				Pretty:                     false,
				Query:                      "{ \"match_all\": {} }",
				RequestCache:               ToPointer(true),
				RestTotalHitsAsInt:         ToPointer(true),
				Routing:                    []string{"route1", "route2"},
				Scroll:                     10 * time.Second,
				SearchPipeline:             "balanced",
				SearchType:                 "query_then_fetch",
				SeqNoPrimaryTerm:           ToPointer(true),
				Size:                       ToPointer(100),
				Sort:                       []string{"title:asc", "date:desc"},
				Source:                     []string{"title", "date"},
				SourceExcludes:             []string{"description"},
				SourceIncludes:             []string{"image"},
				Stats:                      []string{"fielddata"},
				StoredFields:               []string{"embedding"},
				SuggestField:               "title",
				SuggestMode:                "missing",
				SuggestSize:                ToPointer(10),
				SuggestText:                "title",
				TerminateAfter:             ToPointer(30),
				Timeout:                    30 * time.Second,
				TrackScores:                ToPointer(true),
				TrackTotalHits:             1000,
				TypedKeys:                  ToPointer(true),
				Version:                    ToPointer(true),
			},
			want: map[string]string{
				"_source":                       "title,date",
				"_source_excludes":              "description",
				"_source_includes":              "image",
				"allow_no_indices":              "true",
				"allow_partial_search_results":  "true",
				"analyze_wildcard":              "true",
				"analyzer":                      "default",
				"batched_reduce_size":           "30",
				"ccs_minimize_roundtrips":       "true",
				"default_operator":              "OR",
				"df":                            "cake",
				"docvalue_fields":               "title,date",
				"expand_wildcards":              "open,hidden",
				"explain":                       "true",
				"from":                          "30",
				"ignore_throttled":              "true",
				"ignore_unavailable":            "true",
				"lenient":                       "true",
				"max_concurrent_shard_requests": "100",
				"min_compatible_shard_node":     "one",
				"pre_filter_shard_size":         "128",
				"preference":                    "_local",
				"q":                             "{ \"match_all\": {} }",
				"request_cache":                 "true",
				"rest_total_hits_as_int":        "true",
				"routing":                       "route1,route2",
				"scroll":                        "10000ms",
				"search_pipeline":               "balanced",
				"search_type":                   "query_then_fetch",
				"seq_no_primary_term":           "true",
				"size":                          "100",
				"sort":                          "title:asc,date:desc",
				"stats":                         "fielddata",
				"stored_fields":                 "embedding",
				"suggest_field":                 "title",
				"suggest_mode":                  "missing",
				"suggest_size":                  "10",
				"suggest_text":                  "title",
				"terminate_after":               "30",
				"timeout":                       "30000ms",
				"track_scores":                  "true",
				"track_total_hits":              "1000",
				"typed_keys":                    "true",
				"version":                       "true",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := SearchParams{
				AllowNoIndices:             tt.fields.AllowNoIndices,
				AllowPartialSearchResults:  tt.fields.AllowPartialSearchResults,
				Analyzer:                   tt.fields.Analyzer,
				AnalyzeWildcard:            tt.fields.AnalyzeWildcard,
				BatchedReduceSize:          tt.fields.BatchedReduceSize,
				CcsMinimizeRoundtrips:      tt.fields.CcsMinimizeRoundtrips,
				DefaultOperator:            tt.fields.DefaultOperator,
				Df:                         tt.fields.Df,
				DocvalueFields:             tt.fields.DocvalueFields,
				ExpandWildcards:            tt.fields.ExpandWildcards,
				Explain:                    tt.fields.Explain,
				From:                       tt.fields.From,
				IgnoreThrottled:            tt.fields.IgnoreThrottled,
				IgnoreUnavailable:          tt.fields.IgnoreUnavailable,
				Lenient:                    tt.fields.Lenient,
				MaxConcurrentShardRequests: tt.fields.MaxConcurrentShardRequests,
				MinCompatibleShardNode:     tt.fields.MinCompatibleShardNode,
				Preference:                 tt.fields.Preference,
				PreFilterShardSize:         tt.fields.PreFilterShardSize,
				Query:                      tt.fields.Query,
				RequestCache:               tt.fields.RequestCache,
				RestTotalHitsAsInt:         tt.fields.RestTotalHitsAsInt,
				Routing:                    tt.fields.Routing,
				Scroll:                     tt.fields.Scroll,
				SearchPipeline:             tt.fields.SearchPipeline,
				SearchType:                 tt.fields.SearchType,
				SeqNoPrimaryTerm:           tt.fields.SeqNoPrimaryTerm,
				Size:                       tt.fields.Size,
				Sort:                       tt.fields.Sort,
				Source:                     tt.fields.Source,
				SourceExcludes:             tt.fields.SourceExcludes,
				SourceIncludes:             tt.fields.SourceIncludes,
				Stats:                      tt.fields.Stats,
				StoredFields:               tt.fields.StoredFields,
				SuggestField:               tt.fields.SuggestField,
				SuggestMode:                tt.fields.SuggestMode,
				SuggestSize:                tt.fields.SuggestSize,
				SuggestText:                tt.fields.SuggestText,
				TerminateAfter:             tt.fields.TerminateAfter,
				Timeout:                    tt.fields.Timeout,
				TrackScores:                tt.fields.TrackScores,
				TrackTotalHits:             tt.fields.TrackTotalHits,
				TypedKeys:                  tt.fields.TypedKeys,
				Version:                    tt.fields.Version,
				Pretty:                     tt.fields.Pretty,
				Human:                      tt.fields.Human,
				ErrorTrace:                 tt.fields.ErrorTrace,
			}
			assert.Equalf(t, tt.want, r.get(), "get()")
		})
	}
}
