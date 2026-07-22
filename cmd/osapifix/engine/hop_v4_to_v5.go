// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import "github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"

// hop_v4_to_v5.go is the hand-authored v4 -> v5 migration data: the human
// judgment that cannot be auto-derived from the type surfaces. It is the one
// fully worked example of a hop; new transitions follow the same shape in their
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
//     replacements, and value->pointer argument adjustments concern how code
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
	TypeRenames: []apirev.TypeRename{
		{FromPkgPath: v4api, FromName: "DocumentGetReq", ToPkgPath: v5api, ToName: "GetReq"},
		{FromPkgPath: v4api, FromName: "DocumentGetResp", ToPkgPath: v5api, ToName: "GetResp"},
		{FromPkgPath: v4api, FromName: "DocumentDeleteByQueryReq", ToPkgPath: v5api, ToName: "DeleteByQueryReq"},
		{FromPkgPath: v4api, FromName: "DocumentDeleteByQueryResp", ToPkgPath: v5api, ToName: "DeleteByQueryResp"},
		{FromPkgPath: v4api, FromName: "IndicesCountReq", ToPkgPath: v5api, ToName: "CountReq"},
		{FromPkgPath: v4api, FromName: "IndicesCountResp", ToPkgPath: v5api, ToName: "CountResp"},
		{FromPkgPath: v4api, FromName: "ScrollGetReq", ToPkgPath: v5api, ToName: "ScrollReq"},
		{FromPkgPath: v4api, FromName: "ScrollGetResp", ToPkgPath: v5api, ToName: "ScrollResp"},
		// Error-handling per-shard types (opensearchapi/UPGRADING_V4_TO_V5.md).
		{FromPkgPath: v4api, FromName: "ResponseShards", ToPkgPath: v5api, ToName: "ShardStatistics"},
		{FromPkgPath: v4api, FromName: "ResponseShardsFailure", ToPkgPath: v5api, ToName: "ShardSearchFailure"},
	},

	// FieldDispositions rules on struct fields that vanished on the v5 side. Every
	// entry here is a rename established from the v4/v5 package SOURCE, not guessed
	// from name similarity: response-field renames are proven by an identical JSON
	// wire tag (e.g. v4 `Timeout json:"timed_out"` -> v5 `TimedOut`), and
	// request-field renames by the v4 code that assembles the field into the
	// spec-named path/body element (e.g. v4 `ID: r.DocumentID` -> v5 `ID`). All are
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
	MethodRegroups: []methodRegroup{
		{FromPath: []string{"Indices", "Count"}, ToPath: []string{"Count"}, PtrArg: true},
		{FromPath: []string{"Document", "DeleteByQuery"}, ToPath: []string{"DeleteByQuery"}, PtrArg: true},
		{FromPath: []string{"Indices", "Delete"}, ToPath: []string{"Indices", "Delete"}, PtrArg: true},
		{FromPath: []string{"Indices", "Exists"}, ToPath: []string{"Indices", "Exists"}, PtrArg: true},
		{FromPath: []string{"Index"}, ToPath: []string{"Doc", "Index"}, PtrArg: false},
		// Top-level methods that stayed top-level but now take *Req in v5. Same
		// path, PtrArg only - the regroup machinery doubles as a "wrap the arg" rule.
		{FromPath: []string{"UpdateByQuery"}, ToPath: []string{"UpdateByQuery"}, PtrArg: true},
	},

	// RemovedHelpers handles opensearchapi package-level helpers removed in v5.
	//   - ToPointer(x): the identity-ish helper is gone; v5 methods take *Req, so
	//     a call ToPointer(x) becomes &x ("addressOf").
	//   - NewFromClient(c): removed; flagged MANUAL because the v5 replacement
	//     (constructing opensearchapi.Client from a transport client) is
	//     consumer-specific and cannot be mechanically synthesized.
	RemovedHelpers: map[string]string{
		"ToPointer":     "addressOf", // special-cased in the engine: wrap arg in &
		"NewFromClient": "manual",    // report only
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
// proven from source: response fields by a shared JSON wire tag, request fields
// by the v4 code assembling the field into the spec-named element. Most are
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
	}

	// removals: fields genuinely gone in v5, listed explicitly so they are
	// ActionRemove rather than "unclassified". Includes fields inside type-renamed
	// structs (which the same-name diff never sees) and the opensearch/
	// opensearchtransport Config knobs dropped in v5 (EnableMetrics is also called
	// out in SemanticFollowups; DisableResponseBuffering was likewise removed).
	removals := []struct{ pkg, typ, field string }{
		{v4api, "ScrollGetResp", "MaxScore"},        // top-level max_score dropped in v5 (moved per-hit)
		{v4api, "ResponseShardsFailure", "Primary"}, // dropped in v5 ShardSearchFailure
		{v4api, "ResponseShardsFailure", "Status"},  // dropped in v5 ShardSearchFailure
		{v4root, "Config", "EnableMetrics"},
		{v4root, "Config", "DisableResponseBuffering"},
		{v4transport, "Config", "EnableMetrics"},
		{v4transport, "Config", "DisableResponseBuffering"},
	}

	out := make([]apirev.FieldDisposition, 0, len(renames)+len(removals))
	for _, r := range renames {
		toType := r.toType
		if toType == "" {
			toType = r.fromType
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
