// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import "github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"

// hop_v2_to_v3.go is the hand-authored v2 -> v3 migration data. Unlike the quiet
// v3 -> v4 and v4 -> v5 hops, v2 -> v3 is the single largest boundary in the
// project's history: the opensearchapi package was redesigned from a
// function-based API into a typed sub-client API. Of the 182 exported structs in
// v2, only 16 survive by name into v3, and 166 (the entire opensearchapi.*Request
// family) are removed outright.
//
// That redesign cannot be rewritten mechanically. There are two consumer idioms,
// and both change SHAPE, not just spelling:
//
//   - Idiom 1 (function API): opensearchapi.BulkRequest{...}.Do(ctx, client)
//     becomes client.Bulk(ctx, BulkReq{...}). The call is only half of it: v2
//     .Do returns a raw *opensearchapi.Response (read via .Body / .StatusCode /
//     .IsError()), whereas the v3 method returns an already-decoded typed *Resp.
//     The whole response-handling block that follows the call must be reworked,
//     and the receiver changes from the root *opensearch.Client to a separate
//     *opensearchapi.Client. That is a per-op semantic rewrite, not a rename.
//
//   - Idiom 2 (root client): client.Ping(client.Ping.WithContext(ctx)) plus
//     resp.IsError()/resp.Status(). The root opensearch.Client lost all 51 of its
//     API method fields in v3 (only Transport survives); the functional-option
//     args collapse into a Req struct, and the raw-response error check moves to
//     the returned error. Also a control-flow change, not a rename.
//
// So this hop mechanizes the import-path bump plus the two seed ops Ping and
// Indices.Exists (their call, raw-response Status() handling, and Config/NewClient
// lifecycle, best-effort into compiling v3; see rewrite_idiom2.go), and reports
// everything else. The 15 field-identical survivors need no field rulings. The
// root opensearch.Client's removed method fields are ruled as MANUAL (idiom 2),
// so a real consumer that constructs and calls the root client gets an actionable
// worklist line rather than a bare "unclassified" bug. The removed
// opensearchapi.*Request TYPES (idiom 1) are surfaced by the engine's
// removed-type diagnostic (see applydelta.go), which reports any reference to a
// type that vanished on the target. The error-model differences and the two
// idiom transforms are documented as SemanticFollowups and in the README's
// "The v2 -> v3 hop" section.

const (
	// v2root is the root opensearch-go v2 module package (opensearch.Client,
	// opensearch.Config). The API method fields hang off this Client in v2.
	v2root = "github.com/opensearch-project/opensearch-go/v2"
)

// v2RootClientMethods are the API method fields on the root opensearch.Client
// that were removed in v3 (only Transport survives). In v2 each is a callable
// API endpoint on the root client (client.Ping(...), client.Search(...)); v3
// moves every endpoint onto the typed opensearchapi sub-client. Ruled MANUAL: the
// migration is a shape change (construct opensearchapi.NewClient and call
// client.X(ctx, &XReq{}), replacing raw-response checks with the returned error),
// not something the syntactic rewriter can express.
//
//nolint:gochecknoglobals,goconst // immutable data table; naming each repeated method name as a constant would obscure the table
var v2RootClientMethods = []string{
	"Bulk", "Cat", "ClearScroll", "Cluster", "Count", "Create",
	"DanglingIndicesDeleteDanglingIndex", "DanglingIndicesImportDanglingIndex",
	"DanglingIndicesListDanglingIndices", "Delete", "DeleteByQuery",
	"DeleteByQueryRethrottle", "DeleteScript", "Exists", "ExistsSource", "Explain",
	"FieldCaps", "Get", "GetScript", "GetScriptContext", "GetScriptLanguages",
	"GetSource", "Index", "Indices", "Info", "Ingest", "Mget", "Msearch",
	"MsearchTemplate", "Mtermvectors", "Nodes", "Ping", "PointInTime", "PutScript",
	"RankEval", "Reindex", "ReindexRethrottle", "Remote", "RenderSearchTemplate",
	"ScriptsPainlessExecute", "Scroll", "Search", "SearchShards", "SearchTemplate",
	"Snapshot", "Tasks", "TermsEnum", "Termvectors", "Update", "UpdateByQuery",
	"UpdateByQueryRethrottle",
}

// rootClientDispositionsV2toV3 rules every removed root-client method field as
// MANUAL with the same migration guidance. The drift guard
// (TestHopFieldDispositionsAgainstSurfaces) verifies each source field really
// existed on opensearch.Client in the v2 surface.
func rootClientDispositionsV2toV3() []apirev.FieldDisposition {
	const note = "the root opensearch.Client no longer exposes API methods in v3; " +
		"construct a typed client with opensearchapi.NewClient(opensearchapi.Config{Client: cfg}) " +
		"and call client.<Endpoint>(ctx, &<Endpoint>Req{...}), then check the returned error " +
		"instead of the removed resp.IsError()/resp.Status() on a raw *Response"

	out := make([]apirev.FieldDisposition, 0, len(v2RootClientMethods))
	for _, m := range v2RootClientMethods {
		out = append(out, apirev.FieldDisposition{
			FromPkgPath: v2root,
			FromType:    "Client",
			FromField:   m,
			Action:      apirev.ActionManual,
			Note:        note,
		})
	}
	return out
}

// hopV2toV3 is the complete v2 -> v3 transition, registered in hops (see
// transitions.go). Only the import bump is mechanical; the removed root-client
// methods are ruled MANUAL (idiom 2), the removed opensearchapi.*Request types
// are caught by the engine's removed-type diagnostic (idiom 1), and the shape
// changes are reported as followups.
//
//nolint:gochecknoglobals // immutable data table; mirrors hopV3toV4/hopV4toV5
var hopV2toV3 = Hop{
	From: 2,
	To:   3,

	// TypeRenames: none. The 16 surviving types keep their names and packages;
	// the 166 removed opensearchapi.*Request types are NOT renamed to the v3
	// typed *Req/sub-client API (that is a shape change, not a 1:1 rename - see
	// the file header), so they are surfaced by the removed-type diagnostic
	// rather than mis-encoded as renames.
	TypeRenames: nil,

	// FieldDispositions: the root opensearch.Client's removed API method fields,
	// ruled MANUAL (idiom 2). The 15 non-Client survivors are field-identical
	// v2 -> v3 and need no rulings.
	FieldDispositions: rootClientDispositionsV2toV3(),

	// MethodRegroups: none. The v2 root client's methods did not MOVE to a new
	// sub-client path of the same client - the client type itself changed
	// (root opensearch.Client -> opensearchapi.Client) and the call shape changed
	// with it, which a regroup cannot express. Handled via the MANUAL
	// dispositions and followups instead.
	MethodRegroups: nil,

	// RemovedHelpers: none. No package-level opensearchapi helper is removed in a
	// way the engine can act on; the whole function-based API surface is gone and
	// is reported by the removed-type diagnostic.
	RemovedHelpers: nil,

	// SemanticFollowups: the two idiom transforms and the response-model change.
	// Seed ops (Ping, Indices.Exists) are rewritten best-effort; everything else
	// is report-only. See the README's "The v2 -> v3 hop" section.
	SemanticFollowups: []string{
		"The opensearchapi package was redesigned from a function-based API to a typed sub-client API. " +
			"Idiom 1 (function API): opensearchapi.<X>Request{...}.Do(ctx, client) becomes client.<X>(ctx, <X>Req{...}); " +
			"the v3 method returns an already-decoded typed *Resp, so the raw response handling that followed the v2 " +
			".Do call (osResp.Body, osResp.StatusCode, osResp.IsError(), manual json.Unmarshal) must be reworked to the typed result.",
		"Idiom 2 (root client): the root opensearch.Client no longer exposes API methods (only Transport survives). " +
			"Construct a typed client with opensearchapi.NewClient(opensearchapi.Config{Client: cfg}) and call " +
			"client.<Endpoint>(ctx, &<Endpoint>Req{...}). osapifix now rewrites the seed ops Ping and Indices.Exists " +
			"best-effort (the call, its raw-response Status/Warnings handling, and the Config client lifecycle); " +
			"review each rewrite and resolve any _OSAPIFIX_RESOLVE marker. The remaining root-client endpoints are not " +
			"yet mechanized and stay MANUAL: their functional options (client.Ping.WithContext(ctx), ...) collapse into " +
			"the Req struct, and resp.IsError()/resp.Status() on the raw *Response are replaced by the returned error.",
		"The raw *opensearchapi.Response (with .Body, .StatusCode, .Header, .IsError()) is no longer returned by API calls in v3. " +
			"Responses are decoded typed *Resp values; move any status/error inspection to the returned error " +
			"and read fields off the typed response.",
	},
}
