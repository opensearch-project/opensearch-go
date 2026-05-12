// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import "path"

// TestFlag is a bitfield controlling generated integration test behavior.
// Flags are resolved at code-generation time and baked into generated tests.
type TestFlag uint32

const (
	// TestSkip causes the test to be skipped entirely with a reason message.
	// When paired with a Version constraint, emits a runtime SkipIfVersion guard.
	// Without a Version, emits an unconditional t.Skip().
	TestSkip TestFlag = 1 << iota

	// TestWaitReady inserts a cluster readiness poll (all nodes reporting stats)
	// before the test operation executes. Prevents flakes from nodes that haven't
	// fully initialized in CI.
	TestWaitReady
)

// TestRule matches operations by name pattern and/or version constraint.
// Non-empty fields use AND logic: all non-empty conditions must be satisfied.
type TestRule struct {
	// NamePattern matches on the operation group name. Supports path.Match glob
	// syntax (e.g. "dangling_indices.*", "cat.*"). Empty matches all operations.
	NamePattern string

	// Version is the minimum OpenSearch version required for this test
	// to pass (e.g. "2.12"). Paired with the implicit "<" operator, the
	// generator emits SkipIfVersion(t, client, "<", Version, ...). Only
	// meaningful with TestSkip. Empty means no version constraint.
	Version string

	// Flags is the bitfield of actions to apply when this rule matches.
	Flags TestFlag

	// Reason is a human-readable explanation used in skip messages and logs.
	Reason string
}

// TestRules defines the complete set of integration test behavior overrides.
// Rules are evaluated in order; multiple rules can match the same operation
// and their flags are OR'd together.
//
//nolint:gochecknoglobals // Declarative configuration table evaluated at code-gen time.
var TestRules = []TestRule{
	// --- Version-gated skips (server bugs fixed in later releases) ---

	// DELETE /_search/point_in_time/_all returns a malformed error body (HTTP 200
	// with {"error":{...}}) when no PITs exist. Fixed in 2.12 by
	// opensearch-project/OpenSearch#11711 (backport #11713).
	{NamePattern: "delete_all_pits", Version: "2.12", Flags: TestSkip,
		Reason: "malformed error body when no PITs exist (OpenSearch#11711)"},

	// k-NN plugin's KNNScoringScriptEngine.getSupportedContexts() returned null,
	// causing NPE in ScriptService.getScriptLanguages() (opensearch-project/k-NN#560).
	{NamePattern: "get_script_languages", Version: "2.4", Flags: TestSkip,
		Reason: "KNN plugin NPE in getSupportedContexts (k-NN#560)"},

	// --- Unconditional skips (require infrastructure not in test clusters) ---

	{NamePattern: "dangling_indices.*", Flags: TestSkip,
		Reason: "requires dangling index state from node failure"},
	{NamePattern: "snapshot.*", Flags: TestSkip,
		Reason: "requires snapshot repository configuration"},
	{NamePattern: "reindex", Flags: TestSkip,
		Reason: "requires source index with documents"},
	{NamePattern: "reindex_rethrottle", Flags: TestSkip,
		Reason: "requires active reindex task"},
	{NamePattern: "tasks.*", Flags: TestSkip,
		Reason: "requires active long-running task"},
	{NamePattern: "bulk", Flags: TestSkip,
		Reason: "requires NDJSON body with action metadata"},
	{NamePattern: "bulk_stream", Flags: TestSkip,
		Reason: "requires NDJSON body with action metadata"},
	{NamePattern: "msearch", Flags: TestSkip,
		Reason: "requires NDJSON multi-search body"},
	{NamePattern: "msearch_template", Flags: TestSkip,
		Reason: "requires NDJSON multi-search body"},
	{NamePattern: "scroll", Flags: TestSkip,
		Reason: "requires active scroll context"},
	{NamePattern: "clear_scroll", Flags: TestSkip,
		Reason: "requires active scroll context"},
	{NamePattern: "delete_pit", Flags: TestSkip,
		Reason: "requires active point-in-time context"},
	{NamePattern: "rank_eval", Flags: TestSkip,
		Reason: "requires rank evaluation body with rated documents"},
	{NamePattern: "scripts_painless.execute", Flags: TestSkip,
		Reason: "requires painless script body"},
	{NamePattern: "scripts_painless_execute", Flags: TestSkip,
		Reason: "requires painless script body"},
	{NamePattern: "render_search_template", Flags: TestSkip,
		Reason: "requires search template body"},
	{NamePattern: "termvectors", Flags: TestSkip,
		Reason: "requires index with term vectors enabled"},
	{NamePattern: "mtermvectors", Flags: TestSkip,
		Reason: "requires index with term vectors enabled"},
	{NamePattern: "field_caps", Flags: TestSkip,
		Reason: "requires index with mapped fields"},
	{NamePattern: "indices.clone", Flags: TestSkip,
		Reason: "requires read-only source index"},
	{NamePattern: "indices.split", Flags: TestSkip,
		Reason: "requires source index with number_of_routing_shards > number_of_shards"},
	{NamePattern: "indices.shrink", Flags: TestSkip,
		Reason: "requires source index on single node with read-only"},
	{NamePattern: "cluster.allocation_explain", Flags: TestSkip,
		Reason: "requires unassigned shards"},
	{NamePattern: "cluster.post_voting_config_exclusions", Flags: TestSkip,
		Reason: "requires multi-node cluster with voting configuration"},
	{NamePattern: "cluster.delete_voting_config_exclusions", Flags: TestSkip,
		Reason: "requires multi-node cluster with voting configuration"},
	{NamePattern: "cluster.put_decommission_awareness", Flags: TestSkip,
		Reason: "requires awareness attributes configured"},
	{NamePattern: "cluster.delete_decommission_awareness", Flags: TestSkip,
		Reason: "requires awareness attributes configured"},
	{NamePattern: "cluster.get_decommission_awareness", Flags: TestSkip,
		Reason: "requires awareness attributes configured"},
	{NamePattern: "cluster.put_weighted_routing", Flags: TestSkip,
		Reason: "requires awareness attributes and weighted routing setup"},
	{NamePattern: "cluster.get_weighted_routing", Flags: TestSkip,
		Reason: "requires awareness attributes and weighted routing setup"},
	{NamePattern: "cluster.delete_weighted_routing", Flags: TestSkip,
		Reason: "requires awareness attributes and weighted routing setup"},
	{NamePattern: "remote_store.restore", Flags: TestSkip,
		Reason: "requires remote store configuration"},
	{NamePattern: "_core.create", Flags: TestSkip,
		Reason: "op_type=create conflicts with doc fixture"},
	{NamePattern: "create", Flags: TestSkip,
		Reason: "op_type=create conflicts with doc fixture"},
	{NamePattern: "indices.add_block", Flags: TestSkip,
		Reason: "requires valid block name (write/read/read_only/metadata)"},
	{NamePattern: "indices.create_data_stream", Flags: TestSkip,
		Reason: "requires matching index template with data_stream"},
	{NamePattern: "indices.delete_data_stream", Flags: TestSkip,
		Reason: "requires existing data stream"},
	{NamePattern: "delete_by_query_rethrottle", Flags: TestSkip,
		Reason: "requires active long-running query task"},
	{NamePattern: "update_by_query_rethrottle", Flags: TestSkip,
		Reason: "requires active long-running query task"},
	{NamePattern: "nodes.hot_threads", Flags: TestSkip,
		Reason: "returns plain text, not JSON"},
	{NamePattern: "cat.all_pit_segments", Flags: TestSkip,
		Reason: "requires active point-in-time context"},
	{NamePattern: "cat.pit_segments", Flags: TestSkip,
		Reason: "requires active point-in-time context"},
	{NamePattern: "cat.segment_replication", Flags: TestSkip,
		Reason: "requires segment replication enabled"},
	{NamePattern: "cat.help", Flags: TestSkip,
		Reason: "returns plain text, not JSON"},
	{NamePattern: "search_pipeline.get", Flags: TestSkip,
		Reason: "requires search pipeline to exist"},
	{NamePattern: "search_pipeline.delete", Flags: TestSkip,
		Reason: "requires search pipeline to exist"},
	{NamePattern: "cat.snapshots", Flags: TestSkip,
		Reason: "requires snapshot repository configuration"},
	{NamePattern: "list.help", Flags: TestSkip,
		Reason: "returns plain text, not JSON"},
	{NamePattern: "list.*", Flags: TestSkip,
		Reason: "response struct does not match cat-style response format"},

	// Plugin operations requiring complex external state.
	{NamePattern: "asynchronous_search.delete", Flags: TestSkip,
		Reason: "requires async search ID from submit"},
	{NamePattern: "asynchronous_search.get", Flags: TestSkip,
		Reason: "requires async search ID from submit"},
	{NamePattern: "asynchronous_search.stats", Flags: TestSkip,
		Reason: "response struct does not match actual response format"},
	{NamePattern: "asynchronous_search.search", Flags: TestSkip,
		Reason: "response struct does not match actual response format"},
	{NamePattern: "flow_framework*", Flags: TestSkip,
		Reason: "requires workflow ID from prior create"},
	{NamePattern: "geospatial*", Flags: TestSkip,
		Reason: "requires IP2Geo datasource or external network access"},
	{NamePattern: "insights.top_queries", Flags: TestSkip,
		Reason: "requires valid metric type (cpu, memory, latency)"},
	{NamePattern: "ism.add_policy", Flags: TestSkip,
		Reason: "requires policy_id in request body"},
	{NamePattern: "ism.change_policy", Flags: TestSkip,
		Reason: "requires policy_id in request body"},
	{NamePattern: "ism.remove_policy", Flags: TestSkip,
		Reason: "requires index with ISM policy attached"},
	{NamePattern: "ism.retry_index", Flags: TestSkip,
		Reason: "requires index with ISM policy attached"},
	{NamePattern: "ism.put_policy", Flags: TestSkip,
		Reason: "requires valid ISM policy document body"},
	{NamePattern: "ism.put_policies", Flags: TestSkip,
		Reason: "requires valid ISM policy document body"},
	{NamePattern: "ism.delete_policy", Flags: TestSkip,
		Reason: "requires existing ISM policy"},
	{NamePattern: "ism.exists_policy", Flags: TestSkip,
		Reason: "requires existing ISM policy"},
	{NamePattern: "ism.get_policy", Flags: TestSkip,
		Reason: "requires existing ISM policy"},
	{NamePattern: "ism.explain_policy", Flags: TestSkip,
		Reason: "response struct does not match actual response format"},
	{NamePattern: "ism.refresh_search_analyzers", Flags: TestSkip,
		Reason: "response struct does not match actual response format"},
	{NamePattern: "ism.get_policies", Flags: TestSkip,
		Reason: "response struct does not match actual response format"},
	{NamePattern: "knn*", Flags: TestSkip,
		Reason: "requires KNN model training data and infrastructure"},
	{NamePattern: "ltr*", Flags: TestSkip,
		Reason: "requires LTR feature store"},
	{NamePattern: "ml*", Flags: TestSkip,
		Reason: "requires ML model or connector registration"},
	{NamePattern: "neural*", Flags: TestSkip,
		Reason: "requires deployed neural search model"},
	{NamePattern: "notifications*", Flags: TestSkip,
		Reason: "requires notification channel or config"},
	{NamePattern: "observability*", Flags: TestSkip,
		Reason: "requires observability saved object"},
	{NamePattern: "ppl*", Flags: TestSkip,
		Reason: "requires valid PPL query body"},
	{NamePattern: "sql*", Flags: TestSkip,
		Reason: "requires valid SQL query body"},
	{NamePattern: "query*", Flags: TestSkip,
		Reason: "requires valid query DSL body"},
	{NamePattern: "replication*", Flags: TestSkip,
		Reason: "requires cross-cluster replication setup"},
	{NamePattern: "rollups*", Flags: TestSkip,
		Reason: "requires rollup job configuration"},
	{NamePattern: "security*", Flags: TestSkip,
		Reason: "requires security plugin resources"},
	{NamePattern: "sm*", Flags: TestSkip,
		Reason: "requires snapshot management policy"},
	{NamePattern: "transforms*", Flags: TestSkip,
		Reason: "requires transform job configuration"},
	{NamePattern: "ubi*", Flags: TestSkip,
		Reason: "requires UBI plugin initialization"},
	{NamePattern: "ingestion*", Flags: TestSkip,
		Reason: "requires index with pull-based ingestion source configured"},
	{NamePattern: "search_relevance*", Flags: TestSkip,
		Reason: "requires search relevance resources (experiments, query sets)"},
	{NamePattern: "wlm*", Flags: TestSkip,
		Reason: "requires workload management query group"},

	// --- Readiness gates (transient CI startup issues) ---

	{NamePattern: "cat.nodes", Flags: TestWaitReady,
		Reason: "node metrics null until all nodes report stats"},
	{NamePattern: "nodes.stats", Flags: TestWaitReady,
		Reason: "transient node failures during cluster startup"},
	{NamePattern: "nodes.info", Flags: TestWaitReady,
		Reason: "node info incomplete during startup"},
}

// MatchRules evaluates all rules against the given operation group and returns
// the combined flags and the first skip reason encountered.
func MatchRules(group string) (flags TestFlag, skipReason, skipVersion string) {
	for i := range TestRules {
		rule := &TestRules[i]
		if !ruleMatchesName(rule.NamePattern, group) {
			continue
		}
		flags |= rule.Flags
		if rule.Flags&TestSkip != 0 && skipReason == "" {
			skipReason = rule.Reason
			skipVersion = rule.Version
		}
	}
	return flags, skipReason, skipVersion
}

// ruleMatchesName checks if pattern matches group using path.Match glob syntax.
// Empty pattern matches everything.
func ruleMatchesName(pattern, group string) bool {
	if pattern == "" {
		return true
	}
	matched, err := path.Match(pattern, group)
	if err != nil {
		return pattern == group
	}
	return matched
}
