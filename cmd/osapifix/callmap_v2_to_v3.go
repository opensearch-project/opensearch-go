// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

// callmap_v2_to_v3.go is the hand-authored v2 -> v3 API call map: the per-op
// correspondence between a v2 root/sub-client call path and its v3 typed
// sub-client destination. It is the data the surface JSON cannot carry - the
// surfaces record struct fields, not the method regrouping and *Request -> *Req
// renames that the v3 redesign performed.
//
// It exists so the v2 -> v3 hop can mechanize the CALL half of the migration
// (client.<v2path>(...) -> client.<v3path>(ctx, <Req>{...})) for the ops it
// covers, and report the rest. The map + its drift guard
// (callmap_v2_to_v3_test.go) live here; the rewriter consumes the map for the
// seed ops Ping and Indices.Exists (see rewrite_idiom2.go), while the remaining
// rows are data for later increments and stay report-only until wired.
//
// Sources, all in-repo or in the module cache - no external module trees:
//
//   - v2 call paths: the root opensearch.Client + opensearchapi sub-client
//     fields, validated against surface_v2.json.
//   - v3 destinations + Req types: the maintainer-authored "every method that
//     moved, was renamed, or was removed" tables in UPGRADING_V3.md, confirmed
//     against the real v3 method set (opensearch-go/v3@v3.1.0) and validated
//     against surface_v3.json.
//
// The v3 method receiver kind (value Req vs *Req) and whether the op still
// returns a raw *opensearch.Response are recorded per row because the eventual
// call rewrite needs both: ReqPtr decides whether to wrap the Req literal in &,
// and RawResponse marks the 11 ops (the 7 *Exists* checks, Ping, Nodes.HotThreads,
// and Cluster's Post/DeleteVotingConfigExclusions) whose response half stays a raw
// *Response rather than a decoded typed *Resp. Only Remote -> Cluster.RemoteInfo is
// a rename not listed in UPGRADING_V3.md; it is confirmed in the v3 source.

// callMapEntry is one v2 -> v3 op correspondence. The response half is not
// modeled here - it is a per-site semantic rewrite (raw *Response / typed *Resp
// handling) reported as MANUAL, never mechanized from this table.
type callMapEntry struct {
	// V2Path is the call path off the v2 root opensearch.Client, e.g.
	// {"Ping"} for client.Ping or {"Indices","Exists"} for
	// client.Indices.Exists.
	V2Path []string
	// V3Path is the destination path off the v3 opensearchapi.Client, e.g.
	// {"Document","Create"} for client.Document.Create. Empty when Removed.
	V3Path []string
	// V3Req is the unqualified v3 request struct name, e.g. "IndicesExistsReq".
	// Empty when Removed.
	V3Req string
	// ReqPtr is true when the v3 method takes *Req (the call must pass &Req{}).
	ReqPtr bool
	// RawResponse is true when the v3 method still returns a raw
	// *opensearch.Response rather than a decoded typed *Resp.
	//
	// Forward-data for the not-yet-built response-half rewrite; no code reads it
	// yet. Unlike every other column it is NOT covered by
	// TestCallMapV2toV3AgainstSurfaces: the committed surfaces model struct shape
	// (pkg, name, fields), not method return types, so raw-vs-typed cannot be
	// derived from them. The 11 true values are set by hand from the v3 source
	// (v3@v3.1.0) and UPGRADING_V3.md; re-confirm them against the v3 method set
	// when the response-half increment starts to consume this field.
	RawResponse bool
	// Removed is true for a v2 op with no v3 equivalent (UPGRADING_V3.md
	// "Removed APIs"). Such a call has no rewrite target and is reported MANUAL.
	Removed bool
}

// callMapV2toV3 is the complete v2 -> v3 op map: every callable endpoint on the
// v2 root client and its sub-clients (156 ops - 150 mapped, 6 removed). Kept
// sorted by V2Path for auditability and diff stability. Every row is validated
// against surface_v2.json and surface_v3.json by
// TestCallMapV2toV3AgainstSurfaces.
//
//nolint:gochecknoglobals,goconst,lll // immutable data table; naming each repeated API name as a constant, or wrapping rows, would obscure the table
var callMapV2toV3 = []callMapEntry{
	{V2Path: []string{"Bulk"}, V3Path: []string{"Bulk"}, V3Req: "BulkReq"},
	{V2Path: []string{"Cat", "Aliases"}, V3Path: []string{"Cat", "Aliases"}, V3Req: "CatAliasesReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Allocation"}, V3Path: []string{"Cat", "Allocation"}, V3Req: "CatAllocationReq", ReqPtr: true},
	{V2Path: []string{"Cat", "ClusterManager"}, V3Path: []string{"Cat", "ClusterManager"}, V3Req: "CatClusterManagerReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Count"}, V3Path: []string{"Cat", "Count"}, V3Req: "CatCountReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Fielddata"}, V3Path: []string{"Cat", "FieldData"}, V3Req: "CatFieldDataReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Health"}, V3Path: []string{"Cat", "Health"}, V3Req: "CatHealthReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Help"}, Removed: true},
	{V2Path: []string{"Cat", "Indices"}, V3Path: []string{"Cat", "Indices"}, V3Req: "CatIndicesReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Master"}, V3Path: []string{"Cat", "Master"}, V3Req: "CatMasterReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Nodeattrs"}, V3Path: []string{"Cat", "NodeAttrs"}, V3Req: "CatNodeAttrsReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Nodes"}, V3Path: []string{"Cat", "Nodes"}, V3Req: "CatNodesReq", ReqPtr: true},
	{V2Path: []string{"Cat", "PendingTasks"}, V3Path: []string{"Cat", "PendingTasks"}, V3Req: "CatPendingTasksReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Plugins"}, V3Path: []string{"Cat", "Plugins"}, V3Req: "CatPluginsReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Recovery"}, V3Path: []string{"Cat", "Recovery"}, V3Req: "CatRecoveryReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Repositories"}, V3Path: []string{"Cat", "Repositories"}, V3Req: "CatRepositoriesReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Segments"}, V3Path: []string{"Cat", "Segments"}, V3Req: "CatSegmentsReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Shards"}, V3Path: []string{"Cat", "Shards"}, V3Req: "CatShardsReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Snapshots"}, V3Path: []string{"Cat", "Snapshots"}, V3Req: "CatSnapshotsReq"},
	{V2Path: []string{"Cat", "Tasks"}, V3Path: []string{"Cat", "Tasks"}, V3Req: "CatTasksReq", ReqPtr: true},
	{V2Path: []string{"Cat", "Templates"}, V3Path: []string{"Cat", "Templates"}, V3Req: "CatTemplatesReq", ReqPtr: true},
	{V2Path: []string{"Cat", "ThreadPool"}, V3Path: []string{"Cat", "ThreadPool"}, V3Req: "CatThreadPoolReq", ReqPtr: true},
	{V2Path: []string{"ClearScroll"}, V3Path: []string{"Scroll", "Delete"}, V3Req: "ScrollDeleteReq"},
	{V2Path: []string{"Cluster", "AllocationExplain"}, V3Path: []string{"Cluster", "AllocationExplain"}, V3Req: "ClusterAllocationExplainReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "DeleteComponentTemplate"}, V3Path: []string{"ComponentTemplate", "Delete"}, V3Req: "ComponentTemplateDeleteReq"},
	{V2Path: []string{"Cluster", "DeleteVotingConfigExclusions"}, V3Path: []string{"Cluster", "DeleteVotingConfigExclusions"}, V3Req: "ClusterDeleteVotingConfigExclusionsReq", RawResponse: true},
	{V2Path: []string{"Cluster", "ExistsComponentTemplate"}, V3Path: []string{"ComponentTemplate", "Exists"}, V3Req: "ComponentTemplateExistsReq", RawResponse: true},
	{V2Path: []string{"Cluster", "GetComponentTemplate"}, V3Path: []string{"ComponentTemplate", "Get"}, V3Req: "ComponentTemplateGetReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "GetSettings"}, V3Path: []string{"Cluster", "GetSettings"}, V3Req: "ClusterGetSettingsReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "Health"}, V3Path: []string{"Cluster", "Health"}, V3Req: "ClusterHealthReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "PendingTasks"}, V3Path: []string{"Cluster", "PendingTasks"}, V3Req: "ClusterPendingTasksReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "PostVotingConfigExclusions"}, V3Path: []string{"Cluster", "PostVotingConfigExclusions"}, V3Req: "ClusterPostVotingConfigExclusionsReq", RawResponse: true},
	{V2Path: []string{"Cluster", "PutComponentTemplate"}, V3Path: []string{"ComponentTemplate", "Create"}, V3Req: "ComponentTemplateCreateReq"},
	{V2Path: []string{"Cluster", "PutSettings"}, V3Path: []string{"Cluster", "PutSettings"}, V3Req: "ClusterPutSettingsReq"},
	{V2Path: []string{"Cluster", "RemoteInfo"}, V3Path: []string{"Cluster", "RemoteInfo"}, V3Req: "ClusterRemoteInfoReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "Reroute"}, V3Path: []string{"Cluster", "Reroute"}, V3Req: "ClusterRerouteReq"},
	{V2Path: []string{"Cluster", "State"}, V3Path: []string{"Cluster", "State"}, V3Req: "ClusterStateReq", ReqPtr: true},
	{V2Path: []string{"Cluster", "Stats"}, V3Path: []string{"Cluster", "Stats"}, V3Req: "ClusterStatsReq", ReqPtr: true},
	{V2Path: []string{"Count"}, V3Path: []string{"Indices", "Count"}, V3Req: "IndicesCountReq", ReqPtr: true},
	{V2Path: []string{"Create"}, V3Path: []string{"Document", "Create"}, V3Req: "DocumentCreateReq"},
	{V2Path: []string{"DanglingIndicesDeleteDanglingIndex"}, V3Path: []string{"Dangling", "Delete"}, V3Req: "DanglingDeleteReq"},
	{V2Path: []string{"DanglingIndicesImportDanglingIndex"}, V3Path: []string{"Dangling", "Import"}, V3Req: "DanglingImportReq"},
	{V2Path: []string{"DanglingIndicesListDanglingIndices"}, V3Path: []string{"Dangling", "Get"}, V3Req: "DanglingGetReq", ReqPtr: true},
	{V2Path: []string{"Delete"}, V3Path: []string{"Document", "Delete"}, V3Req: "DocumentDeleteReq"},
	{V2Path: []string{"DeleteByQuery"}, V3Path: []string{"Document", "DeleteByQuery"}, V3Req: "DocumentDeleteByQueryReq"},
	{V2Path: []string{"DeleteByQueryRethrottle"}, V3Path: []string{"Document", "DeleteByQueryRethrottle"}, V3Req: "DocumentDeleteByQueryRethrottleReq"},
	{V2Path: []string{"DeleteScript"}, V3Path: []string{"Script", "Delete"}, V3Req: "ScriptDeleteReq"},
	{V2Path: []string{"Exists"}, V3Path: []string{"Document", "Exists"}, V3Req: "DocumentExistsReq", RawResponse: true},
	{V2Path: []string{"ExistsSource"}, V3Path: []string{"Document", "ExistsSource"}, V3Req: "DocumentExistsSourceReq", RawResponse: true},
	{V2Path: []string{"Explain"}, V3Path: []string{"Document", "Explain"}, V3Req: "DocumentExplainReq"},
	{V2Path: []string{"FieldCaps"}, V3Path: []string{"Indices", "FieldCaps"}, V3Req: "IndicesFieldCapsReq"},
	{V2Path: []string{"Get"}, V3Path: []string{"Document", "Get"}, V3Req: "DocumentGetReq"},
	{V2Path: []string{"GetScript"}, V3Path: []string{"Script", "Get"}, V3Req: "ScriptGetReq"},
	{V2Path: []string{"GetScriptContext"}, V3Path: []string{"Script", "Context"}, V3Req: "ScriptContextReq", ReqPtr: true},
	{V2Path: []string{"GetScriptLanguages"}, V3Path: []string{"Script", "Language"}, V3Req: "ScriptLanguageReq", ReqPtr: true},
	{V2Path: []string{"GetSource"}, V3Path: []string{"Document", "Source"}, V3Req: "DocumentSourceReq"},
	{V2Path: []string{"Index"}, V3Path: []string{"Index"}, V3Req: "IndexReq"},
	{V2Path: []string{"Indices", "AddBlock"}, V3Path: []string{"Indices", "Block"}, V3Req: "IndicesBlockReq"},
	{V2Path: []string{"Indices", "Analyze"}, V3Path: []string{"Indices", "Analyze"}, V3Req: "IndicesAnalyzeReq"},
	{V2Path: []string{"Indices", "ClearCache"}, V3Path: []string{"Indices", "ClearCache"}, V3Req: "IndicesClearCacheReq", ReqPtr: true},
	{V2Path: []string{"Indices", "Clone"}, V3Path: []string{"Indices", "Clone"}, V3Req: "IndicesCloneReq"},
	{V2Path: []string{"Indices", "Close"}, V3Path: []string{"Indices", "Close"}, V3Req: "IndicesCloseReq"},
	{V2Path: []string{"Indices", "Create"}, V3Path: []string{"Indices", "Create"}, V3Req: "IndicesCreateReq"},
	{V2Path: []string{"Indices", "CreateDataStream"}, V3Path: []string{"DataStream", "Create"}, V3Req: "DataStreamCreateReq"},
	{V2Path: []string{"Indices", "Delete"}, V3Path: []string{"Indices", "Delete"}, V3Req: "IndicesDeleteReq"},
	{V2Path: []string{"Indices", "DeleteAlias"}, V3Path: []string{"Indices", "Alias", "Delete"}, V3Req: "AliasDeleteReq"},
	{V2Path: []string{"Indices", "DeleteDataStream"}, V3Path: []string{"DataStream", "Delete"}, V3Req: "DataStreamDeleteReq"},
	{V2Path: []string{"Indices", "DeleteIndexTemplate"}, V3Path: []string{"IndexTemplate", "Delete"}, V3Req: "IndexTemplateDeleteReq"},
	{V2Path: []string{"Indices", "DeleteTemplate"}, V3Path: []string{"Template", "Delete"}, V3Req: "TemplateDeleteReq"},
	{V2Path: []string{"Indices", "DiskUsage"}, Removed: true},
	{V2Path: []string{"Indices", "Exists"}, V3Path: []string{"Indices", "Exists"}, V3Req: "IndicesExistsReq", RawResponse: true},
	{V2Path: []string{"Indices", "ExistsAlias"}, V3Path: []string{"Indices", "Alias", "Exists"}, V3Req: "AliasExistsReq", RawResponse: true},
	{V2Path: []string{"Indices", "ExistsIndexTemplate"}, V3Path: []string{"IndexTemplate", "Exists"}, V3Req: "IndexTemplateExistsReq", RawResponse: true},
	{V2Path: []string{"Indices", "ExistsTemplate"}, V3Path: []string{"Template", "Exists"}, V3Req: "TemplateExistsReq", RawResponse: true},
	{V2Path: []string{"Indices", "FieldUsageStats"}, Removed: true},
	{V2Path: []string{"Indices", "Flush"}, V3Path: []string{"Indices", "Flush"}, V3Req: "IndicesFlushReq", ReqPtr: true},
	{V2Path: []string{"Indices", "Forcemerge"}, V3Path: []string{"Indices", "Forcemerge"}, V3Req: "IndicesForcemergeReq", ReqPtr: true},
	{V2Path: []string{"Indices", "Get"}, V3Path: []string{"Indices", "Get"}, V3Req: "IndicesGetReq"},
	{V2Path: []string{"Indices", "GetAlias"}, V3Path: []string{"Indices", "Alias", "Get"}, V3Req: "AliasGetReq"},
	{V2Path: []string{"Indices", "GetDataStream"}, V3Path: []string{"DataStream", "Get"}, V3Req: "DataStreamGetReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetDataStreamStats"}, V3Path: []string{"DataStream", "Stats"}, V3Req: "DataStreamStatsReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetFieldMapping"}, V3Path: []string{"Indices", "Mapping", "Field"}, V3Req: "MappingFieldReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetIndexTemplate"}, V3Path: []string{"IndexTemplate", "Get"}, V3Req: "IndexTemplateGetReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetMapping"}, V3Path: []string{"Indices", "Mapping", "Get"}, V3Req: "MappingGetReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetSettings"}, V3Path: []string{"Indices", "Settings", "Get"}, V3Req: "SettingsGetReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetTemplate"}, V3Path: []string{"Template", "Get"}, V3Req: "TemplateGetReq", ReqPtr: true},
	{V2Path: []string{"Indices", "GetUpgrade"}, Removed: true},
	{V2Path: []string{"Indices", "Open"}, V3Path: []string{"Indices", "Open"}, V3Req: "IndicesOpenReq"},
	{V2Path: []string{"Indices", "PutAlias"}, V3Path: []string{"Indices", "Alias", "Put"}, V3Req: "AliasPutReq"},
	{V2Path: []string{"Indices", "PutIndexTemplate"}, V3Path: []string{"IndexTemplate", "Create"}, V3Req: "IndexTemplateCreateReq"},
	{V2Path: []string{"Indices", "PutMapping"}, V3Path: []string{"Indices", "Mapping", "Put"}, V3Req: "MappingPutReq"},
	{V2Path: []string{"Indices", "PutSettings"}, V3Path: []string{"Indices", "Settings", "Put"}, V3Req: "SettingsPutReq"},
	{V2Path: []string{"Indices", "PutTemplate"}, V3Path: []string{"Template", "Create"}, V3Req: "TemplateCreateReq"},
	{V2Path: []string{"Indices", "Recovery"}, V3Path: []string{"Indices", "Recovery"}, V3Req: "IndicesRecoveryReq", ReqPtr: true},
	{V2Path: []string{"Indices", "Refresh"}, V3Path: []string{"Indices", "Refresh"}, V3Req: "IndicesRefreshReq", ReqPtr: true},
	{V2Path: []string{"Indices", "ResolveIndex"}, V3Path: []string{"Indices", "Resolve"}, V3Req: "IndicesResolveReq"},
	{V2Path: []string{"Indices", "Rollover"}, V3Path: []string{"Indices", "Rollover"}, V3Req: "IndicesRolloverReq"},
	{V2Path: []string{"Indices", "Segments"}, V3Path: []string{"Indices", "Segments"}, V3Req: "IndicesSegmentsReq", ReqPtr: true},
	{V2Path: []string{"Indices", "ShardStores"}, V3Path: []string{"Indices", "ShardStores"}, V3Req: "IndicesShardStoresReq", ReqPtr: true},
	{V2Path: []string{"Indices", "Shrink"}, V3Path: []string{"Indices", "Shrink"}, V3Req: "IndicesShrinkReq"},
	{V2Path: []string{"Indices", "SimulateIndexTemplate"}, V3Path: []string{"IndexTemplate", "SimulateIndex"}, V3Req: "IndexTemplateSimulateIndexReq"},
	{V2Path: []string{"Indices", "SimulateTemplate"}, V3Path: []string{"IndexTemplate", "Simulate"}, V3Req: "IndexTemplateSimulateReq"},
	{V2Path: []string{"Indices", "Split"}, V3Path: []string{"Indices", "Split"}, V3Req: "IndicesSplitReq"},
	{V2Path: []string{"Indices", "Stats"}, V3Path: []string{"Indices", "Stats"}, V3Req: "IndicesStatsReq", ReqPtr: true},
	{V2Path: []string{"Indices", "UpdateAliases"}, V3Path: []string{"Aliases"}, V3Req: "AliasesReq"},
	{V2Path: []string{"Indices", "Upgrade"}, Removed: true},
	{V2Path: []string{"Indices", "ValidateQuery"}, V3Path: []string{"Indices", "ValidateQuery"}, V3Req: "IndicesValidateQueryReq"},
	{V2Path: []string{"Info"}, V3Path: []string{"Info"}, V3Req: "InfoReq", ReqPtr: true},
	{V2Path: []string{"Ingest", "DeletePipeline"}, V3Path: []string{"Ingest", "Delete"}, V3Req: "IngestDeleteReq"},
	{V2Path: []string{"Ingest", "GetPipeline"}, V3Path: []string{"Ingest", "Get"}, V3Req: "IngestGetReq", ReqPtr: true},
	{V2Path: []string{"Ingest", "ProcessorGrok"}, V3Path: []string{"Ingest", "Grok"}, V3Req: "IngestGrokReq", ReqPtr: true},
	{V2Path: []string{"Ingest", "PutPipeline"}, V3Path: []string{"Ingest", "Create"}, V3Req: "IngestCreateReq"},
	{V2Path: []string{"Ingest", "Simulate"}, V3Path: []string{"Ingest", "Simulate"}, V3Req: "IngestSimulateReq"},
	{V2Path: []string{"Mget"}, V3Path: []string{"MGet"}, V3Req: "MGetReq"},
	{V2Path: []string{"Msearch"}, V3Path: []string{"MSearch"}, V3Req: "MSearchReq"},
	{V2Path: []string{"MsearchTemplate"}, V3Path: []string{"MSearchTemplate"}, V3Req: "MSearchTemplateReq"},
	{V2Path: []string{"Mtermvectors"}, V3Path: []string{"MTermvectors"}, V3Req: "MTermvectorsReq"},
	{V2Path: []string{"Nodes", "HotThreads"}, V3Path: []string{"Nodes", "HotThreads"}, V3Req: "NodesHotThreadsReq", ReqPtr: true, RawResponse: true},
	{V2Path: []string{"Nodes", "Info"}, V3Path: []string{"Nodes", "Info"}, V3Req: "NodesInfoReq", ReqPtr: true},
	{V2Path: []string{"Nodes", "ReloadSecureSettings"}, V3Path: []string{"Nodes", "ReloadSecurity"}, V3Req: "NodesReloadSecurityReq", ReqPtr: true},
	{V2Path: []string{"Nodes", "Stats"}, V3Path: []string{"Nodes", "Stats"}, V3Req: "NodesStatsReq", ReqPtr: true},
	{V2Path: []string{"Nodes", "Usage"}, V3Path: []string{"Nodes", "Usage"}, V3Req: "NodesUsageReq", ReqPtr: true},
	{V2Path: []string{"Ping"}, V3Path: []string{"Ping"}, V3Req: "PingReq", ReqPtr: true, RawResponse: true},
	{V2Path: []string{"PointInTime", "Create"}, V3Path: []string{"PointInTime", "Create"}, V3Req: "PointInTimeCreateReq"},
	{V2Path: []string{"PointInTime", "Delete"}, V3Path: []string{"PointInTime", "Delete"}, V3Req: "PointInTimeDeleteReq"},
	{V2Path: []string{"PointInTime", "Get"}, V3Path: []string{"PointInTime", "Get"}, V3Req: "PointInTimeGetReq", ReqPtr: true},
	{V2Path: []string{"PutScript"}, V3Path: []string{"Script", "Put"}, V3Req: "ScriptPutReq"},
	{V2Path: []string{"RankEval"}, V3Path: []string{"RankEval"}, V3Req: "RankEvalReq"},
	{V2Path: []string{"Reindex"}, V3Path: []string{"Reindex"}, V3Req: "ReindexReq"},
	{V2Path: []string{"ReindexRethrottle"}, V3Path: []string{"ReindexRethrottle"}, V3Req: "ReindexRethrottleReq"},
	{V2Path: []string{"Remote"}, V3Path: []string{"Cluster", "RemoteInfo"}, V3Req: "ClusterRemoteInfoReq", ReqPtr: true},
	{V2Path: []string{"RenderSearchTemplate"}, V3Path: []string{"RenderSearchTemplate"}, V3Req: "RenderSearchTemplateReq"},
	{V2Path: []string{"ScriptsPainlessExecute"}, V3Path: []string{"Script", "PainlessExecute"}, V3Req: "ScriptPainlessExecuteReq"},
	{V2Path: []string{"Scroll"}, V3Path: []string{"Scroll", "Get"}, V3Req: "ScrollGetReq"},
	{V2Path: []string{"Search"}, V3Path: []string{"Search"}, V3Req: "SearchReq", ReqPtr: true},
	{V2Path: []string{"SearchShards"}, V3Path: []string{"SearchShards"}, V3Req: "SearchShardsReq", ReqPtr: true},
	{V2Path: []string{"SearchTemplate"}, V3Path: []string{"SearchTemplate"}, V3Req: "SearchTemplateReq"},
	{V2Path: []string{"Snapshot", "CleanupRepository"}, V3Path: []string{"Snapshot", "Repository", "Cleanup"}, V3Req: "SnapshotRepositoryCleanupReq"},
	{V2Path: []string{"Snapshot", "Clone"}, V3Path: []string{"Snapshot", "Clone"}, V3Req: "SnapshotCloneReq"},
	{V2Path: []string{"Snapshot", "Create"}, V3Path: []string{"Snapshot", "Create"}, V3Req: "SnapshotCreateReq"},
	{V2Path: []string{"Snapshot", "CreateRepository"}, V3Path: []string{"Snapshot", "Repository", "Create"}, V3Req: "SnapshotRepositoryCreateReq"},
	{V2Path: []string{"Snapshot", "Delete"}, V3Path: []string{"Snapshot", "Delete"}, V3Req: "SnapshotDeleteReq"},
	{V2Path: []string{"Snapshot", "DeleteRepository"}, V3Path: []string{"Snapshot", "Repository", "Delete"}, V3Req: "SnapshotRepositoryDeleteReq"},
	{V2Path: []string{"Snapshot", "Get"}, V3Path: []string{"Snapshot", "Get"}, V3Req: "SnapshotGetReq"},
	{V2Path: []string{"Snapshot", "GetRepository"}, V3Path: []string{"Snapshot", "Repository", "Get"}, V3Req: "SnapshotRepositoryGetReq", ReqPtr: true},
	{V2Path: []string{"Snapshot", "Restore"}, V3Path: []string{"Snapshot", "Restore"}, V3Req: "SnapshotRestoreReq"},
	{V2Path: []string{"Snapshot", "Status"}, V3Path: []string{"Snapshot", "Status"}, V3Req: "SnapshotStatusReq"},
	{V2Path: []string{"Snapshot", "VerifyRepository"}, V3Path: []string{"Snapshot", "Repository", "Verify"}, V3Req: "SnapshotRepositoryVerifyReq"},
	{V2Path: []string{"Tasks", "Cancel"}, V3Path: []string{"Tasks", "Cancel"}, V3Req: "TasksCancelReq"},
	{V2Path: []string{"Tasks", "Get"}, V3Path: []string{"Tasks", "Get"}, V3Req: "TasksGetReq"},
	{V2Path: []string{"Tasks", "List"}, V3Path: []string{"Tasks", "List"}, V3Req: "TasksListReq", ReqPtr: true},
	{V2Path: []string{"TermsEnum"}, Removed: true},
	{V2Path: []string{"Termvectors"}, V3Path: []string{"Termvectors"}, V3Req: "TermvectorsReq"},
	{V2Path: []string{"Update"}, V3Path: []string{"Update"}, V3Req: "UpdateReq"},
	{V2Path: []string{"UpdateByQuery"}, V3Path: []string{"UpdateByQuery"}, V3Req: "UpdateByQueryReq"},
	{V2Path: []string{"UpdateByQueryRethrottle"}, V3Path: []string{"UpdateByQueryRethrottle"}, V3Req: "UpdateByQueryRethrottleReq"},
}
