// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// TestOperationID_StringExhaustive locks the wire-level identifier each
// OperationID emits via String(). Telemetry, structured logs, and the
// metrics layer key off these strings, so any rename or accidental
// reordering of constants would silently shift dashboards and alerts
// onto a different label. The table below is the contract.
//
// When a new Op* constant lands, add it here AND to the matching
// case in operation.go's String() switch. Codecov picked up the gap
// pre-merge: 145 unexecuted case branches in String() came from this
// test's predecessor only listing ~50 of the 140+ named ops.
func TestOperationID_StringExhaustive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op   opensearchtransport.OperationID
		want string
	}{
		// Search
		{opensearchtransport.OpSearch, "search"},
		{opensearchtransport.OpMSearch, "msearch"},
		{opensearchtransport.OpCount, "count"},
		{opensearchtransport.OpSearchTemplate, "search_template"},
		{opensearchtransport.OpMSearchTmpl, "msearch_template"},
		{opensearchtransport.OpValidate, "validate"},
		{opensearchtransport.OpRankEval, "rank_eval"},
		{opensearchtransport.OpExplain, "explain"},
		{opensearchtransport.OpSearchShards, "search_shards"},
		{opensearchtransport.OpFieldCaps, "field_caps"},

		// Scroll
		{opensearchtransport.OpScrollGet, "scroll_get"},
		{opensearchtransport.OpScrollDelete, "scroll_delete"},

		// PIT
		{opensearchtransport.OpPITList, "pit_list"},
		{opensearchtransport.OpPITCreate, "pit_create"},
		{opensearchtransport.OpPITDelete, "pit_delete"},

		// Document read
		{opensearchtransport.OpDocGet, "doc_get"},
		{opensearchtransport.OpDocExists, "doc_exists"},
		{opensearchtransport.OpDocSourceGet, "doc_source_get"},
		{opensearchtransport.OpDocSourceExist, "doc_source_exists"},
		{opensearchtransport.OpMGet, "mget"},
		{opensearchtransport.OpTermVectors, "termvectors"},
		{opensearchtransport.OpMTermVectors, "mtermvectors"},

		// Document write
		{opensearchtransport.OpDocIndex, "doc_index"},
		{opensearchtransport.OpDocCreate, "doc_create"},
		{opensearchtransport.OpDocUpdate, "doc_update"},
		{opensearchtransport.OpDocDelete, "doc_delete"},

		// Bulk
		{opensearchtransport.OpBulk, "bulk"},
		{opensearchtransport.OpBulkStream, "bulk_stream"},
		{opensearchtransport.OpReindex, "reindex"},
		{opensearchtransport.OpDeleteByQuery, "delete_by_query"},
		{opensearchtransport.OpUpdateByQuery, "update_by_query"},
		{opensearchtransport.OpReindexRethrottle, "reindex_rethrottle"},
		{opensearchtransport.OpDBQRethrottle, "dbq_rethrottle"},
		{opensearchtransport.OpUBQRethrottle, "ubq_rethrottle"},

		// Index management
		{opensearchtransport.OpIndexGet, "index_get"},
		{opensearchtransport.OpIndexExists, "index_exists"},
		{opensearchtransport.OpIndexCreate, "index_create"},
		{opensearchtransport.OpIndexDelete, "index_delete"},
		{opensearchtransport.OpIndexOpen, "index_open"},
		{opensearchtransport.OpIndexClose, "index_close"},
		{opensearchtransport.OpIndexClone, "index_clone"},
		{opensearchtransport.OpIndexShrink, "index_shrink"},
		{opensearchtransport.OpIndexSplit, "index_split"},
		{opensearchtransport.OpIndexRollover, "index_rollover"},
		{opensearchtransport.OpIndexBlock, "index_block"},
		{opensearchtransport.OpIndexResolve, "index_resolve"},
		{opensearchtransport.OpIndexAnalyze, "index_analyze"},

		// Mapping
		{opensearchtransport.OpMappingGet, "mapping_get"},
		{opensearchtransport.OpMappingPut, "mapping_put"},

		// Alias
		{opensearchtransport.OpAliasGet, "alias_get"},
		{opensearchtransport.OpAliasPut, "alias_put"},
		{opensearchtransport.OpAliasDelete, "alias_delete"},
		{opensearchtransport.OpCatAliases, "cat_aliases"},

		// Template
		{opensearchtransport.OpIndexTemplateGet, "index_template_get"},
		{opensearchtransport.OpIndexTemplateCreate, "index_template_create"},
		{opensearchtransport.OpIndexTemplateDelete, "index_template_delete"},
		{opensearchtransport.OpIndexTemplateExists, "index_template_exists"},
		{opensearchtransport.OpIndexTemplateSimulate, "index_template_simulate"},
		{opensearchtransport.OpIndexTemplateSimulateIndex, "index_template_simulate_index"},
		{opensearchtransport.OpComponentTemplateGet, "component_template_get"},
		{opensearchtransport.OpComponentTemplateCreate, "component_template_create"},
		{opensearchtransport.OpComponentTemplateDelete, "component_template_delete"},
		{opensearchtransport.OpComponentTemplateExists, "component_template_exists"},
		{opensearchtransport.OpLegacyTemplateGet, "legacy_template_get"},
		{opensearchtransport.OpLegacyTemplateCreate, "legacy_template_create"},
		{opensearchtransport.OpLegacyTemplateDelete, "legacy_template_delete"},
		{opensearchtransport.OpLegacyTemplateExists, "legacy_template_exists"},

		// Maintenance
		{opensearchtransport.OpRefresh, "refresh"},
		{opensearchtransport.OpFlush, "flush"},
		{opensearchtransport.OpForceMerge, "forcemerge"},
		{opensearchtransport.OpCacheClear, "cache_clear"},
		{opensearchtransport.OpSegments, "segments"},
		{opensearchtransport.OpRecovery, "recovery"},
		{opensearchtransport.OpShardStores, "shard_stores"},
		{opensearchtransport.OpStats, "stats"},

		// Ingest
		{opensearchtransport.OpIngestGet, "ingest_get"},
		{opensearchtransport.OpIngestCreate, "ingest_create"},
		{opensearchtransport.OpIngestDelete, "ingest_delete"},
		{opensearchtransport.OpIngestSimulate, "ingest_simulate"},
		{opensearchtransport.OpIngestGrok, "ingest_grok"},

		// Cluster
		{opensearchtransport.OpClusterInfo, "cluster_info"},
		{opensearchtransport.OpClusterHealth, "cluster_health"},
		{opensearchtransport.OpClusterStats, "cluster_stats"},
		{opensearchtransport.OpClusterState, "cluster_state"},
		{opensearchtransport.OpClusterSettingsGet, "cluster_settings_get"},
		{opensearchtransport.OpClusterSettingsPut, "cluster_settings_put"},
		{opensearchtransport.OpClusterReroute, "cluster_reroute"},
		{opensearchtransport.OpClusterPendingTasks, "cluster_pending_tasks"},
		{opensearchtransport.OpClusterAllocExplain, "cluster_alloc_explain"},
		{opensearchtransport.OpClusterRemoteInfo, "cluster_remote_info"},
		{opensearchtransport.OpClusterVotingConfigEx, "cluster_voting_config"},

		// Ping
		{opensearchtransport.OpPing, "ping"},

		// OpOther
		{opensearchtransport.OpOther, "other"},
	}

	for _, tc := range cases {
		got := tc.op.String()
		require.Equalf(t, tc.want, got,
			"OperationID(%#x).String() = %q, want %q", int64(tc.op), got, tc.want)
	}

	// Uniqueness across the named set: a duplicate string would silently
	// merge two ops in metrics. Re-check here so a future copy/paste
	// renaming bug surfaces.
	seen := make(map[string]opensearchtransport.OperationID, len(cases))
	for _, tc := range cases {
		if prev, dup := seen[tc.want]; dup {
			t.Fatalf("String() collision: %s and %s both return %q", prev, tc.op, tc.want)
		}
		seen[tc.want] = tc.op
	}
}

// TestOperationID_StringFallback exercises the second switch in
// operation.go's String() -- the one reached when an OperationID's full
// value isn't in the named-constant table but its category is. Important
// for forward compatibility: a newly added op (with a Minor not yet
// listed in String()'s primary switch) must still produce a meaningful
// "<category>_<minor>" label rather than an empty string or panic.
//
// Constructing synthetic ops with Minor=255 (well above any current
// per-category minor count) forces every category branch in the
// fallback switch to execute.
func TestOperationID_StringFallback(t *testing.T) {
	t.Parallel()

	const syntheticMinor opensearchtransport.OperationID = 255

	// Every category constant the package exports gets a fallback string
	// in operation.go. The expected prefix is the category name; the
	// suffix is "_<minor>". If a category's branch is missing, the
	// outer default returns "unknown_<minor>".
	cases := []struct {
		name     string
		category opensearchtransport.OperationID
		prefix   string
	}{
		{"CatSearch", opensearchtransport.CatSearch, "search_"},
		{"CatScroll", opensearchtransport.CatScroll, "scroll_"},
		{"CatScrollWrite", opensearchtransport.CatScrollWrite, "scroll_write_"},
		{"CatPIT", opensearchtransport.CatPIT, "pit_"},
		{"CatPITWrite", opensearchtransport.CatPITWrite, "pit_write_"},
		{"CatDocRead", opensearchtransport.CatDocRead, "doc_read_"},
		{"CatDocWrite", opensearchtransport.CatDocWrite, "doc_write_"},
		{"CatBulk", opensearchtransport.CatBulk, "bulk_"},
		{"CatIndex", opensearchtransport.CatIndex, "index_"},
		{"CatIndexWrite", opensearchtransport.CatIndexWrite, "index_write_"},
		{"CatMapping", opensearchtransport.CatMapping, "mapping_"},
		{"CatMappingWrite", opensearchtransport.CatMappingWrite, "mapping_write_"},
		{"CatAlias", opensearchtransport.CatAlias, "alias_"},
		{"CatAliasWrite", opensearchtransport.CatAliasWrite, "alias_write_"},
		{"CatTemplate", opensearchtransport.CatTemplate, "template_"},
		{"CatTemplateWrite", opensearchtransport.CatTemplateWrite, "template_write_"},
		{"CatMaint", opensearchtransport.CatMaint, "maint_"},
		{"CatMaintWrite", opensearchtransport.CatMaintWrite, "maint_write_"},
		{"CatIngest", opensearchtransport.CatIngest, "ingest_"},
		{"CatIngestWrite", opensearchtransport.CatIngestWrite, "ingest_write_"},
		{"CatCluster", opensearchtransport.CatCluster, "cluster_"},
		{"CatClusterWrite", opensearchtransport.CatClusterWrite, "cluster_write_"},
		{"CatAdmin", opensearchtransport.CatAdmin, "admin_"},
		{"CatAdminWrite", opensearchtransport.CatAdminWrite, "admin_write_"},
		{"CatPing", opensearchtransport.CatPing, "ping_"},
	}

	want := "_" + strconv.FormatInt(int64(syntheticMinor), 10)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := tc.category | syntheticMinor
			got := op.String()
			require.Equalf(t, tc.prefix+want[1:], got,
				"category %s | minor %d -> %q", tc.name, syntheticMinor, got)
		})
	}

	// An OperationID with a category outside the known set must
	// fall through to the "unknown_<minor>" branch. The category
	// 0x4F00... is intentionally far from any defined slot.
	t.Run("unknown_category", func(t *testing.T) {
		t.Parallel()
		const unknownCat opensearchtransport.OperationID = 0x4F << 56
		op := unknownCat | syntheticMinor
		got := op.String()
		require.Equal(t, "unknown_255", got,
			"unknown category should yield unknown_<minor>")
	})
}

// TestOperationID_MaskingExhaustive covers IsRead / IsWrite / Category
// against a broad set of named ops -- complement to the smoke-test
// version in classify_test.go that only checks a handful. Read- and
// write-side constants of the same Cat<X> share the lower category
// bits but differ on bit 63, and a regression in the bit layout would
// silently misroute ops in the policy layer (writes treated as reads
// or vice versa).
func TestOperationID_MaskingExhaustive(t *testing.T) {
	t.Parallel()

	readOps := []opensearchtransport.OperationID{
		opensearchtransport.OpSearch,
		opensearchtransport.OpDocGet,
		opensearchtransport.OpClusterInfo,
		opensearchtransport.OpCatNodes,
		opensearchtransport.OpPing,
		opensearchtransport.OpScrollGet,
		opensearchtransport.OpPITList,
		opensearchtransport.OpIndexGet,
		opensearchtransport.OpMappingGet,
		opensearchtransport.OpAliasGet,
		opensearchtransport.OpIndexTemplateGet,
		opensearchtransport.OpSegments,
		opensearchtransport.OpIngestGet,
	}
	for _, op := range readOps {
		require.Truef(t, op.IsRead(), "%s should be a read op", op)
		require.Falsef(t, op.IsWrite(), "%s should not be a write op", op)
	}

	writeOps := []opensearchtransport.OperationID{
		opensearchtransport.OpDocIndex,
		opensearchtransport.OpDocDelete,
		opensearchtransport.OpDocUpdate,
		opensearchtransport.OpBulk,
		opensearchtransport.OpReindex,
		opensearchtransport.OpDeleteByQuery,
		opensearchtransport.OpScrollDelete,
		opensearchtransport.OpPITCreate,
		opensearchtransport.OpPITDelete,
		opensearchtransport.OpIndexCreate,
		opensearchtransport.OpIndexDelete,
		opensearchtransport.OpMappingPut,
		opensearchtransport.OpAliasPut,
		opensearchtransport.OpAliasDelete,
		opensearchtransport.OpClusterSettingsPut,
		opensearchtransport.OpClusterReroute,
		opensearchtransport.OpRefresh,
		opensearchtransport.OpFlush,
		opensearchtransport.OpForceMerge,
		opensearchtransport.OpIngestCreate,
		opensearchtransport.OpIngestDelete,
		opensearchtransport.OpIndexTemplateCreate,
		opensearchtransport.OpDataStreamCreate,
		opensearchtransport.OpNodesReloadSecurity,
		opensearchtransport.OpTasksCancel,
		opensearchtransport.OpSnapshotCreate,
		opensearchtransport.OpScriptPut,
		opensearchtransport.OpDanglingDelete,
	}
	for _, op := range writeOps {
		require.Truef(t, op.IsWrite(), "%s should be a write op", op)
		require.Falsef(t, op.IsRead(), "%s should not be a read op", op)
	}

	// Read/write-paired categories must share a Category().
	pairs := []struct {
		read, write opensearchtransport.OperationID
	}{
		{opensearchtransport.CatScroll, opensearchtransport.CatScrollWrite},
		{opensearchtransport.CatPIT, opensearchtransport.CatPITWrite},
		{opensearchtransport.CatDocRead, opensearchtransport.CatDocWrite},
		{opensearchtransport.CatIndex, opensearchtransport.CatIndexWrite},
		{opensearchtransport.CatMapping, opensearchtransport.CatMappingWrite},
		{opensearchtransport.CatAlias, opensearchtransport.CatAliasWrite},
		{opensearchtransport.CatTemplate, opensearchtransport.CatTemplateWrite},
		{opensearchtransport.CatMaint, opensearchtransport.CatMaintWrite},
		{opensearchtransport.CatIngest, opensearchtransport.CatIngestWrite},
		{opensearchtransport.CatCluster, opensearchtransport.CatClusterWrite},
		{opensearchtransport.CatAdmin, opensearchtransport.CatAdminWrite},
	}
	for _, p := range pairs {
		require.Falsef(t, p.read.IsWrite(),
			"read category %s should not have R/W bit set", p.read)
		require.Truef(t, p.write.IsWrite(),
			"write category %s should have R/W bit set", p.write)
	}
}

// TestOperationID_AdminOps_String covers the admin-category String
// branches not exercised by the named-table test. The admin ops
// (cat/nodes/snapshot/script/dangling/data-stream) currently fall to
// the fallback path in operation.go's String() because they don't
// have explicit case branches in the primary switch. This test
// documents the resulting strings (category_<minor>) so a future
// rearrangement that promotes one to the primary switch -- and
// changes its label -- breaks here loudly rather than silently
// reshaping telemetry.
func TestOperationID_AdminOps_String(t *testing.T) {
	t.Parallel()

	// Each entry: op, expected string. Strings come from the fallback
	// path: catName + "_" + strconv(minor).
	cases := []struct {
		op   opensearchtransport.OperationID
		want string
	}{
		// Cat
		{opensearchtransport.OpCatAllocation, "admin_1"},
		{opensearchtransport.OpCatClusterMgr, "admin_2"},
		{opensearchtransport.OpCatCount, "admin_3"},
		{opensearchtransport.OpCatFielddata, "admin_4"},
		{opensearchtransport.OpCatHealth, "admin_5"},
		{opensearchtransport.OpCatIndices, "admin_6"},
		{opensearchtransport.OpCatMaster, "admin_7"},
		{opensearchtransport.OpCatNodeAttrs, "admin_8"},
		{opensearchtransport.OpCatNodes, "admin_9"},
		{opensearchtransport.OpCatPendingTask, "admin_10"},
		{opensearchtransport.OpCatPlugins, "admin_11"},
		{opensearchtransport.OpCatRecovery, "admin_12"},
		{opensearchtransport.OpCatRepos, "admin_13"},
		{opensearchtransport.OpCatSegments, "admin_14"},
		{opensearchtransport.OpCatShards, "admin_15"},
		{opensearchtransport.OpCatSnapshots, "admin_16"},
		{opensearchtransport.OpCatTasks, "admin_17"},
		{opensearchtransport.OpCatTemplates, "admin_18"},
		{opensearchtransport.OpCatThreadPool, "admin_19"},

		// Nodes
		{opensearchtransport.OpNodesInfo, "admin_20"},
		{opensearchtransport.OpNodesStats, "admin_21"},
		{opensearchtransport.OpNodesUsage, "admin_22"},
		{opensearchtransport.OpNodesHotThreads, "admin_23"},
		{opensearchtransport.OpNodesReloadSecurity, "admin_write_24"},

		// Tasks
		{opensearchtransport.OpTasksList, "admin_25"},
		{opensearchtransport.OpTasksGet, "admin_26"},
		{opensearchtransport.OpTasksCancel, "admin_write_27"},

		// Snapshots
		{opensearchtransport.OpSnapshotCreate, "admin_write_28"},
		{opensearchtransport.OpSnapshotGet, "admin_29"},
		{opensearchtransport.OpSnapshotDelete, "admin_write_30"},
		{opensearchtransport.OpSnapshotClone, "admin_write_31"},
		{opensearchtransport.OpSnapshotRestore, "admin_write_32"},
		{opensearchtransport.OpSnapshotStatus, "admin_33"},
		{opensearchtransport.OpSnapshotRepoCreate, "admin_write_34"},
		{opensearchtransport.OpSnapshotRepoGet, "admin_35"},
		{opensearchtransport.OpSnapshotRepoDelete, "admin_write_36"},
		{opensearchtransport.OpSnapshotRepoVerify, "admin_write_37"},
		{opensearchtransport.OpSnapshotRepoClean, "admin_write_38"},

		// Scripts
		{opensearchtransport.OpScriptGet, "admin_39"},
		{opensearchtransport.OpScriptPut, "admin_write_40"},
		{opensearchtransport.OpScriptDelete, "admin_write_41"},
		{opensearchtransport.OpScriptContext, "admin_42"},
		{opensearchtransport.OpScriptLanguage, "admin_43"},
		{opensearchtransport.OpScriptPainlessExec, "admin_write_44"},

		// Dangling
		{opensearchtransport.OpDanglingGet, "admin_45"},
		{opensearchtransport.OpDanglingDelete, "admin_write_46"},
		{opensearchtransport.OpDanglingImport, "admin_write_47"},

		// Data stream (in CatIndex/CatIndexWrite, not CatAdmin)
		{opensearchtransport.OpDataStreamGet, "index_48"},
		{opensearchtransport.OpDataStreamCreate, "index_write_49"},
		{opensearchtransport.OpDataStreamDelete, "index_write_50"},
		{opensearchtransport.OpDataStreamStats, "index_51"},

		// Render search template (in CatSearch)
		{opensearchtransport.OpRenderSearchTemplate, "search_52"},
	}

	for _, tc := range cases {
		got := tc.op.String()
		require.Equalf(t, tc.want, got,
			"OperationID(%#x).String() = %q, want %q",
			int64(tc.op), got, tc.want)
	}
}
