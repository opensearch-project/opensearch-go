// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVersionAgnostic verifies that the module-major-version segment is
// normalized so v4 and v5 package paths for the same logical package pair up.
// This is what lets same-name survivors (opensearch.Config) match across the
// version bump, and is required for the EnableMetrics fan-in to be found in both
// opensearch.Config and opensearchtransport.Config.
func TestVersionAgnostic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"github.com/opensearch-project/opensearch-go/v4/opensearchapi", "github.com/opensearch-project/opensearch-go/opensearchapi"},
		{"github.com/opensearch-project/opensearch-go/v5/opensearchapi", "github.com/opensearch-project/opensearch-go/opensearchapi"},
		{"github.com/opensearch-project/opensearch-go/v4", "github.com/opensearch-project/opensearch-go"},
		{"github.com/opensearch-project/opensearch-go/v5/opensearchtransport", "github.com/opensearch-project/opensearch-go/opensearchtransport"},
		{"example.com/no/version/here", "example.com/no/version/here"},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, versionAgnostic(c.in), "versionAgnostic(%q)", c.in)
	}
	// v4 and v5 forms of the same package must be equal after normalization.
	require.Equal(t,
		versionAgnostic("github.com/opensearch-project/opensearch-go/v4/opensearchapi"),
		versionAgnostic("github.com/opensearch-project/opensearch-go/v5/opensearchapi"),
		"v4 and v5 opensearchapi must normalize equal")
}

// TestIncompatibleTypeChange pins the narrow json.RawMessage boundary that flags
// a field as "manual" (the SearchResp.Aggregations []byte -> typed-map case),
// while leaving compatible changes alone.
func TestIncompatibleTypeChange(t *testing.T) {
	raw := "encoding/json.RawMessage"
	typedMap := "map[string]github.com/opensearch-project/opensearch-go/v5/opensearchapi.SearchResultAggregationsValue"
	cases := []struct {
		from, to string
		want     bool
	}{
		{raw, typedMap, true}, // Aggregations: RawMessage -> typed map
		{typedMap, raw, true}, // symmetric
		{"string", "string", false},
		{"int", "int64", false}, // numeric widening is not this hazard
		{raw, raw, false},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, incompatibleTypeChange(c.from, c.to), "incompatibleTypeChange(%q, %q)", c.from, c.to)
	}
}

// TestIsRawBodyCollapse verifies detection of the v5 "dynamic schema captured as
// raw JSON" response shape (a single Body json.RawMessage field), which drives
// the by-query "manual" classification.
func TestIsRawBodyCollapse(t *testing.T) {
	collapsed := Struct{Name: "DeleteByQueryResp", Fields: []Field{
		{Name: "Body", Type: "encoding/json.RawMessage"},
	}}
	require.True(t, isRawBodyCollapse(collapsed), "single Body json.RawMessage struct is a raw-body collapse")

	structured := Struct{Name: "GetResp", Fields: []Field{
		{Name: "Body", Type: "encoding/json.RawMessage"},
		{Name: "Found", Type: "bool"},
	}}
	require.False(t, isRawBodyCollapse(structured), "multi-field struct must not be treated as a raw-body collapse")
}

// TestDeriveDelta_RemovedTypes verifies that a source type with no target
// counterpart (and no covering TypeRename) is recorded in Delta.RemovedTypes -
// the v2->v3 case where the opensearchapi.*Request family is deleted outright.
// A surviving type must NOT appear there, and a type covered by a TypeRename is a
// rename, not a removal.
func TestDeriveDelta_RemovedTypes(t *testing.T) {
	const pkg = "github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	const pkgV3 = "github.com/opensearch-project/opensearch-go/v3/opensearchapi"

	from := &Snapshot{Version: "v2", Structs: []Struct{
		{PkgPath: pkg, Name: "BulkRequest", Fields: []Field{{Name: "Index", Type: "string"}}}, // removed outright
		{PkgPath: pkg, Name: "Renamed", Fields: []Field{{Name: "X", Type: "string"}}},         // covered by a rename
		{PkgPath: pkg, Name: "InfoResp", Fields: []Field{{Name: "Version", Type: "string"}}},  // survives by name
	}}
	to := &Snapshot{Version: "v3", Structs: []Struct{
		{PkgPath: pkgV3, Name: "NewName", Fields: []Field{{Name: "X", Type: "string"}}},
		{PkgPath: pkgV3, Name: "InfoResp", Fields: []Field{{Name: "Version", Type: "string"}}},
	}}
	renames := []TypeRename{{FromPkgPath: pkg, FromName: "Renamed", ToPkgPath: pkgV3, ToName: "NewName"}}

	d := DeriveDelta(from, to, renames, nil)

	require.True(t, d.RemovedTypes[pkg+".BulkRequest"], "type deleted outright must be recorded as removed")
	require.False(t, d.RemovedTypes[pkg+".Renamed"], "a renamed type is resolved, not removed")
	require.False(t, d.RemovedTypes[pkg+".InfoResp"], "a same-name survivor is not removed")
}

// TestDeriveDelta_PointerFieldClassification pins how a surviving field that
// became a pointer is classified. Only a pointer to the SAME type (after
// version normalization and type renames) is a pointerWrap; a v4 Body io.Reader
// facing a v5 typed Body plus a BodyReader io.Reader is a rename to BodyReader;
// any other type change is manual, because wrapping the value in & would not
// type-check.
func TestDeriveDelta_PointerFieldClassification(t *testing.T) {
	t.Parallel()
	const (
		v4root = "github.com/opensearch-project/opensearch-go/v4"
		v5root = "github.com/opensearch-project/opensearch-go/v5"
		v4api  = v4root + "/opensearchapi"
		v5api  = v5root + "/opensearchapi"
	)
	renames := []TypeRename{{FromPkgPath: v4api, FromName: "ResponseShards", ToPkgPath: v5api, ToName: "ShardStatistics"}}

	cases := []struct {
		name     string
		from, to []Field // fields of a same-name struct Req in each version
		want     FieldChange
	}{
		{
			name: "same elem T -> *T",
			from: []Field{{Name: "Params", Type: v4api + ".SearchParams"}},
			to:   []Field{{Name: "Params", Type: "*" + v5api + ".SearchParams"}},
			want: FieldChange{Kind: KindPointerWrap, From: "Params", NewType: "*" + v5api + ".SearchParams"},
		},
		{
			name: "same elem in root package",
			from: []Field{{Name: "Cause", Type: v4root + ".CausedBy"}},
			to:   []Field{{Name: "Cause", Type: "*" + v5root + ".CausedBy"}},
			want: FieldChange{Kind: KindPointerWrap, From: "Cause", NewType: "*" + v5root + ".CausedBy"},
		},
		{
			name: "elem follows a type rename",
			from: []Field{{Name: "Shards", Type: v4api + ".ResponseShards"}},
			to:   []Field{{Name: "Shards", Type: "*" + v5api + ".ShardStatistics"}},
			want: FieldChange{Kind: KindPointerWrap, From: "Shards", NewType: "*" + v5api + ".ShardStatistics"},
		},
		{
			name: "Body io.Reader with target BodyReader",
			from: []Field{{Name: "Body", Type: "io.Reader"}},
			to: []Field{
				{Name: "Body", Type: "*" + v5api + ".ReqBody"},
				{Name: "BodyReader", Type: "io.Reader"},
			},
			want: FieldChange{Kind: KindRename, From: "Body", To: "BodyReader", NewType: "io.Reader"},
		},
		{
			name: "Body io.Reader without target BodyReader",
			from: []Field{{Name: "Body", Type: "io.Reader"}},
			to:   []Field{{Name: "Body", Type: "*" + v5api + ".ReqBody"}},
			want: FieldChange{
				Kind: KindManual, From: "Body", NewType: "*" + v5api + ".ReqBody",
				Note: "field type changed from io.Reader to *" + v5api + ".ReqBody; migrate this use by hand",
			},
		},
		{
			name: "int -> *int64",
			from: []Field{{Name: "Took", Type: "int"}},
			to:   []Field{{Name: "Took", Type: "*int64"}},
			want: FieldChange{
				Kind: KindManual, From: "Took", NewType: "*int64",
				Note: "field type changed from int to *int64; migrate this use by hand",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			from := &Snapshot{Structs: []Struct{{PkgPath: v4api, Name: "Req", Fields: tc.from}}}
			to := &Snapshot{Structs: []Struct{{PkgPath: v5api, Name: "Req", Fields: tc.to}}}

			d := DeriveDelta(from, to, renames, nil)

			require.Equal(t, []FieldChange{tc.want}, d.Structs[v4api+".Req"].Changes)
		})
	}
}
