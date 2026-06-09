// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package errwrap is the cmd/osgen-side catalog of partial-failure
// "wrapper schemas" (per the proposed x-error-responses extension --
// see opensearch-api-specification/issue-x-partial-failure-mode.md).
//
// Until the spec carries the extension natively, this package ships the
// per-operation map cmd/osgen needs at codegen time. Wrapper names match
// the catalog 1:1; the bit positions assigned by [github.com/opensearch-project/opensearch-go/v5/errmask]
// are kept in lock-step with the order of [Wrappers].
package errwrap

import "sort"

// Wrapper names. One constant per wrapper schema in the proposal's
// catalog. These strings are also accepted by errmask.Parse (in
// PascalCase) and by cmd/osgen as the hardcoded keys below.
const (
	WrapperBulkItems                   = "BulkItems"
	WrapperSearchShards                = "SearchShards"
	WrapperWriteShards                 = "WriteShards"
	WrapperBroadcastShards             = "BroadcastShards"
	WrapperNodeFailures                = "NodeFailures"
	WrapperBulkByScrollFailures        = "BulkByScrollFailures"
	WrapperTaskFailures                = "TaskFailures"
	WrapperMultiSearchItems            = "MultiSearchItems"
	WrapperMultiDocItems               = "MultiDocItems"
	WrapperSnapshotCreateShardFailures = "SnapshotCreateShardFailures"
	WrapperSnapshotGetShardFailures    = "SnapshotGetShardFailures"
	WrapperSimulateDocFailures         = "SimulateDocFailures"
	WrapperRankEvalFailures            = "RankEvalFailures"
	WrapperIngestionShardFailures      = "IngestionShardFailures"
	WrapperPitNodeFailures             = "PitNodeFailures"
)

// Identifiers used by the codegen to populate
// ShardFailureError.Operation in opensearchapi. These match
// the OperationXxx const values declared in the package's hand-written
// errors.go; cmd/osgen carries duplicates because it is a separate Go
// module and cannot import the opensearchapi package directly.
const (
	WriteOpIndex  = "OperationIndex"
	WriteOpCreate = "OperationCreate"
	WriteOpUpdate = "OperationUpdate"
	WriteOpDelete = "OperationDelete"
)

// Group names used as keys in [OperationWrappers]. Mirrors the
// x-operation-group tags in the OpenAPI spec; centralized so the
// dispatch template and the catalog stay in sync.
const (
	GroupBulk                        = "bulk"
	GroupBulkStream                  = "bulk_stream"
	GroupSearch                      = "search"
	GroupScroll                      = "scroll"
	GroupSearchTemplate              = "search_template"
	GroupCreatePIT                   = "create_pit"
	GroupCount                       = "count"
	GroupIndex                       = "index"
	GroupCreate                      = "create"
	GroupUpdate                      = "update"
	GroupDelete                      = "delete"
	GroupReindex                     = "reindex"
	GroupUpdateByQuery               = "update_by_query"
	GroupDeleteByQuery               = "delete_by_query"
	GroupMSearch                     = "msearch"
	GroupMSearchTemplate             = "msearch_template"
	GroupMGet                        = "mget"
	GroupMTermvectors                = "mtermvectors"
	GroupRankEval                    = "rank_eval"
	GroupGetAllPITs                  = "get_all_pits"
	GroupIndicesRefresh              = "indices.refresh"
	GroupIndicesFlush                = "indices.flush"
	GroupIndicesForceMerge           = "indices.forcemerge"
	GroupIndicesClearCache           = "indices.clear_cache"
	GroupIndicesValidateQuery        = "indices.validate_query"
	GroupIndicesSegments             = "indices.segments"
	GroupIndicesStats                = "indices.stats"
	GroupIndicesUpgrade              = "indices.upgrade"
	GroupIndicesDataStreamsStats     = "indices.data_streams_stats"
	GroupClusterStats                = "cluster.stats"
	GroupNodesInfo                   = "nodes.info"
	GroupNodesStats                  = "nodes.stats"
	GroupNodesUsage                  = "nodes.usage"
	GroupNodesReloadSecureSettings   = "nodes.reload_secure_settings"
	GroupDanglingIndicesList         = "dangling_indices.list_dangling_indices"
	GroupTasksList                   = "tasks.list"
	GroupTasksCancel                 = "tasks.cancel"
	GroupSnapshotCreate              = "snapshot.create"
	GroupSnapshotGet                 = "snapshot.get"
	GroupIngestSimulate              = "ingest.simulate"
	GroupIngestionGetState           = "ingestion.get_state"
	GroupIngestionPause              = "ingestion.pause"
	GroupIngestionResume             = "ingestion.resume"
	GroupAsynchronousSearchSearch    = "asynchronous_search.search"
	GroupAsynchronousSearchGet       = "asynchronous_search.get"
	GroupSearchRelevanceGetNodeStats = "search_relevance.get_node_stats"
	GroupSearchRelevanceGetStats     = "search_relevance.get_stats"
)

// Wrappers returns the canonical order of wrapper schemas. Parallels the bit
// positions in errmask so a Wrappers index N corresponds to errmask bit
// 1<<N. Returning a fresh slice (rather than exporting a package var) keeps
// the catalog immutable from a caller's perspective.
func Wrappers() []string {
	return []string{
		WrapperBulkItems,
		WrapperSearchShards,
		WrapperWriteShards,
		WrapperBroadcastShards,
		WrapperNodeFailures,
		WrapperBulkByScrollFailures,
		WrapperTaskFailures,
		WrapperMultiSearchItems,
		WrapperMultiDocItems,
		WrapperSnapshotCreateShardFailures,
		WrapperSnapshotGetShardFailures,
		WrapperSimulateDocFailures,
		WrapperRankEvalFailures,
		WrapperIngestionShardFailures,
		WrapperPitNodeFailures,
	}
}

// OperationWrappers returns the hardcoded per-operation map:
// x-operation-group (the spec's "Group" string -- see ir.Operation.Group) ->
// wrapper-schema names that operation may surface.
//
// Every entry in this map is what would otherwise be the
// `x-error-responses` array on the upstream operation. When the spec
// gains a native extension, swap this lookup for one driven by the
// parsed extension; the code in cmd/osgen/api_extract.go is the only
// caller. Returning a fresh map (rather than exporting a package var) keeps
// the catalog immutable from a caller's perspective.
func OperationWrappers() map[string][]string {
	return map[string][]string{
		// _core
		// NOTE: bulk and bulk_stream also surface WriteShards (per-item replica
		// failures) per the spec proposal, but the wire shape lives inside
		// items[].error rather than at top-level _shards -- emit a dedicated
		// handler when we add per-item write inspection. Until then the
		// catalog records the wrapper so the user-facing bit is reserved.
		GroupBulk:            {WrapperBulkItems},
		GroupBulkStream:      {WrapperBulkItems},
		GroupSearch:          {WrapperSearchShards},
		GroupScroll:          {WrapperSearchShards},
		GroupSearchTemplate:  {WrapperSearchShards},
		GroupCreatePIT:       {WrapperSearchShards},
		GroupCount:           {WrapperSearchShards},
		GroupIndex:           {WrapperWriteShards},
		GroupCreate:          {WrapperWriteShards},
		GroupUpdate:          {WrapperWriteShards},
		GroupDelete:          {WrapperWriteShards},
		GroupReindex:         {WrapperBulkByScrollFailures},
		GroupUpdateByQuery:   {WrapperBulkByScrollFailures},
		GroupDeleteByQuery:   {WrapperBulkByScrollFailures},
		GroupMSearch:         {WrapperSearchShards, WrapperMultiSearchItems},
		GroupMSearchTemplate: {WrapperSearchShards, WrapperMultiSearchItems},
		GroupMGet:            {WrapperMultiDocItems},
		GroupMTermvectors:    {WrapperMultiDocItems},
		GroupRankEval:        {WrapperRankEvalFailures},
		GroupGetAllPITs:      {WrapperPitNodeFailures},

		// indices
		GroupIndicesRefresh:          {WrapperBroadcastShards},
		GroupIndicesFlush:            {WrapperBroadcastShards},
		GroupIndicesForceMerge:       {WrapperBroadcastShards},
		GroupIndicesClearCache:       {WrapperBroadcastShards},
		GroupIndicesValidateQuery:    {WrapperBroadcastShards},
		GroupIndicesSegments:         {WrapperBroadcastShards},
		GroupIndicesStats:            {WrapperBroadcastShards},
		GroupIndicesUpgrade:          {WrapperBroadcastShards},
		GroupIndicesDataStreamsStats: {WrapperBroadcastShards},

		// cluster / nodes / dangling
		GroupClusterStats:              {WrapperNodeFailures},
		GroupNodesInfo:                 {WrapperNodeFailures},
		GroupNodesStats:                {WrapperNodeFailures},
		GroupNodesUsage:                {WrapperNodeFailures},
		GroupNodesReloadSecureSettings: {WrapperNodeFailures},
		GroupDanglingIndicesList:       {WrapperNodeFailures},

		// tasks
		GroupTasksList:   {WrapperTaskFailures},
		GroupTasksCancel: {WrapperTaskFailures},

		// snapshot
		GroupSnapshotCreate: {WrapperSnapshotCreateShardFailures},
		GroupSnapshotGet:    {WrapperSnapshotGetShardFailures},

		// ingest / ingestion
		GroupIngestSimulate:    {WrapperSimulateDocFailures},
		GroupIngestionGetState: {WrapperBroadcastShards},
		GroupIngestionPause:    {WrapperIngestionShardFailures},
		GroupIngestionResume:   {WrapperIngestionShardFailures},

		// asynchronous_search (plugin)
		GroupAsynchronousSearchSearch: {WrapperSearchShards},
		GroupAsynchronousSearchGet:    {WrapperSearchShards},

		// search_relevance (plugin)
		GroupSearchRelevanceGetNodeStats: {WrapperNodeFailures},
		GroupSearchRelevanceGetStats:     {WrapperNodeFailures},
	}
}

// For looks up the wrappers declared for a given operation group. Returns
// nil for operations with no partial-failure surface area. The returned
// slice is sorted (canonical order from Wrappers) so codegen output stays
// deterministic across runs.
func For(group string) []string {
	raw, ok := OperationWrappers()[group]
	if !ok {
		return nil
	}
	return sortedCanonical(raw)
}

// sortedCanonical returns a copy of in sorted by wrapper bit position.
func sortedCanonical(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	wrappers := Wrappers()
	idx := make(map[string]int, len(wrappers))
	for i, w := range wrappers {
		idx[w] = i
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		return idx[out[i]] < idx[out[j]]
	})
	return out
}
