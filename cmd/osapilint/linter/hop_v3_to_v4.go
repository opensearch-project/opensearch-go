// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

// hop_v3_to_v4.go is the hand-authored v3 -> v4 migration data. It follows the
// shape of hop_v4_to_v5.go (the fully worked example); see that file for the
// rationale behind each field of hop.
//
// The v3 -> v4 boundary is far quieter than v4 -> v5. The generated opensearchapi
// Client and its sub-clients are byte-identical across the two versions, so there
// are no method regroups and no *Req argument changes. The one real structural
// change is the error model: the API error types moved out of the opensearchapi
// package into the root opensearch package and were redesigned. That move cannot
// be rewritten mechanically (see SemanticFollowups), so it is reported to the
// operator rather than encoded as a rename. Everything else the surface diff
// derives on its own (notably ~90 fields that became pointers), and any request/
// response field that genuinely vanished is left to the fail-loud "unclassified"
// default until a real consumer proves a ruling is needed.

const (
	v3api       = "github.com/opensearch-project/opensearch-go/v3/opensearchapi"
	v3transport = "github.com/opensearch-project/opensearch-go/v3/opensearchtransport"
)

// hopV3toV4 is the complete v3 -> v4 transition, registered in hops (see
// transitions.go). Unlike hopV4toV5 most tables are empty: the surface diff plus
// the fail-loud default cover the mechanical changes, and the sole hand ruling is
// the error-model followup.
//
//nolint:gochecknoglobals // immutable data table; mirrors hopV4toV5
var hopV3toV4 = hop{
	From: 3,
	To:   4,

	// TypeRenames: none. The four error types (Error, Err, RootCause, StringError)
	// change PACKAGE (opensearchapi -> root opensearch), not just name. The linter's
	// rewriteTypeRef rewrites only the type name, never the package qualifier, and
	// RewriteImports only version-bumps an import prefix - so a cross-package rename
	// would pass the drift guard yet emit non-compiling code. They are handled as
	// SemanticFollowups instead. Every other type keeps its name and package.
	TypeRenames: nil,

	// FieldDispositions: none up front. The genuinely vanished fields
	// (opensearchapi.*Resp.Indices, which became an unexported field plus a
	// GetIndices() accessor; and opensearchtransport.Connection's dropped liveness
	// fields) are left to the fail-loud "unclassified" default, matching hopV4toV5's
	// discipline: a ruling is added only when a real consumer actually touches the
	// field, proven from source.
	FieldDispositions: nil,

	// MethodRegroups: none. The opensearchapi Client and sub-clients are identical
	// v3 -> v4; no call site moves.
	MethodRegroups: nil,

	// RemovedHelpers: none. No package-level opensearchapi helper was removed.
	RemovedHelpers: nil,

	// SemanticFollowups: the error-model redesign, which cannot be rewritten
	// mechanically (cross-package move + shape change). Proven from the v3 and v4
	// error.go sources.
	SemanticFollowups: []string{
		"Error types moved from the opensearchapi package to the root opensearch package: " +
			"opensearchapi.{Error,Err,RootCause,StringError} are now opensearch.{Error,Err,RootCause,StringError}. " +
			"Update the imports and package qualifiers by hand - osapilint cannot rewrite a package qualifier.",
		"opensearchapi.Error changed shape: the v3 Error{Err Err; Status int} is now opensearch.StructError; " +
			"the v4 opensearch.Error is a different, simpler type ({Err string}). " +
			"Re-point type switches and assertions to opensearch.StructError where you decoded the detailed error.",
		"opensearch.Err gained an optional CausedBy *CausedBy field (nested causes); " +
			"existing field access is unaffected, but new nested-cause data is now available.",
	},
}
