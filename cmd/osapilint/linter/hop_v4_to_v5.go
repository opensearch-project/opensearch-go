// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"slices"
	"strings"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// hop_v4_to_v5.go is the hand-authored v4 -> v5 migration data: the human
// judgment that cannot be auto-derived from the type surfaces. It is the
// canonical reference for a hop; new transitions follow the same shape in their
// own hop_vX_to_vY.go file.
//
// Two kinds of hand data live here:
//
//   - Type renames. A vanished v4 type could have been renamed OR removed, and
//     field-set similarity is too ambiguous to tell which v5 type a v4 type
//     became (e.g. DocumentGetReq and GetSourceReq share nearly identical
//     fields). Every entry is mechanically verified against the committed
//     surfaces by TestTypeMapAgainstSurfaces: the v4 type must be absent from
//     the v5 surface under its old name, and the v5 target must be present, so
//     the table cannot silently drift from the real package types. Same-name
//     survivors need no entry; the surface diff handles them.
//
//   - Call-site rules. Method regrouping onto sub-clients, removed-helper
//     replacements, and argument-shape adjustments (Req vs *Req) concern how code
//     CALLS the client, not the shape of request/response structs, so they are
//     expressed as explicit rules verified by the osv4mig corpus build rather
//     than derived from the type surfaces.

const (
	v4api = "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	v5api = "github.com/opensearch-project/opensearch-go/v5/opensearchapi"

	v4root      = "github.com/opensearch-project/opensearch-go/v4"
	v4transport = "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// hopV4toV5 is the complete v4 -> v5 transition, registered in hops (see
// transitions.go). It is a const-ish migration data table, immutable after
// init; repeated API path/field names read clearer inline than as constants.
//
//nolint:gochecknoglobals,goconst // immutable data table; inline names read clearer
var hopV4toV5 = hop{
	From: 4,
	To:   5,

	// TypeRenames lists v4 opensearchapi types whose name changed in v5. Derived
	// by diffing the osv4 consumer's referenced types against the pinned v5
	// surface (the DocumentGetReq -> GetReq family) and from the documented
	// renames in opensearchapi/UPGRADING_V4_TO_V5.md (the per-shard error types);
	// extend as new consumers or upgrade-doc entries surface additional renames.
	TypeRenames: slices.Concat([]apirev.TypeRename{
		{FromPkgPath: v4api, FromName: "DocumentGetReq", ToPkgPath: v5api, ToName: "GetReq"},
		{FromPkgPath: v4api, FromName: "DocumentGetResp", ToPkgPath: v5api, ToName: "GetResp"},
		{FromPkgPath: v4api, FromName: "DocumentDeleteByQueryReq", ToPkgPath: v5api, ToName: "DeleteByQueryReq"},
		{FromPkgPath: v4api, FromName: "DocumentDeleteByQueryResp", ToPkgPath: v5api, ToName: "DeleteByQueryResp"},
		{FromPkgPath: v4api, FromName: "IndicesCountReq", ToPkgPath: v5api, ToName: "CountReq"},
		{FromPkgPath: v4api, FromName: "IndicesCountResp", ToPkgPath: v5api, ToName: "CountResp"},
		{FromPkgPath: v4api, FromName: "ScrollGetReq", ToPkgPath: v5api, ToName: "ScrollReq"},
		{FromPkgPath: v4api, FromName: "ScrollGetResp", ToPkgPath: v5api, ToName: "ScrollResp"},
		{FromPkgPath: v4api, FromName: "DocumentGetParams", ToPkgPath: v5api, ToName: "GetParams"},
		{FromPkgPath: v4api, FromName: "DocumentDeleteByQueryParams", ToPkgPath: v5api, ToName: "DeleteByQueryParams"},
		{FromPkgPath: v4api, FromName: "IndicesCountParams", ToPkgPath: v5api, ToName: "CountParams"},
		{FromPkgPath: v4api, FromName: "ScrollGetParams", ToPkgPath: v5api, ToName: "ScrollParams"},
		// Snapshot element types behind SnapshotGetResp.
		{FromPkgPath: v4api, FromName: "SnapshotGet", ToPkgPath: v5api, ToName: "SnapshotInfo"},
		{FromPkgPath: v4api, FromName: "SnapshotGetShards", ToPkgPath: v5api, ToName: "ShardStatistics"},
		// Error-handling per-shard types (opensearchapi/UPGRADING_V4_TO_V5.md).
		{FromPkgPath: v4api, FromName: "ResponseShards", ToPkgPath: v5api, ToName: "ShardStatistics"},
		{FromPkgPath: v4api, FromName: "ResponseShardsFailure", ToPkgPath: v5api, ToName: "ShardSearchFailure"},
	}, opFamilyTypeRenames()),

	// FieldDispositions rules on struct fields that vanished on the v5 side. Every
	// entry here is a rename established from the v4/v5 package SOURCE, not guessed
	// from name similarity: response-field renames are proven by an identical JSON
	// wire tag (e.g. v4 `Timeout json:"timed_out"` -> v5 `TimedOut`) or the
	// decoder's response-array destination (CAT responses), and
	// request-field renames by the v4 code that assembles the field into the
	// spec-named path/body element (e.g. v4 `ID: r.DocumentID` -> v5 `ID`). Most are
	// same-type-name survivors, so ToType == FromType. A vanished field NOT listed
	// here is reported as "unclassified" and fails the run - that is a signal to
	// add its ruling, never to guess.
	//
	// Genuine removals are intentionally NOT enumerated: the fail-loud default
	// surfaces a real removal only if a consumer actually sets/reads it, at which
	// point an explicit ActionRemove entry is added. This keeps the table to the
	// changes that matter (renames, which silently lose data if missed) rather
	// than cataloging all 600+ dropped spec fields up front.
	FieldDispositions: fieldRenamesV4toV5(),

	// MethodRegroups covers the sub-client moves the osv4 wrapper hits. Sourced
	// from opensearchapi/UPGRADING_V4_TO_V5.md's method-grouping tables plus the
	// v5 generated client shape (Count/DeleteByQuery moved to top-level; document
	// ops to Doc; etc.). Extend as new consumers surface additional paths.
	MethodRegroups: append([]methodRegroup{
		{FromPath: []string{"Indices", "Count"}, ToPath: []string{"Count"}, Arg: argAddr},
		{FromPath: []string{"Document", "DeleteByQuery"}, ToPath: []string{"DeleteByQuery"}, Arg: argAddr},
		{FromPath: []string{"Indices", "Delete"}, ToPath: []string{"Indices", "Delete"}, Arg: argAddr},
		{FromPath: []string{"Indices", "Exists"}, ToPath: []string{"Indices", "Exists"}, Arg: argAddr},
		{FromPath: []string{"Index"}, ToPath: []string{"Doc", "Index"}},
		// Top-level methods that stayed top-level but now take *Req in v5. Same
		// path, Arg only - the regroup machinery doubles as a "wrap the arg" rule.
		{FromPath: []string{"UpdateByQuery"}, ToPath: []string{"UpdateByQuery"}, Arg: argAddr},
	}, opFamilyRegroups()...),

	// RemovedHelpers handles package-level helpers removed in v5, from
	// opensearchapi or the root package.
	//   - ToPointer(x): v4 exports it from both packages; v5 requires Go 1.26, so
	//     a call ToPointer(x) becomes the native new(x) (helperNativeNew).
	//   - NewFromClient(c): removed; flagged MANUAL because the v5 replacement
	//     (constructing opensearchapi.Client from a transport client) is
	//     consumer-specific and cannot be mechanically synthesized.
	RemovedHelpers: map[string]string{
		"ToPointer":     helperNativeNew,   // special-cased in the linter: ToPointer(x) -> new(x)
		"NewFromClient": apirev.KindManual, // report only
	},

	// SemanticFollowups are v4->v5 changes that cannot be mechanically rewritten
	// (behavioral, not shape). The rewrite subcommand prints them so the operator
	// knows the automated rewrite is necessary but not sufficient.
	SemanticFollowups: []string{
		"errmask default flipped: Config.Errors == nil now reports every partial-failure category (v4 masked all).",
		"opensearchapi.NewClient now injects a default Router when Config.Client.Router is nil - verify OPENSEARCH_GO_ROUTER expectations.",
		"EnableMetrics removed: Metrics() no longer errors when disabled - drop any code that branched on that error.",
		"Timeout/Pretty/Human/ErrorTrace moved into embedded TimeoutParams/DebugParams - restructure those assignments by hand.",
		"DocumentError -> ErrorRespBase: v5 nests the cause in ErrorRespBase.Error (an ErrorCause) with Status on the envelope; " +
			"read Reason/Type/CausedBy/RootCause from .Error (Reason is now *string - nil-check) and rework per-error access by hand " +
			"- see opensearchapi/UPGRADING_V4_TO_V5.md.",
	},
}

// fieldRenamesV4toV5 returns the v4->v5 field dispositions. Every rename is
// proven from source: response fields by a shared JSON wire tag or response-array
// decoding, request fields by the v4 code assembling the field into the
// spec-named element. Most are
// same-type-name survivors (toType == fromType); a few ride across a type rename
// (e.g. DocumentGetReq#DocumentID -> GetReq#ID), which is why fromType and toType
// are stated separately. A handful of genuine removals inside type-renamed
// structs are listed explicitly so they are ActionRemove, not "unclassified".
// See the drift guards in delta_test.go for the provenance checks.
//
//nolint:goconst // OpenSearch API type/field names repeat across rows of this data table; naming them as constants would obscure the table
func fieldRenamesV4toV5() []apirev.FieldDisposition {
	// rename entries: (fromType, fromField) -> (toType, toField). toType == ""
	// means "same as fromType" (the common same-name-survivor case).
	renames := []struct{ fromType, fromField, toType, toField string }{
		// Response fields - proven by identical JSON tag across v4 and v5.
		{"SearchResp", "Timeout", "", "TimedOut"},         // json:"timed_out"
		{"SearchTemplateResp", "Timeout", "", "TimedOut"}, // json:"timed_out"
		// CAT response arrays: v4 CatClient.Shards/Indices decode into these
		// fields; v5 CatShardsResp/CatIndicesResp.UnmarshalJSON decode into Records.
		{"CatShardsResp", "Shards", "", "Records"},
		{"CatIndicesResp", "Indices", "", "Records"},
		// Cross-type: field rename riding across a type rename.
		{"ScrollGetResp", "Timeout", "ScrollResp", "TimedOut"}, // json:"timed_out"
		{"DocumentGetReq", "DocumentID", "GetReq", "ID"},       // v4 `ID: r.DocumentID`

		// Request fields - proven by v4 source assembling the field into the
		// spec-named path/body element (e.g. `ID: r.DocumentID`).
		{"IndexReq", "DocumentID", "", "ID"},
		{"UpdateReq", "DocumentID", "", "ID"},
		{"IngestSimulateReq", "PipelineID", "", "ID"},
		{"RenderSearchTemplateReq", "TemplateID", "", "ID"},
		{"IndicesRolloverReq", "Index", "", "NewIndex"},
		{"CatAliasesReq", "Aliases", "", "Name"},
		{"CatTemplatesReq", "Templates", "", "Name"},
		{"CatAllocationReq", "NodeIDs", "", "NodeID"},
		{"CatThreadPoolReq", "Pools", "", "ThreadPoolPatterns"},
		{"ClusterStateReq", "Metrics", "", "Metric"},
		{"ClusterStatsReq", "NodeFilters", "", "NodeID"},
		{"IndicesStatsReq", "Metrics", "", "Metric"},
		{"NodesInfoReq", "Metrics", "", "Metric"},
		{"NodesUsageReq", "Metrics", "", "Metric"},
		{"SnapshotCloneReq", "Repo", "", "Repository"},
		{"SnapshotCreateReq", "Repo", "", "Repository"},
		{"SnapshotDeleteReq", "Repo", "", "Repository"},
		{"SnapshotDeleteReq", "Snapshots", "", "Snapshot"},
		{"SnapshotGetReq", "Repo", "", "Repository"},
		{"SnapshotGetReq", "Snapshots", "", "Snapshot"},
		{"SnapshotRestoreReq", "Repo", "", "Repository"},
		{"SnapshotStatusReq", "Repo", "", "Repository"},
		{"SnapshotStatusReq", "Snapshots", "", "Snapshot"},
		// Params: v4 `params["q"] = r.Query`.
		{"DocumentDeleteByQueryParams", "Query", "DeleteByQueryParams", "Q"},
		{"IndicesCountParams", "Query", "CountParams", "Q"},

		// Template, script and data stream op families. The v4 identifier fields
		// are the spec path elements (ospath.<Name>Path{...}); the target type
		// follows the op family rename.
		{"IndexTemplateCreateReq", "IndexTemplate", "", "Name"},
		{"IndexTemplateDeleteReq", "IndexTemplate", "", "Name"},
		{"IndexTemplateExistsReq", "IndexTemplate", "", "Name"},
		{"IndexTemplateSimulateReq", "IndexTemplate", "", "Name"},
		{"IndexTemplateGetReq", "IndexTemplates", "", "Name"},
		{"IndexTemplateSimulateIndexReq", "Index", "", "Name"},
		{"ComponentTemplateCreateReq", "ComponentTemplate", "", "Name"},
		{"ComponentTemplateDeleteReq", "ComponentTemplate", "", "Name"},
		{"ComponentTemplateGetReq", "ComponentTemplate", "", "Name"},
		{"ComponentTemplateExistsReq", "ComponentTemplate", "", "Name"},
		{"TemplateCreateReq", "Template", "", "Name"},
		{"TemplateDeleteReq", "Template", "", "Name"},
		{"TemplateGetReq", "Templates", "", "Name"},
		{"TemplateGetResp", "Templates", "", "Entries"}, // v4 decodes into &data.Templates, v5 UnmarshalJSON into Entries
		{"ScriptDeleteReq", "ScriptID", "", "ID"},
		{"ScriptGetReq", "ScriptID", "", "ID"},
		{"ScriptPutReq", "ScriptID", "", "ID"},
		{"ScriptPutReq", "ScriptContext", "", "Context"},
		{"DataStreamCreateReq", "DataStream", "", "Name"},
		{"DataStreamGetReq", "DataStreams", "", "Name"},
		{"DataStreamStatsReq", "DataStreams", "", "Name"},
		{"DataStreamIndices", "Name", "", "IndexName"}, // json:"index_name"
		{"DataStreamIndices", "UUID", "", "IndexUUID"}, // json:"index_uuid"
	}

	// manuals: fields whose data relocated or changed shape, so no 1:1 rewrite is
	// safe.
	const simulateIndexParamNote = "v5 IndicesSimulateIndexTemplateParams has no such param; dropping it would change the request"
	manuals := []struct{ typ, field, note string }{
		{"TemplateExistsReq", "Template", "v5 takes Name []string; v4 sent []string{Template}"},
		{"DataStreamDeleteReq", "DataStream", "v5 takes Name []string; v4 sent []string{DataStream}"},
		{"IndexTemplateSimulateIndexParams", "Cause", simulateIndexParamNote},
		{"IndexTemplateSimulateIndexParams", "Create", simulateIndexParamNote},

		// Sub-clients removed from the API client. A call through them is
		// regrouped (MethodRegroups); a bare reference, such as storing the
		// sub-client in a variable, has no mechanical target.
		{
			typeClient, "IndexTemplate",
			"removed in v5; call client.Indices.{PutIndexTemplate,DeleteIndexTemplate,GetIndexTemplate," +
				"ExistsIndexTemplate,SimulateTemplate,SimulateIndexTemplate} instead",
		},
		{typeClient, "ComponentTemplate", "removed in v5; call client.Cluster.{Put,Delete,Get,Exists}ComponentTemplate instead"},
		{typeClient, "Template", "removed in v5; call client.Indices.{Put,Delete,Get,Exists}Template instead"},
		{
			typeClient, "Script",
			"removed in v5; call client.{DeleteScript,PutScript,GetScript,GetScriptContext,GetScriptLanguages," +
				"ScriptsPainlessExecute} instead",
		},
		{
			typeClient, "DataStream",
			"removed in v5; call client.Indices.{CreateDataStream,DeleteDataStream,GetDataStream,DataStreamsStats} instead",
		},
	}

	// removals: fields genuinely gone in v5, listed explicitly so they are
	// ActionRemove rather than "unclassified". Includes fields inside type-renamed
	// structs (which the same-name diff never sees) and the opensearch/
	// opensearchtransport Config knob dropped in v5 (EnableMetrics is also called
	// out in SemanticFollowups).
	removals := []struct{ pkg, typ, field string }{
		{v4api, "ScrollGetResp", "MaxScore"},        // top-level max_score dropped in v5 (moved per-hit)
		{v4api, "ResponseShardsFailure", "Primary"}, // dropped in v5 ShardSearchFailure
		{v4api, "ResponseShardsFailure", "Status"},  // dropped in v5 ShardSearchFailure
		{v4root, "Config", "EnableMetrics"},
		{v4transport, "Config", "EnableMetrics"},
	}

	toName := map[string]string{}
	for _, r := range opFamilyTypeRenames() {
		toName[r.FromName] = r.ToName
	}

	out := make([]apirev.FieldDisposition, 0, len(renames)+len(manuals)+len(removals))
	out = append(out, sharedParamDispositionsV4toV5()...)
	for _, m := range manuals {
		out = append(out, apirev.FieldDisposition{
			FromPkgPath: v4api, FromType: m.typ, FromField: m.field,
			Action: apirev.ActionManual, Note: m.note,
		})
	}
	for _, r := range renames {
		toType := r.toType
		if toType == "" {
			toType = r.fromType
			if renamed, ok := toName[r.fromType]; ok {
				toType = renamed
			}
		}
		out = append(out, apirev.FieldDisposition{
			FromPkgPath: v4api, FromType: r.fromType, FromField: r.fromField,
			Action:    apirev.ActionRename,
			ToPkgPath: v5api, ToType: toType, ToField: r.toField,
		})
	}
	for _, r := range removals {
		out = append(out, apirev.FieldDisposition{
			FromPkgPath: r.pkg, FromType: r.typ, FromField: r.field,
			Action: apirev.ActionRemove,
		})
	}
	return out
}

// debugParamFields are the fields every v4 Params type in sharedParamGroups
// carries and v5 moved into the embedded DebugParams.
//
//nolint:gochecknoglobals // immutable data table
var debugParamFields = []string{"ErrorTrace", "FilterPath", "Human", "Pretty"}

// sharedParamGroups lists the v4 Params types by the shared-param fields v5
// moved into the embedded TimeoutParams/DebugParams. Each type name is the v4
// opensearchapi type; the fields vanish from its renamed v5 counterpart.
// extra holds the timeout fields a group carries on top of debugParamFields.
//
//nolint:gochecknoglobals,goconst // immutable data table; OpenSearch param names repeat across rows
var sharedParamGroups = []struct {
	extra []string
	types []string
}{
	{
		types: []string{
			"DocumentGetParams", "IndicesCountParams", "ScrollGetParams", "DataStreamCreateParams",
			"ScriptContextParams", "ScriptLanguageParams", "ScriptPainlessExecuteParams",
		},
	},
	{
		extra: []string{"Timeout"},
		types: []string{"DocumentDeleteByQueryParams"},
	},
	{
		extra: []string{"ClusterManagerTimeout"},
		types: []string{"DataStreamGetParams", "DataStreamStatsParams"},
	},
	{
		extra: []string{"ClusterManagerTimeout", "Timeout"},
		types: []string{"DataStreamDeleteParams"},
	},
	{
		extra: []string{"ClusterManagerTimeout", "MasterTimeout"},
		types: []string{
			"ComponentTemplateExistsParams", "ComponentTemplateGetParams", "IndexTemplateCreateParams",
			"IndexTemplateExistsParams", "IndexTemplateGetParams", "IndexTemplateSimulateIndexParams",
			"IndexTemplateSimulateParams", "ScriptGetParams", "TemplateCreateParams", "TemplateExistsParams",
			"TemplateGetParams",
		},
	},
	{
		extra: []string{"ClusterManagerTimeout", "MasterTimeout", "Timeout"},
		types: []string{
			"ComponentTemplateCreateParams", "ComponentTemplateDeleteParams", "IndexTemplateDeleteParams",
			"ScriptDeleteParams", "ScriptPutParams", "TemplateDeleteParams",
		},
	},
}

// sharedParamDispositionsV4toV5 rules every shared param that moved into the
// embedded TimeoutParams/DebugParams as MANUAL. The drift guard verifies each
// field existed on the v4 type. The v5 params surface cannot confirm the move
// (the committed surface omits fields promoted through the embedded type
// aliases), so these rows are not renames.
func sharedParamDispositionsV4toV5() []apirev.FieldDisposition {
	var out []apirev.FieldDisposition
	for _, g := range sharedParamGroups {
		for _, typ := range g.types {
			for _, f := range slices.Concat(debugParamFields, g.extra) {
				embedded := "DebugParams"
				switch f {
				case "Timeout", "ClusterManagerTimeout", "MasterTimeout":
					embedded = "TimeoutParams"
				}
				out = append(out, apirev.FieldDisposition{
					FromPkgPath: v4api, FromType: typ, FromField: f,
					Action: apirev.ActionManual,
					Note: "moved into the embedded opensearchapi." + embedded + " in v5; a selector (p." + f +
						" = x) still compiles, a composite-literal key must move into " + embedded + ": opensearchapi." + embedded + "{...}",
				})
			}
		}
	}
	return out
}

// opFamilies pairs each v4 template, script and data stream op family with its
// v5 name. Each family has a Req, Params and Resp type; the template Exists ops
// have no Resp. Every v4 name is the ospath element its request builds, and
// Simulate/SimulateIndex are paired by URL (/_index_template/_simulate and
// /_simulate_index). from and to name the Req/Params/Resp type family; fromCall
// and toCall are the dotted client call paths the family's method moves between.
// The argument shape follows the v4 and v5 method signatures: v4 Get on the index
// and component templates takes *Req (nil-checked) where v5 takes Req; v4
// Template Exists, Script PainlessExecute and DataStream Delete take Req where v5
// takes *Req. Where both sides take *Req, the target nil-checks it, so the
// argument passes through (argSame): wrapping nil in & would not compile.
//
//nolint:gochecknoglobals,goconst // immutable data table; op names repeat across tables
var opFamilies = []struct {
	from, to         string
	fromCall, toCall string
	arg              argShape
	noResp           bool
}{
	{"IndexTemplateCreate", "IndicesPutIndexTemplate", "IndexTemplate.Create", "Indices.PutIndexTemplate", argSame, false},
	{"IndexTemplateDelete", "IndicesDeleteIndexTemplate", "IndexTemplate.Delete", "Indices.DeleteIndexTemplate", argSame, false},
	{"IndexTemplateGet", "IndicesGetIndexTemplate", "IndexTemplate.Get", "Indices.GetIndexTemplate", argValue, false},
	{"IndexTemplateExists", "IndicesExistsIndexTemplate", "IndexTemplate.Exists", "Indices.ExistsIndexTemplate", argSame, true},
	{"IndexTemplateSimulate", "IndicesSimulateTemplate", "IndexTemplate.Simulate", "Indices.SimulateTemplate", argSame, false},
	{
		"IndexTemplateSimulateIndex", "IndicesSimulateIndexTemplate",
		"IndexTemplate.SimulateIndex", "Indices.SimulateIndexTemplate",
		argSame, false,
	},
	{
		"ComponentTemplateCreate", "ClusterPutComponentTemplate",
		"ComponentTemplate.Create", "Cluster.PutComponentTemplate",
		argSame, false,
	},
	{
		"ComponentTemplateDelete", "ClusterDeleteComponentTemplate",
		"ComponentTemplate.Delete", "Cluster.DeleteComponentTemplate",
		argSame, false,
	},
	{"ComponentTemplateGet", "ClusterGetComponentTemplate", "ComponentTemplate.Get", "Cluster.GetComponentTemplate", argValue, false},
	{
		"ComponentTemplateExists", "ClusterExistsComponentTemplate",
		"ComponentTemplate.Exists", "Cluster.ExistsComponentTemplate",
		argSame, true,
	},
	{"TemplateCreate", "IndicesPutTemplate", "Template.Create", "Indices.PutTemplate", argSame, false},
	{"TemplateDelete", "IndicesDeleteTemplate", "Template.Delete", "Indices.DeleteTemplate", argSame, false},
	{"TemplateGet", "IndicesGetTemplate", "Template.Get", "Indices.GetTemplate", argSame, false},
	{"TemplateExists", "IndicesExistsTemplate", "Template.Exists", "Indices.ExistsTemplate", argAddr, true},
	{"ScriptDelete", "DeleteScript", "Script.Delete", "DeleteScript", argSame, false},
	{"ScriptPut", "PutScript", "Script.Put", "PutScript", argSame, false},
	{"ScriptGet", "GetScript", "Script.Get", "GetScript", argSame, false},
	{"ScriptContext", "GetScriptContext", "Script.Context", "GetScriptContext", argSame, false},
	{"ScriptLanguage", "GetScriptLanguages", "Script.Language", "GetScriptLanguages", argSame, false},
	{"ScriptPainlessExecute", "ScriptsPainlessExecute", "Script.PainlessExecute", "ScriptsPainlessExecute", argAddr, false},
	{"DataStreamCreate", "IndicesCreateDataStream", "DataStream.Create", "Indices.CreateDataStream", argSame, false},
	{"DataStreamDelete", "IndicesDeleteDataStream", "DataStream.Delete", "Indices.DeleteDataStream", argAddr, false},
	{"DataStreamGet", "IndicesGetDataStream", "DataStream.Get", "Indices.GetDataStream", argSame, false},
	{"DataStreamStats", "IndicesDataStreamsStats", "DataStream.Stats", "Indices.DataStreamsStats", argSame, false},
}

// opFamilyTypeRenames returns the type renames for opFamilies plus the nested
// element types behind their responses. Each nested rename is proven by the
// parent field's identical JSON tag.
func opFamilyTypeRenames() []apirev.TypeRename {
	out := make([]apirev.TypeRename, 0, len(opFamilies)*3+6)
	add := func(from, to string) {
		out = append(out, apirev.TypeRename{FromPkgPath: v4api, FromName: from, ToPkgPath: v5api, ToName: to})
	}
	for _, f := range opFamilies {
		add(f.from+"Req", f.to+"Req")
		add(f.from+"Params", f.to+"Params")
		if !f.noResp {
			add(f.from+"Resp", f.to+"Resp")
		}
	}
	add("IndexTemplateGetDetails", "IndicesGetIndexTemplateIndexTemplateItem")
	add("ComponentTemplateGetDetails", "ClusterComponentTemplate")
	add("TemplateGetDetails", "IndicesTemplateMapping")
	add("DataStreamGetDetails", "IndicesDataStream")
	add("DataStreamStatsDetails", "IndicesDataStreamStats")
	add("DataStreamIndices", "IndicesDataStreamIndex")
	return out
}

// opFamilyRegroups returns the sub-client moves for opFamilies.
func opFamilyRegroups() []methodRegroup {
	out := make([]methodRegroup, 0, len(opFamilies))
	for _, f := range opFamilies {
		out = append(out, methodRegroup{FromPath: strings.Split(f.fromCall, "."), ToPath: strings.Split(f.toCall, "."), Arg: f.arg})
	}
	return out
}
