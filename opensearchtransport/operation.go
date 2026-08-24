// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import "strconv"

// OperationID is a bit-packed int64 that identifies an OpenSearch operation.
//
// Layout:
//
//	 63  62       56  55       40  39       32  31                0
//	┌───┬──────────┬────────────┬────────────┬─────────────────────┐
//	│R/W│ Category │  Reserved  │  Reserved  │   Minor operation   │
//	│1b │   7b     │   16b      │    8b      │       32b           │
//	└───┴──────────┴────────────┴────────────┴─────────────────────┘
//	     ◄── 24 bits: major ──►  ◄── gap ──► ◄──── 32b: minor ───►
//
// Bit 63:    R/W flag (0=read, 1=write)
// Bits 62-56: Category (7 bits, 128 per R/W, 256 total)
// Bits 55-40: Reserved for future sub-categories or flags
// Bits 39-32: Reserved gap
// Bits 31-0:  Minor operation (sequential per category)
type OperationID int64

// Bit layout constants.
const (
	opRWBit                = 63
	opRWMask   OperationID = -1 << opRWBit // 0x8000_0000_0000_0000 (sign bit)
	opCatShift             = 56
	opCatMask  OperationID = 0x7F << opCatShift    // 0x7F00_0000_0000_0000
	opMinMask  OperationID = 0x0000_0000_FFFF_FFFF // low 32 bits
	opMajMask  OperationID = opRWMask | opCatMask  // top 8 bits (R/W + category)
)

// IsWrite reports whether the operation is a write (mutating) operation.
func (op OperationID) IsWrite() bool { return op&opRWMask != 0 }

// IsRead reports whether the operation is a read-only operation.
func (op OperationID) IsRead() bool { return op&opRWMask == 0 }

// Category returns the major category bits (R/W + category slot).
func (op OperationID) Category() OperationID { return op & opMajMask }

// Minor returns the minor operation identifier within the category.
func (op OperationID) Minor() OperationID { return op & opMinMask }

// ---------------------------------------------------------------------------
// Read categories (bit 63 = 0)
// ---------------------------------------------------------------------------

// CatCluster and the following constants define read-side operation categories.
const (
	CatCluster  OperationID = iota << opCatShift //  0: Cluster info and management reads
	CatSearch                                    //  1: Search, msearch, count, etc.
	CatScroll                                    //  2: Scroll reads
	CatPIT                                       //  3: Point-in-time reads
	CatDocRead                                   //  4: Document retrieval
	CatIndex                                     //  5: Index metadata reads
	CatMapping                                   //  6: Mapping reads
	CatAlias                                     //  7: Alias reads
	CatTemplate                                  //  8: Template reads
	CatMaint                                     //  9: Maintenance reads (segments, recovery, stats)
	CatIngest                                    // 10: Ingest pipeline reads
	CatAdmin                                     // 11: Administrative reads (cat, nodes, tasks, etc.)
	CatPing                                      // 12: Ping

	catBulkSlot OperationID = 13 << opCatShift // 13: Bulk (write-only, no read equivalent)
)

// ---------------------------------------------------------------------------
// Write categories (bit 63 = 1)
// ---------------------------------------------------------------------------

// CatClusterWrite and the following constants define write-side operation categories.
const (
	CatClusterWrite  = opRWMask | CatCluster
	CatScrollWrite   = opRWMask | CatScroll
	CatPITWrite      = opRWMask | CatPIT
	CatDocWrite      = opRWMask | CatDocRead // reuses slot 4; R/W bit distinguishes
	CatBulk          = opRWMask | catBulkSlot
	CatIndexWrite    = opRWMask | CatIndex
	CatMappingWrite  = opRWMask | CatMapping
	CatAliasWrite    = opRWMask | CatAlias
	CatTemplateWrite = opRWMask | CatTemplate
	CatMaintWrite    = opRWMask | CatMaint
	CatIngestWrite   = opRWMask | CatIngest
	CatAdminWrite    = opRWMask | CatAdmin
)

// ---------------------------------------------------------------------------
// Minor operations — Search (CatSearch)
// ---------------------------------------------------------------------------

const (
	minSearch OperationID = iota + 1
	minMSearch
	minCount
	minSearchTemplate
	minMSearchTemplate
	minValidate
	minRankEval
	minExplain
	minSearchShards
	minFieldCaps
)

// ---------------------------------------------------------------------------
// Minor operations — Scroll (CatScroll / CatScrollWrite)
// ---------------------------------------------------------------------------

const (
	minScrollGet OperationID = iota + 1
	minScrollDelete
)

// ---------------------------------------------------------------------------
// Minor operations — PIT (CatPIT / CatPITWrite)
// ---------------------------------------------------------------------------

const (
	minPITList OperationID = iota + 1
	minPITCreate
	minPITDelete
)

// ---------------------------------------------------------------------------
// Minor operations — Document read (CatDocRead)
// ---------------------------------------------------------------------------

const (
	minDocGet OperationID = iota + 1
	minDocExists
	minDocSourceGet
	minDocSourceExists
	minMGet
	minTermVectors
	minMTermVectors
)

// ---------------------------------------------------------------------------
// Minor operations — Document write (CatDocWrite)
// ---------------------------------------------------------------------------

const (
	minDocIndex OperationID = iota + 1
	minDocCreate
	minDocUpdate
	minDocDelete
)

// ---------------------------------------------------------------------------
// Minor operations — Bulk (CatBulk)
// ---------------------------------------------------------------------------

const (
	minBulk OperationID = iota + 1
	minBulkStream
	minReindex
	minDeleteByQuery
	minUpdateByQuery
	minReindexRethrottle
	minDBQRethrottle
	minUBQRethrottle
)

// ---------------------------------------------------------------------------
// Minor operations — Index management (CatIndex / CatIndexWrite)
// ---------------------------------------------------------------------------

const (
	minIndexGet OperationID = iota + 1
	minIndexExists
	minIndexCreate
	minIndexDelete
	minIndexOpen
	minIndexClose
	minIndexClone
	minIndexShrink
	minIndexSplit
	minIndexRollover
	minIndexBlock
	minIndexResolve
	minIndexAnalyze
)

// ---------------------------------------------------------------------------
// Minor operations — Mapping (CatMapping / CatMappingWrite)
// ---------------------------------------------------------------------------

const (
	minMappingGet OperationID = iota + 1
	minMappingPut
)

// ---------------------------------------------------------------------------
// Minor operations — Alias (CatAlias / CatAliasWrite)
// ---------------------------------------------------------------------------

const (
	minAliasGet OperationID = iota + 1
	minAliasPut
	minAliasDelete
	minCatAliases
)

// ---------------------------------------------------------------------------
// Minor operations — Template (CatTemplate / CatTemplateWrite)
// ---------------------------------------------------------------------------

const (
	minIndexTemplateGet OperationID = iota + 1
	minIndexTemplateCreate
	minIndexTemplateDelete
	minIndexTemplateExists
	minIndexTemplateSimulate
	minIndexTemplateSimulateIndex
	minComponentTemplateGet
	minComponentTemplateCreate
	minComponentTemplateDelete
	minComponentTemplateExists
	minLegacyTemplateGet
	minLegacyTemplateCreate
	minLegacyTemplateDelete
	minLegacyTemplateExists
)

// ---------------------------------------------------------------------------
// Minor operations — Maintenance (CatMaint / CatMaintWrite)
// ---------------------------------------------------------------------------

const (
	minRefresh OperationID = iota + 1
	minFlush
	minForceMerge
	minCacheClear
	minSegments
	minRecovery
	minShardStores
	minStats
)

// ---------------------------------------------------------------------------
// Minor operations — Ingest (CatIngest / CatIngestWrite)
// ---------------------------------------------------------------------------

const (
	minIngestGet OperationID = iota + 1
	minIngestCreate
	minIngestDelete
	minIngestSimulate
	minIngestGrok
)

// ---------------------------------------------------------------------------
// Minor operations — Cluster (CatCluster / CatClusterWrite)
// ---------------------------------------------------------------------------

const (
	minClusterInfo OperationID = iota + 1
	minClusterHealth
	minClusterStats
	minClusterState
	minClusterSettingsGet
	minClusterSettingsPut
	minClusterReroute
	minClusterPendingTasks
	minClusterAllocExplain
	minClusterRemoteInfo
	minClusterVotingConfigGet
	minClusterVotingConfigPut
	minClusterDecommission
)

// ---------------------------------------------------------------------------
// Minor operations — Admin (CatAdmin / CatAdminWrite)
// ---------------------------------------------------------------------------

const (
	// Cat operations
	minCatAllocation OperationID = iota + 1
	minCatClusterMgr
	minCatCount
	minCatFielddata
	minCatHealth
	minCatIndices
	minCatMaster
	minCatNodeAttrs
	minCatNodes
	minCatPendingTasks
	minCatPlugins
	minCatRecovery
	minCatRepositories
	minCatSegments
	minCatShards
	minCatSnapshots
	minCatTasks
	minCatTemplates
	minCatThreadPool

	// Nodes
	minNodesInfo
	minNodesStats
	minNodesUsage
	minNodesHotThreads
	minNodesReloadSecurity

	// Tasks
	minTasksList
	minTasksGet
	minTasksCancel

	// Snapshots
	minSnapshotCreate
	minSnapshotGet
	minSnapshotDelete
	minSnapshotClone
	minSnapshotRestore
	minSnapshotStatus
	minSnapshotRepoCreate
	minSnapshotRepoGet
	minSnapshotRepoDelete
	minSnapshotRepoVerify
	minSnapshotRepoCleanup

	// Scripts
	minScriptGet
	minScriptPut
	minScriptDelete
	minScriptContext
	minScriptLanguage
	minScriptPainlessExec

	// Dangling
	minDanglingGet
	minDanglingDelete
	minDanglingImport

	// DataStream
	minDataStreamGet
	minDataStreamCreate
	minDataStreamDelete
	minDataStreamStats

	// Render search template
	minRenderSearchTemplate
)

// ---------------------------------------------------------------------------
// Minor operations — Ping (CatPing)
// ---------------------------------------------------------------------------

const (
	minPing OperationID = 1
)

// ---------------------------------------------------------------------------
// Composed operation IDs
// ---------------------------------------------------------------------------

// OpOther is returned for unrecognized HTTP method+path combinations.
const OpOther OperationID = -1

// Search operations.
const (
	OpSearch         = CatSearch | minSearch
	OpMSearch        = CatSearch | minMSearch
	OpCount          = CatSearch | minCount
	OpSearchTemplate = CatSearch | minSearchTemplate
	OpMSearchTmpl    = CatSearch | minMSearchTemplate
	OpValidate       = CatSearch | minValidate
	OpRankEval       = CatSearch | minRankEval
	OpExplain        = CatSearch | minExplain
	OpSearchShards   = CatSearch | minSearchShards
	OpFieldCaps      = CatSearch | minFieldCaps
)

// Scroll operations.
const (
	OpScrollGet    = CatScroll | minScrollGet
	OpScrollDelete = CatScrollWrite | minScrollDelete
)

// Point-in-time operations.
const (
	OpPITList   = CatPIT | minPITList
	OpPITCreate = CatPITWrite | minPITCreate
	OpPITDelete = CatPITWrite | minPITDelete
)

// Document read operations.
const (
	OpDocGet         = CatDocRead | minDocGet
	OpDocExists      = CatDocRead | minDocExists
	OpDocSourceGet   = CatDocRead | minDocSourceGet
	OpDocSourceExist = CatDocRead | minDocSourceExists
	OpMGet           = CatDocRead | minMGet
	OpTermVectors    = CatDocRead | minTermVectors
	OpMTermVectors   = CatDocRead | minMTermVectors
)

// Document write operations.
const (
	OpDocIndex  = CatDocWrite | minDocIndex
	OpDocCreate = CatDocWrite | minDocCreate
	OpDocUpdate = CatDocWrite | minDocUpdate
	OpDocDelete = CatDocWrite | minDocDelete
)

// Bulk and query-based write operations.
const (
	OpBulk              = CatBulk | minBulk
	OpBulkStream        = CatBulk | minBulkStream
	OpReindex           = CatBulk | minReindex
	OpDeleteByQuery     = CatBulk | minDeleteByQuery
	OpUpdateByQuery     = CatBulk | minUpdateByQuery
	OpReindexRethrottle = CatBulk | minReindexRethrottle
	OpDBQRethrottle     = CatBulk | minDBQRethrottle
	OpUBQRethrottle     = CatBulk | minUBQRethrottle
)

// Index management operations.
const (
	OpIndexGet      = CatIndex | minIndexGet
	OpIndexExists   = CatIndex | minIndexExists
	OpIndexCreate   = CatIndexWrite | minIndexCreate
	OpIndexDelete   = CatIndexWrite | minIndexDelete
	OpIndexOpen     = CatIndexWrite | minIndexOpen
	OpIndexClose    = CatIndexWrite | minIndexClose
	OpIndexClone    = CatIndexWrite | minIndexClone
	OpIndexShrink   = CatIndexWrite | minIndexShrink
	OpIndexSplit    = CatIndexWrite | minIndexSplit
	OpIndexRollover = CatIndexWrite | minIndexRollover
	OpIndexBlock    = CatIndexWrite | minIndexBlock
	OpIndexResolve  = CatIndex | minIndexResolve
	OpIndexAnalyze  = CatIndexWrite | minIndexAnalyze
)

// Mapping operations.
const (
	OpMappingGet = CatMapping | minMappingGet
	OpMappingPut = CatMappingWrite | minMappingPut
)

// Alias operations.
const (
	OpAliasGet    = CatAlias | minAliasGet
	OpAliasPut    = CatAliasWrite | minAliasPut
	OpAliasDelete = CatAliasWrite | minAliasDelete
	OpCatAliases  = CatAlias | minCatAliases
)

// Template operations.
const (
	OpIndexTemplateGet           = CatTemplate | minIndexTemplateGet
	OpIndexTemplateCreate        = CatTemplateWrite | minIndexTemplateCreate
	OpIndexTemplateDelete        = CatTemplateWrite | minIndexTemplateDelete
	OpIndexTemplateExists        = CatTemplate | minIndexTemplateExists
	OpIndexTemplateSimulate      = CatTemplate | minIndexTemplateSimulate
	OpIndexTemplateSimulateIndex = CatTemplate | minIndexTemplateSimulateIndex
	OpComponentTemplateGet       = CatTemplate | minComponentTemplateGet
	OpComponentTemplateCreate    = CatTemplateWrite | minComponentTemplateCreate
	OpComponentTemplateDelete    = CatTemplateWrite | minComponentTemplateDelete
	OpComponentTemplateExists    = CatTemplate | minComponentTemplateExists
	OpLegacyTemplateGet          = CatTemplate | minLegacyTemplateGet
	OpLegacyTemplateCreate       = CatTemplateWrite | minLegacyTemplateCreate
	OpLegacyTemplateDelete       = CatTemplateWrite | minLegacyTemplateDelete
	OpLegacyTemplateExists       = CatTemplate | minLegacyTemplateExists
)

// Maintenance operations.
const (
	OpRefresh     = CatMaintWrite | minRefresh
	OpFlush       = CatMaintWrite | minFlush
	OpForceMerge  = CatMaintWrite | minForceMerge
	OpCacheClear  = CatMaintWrite | minCacheClear
	OpSegments    = CatMaint | minSegments
	OpRecovery    = CatMaint | minRecovery
	OpShardStores = CatMaint | minShardStores
	OpStats       = CatMaint | minStats
)

// Ingest operations.
const (
	OpIngestGet      = CatIngest | minIngestGet
	OpIngestCreate   = CatIngestWrite | minIngestCreate
	OpIngestDelete   = CatIngestWrite | minIngestDelete
	OpIngestSimulate = CatIngestWrite | minIngestSimulate
	OpIngestGrok     = CatIngest | minIngestGrok
)

// Cluster operations.
const (
	OpClusterInfo           = CatCluster | minClusterInfo
	OpClusterHealth         = CatCluster | minClusterHealth
	OpClusterStats          = CatCluster | minClusterStats
	OpClusterState          = CatCluster | minClusterState
	OpClusterSettingsGet    = CatCluster | minClusterSettingsGet
	OpClusterSettingsPut    = CatClusterWrite | minClusterSettingsPut
	OpClusterReroute        = CatClusterWrite | minClusterReroute
	OpClusterPendingTasks   = CatCluster | minClusterPendingTasks
	OpClusterAllocExplain   = CatCluster | minClusterAllocExplain
	OpClusterRemoteInfo     = CatCluster | minClusterRemoteInfo
	OpClusterVotingConfigEx = CatClusterWrite | minClusterVotingConfigPut
)

// Admin — cat operations.
const (
	OpCatAllocation  = CatAdmin | minCatAllocation
	OpCatClusterMgr  = CatAdmin | minCatClusterMgr
	OpCatCount       = CatAdmin | minCatCount
	OpCatFielddata   = CatAdmin | minCatFielddata
	OpCatHealth      = CatAdmin | minCatHealth
	OpCatIndices     = CatAdmin | minCatIndices
	OpCatMaster      = CatAdmin | minCatMaster
	OpCatNodeAttrs   = CatAdmin | minCatNodeAttrs
	OpCatNodes       = CatAdmin | minCatNodes
	OpCatPendingTask = CatAdmin | minCatPendingTasks
	OpCatPlugins     = CatAdmin | minCatPlugins
	OpCatRecovery    = CatAdmin | minCatRecovery
	OpCatRepos       = CatAdmin | minCatRepositories
	OpCatSegments    = CatAdmin | minCatSegments
	OpCatShards      = CatAdmin | minCatShards
	OpCatSnapshots   = CatAdmin | minCatSnapshots
	OpCatTasks       = CatAdmin | minCatTasks
	OpCatTemplates   = CatAdmin | minCatTemplates
	OpCatThreadPool  = CatAdmin | minCatThreadPool
)

// Admin — node operations.
const (
	OpNodesInfo           = CatAdmin | minNodesInfo
	OpNodesStats          = CatAdmin | minNodesStats
	OpNodesUsage          = CatAdmin | minNodesUsage
	OpNodesHotThreads     = CatAdmin | minNodesHotThreads
	OpNodesReloadSecurity = CatAdminWrite | minNodesReloadSecurity
)

// Admin — task operations.
const (
	OpTasksList   = CatAdmin | minTasksList
	OpTasksGet    = CatAdmin | minTasksGet
	OpTasksCancel = CatAdminWrite | minTasksCancel
)

// Admin — snapshot operations.
const (
	OpSnapshotCreate     = CatAdminWrite | minSnapshotCreate
	OpSnapshotGet        = CatAdmin | minSnapshotGet
	OpSnapshotDelete     = CatAdminWrite | minSnapshotDelete
	OpSnapshotClone      = CatAdminWrite | minSnapshotClone
	OpSnapshotRestore    = CatAdminWrite | minSnapshotRestore
	OpSnapshotStatus     = CatAdmin | minSnapshotStatus
	OpSnapshotRepoCreate = CatAdminWrite | minSnapshotRepoCreate
	OpSnapshotRepoGet    = CatAdmin | minSnapshotRepoGet
	OpSnapshotRepoDelete = CatAdminWrite | minSnapshotRepoDelete
	OpSnapshotRepoVerify = CatAdminWrite | minSnapshotRepoVerify
	OpSnapshotRepoClean  = CatAdminWrite | minSnapshotRepoCleanup
)

// Admin — script operations.
const (
	OpScriptGet          = CatAdmin | minScriptGet
	OpScriptPut          = CatAdminWrite | minScriptPut
	OpScriptDelete       = CatAdminWrite | minScriptDelete
	OpScriptContext      = CatAdmin | minScriptContext
	OpScriptLanguage     = CatAdmin | minScriptLanguage
	OpScriptPainlessExec = CatAdminWrite | minScriptPainlessExec
)

// Admin — dangling index operations.
const (
	OpDanglingGet    = CatAdmin | minDanglingGet
	OpDanglingDelete = CatAdminWrite | minDanglingDelete
	OpDanglingImport = CatAdminWrite | minDanglingImport
)

// Data stream operations.
const (
	OpDataStreamGet    = CatIndex | minDataStreamGet
	OpDataStreamCreate = CatIndexWrite | minDataStreamCreate
	OpDataStreamDelete = CatIndexWrite | minDataStreamDelete
	OpDataStreamStats  = CatIndex | minDataStreamStats
)

// OpRenderSearchTemplate identifies the render search template operation.
const OpRenderSearchTemplate = CatSearch | minRenderSearchTemplate

// OpPing identifies a cluster ping operation.
const OpPing = CatPing | minPing

// Operation name tokens: the wire label for every [OperationID], returned by
// [OperationID.String]. Naming them as constants keeps each token declared once,
// so sites that must spell the same token (e.g. discovery-flag names in
// feature_config.go, or tests asserting a label) reference the constant instead
// of re-typing the string, and tooling can trace every use back here.
const (
	opNameSearch                     = "search"
	opNameMSearch                    = "msearch"
	opNameCount                      = "count"
	opNameSearchTemplate             = "search_template"
	opNameMSearchTmpl                = "msearch_template"
	opNameValidate                   = "validate"
	opNameRankEval                   = "rank_eval"
	opNameExplain                    = "explain"
	opNameSearchShards               = "search_shards"
	opNameFieldCaps                  = "field_caps"
	opNameScrollGet                  = "scroll_get"
	opNameScrollDelete               = "scroll_delete"
	opNamePITList                    = "pit_list"
	opNamePITCreate                  = "pit_create"
	opNamePITDelete                  = "pit_delete"
	opNameDocGet                     = "doc_get"
	opNameDocExists                  = "doc_exists"
	opNameDocSourceGet               = "doc_source_get"
	opNameDocSourceExist             = "doc_source_exists"
	opNameMGet                       = "mget"
	opNameTermVectors                = "termvectors"
	opNameMTermVectors               = "mtermvectors"
	opNameDocIndex                   = "doc_index"
	opNameDocCreate                  = "doc_create"
	opNameDocUpdate                  = "doc_update"
	opNameDocDelete                  = "doc_delete"
	opNameBulk                       = "bulk"
	opNameBulkStream                 = "bulk_stream"
	opNameReindex                    = "reindex"
	opNameDeleteByQuery              = "delete_by_query"
	opNameUpdateByQuery              = "update_by_query"
	opNameReindexRethrottle          = "reindex_rethrottle"
	opNameDBQRethrottle              = "dbq_rethrottle"
	opNameUBQRethrottle              = "ubq_rethrottle"
	opNameIndexGet                   = "index_get"
	opNameIndexExists                = "index_exists"
	opNameIndexCreate                = "index_create"
	opNameIndexDelete                = "index_delete"
	opNameIndexOpen                  = "index_open"
	opNameIndexClose                 = "index_close"
	opNameIndexClone                 = "index_clone"
	opNameIndexShrink                = "index_shrink"
	opNameIndexSplit                 = "index_split"
	opNameIndexRollover              = "index_rollover"
	opNameIndexBlock                 = "index_block"
	opNameIndexResolve               = "index_resolve"
	opNameIndexAnalyze               = "index_analyze"
	opNameMappingGet                 = "mapping_get"
	opNameMappingPut                 = "mapping_put"
	opNameAliasGet                   = "alias_get"
	opNameAliasPut                   = "alias_put"
	opNameAliasDelete                = "alias_delete"
	opNameCatAliases                 = "cat_aliases"
	opNameIndexTemplateGet           = "index_template_get"
	opNameIndexTemplateCreate        = "index_template_create"
	opNameIndexTemplateDelete        = "index_template_delete"
	opNameIndexTemplateExists        = "index_template_exists"
	opNameIndexTemplateSimulate      = "index_template_simulate"
	opNameIndexTemplateSimulateIndex = "index_template_simulate_index"
	opNameComponentTemplateGet       = "component_template_get"
	opNameComponentTemplateCreate    = "component_template_create"
	opNameComponentTemplateDelete    = "component_template_delete"
	opNameComponentTemplateExists    = "component_template_exists"
	opNameLegacyTemplateGet          = "legacy_template_get"
	opNameLegacyTemplateCreate       = "legacy_template_create"
	opNameLegacyTemplateDelete       = "legacy_template_delete"
	opNameLegacyTemplateExists       = "legacy_template_exists"
	opNameRefresh                    = "refresh"
	opNameFlush                      = "flush"
	opNameForceMerge                 = "forcemerge"
	opNameCacheClear                 = "cache_clear"
	opNameSegments                   = "segments"
	opNameRecovery                   = "recovery"
	opNameShardStores                = "shard_stores"
	opNameStats                      = "stats"
	opNameIngestGet                  = "ingest_get"
	opNameIngestCreate               = "ingest_create"
	opNameIngestDelete               = "ingest_delete"
	opNameIngestSimulate             = "ingest_simulate"
	opNameIngestGrok                 = "ingest_grok"
	opNameClusterInfo                = "cluster_info"
	opNameClusterHealth              = "cluster_health"
	opNameClusterStats               = "cluster_stats"
	opNameClusterState               = "cluster_state"
	opNameClusterSettingsGet         = "cluster_settings_get"
	opNameClusterSettingsPut         = "cluster_settings_put"
	opNameClusterReroute             = "cluster_reroute"
	opNameClusterPendingTasks        = "cluster_pending_tasks"
	opNameClusterAllocExplain        = "cluster_alloc_explain"
	opNameClusterRemoteInfo          = "cluster_remote_info"
	opNameClusterVotingConfigEx      = "cluster_voting_config"
	opNamePing                       = "ping"
	opNameCatAllocation              = "cat_allocation"
	opNameCatClusterMgr              = "cat_cluster_manager"
	opNameCatCount                   = "cat_count"
	opNameCatFielddata               = "cat_fielddata"
	opNameCatHealth                  = "cat_health"
	opNameCatIndices                 = "cat_indices"
	opNameCatMaster                  = "cat_master"
	opNameCatNodeAttrs               = "cat_nodeattrs"
	opNameCatNodes                   = "cat_nodes"
	opNameCatPendingTask             = "cat_pending_tasks"
	opNameCatPlugins                 = "cat_plugins"
	opNameCatRecovery                = "cat_recovery"
	opNameCatRepos                   = "cat_repositories"
	opNameCatSegments                = "cat_segments"
	opNameCatShards                  = "cat_shards"
	opNameCatSnapshots               = "cat_snapshots"
	opNameCatTasks                   = "cat_tasks"
	opNameCatTemplates               = "cat_templates"
	opNameCatThreadPool              = "cat_thread_pool"
	opNameNodesInfo                  = "nodes_info"
	opNameNodesStats                 = "nodes_stats"
	opNameNodesUsage                 = "nodes_usage"
	opNameNodesHotThreads            = "nodes_hot_threads"
	opNameNodesReloadSecurity        = "nodes_reload_secure_settings"
	opNameTasksList                  = "tasks_list"
	opNameTasksGet                   = "tasks_get"
	opNameTasksCancel                = "tasks_cancel"
	opNameSnapshotCreate             = "snapshot_create"
	opNameSnapshotGet                = "snapshot_get"
	opNameSnapshotDelete             = "snapshot_delete"
	opNameSnapshotClone              = "snapshot_clone"
	opNameSnapshotRestore            = "snapshot_restore"
	opNameSnapshotStatus             = "snapshot_status"
	opNameSnapshotRepoCreate         = "snapshot_repo_create"
	opNameSnapshotRepoGet            = "snapshot_repo_get"
	opNameSnapshotRepoDelete         = "snapshot_repo_delete"
	opNameSnapshotRepoVerify         = "snapshot_repo_verify"
	opNameSnapshotRepoClean          = "snapshot_repo_cleanup"
	opNameScriptGet                  = "script_get"
	opNameScriptPut                  = "script_put"
	opNameScriptDelete               = "script_delete"
	opNameScriptContext              = "script_context"
	opNameScriptLanguage             = "script_language"
	opNameScriptPainlessExec         = "script_painless_execute"
	opNameDanglingGet                = "dangling_get"
	opNameDanglingDelete             = "dangling_delete"
	opNameDanglingImport             = "dangling_import"
	opNameDataStreamGet              = "data_stream_get"
	opNameDataStreamCreate           = "data_stream_create"
	opNameDataStreamDelete           = "data_stream_delete"
	opNameDataStreamStats            = "data_stream_stats"
	opNameRenderSearchTemplate       = "render_search_template"
	opNameOther                      = "other"
)

// ---------------------------------------------------------------------------
// String
// ---------------------------------------------------------------------------

//nolint:cyclop,gocyclo,exhaustive,goconst // intentional large switch with default fallback
func (op OperationID) String() string {
	switch op {
	case OpSearch:
		return opNameSearch
	case OpMSearch:
		return opNameMSearch
	case OpCount:
		return opNameCount
	case OpSearchTemplate:
		return opNameSearchTemplate
	case OpMSearchTmpl:
		return opNameMSearchTmpl
	case OpValidate:
		return opNameValidate
	case OpRankEval:
		return opNameRankEval
	case OpExplain:
		return opNameExplain
	case OpSearchShards:
		return opNameSearchShards
	case OpFieldCaps:
		return opNameFieldCaps
	case OpScrollGet:
		return opNameScrollGet
	case OpScrollDelete:
		return opNameScrollDelete
	case OpPITList:
		return opNamePITList
	case OpPITCreate:
		return opNamePITCreate
	case OpPITDelete:
		return opNamePITDelete
	case OpDocGet:
		return opNameDocGet
	case OpDocExists:
		return opNameDocExists
	case OpDocSourceGet:
		return opNameDocSourceGet
	case OpDocSourceExist:
		return opNameDocSourceExist
	case OpMGet:
		return opNameMGet
	case OpTermVectors:
		return opNameTermVectors
	case OpMTermVectors:
		return opNameMTermVectors
	case OpDocIndex:
		return opNameDocIndex
	case OpDocCreate:
		return opNameDocCreate
	case OpDocUpdate:
		return opNameDocUpdate
	case OpDocDelete:
		return opNameDocDelete
	case OpBulk:
		return opNameBulk
	case OpBulkStream:
		return opNameBulkStream
	case OpReindex:
		return opNameReindex
	case OpDeleteByQuery:
		return opNameDeleteByQuery
	case OpUpdateByQuery:
		return opNameUpdateByQuery
	case OpReindexRethrottle:
		return opNameReindexRethrottle
	case OpDBQRethrottle:
		return opNameDBQRethrottle
	case OpUBQRethrottle:
		return opNameUBQRethrottle
	case OpIndexGet:
		return opNameIndexGet
	case OpIndexExists:
		return opNameIndexExists
	case OpIndexCreate:
		return opNameIndexCreate
	case OpIndexDelete:
		return opNameIndexDelete
	case OpIndexOpen:
		return opNameIndexOpen
	case OpIndexClose:
		return opNameIndexClose
	case OpIndexClone:
		return opNameIndexClone
	case OpIndexShrink:
		return opNameIndexShrink
	case OpIndexSplit:
		return opNameIndexSplit
	case OpIndexRollover:
		return opNameIndexRollover
	case OpIndexBlock:
		return opNameIndexBlock
	case OpIndexResolve:
		return opNameIndexResolve
	case OpIndexAnalyze:
		return opNameIndexAnalyze
	case OpMappingGet:
		return opNameMappingGet
	case OpMappingPut:
		return opNameMappingPut
	case OpAliasGet:
		return opNameAliasGet
	case OpAliasPut:
		return opNameAliasPut
	case OpAliasDelete:
		return opNameAliasDelete
	case OpCatAliases:
		return opNameCatAliases
	case OpIndexTemplateGet:
		return opNameIndexTemplateGet
	case OpIndexTemplateCreate:
		return opNameIndexTemplateCreate
	case OpIndexTemplateDelete:
		return opNameIndexTemplateDelete
	case OpIndexTemplateExists:
		return opNameIndexTemplateExists
	case OpIndexTemplateSimulate:
		return opNameIndexTemplateSimulate
	case OpIndexTemplateSimulateIndex:
		return opNameIndexTemplateSimulateIndex
	case OpComponentTemplateGet:
		return opNameComponentTemplateGet
	case OpComponentTemplateCreate:
		return opNameComponentTemplateCreate
	case OpComponentTemplateDelete:
		return opNameComponentTemplateDelete
	case OpComponentTemplateExists:
		return opNameComponentTemplateExists
	case OpLegacyTemplateGet:
		return opNameLegacyTemplateGet
	case OpLegacyTemplateCreate:
		return opNameLegacyTemplateCreate
	case OpLegacyTemplateDelete:
		return opNameLegacyTemplateDelete
	case OpLegacyTemplateExists:
		return opNameLegacyTemplateExists
	case OpRefresh:
		return opNameRefresh
	case OpFlush:
		return opNameFlush
	case OpForceMerge:
		return opNameForceMerge
	case OpCacheClear:
		return opNameCacheClear
	case OpSegments:
		return opNameSegments
	case OpRecovery:
		return opNameRecovery
	case OpShardStores:
		return opNameShardStores
	case OpStats:
		return opNameStats
	case OpIngestGet:
		return opNameIngestGet
	case OpIngestCreate:
		return opNameIngestCreate
	case OpIngestDelete:
		return opNameIngestDelete
	case OpIngestSimulate:
		return opNameIngestSimulate
	case OpIngestGrok:
		return opNameIngestGrok
	case OpClusterInfo:
		return opNameClusterInfo
	case OpClusterHealth:
		return opNameClusterHealth
	case OpClusterStats:
		return opNameClusterStats
	case OpClusterState:
		return opNameClusterState
	case OpClusterSettingsGet:
		return opNameClusterSettingsGet
	case OpClusterSettingsPut:
		return opNameClusterSettingsPut
	case OpClusterReroute:
		return opNameClusterReroute
	case OpClusterPendingTasks:
		return opNameClusterPendingTasks
	case OpClusterAllocExplain:
		return opNameClusterAllocExplain
	case OpClusterRemoteInfo:
		return opNameClusterRemoteInfo
	case OpClusterVotingConfigEx:
		return opNameClusterVotingConfigEx
	case OpPing:
		return opNamePing
	case OpCatAllocation:
		return opNameCatAllocation
	case OpCatClusterMgr:
		return opNameCatClusterMgr
	case OpCatCount:
		return opNameCatCount
	case OpCatFielddata:
		return opNameCatFielddata
	case OpCatHealth:
		return opNameCatHealth
	case OpCatIndices:
		return opNameCatIndices
	case OpCatMaster:
		return opNameCatMaster
	case OpCatNodeAttrs:
		return opNameCatNodeAttrs
	case OpCatNodes:
		return opNameCatNodes
	case OpCatPendingTask:
		return opNameCatPendingTask
	case OpCatPlugins:
		return opNameCatPlugins
	case OpCatRecovery:
		return opNameCatRecovery
	case OpCatRepos:
		return opNameCatRepos
	case OpCatSegments:
		return opNameCatSegments
	case OpCatShards:
		return opNameCatShards
	case OpCatSnapshots:
		return opNameCatSnapshots
	case OpCatTasks:
		return opNameCatTasks
	case OpCatTemplates:
		return opNameCatTemplates
	case OpCatThreadPool:
		return opNameCatThreadPool
	case OpNodesInfo:
		return opNameNodesInfo
	case OpNodesStats:
		return opNameNodesStats
	case OpNodesUsage:
		return opNameNodesUsage
	case OpNodesHotThreads:
		return opNameNodesHotThreads
	case OpNodesReloadSecurity:
		return opNameNodesReloadSecurity
	case OpTasksList:
		return opNameTasksList
	case OpTasksGet:
		return opNameTasksGet
	case OpTasksCancel:
		return opNameTasksCancel
	case OpSnapshotCreate:
		return opNameSnapshotCreate
	case OpSnapshotGet:
		return opNameSnapshotGet
	case OpSnapshotDelete:
		return opNameSnapshotDelete
	case OpSnapshotClone:
		return opNameSnapshotClone
	case OpSnapshotRestore:
		return opNameSnapshotRestore
	case OpSnapshotStatus:
		return opNameSnapshotStatus
	case OpSnapshotRepoCreate:
		return opNameSnapshotRepoCreate
	case OpSnapshotRepoGet:
		return opNameSnapshotRepoGet
	case OpSnapshotRepoDelete:
		return opNameSnapshotRepoDelete
	case OpSnapshotRepoVerify:
		return opNameSnapshotRepoVerify
	case OpSnapshotRepoClean:
		return opNameSnapshotRepoClean
	case OpScriptGet:
		return opNameScriptGet
	case OpScriptPut:
		return opNameScriptPut
	case OpScriptDelete:
		return opNameScriptDelete
	case OpScriptContext:
		return opNameScriptContext
	case OpScriptLanguage:
		return opNameScriptLanguage
	case OpScriptPainlessExec:
		return opNameScriptPainlessExec
	case OpDanglingGet:
		return opNameDanglingGet
	case OpDanglingDelete:
		return opNameDanglingDelete
	case OpDanglingImport:
		return opNameDanglingImport
	case OpDataStreamGet:
		return opNameDataStreamGet
	case OpDataStreamCreate:
		return opNameDataStreamCreate
	case OpDataStreamDelete:
		return opNameDataStreamDelete
	case OpDataStreamStats:
		return opNameDataStreamStats
	case OpRenderSearchTemplate:
		return opNameRenderSearchTemplate
	case OpOther:
		return opNameOther
	}

	// Fallback: category name + numeric minor
	cat := op.Category()
	minor := op.Minor()

	var catName string
	switch cat {
	case CatSearch:
		catName = "search"
	case CatScroll:
		catName = "scroll"
	case CatScrollWrite:
		catName = "scroll_write"
	case CatPIT:
		catName = "pit"
	case CatPITWrite:
		catName = "pit_write"
	case CatDocRead:
		catName = "doc_read"
	case CatDocWrite:
		catName = "doc_write"
	case CatBulk:
		catName = "bulk"
	case CatIndex:
		catName = "index"
	case CatIndexWrite:
		catName = "index_write"
	case CatMapping:
		catName = "mapping"
	case CatMappingWrite:
		catName = "mapping_write"
	case CatAlias:
		catName = "alias"
	case CatAliasWrite:
		catName = "alias_write"
	case CatTemplate:
		catName = "template"
	case CatTemplateWrite:
		catName = "template_write"
	case CatMaint:
		catName = "maint"
	case CatMaintWrite:
		catName = "maint_write"
	case CatIngest:
		catName = "ingest"
	case CatIngestWrite:
		catName = "ingest_write"
	case CatCluster:
		catName = "cluster"
	case CatClusterWrite:
		catName = "cluster_write"
	case CatAdmin:
		catName = "admin"
	case CatAdminWrite:
		catName = "admin_write"
	case CatPing:
		catName = "ping"
	default:
		catName = "unknown"
	}

	return catName + "_" + strconv.FormatInt(int64(minor), 10)
}
