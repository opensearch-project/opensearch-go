// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import "github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"

// hop_v5_to_v5_2.go is the hand-authored v5 -> v5.2 migration data. v5.2 keeps
// the module path and regenerates types from a spec corrected against the server
// source, so the hop is all struct shapes: no regroups and no removed helpers.
// The surfaces and the guards in delta_test.go are the same as for a major hop.

const (
	v5asyncSearch = "github.com/opensearch-project/opensearch-go/v5/plugins/asynchronous_search"
	v5knn         = "github.com/opensearch-project/opensearch-go/v5/plugins/knn"
)

// hopV5toV5_2 is the complete v5 -> v5.2 transition, registered in hops (see
// transitions.go).
//
//nolint:gochecknoglobals,goconst // immutable data table; inline names read clearer
var hopV5toV5_2 = hop{
	From: version{major: 5},
	To:   version{major: 5, minor: 2},

	// The wrappers redeclared their base's fields; v5.2 resolves them to the base
	// (SearchCompletionSuggest) or to a struct embedding it (MLSearchRespHits
	// embeds SearchHitsMetadata, which carries total and max_score).
	TypeRenames: []apirev.TypeRename{
		{FromPkgPath: v5api, FromName: "SearchSuggestCompletion", ToPkgPath: v5api, ToName: "SearchCompletionSuggest"},
		{FromPkgPath: v5api, FromName: "SearchResultJSONValueSuggestValueItemCompletion", ToPkgPath: v5api, ToName: "SearchCompletionSuggest"},
		{FromPkgPath: v5api, FromName: "MLSearchHits", ToPkgPath: v5api, ToName: "MLSearchRespHits"},
	},

	SymbolRenames: []apirev.TypeRename{
		{FromPkgPath: v5api, FromName: "SearchTotalHitsRelation", ToPkgPath: v5api, ToName: "TotalHitsRelation"},
		{FromPkgPath: v5api, FromName: "SearchTotalHitsRelationEq", ToPkgPath: v5api, ToName: "TotalHitsRelationEq"},
		{FromPkgPath: v5api, FromName: "SearchTotalHitsRelationGte", ToPkgPath: v5api, ToName: "TotalHitsRelationGte"},
		{FromPkgPath: v5api, FromName: "SearchSuggestCompletionOptions", ToPkgPath: v5api, ToName: "SearchCompletionSuggestOptions"},
		{FromPkgPath: v5api, FromName: "SearchSuggestCompletionOptionsType", ToPkgPath: v5api, ToName: "SearchCompletionSuggestOptionsType"},
		{
			FromPkgPath: v5api, FromName: "SearchSuggestCompletionOptionsUnknownType",
			ToPkgPath: v5api, ToName: "SearchCompletionSuggestOptionsUnknownType",
		},
		{
			FromPkgPath: v5api, FromName: "SearchSuggestCompletionOptionsTextType",
			ToPkgPath: v5api, ToName: "SearchCompletionSuggestOptionsTextType",
		},
		{
			FromPkgPath: v5api, FromName: "SearchSuggestCompletionOptionsArrayType",
			ToPkgPath: v5api, ToName: "SearchCompletionSuggestOptionsArrayType",
		},
		{
			FromPkgPath: v5api, FromName: "NewSearchSuggestCompletionOptionsFromText",
			ToPkgPath: v5api, ToName: "NewSearchCompletionSuggestOptionsFromText",
		},
		{
			FromPkgPath: v5api, FromName: "NewSearchSuggestCompletionOptionsFromArray",
			ToPkgPath: v5api, ToName: "NewSearchCompletionSuggestOptionsFromArray",
		},
	},

	FieldDispositions: []apirev.FieldDisposition{
		// The server never sent a top-level took on an asynchronous search; the
		// search's own took is on Response.
		{FromPkgPath: v5api, FromType: "AsynchronousSearchRespBody", FromField: "Took", Action: apirev.ActionRemove},
		{FromPkgPath: v5asyncSearch, FromType: "GetResp", FromField: "Took", Action: apirev.ActionRemove},
		{FromPkgPath: v5asyncSearch, FromType: "SearchResp", FromField: "Took", Action: apirev.ActionRemove},
		// The server sends query_group_hashcode; query_hashcode never decoded.
		{
			FromPkgPath: v5api, FromType: "InsightsTopQuery", FromField: "QueryHashcode",
			Action: apirev.ActionRename, ToPkgPath: v5api, ToType: "InsightsTopQuery", ToField: "QueryGroupHashcode",
		},
		{
			FromPkgPath: v5api, FromType: "RollupsInterval", FromField: "Cron", Action: apirev.ActionManual,
			Note: "a cron schedule is no longer nested in the interval: " +
				"set RollupsSchedule.Cron (*RollupsCron) instead of RollupsSchedule.Interval.Cron",
		},
		{
			FromPkgPath: v5knn, FromType: "GetModelResp", FromField: "Body", Action: apirev.ActionManual,
			Note: "the model now decodes into typed fields (ModelID, State, Dimension, ...); read those instead of decoding Body",
		},
	},

	SemanticFollowups: []string{
		"search scores are float32 in v5.2, as the server computes them: SearchHit.Score and the phrase and term " +
			"suggest option scores; convert where a float64 is stored or compared",
		"the plugin responses' total hits relations use TotalHitsRelation instead of a string",
		"SearchTermSuggestOption.Freq is an int (the server's int32), no longer an int64",
		"SearchCompletionSuggest.Options is a value, not a pointer: drop nil checks on it",
		"histogram and date_histogram extended_bounds and hard_bounds are typed (CommonAggregationsExtendedBoundsDouble, " +
			"CommonAggregationsExtendedBoundsFieldDateMath) instead of raw JSON min/max",
		"ML and flow framework search hits embed SearchHit, so ID, Index, Score, SeqNo, Version and Sort ([]FieldValue) " +
			"come from it, set through the SearchHit field in a literal, and _source decodes into a typed struct " +
			"(MLSource, FlowFrameworkCommonGetResp, FlowFrameworkCommonSearchStateResponse) instead of raw JSON",
		"ML search responses hold hits as a *MLSearchRespHits: literals gain & automatically, " +
			"but a read of Hits into a value needs a dereference; Took is an int64, TimedOut a bool and Shards a value",
		"MLHitsTotal is gone: the ML hits total is the SearchHitsMetadataTotal union, read with TotalHits()",
		"flow framework user_outputs is a map of raw JSON values, created/updated/provisioned times are int64 epoch millis, " +
			"_shards counts are ints and hits.total is a TotalHitsRelation and an int64",
		"notifications webhook header_params values are strings, as the server stores them",
		"insights measurement numbers are float64, phase_latency_map is a map[string]int64, and timestamps and task IDs are int64",
		"security analytics finding scores are float64, and the alert and finding totals are ints",
		"asynchronous search stats counters and start/expiration times are int64; rollups interval period is an int " +
			"and start_time and schedule_delay are int64",
		"neural stat metadata values gain a Float64 branch, which renumbers NeuralStatMetadataValueStringType; " +
			"compare branch types by name, not by stored number",
	},
}
