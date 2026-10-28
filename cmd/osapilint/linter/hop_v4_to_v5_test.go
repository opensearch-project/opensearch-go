// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// hop_v4_to_v5_test.go pins the concrete v4->v5 facts the rewriter depends on.
// Unlike the generic drift guards in delta_test.go, these assertions ARE
// version-specific - they encode the exact changes the osv4 consumer corpus must
// undergo - so they live beside the v4->v5 data and are named for that hop.

// planV4toV5 returns the single hop plan for v4->v5.
func planV4toV5(t *testing.T) hopPlan {
	t.Helper()
	plans, err := planChain(4, 5)
	require.NoError(t, err)
	require.Len(t, plans, 1, "v4->v5 is a single hop")
	return plans[0]
}

// TestHopV4toV5_KnownChanges verifies the v4->v5 delta carries the specific type
// renames, field renames, pointer-wraps, and removals we rely on.
func TestHopV4toV5_KnownChanges(t *testing.T) {
	d := planV4toV5(t).delta

	// DocumentGetReq -> GetReq (type rename) carrying a field rename. Params is a
	// pointerWrap: DocumentGetParams -> GetParams is a type rename, so wrapping
	// the literal in & compiles on v5.
	getReq := d.Structs[v4api+".DocumentGetReq"]
	require.Equalf(t, v5api+".GetReq", getReq.To, "DocumentGetReq should map to %s.GetReq", v5api)
	assertChange(t, getReq.Changes, apirev.FieldChange{Kind: "rename", From: "DocumentID", To: "ID", NewType: "string"})
	assertChangeKind(t, getReq.Changes, "Params", apirev.KindPointerWrap)

	// A raw request body moves to BodyReader where v5 added a typed Body, on
	// same-name survivors and across a type rename alike.
	for _, typ := range []string{"SnapshotRestoreReq", "IndicesCountReq", "DocumentDeleteByQueryReq"} {
		assertChange(t, d.Structs[v4api+"."+typ].Changes,
			apirev.FieldChange{Kind: "rename", From: "Body", To: "BodyReader", NewType: "io.Reader"})
	}
	// Same-type pointer change stays a pointerWrap.
	assertChangeKind(t, d.Structs[v4api+".SearchReq"].Changes, "Params", "pointerWrap")

	// Field rename proven by a shared JSON tag: SearchResp.Timeout -> TimedOut.
	assertChangeKind(t, d.Structs[v4api+".SearchResp"].Changes, "Timeout", "rename")

	// Field rename proven by v4 source assembly: UpdateReq.DocumentID -> ID.
	assertChange(t, d.Structs[v4api+".UpdateReq"].Changes,
		apirev.FieldChange{Kind: "rename", From: "DocumentID", To: "ID", NewType: "string"})

	// EnableMetrics fan-in: removed from BOTH Config structs, as distinct keys.
	root := "github.com/opensearch-project/opensearch-go/v4.Config"
	transport := "github.com/opensearch-project/opensearch-go/v4/opensearchtransport.Config"
	assertChangeKind(t, d.Structs[root].Changes, "EnableMetrics", "remove")
	assertChangeKind(t, d.Structs[transport].Changes, "EnableMetrics", "remove")

	// Error-handling per-shard type renames (opensearchapi/UPGRADING_V4_TO_V5.md).
	// ResponseShards -> ShardStatistics: same field names, but Skipped becomes
	// *int (pointerWrap) and Failures' element type rides the ResponseShardsFailure
	// rename below.
	shards := d.Structs[v4api+".ResponseShards"]
	require.Equalf(t, v5api+".ShardStatistics", shards.To,
		"ResponseShards should map to %s.ShardStatistics", v5api)
	assertChangeKind(t, shards.Changes, "Skipped", "pointerWrap")

	// ResponseShardsFailure -> ShardSearchFailure: rename dropping Primary, Status.
	shardsFail := d.Structs[v4api+".ResponseShardsFailure"]
	require.Equalf(t, v5api+".ShardSearchFailure", shardsFail.To,
		"ResponseShardsFailure should map to %s.ShardSearchFailure", v5api)
	assertChangeKind(t, shardsFail.Changes, "Primary", "remove")
	assertChangeKind(t, shardsFail.Changes, "Status", "remove")
}

// TestHopV4toV5_ErrorHandlingFollowups asserts the two error-handling changes
// that are surfaced as semantic followups rather than mechanically rewritten:
// the DocumentError -> ErrorRespBase restructure (no 1:1 field mapping, per
// opensearchapi/UPGRADING_V4_TO_V5.md) and the errmask default flip (the
// Config.Errors toggle whose behavior changes v4 -> v5). The DocumentError
// followup must carry actionable guidance (the .Error nesting), not just name
// the type, so an operator knows where the cause moved.
func TestHopV4toV5_ErrorHandlingFollowups(t *testing.T) {
	followups := planV4toV5(t).followups
	require.True(t, containsSubstr(followups, "DocumentError"),
		"expected a followup for the DocumentError -> ErrorRespBase restructure")
	require.True(t, containsSubstr(followups, "ErrorRespBase.Error"),
		"DocumentError followup must point at the v5 .Error nesting, not just name the type")
	require.True(t, containsSubstr(followups, "errmask default flipped"),
		"expected a followup for the errmask default flip (Config.Errors toggle)")
}

// TestHopV4toV5_PluginNewClientFollowup asserts the plugin constructor change
// is surfaced as a followup.
func TestHopV4toV5_PluginNewClientFollowup(t *testing.T) {
	t.Parallel()

	require.True(t, containsSubstr(planV4toV5(t).followups, "NewClient(*opensearch.Client)"),
		"expected a followup for the plugin NewClient(Config) -> NewClient(*opensearch.Client) change")
}

// TestHopV4toV5_CatResponseFields pins the CAT response-array container renames
// (v4 CatClient.Shards/Indices decode into these fields; v5 decodes into
// Records). The rename carries a type-change Note because the element type also
// changed: the container rename alone does not convert the records.
func TestHopV4toV5_CatResponseFields(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	tests := []struct {
		typ       string
		field     string
		oldRecord string
		record    string
	}{
		{typ: "CatShardsResp", field: "Shards", oldRecord: "CatShardResp", record: "CatShardsRecord"},
		{typ: "CatIndicesResp", field: "Indices", oldRecord: "CatIndexResp", record: "CatIndicesRecord"},
	}

	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			t.Parallel()

			assertChange(t, d.Structs[v4api+"."+tt.typ].Changes,
				apirev.FieldChange{
					Kind: "rename", From: tt.field, To: "Records", NewType: "[]" + v5api + "." + tt.record,
					Note: `type changed from "[]` + v4api + `.` + tt.oldRecord + `" to "[]` + v5api + `.` + tt.record + `"`,
				})
		})
	}
}

// TestHopV4toV5_NoUnclassifiedInCorpus asserts that none of the field changes the
// v4->v5 delta produces for the referenced types are "unclassified" for the
// fields our own osv4 wrapper actually sets/reads. A stray unclassified here
// would fail a real rewrite loudly (by design); this catches a missing
// disposition at unit-test time instead. It is intentionally scoped to the
// request/response types the wrapper touches, not the entire apirev.
func TestHopV4toV5_NoUnclassifiedForKnownFields(t *testing.T) {
	d := planV4toV5(t).delta
	// Fields the osv4 wrapper is known to set; each must resolve to a concrete
	// action (rename/remove/pointerWrap), never "unclassified".
	knownlySet := map[string][]string{
		v4api + ".SearchResp":     {"Timeout"},
		v4api + ".UpdateReq":      {"DocumentID"},
		v4api + ".IndexReq":       {"DocumentID"},
		v4api + ".SnapshotGetReq": {"Repo", "Snapshots"},
	}
	for typ, fields := range knownlySet {
		sd := d.Structs[typ]
		for _, f := range fields {
			for _, ch := range sd.Changes {
				if ch.From == f {
					require.NotEqualf(t, "unclassified", ch.Kind,
						"%s#%s is unclassified - add a FieldDisposition", typ, f)
				}
			}
		}
	}
}

// TestHopV4toV5_SnapshotInfoRenames pins the snapshot element types behind
// SnapshotGetResp: the v4 SnapshotGet and SnapshotGetShards structs are the v5
// SnapshotInfo and ShardStatistics, so a reference to either is rewritten
// instead of reported as removed. SnapshotGetShards is field-identical to its
// target, so it has no struct delta; it only has to leave RemovedTypes.
func TestHopV4toV5_SnapshotInfoRenames(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	for _, typ := range []string{"SnapshotGet", "SnapshotGetShards"} {
		require.Falsef(t, d.RemovedTypes[v4api+"."+typ], "%q should be renamed, not removed", typ)
	}
	require.Equal(t, v5api+".SnapshotInfo", d.Structs[v4api+".SnapshotGet"].To)
	assertChangeKind(t, d.Structs[v4api+".SnapshotGet"].Changes, "Shards", apirev.KindPointerWrap)
}

// TestHopV4toV5_ParamsRenames pins the Params types renamed alongside their Req
// types, the Query -> Q field renames proven by the v4 `params["q"]` assembly,
// the params v5 dropped, which are manual, and the shared params v5 moved into
// the embedded TimeoutParams/DebugParams, which are promoted fields on v5 and
// so carry no change.
func TestHopV4toV5_ParamsRenames(t *testing.T) {
	t.Parallel()

	p := planV4toV5(t)
	d := p.delta

	renames := []struct{ from, to string }{
		{"DocumentGetParams", "GetParams"},
		{"DocumentDeleteByQueryParams", "DeleteByQueryParams"},
		{"IndicesCountParams", "CountParams"},
		{"ScrollGetParams", "ScrollParams"},
	}
	for _, r := range renames {
		require.Containsf(t, p.renames,
			apirev.TypeRename{FromPkgPath: v4api, FromName: r.from, ToPkgPath: v5api, ToName: r.to},
			"%q should map to %q", r.from, r.to)
		require.Falsef(t, d.RemovedTypes[v4api+"."+r.from], "%q should be renamed, not removed", r.from)
	}

	assertChange(t, d.Structs[v4api+".DocumentDeleteByQueryParams"].Changes,
		apirev.FieldChange{Kind: apirev.KindRename, From: "Query", To: "Q", NewType: "string"})
	assertChange(t, d.Structs[v4api+".IndicesCountParams"].Changes,
		apirev.FieldChange{Kind: apirev.KindRename, From: "Query", To: "Q", NewType: "string"})
	assertChange(t, d.Structs[v4api+".SearchParams"].Changes,
		apirev.FieldChange{Kind: apirev.KindRename, From: "Query", To: "Q", NewType: "string"})
	assertChange(t, d.Structs[v4api+".UpdateByQueryParams"].Changes,
		apirev.FieldChange{Kind: apirev.KindRename, From: "Query", To: "Q", NewType: "string"})
	assertChangeKind(t, d.Structs[v4api+".SearchParams"].Changes, "MinCompatibleShardNode", apirev.KindManual)
	assertChangeKind(t, d.Structs[v4api+".UpdateByQueryParams"].Changes, "VersionType", apirev.KindManual)

	promoted := []struct{ typ, v5typ, field string }{
		{"DocumentGetParams", "GetParams", "Pretty"},
		{"ScrollGetParams", "ScrollParams", "ErrorTrace"},
		{"DocumentDeleteByQueryParams", "DeleteByQueryParams", "Timeout"},
		{"SearchParams", "SearchParams", "Pretty"},
	}
	for _, m := range promoted {
		fields, ok := LookupStruct(5, v5api, m.v5typ)
		require.Truef(t, ok, "%q not in the v5 surface", m.v5typ)
		require.Truef(t, slices.ContainsFunc(fields, func(f SurfaceField) bool { return f.Name == m.field }),
			"%q should be promoted onto v5 %q", m.field, m.v5typ)
		for _, c := range d.Structs[v4api+"."+m.typ].Changes {
			require.NotEqualf(t, m.field, c.From, "%q is a promoted field on v5 and should carry no change, got %+v",
				m.typ+"."+m.field, c)
		}
	}
}

// TestHopV4toV5_OpFamilyTypes pins the template, script and data stream op
// families: the types are renamed (not reported removed), the identifier fields
// become Name/ID, and the params v5 dropped are manual.
func TestHopV4toV5_OpFamilyTypes(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	for _, typ := range []string{
		"IndexTemplateCreateReq", "IndexTemplateGetResp", "IndexTemplateSimulateIndexParams",
		"ComponentTemplateExistsReq", "TemplateGetResp", "ScriptContextParams", "ScriptPainlessExecuteResp",
		"DataStreamStatsResp", "DataStreamCreateParams",
		"IndexTemplateGetDetails", "ComponentTemplateGetDetails", "TemplateGetDetails",
		"DataStreamGetDetails", "DataStreamStatsDetails", "DataStreamIndices",
	} {
		require.Falsef(t, d.RemovedTypes[v4api+"."+typ], "%q should be renamed, not removed", typ)
	}

	tests := []struct {
		typ   string
		field string
		kind  string
	}{
		{"IndexTemplateCreateReq", "IndexTemplate", apirev.KindRename},
		{"IndexTemplateGetReq", "IndexTemplates", apirev.KindRename},
		{"IndexTemplateSimulateIndexReq", "Index", apirev.KindRename},
		{"ComponentTemplateGetReq", "ComponentTemplate", apirev.KindRename},
		{"TemplateCreateReq", "Template", apirev.KindRename},
		{"TemplateGetReq", "Templates", apirev.KindRename},
		{"TemplateGetResp", "Templates", apirev.KindRename},
		{"ScriptGetReq", "ScriptID", apirev.KindRename},
		{"ScriptPutReq", "ScriptContext", apirev.KindRename},
		{"DataStreamCreateReq", "DataStream", apirev.KindRename},
		{"DataStreamStatsReq", "DataStreams", apirev.KindRename},
		{"DataStreamIndices", "Name", apirev.KindRename},
		{"DataStreamIndices", "UUID", apirev.KindRename},
		{"TemplateExistsReq", "Template", apirev.KindManual},
		{"DataStreamDeleteReq", "DataStream", apirev.KindManual},
		{"IndexTemplateSimulateIndexParams", "Cause", apirev.KindManual},
		{"IndexTemplateSimulateIndexParams", "Create", apirev.KindManual},
	}
	for _, tt := range tests {
		t.Run(tt.typ+"#"+tt.field, func(t *testing.T) {
			t.Parallel()

			assertChangeKind(t, d.Structs[v4api+"."+tt.typ].Changes, tt.field, tt.kind)
		})
	}
}

// TestHopV4toV5_TemplateClientFields pins two things: IndexTemplateGetReq maps
// to IndicesGetIndexTemplateReq, and the five v4 Client fields (IndexTemplate,
// ComponentTemplate, Template, Script, DataStream) are classified manual.
func TestHopV4toV5_TemplateClientFields(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	require.Equal(t, v5api+".IndicesGetIndexTemplateReq", d.Structs[v4api+".IndexTemplateGetReq"].To)

	tests := []string{"IndexTemplate", "ComponentTemplate", "Template", "Script", "DataStream"}
	for _, field := range tests {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			assertChangeKind(t, d.Structs[v4api+".Client"].Changes, field, apirev.KindManual)
		})
	}
}

// TestHopV4toV5_PluginRemovedTypes pins the removed plugin types: reporting
// them as removed proves the plugin surfaces are loaded.
func TestHopV4toV5_PluginRemovedTypes(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	tests := []string{v4security + ".Config", v4ism + ".Config", v4ism + ".AddReq"}
	for _, typ := range tests {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			require.Truef(t, d.RemovedTypes[typ], "%q should be reported removed", typ)
		})
	}
}

// TestHopV4toV5_PluginManualFields pins the plugin Client sub-client fields as
// manual.
func TestHopV4toV5_PluginManualFields(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	tests := []struct {
		pkg    string
		typ    string
		fields []string
	}{
		{v4security, typeClient, []string{
			"Account", "ActionGroups", "Audit", "InternalUsers", "NodesDN", "Roles", "RolesMapping",
			"SecurityConfig", "SSL", "Tenants",
		}},
		{v4ism, typeClient, []string{"Policies"}},
	}
	for _, tt := range tests {
		for _, field := range tt.fields {
			t.Run(tt.pkg+"."+tt.typ+"#"+field, func(t *testing.T) {
				t.Parallel()

				assertChangeKind(t, d.Structs[tt.pkg+"."+tt.typ].Changes, field, apirev.KindManual)
			})
		}
	}
}

// TestHopV4toV5_OpFamilyRegroups pins the call-site regroups of the template,
// script and data stream sub-clients, one row per argument shape.
func TestHopV4toV5_OpFamilyRegroups(t *testing.T) {
	t.Parallel()

	byFrom := map[string]methodRegroup{}
	for _, rg := range planV4toV5(t).regroups {
		byFrom[strings.Join(rg.FromPath, ".")] = rg
	}

	tests := []struct {
		from string
		to   []string
		arg  argShape
	}{
		{"IndexTemplate.Create", []string{"Indices", "PutIndexTemplate"}, argSame},
		{"IndexTemplate.SimulateIndex", []string{"Indices", "SimulateIndexTemplate"}, argSame},
		{"ComponentTemplate.Exists", []string{"Cluster", "ExistsComponentTemplate"}, argSame},
		{"Script.Context", []string{"GetScriptContext"}, argSame},
		{"Script.Language", []string{"GetScriptLanguages"}, argSame},
		{"DataStream.Stats", []string{"Indices", "DataStreamsStats"}, argSame},
		{"Template.Exists", []string{"Indices", "ExistsTemplate"}, argAddr},
		{"Script.PainlessExecute", []string{"ScriptsPainlessExecute"}, argAddr},
		{"DataStream.Delete", []string{"Indices", "DeleteDataStream"}, argAddr},
		{"IndexTemplate.Get", []string{"Indices", "GetIndexTemplate"}, argValue},
		{"ComponentTemplate.Get", []string{"Cluster", "GetComponentTemplate"}, argValue},
	}
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			t.Parallel()

			rg, ok := byFrom[tt.from]
			require.Truef(t, ok, "no regroup for %q", tt.from)
			require.Equal(t, tt.to, rg.ToPath)
			require.Equal(t, tt.arg, rg.Arg)
		})
	}
}

// TestHopV4toV5_OpFamiliesClassified is the completeness guard for opFamilies:
// no field of any family's Req, Params or Resp type is left unclassified, so a
// consumer that sets it gets a ruling rather than an aborted run.
func TestHopV4toV5_OpFamiliesClassified(t *testing.T) {
	t.Parallel()

	d := planV4toV5(t).delta

	for _, f := range opFamilies {
		for _, suffix := range []string{"Req", "Params", "Resp"} {
			if suffix == "Resp" && f.noResp {
				continue
			}
			typ := f.from + suffix
			require.Falsef(t, d.RemovedTypes[v4api+"."+typ], "%q should be renamed, not removed", typ)
			for _, ch := range d.Structs[v4api+"."+typ].Changes {
				require.NotEqualf(t, apirev.KindUnclassified, ch.Kind, "%q#%q is unclassified - add a FieldDisposition", typ, ch.From)
			}
		}
	}
}
