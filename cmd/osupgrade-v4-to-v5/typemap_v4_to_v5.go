package main

import "github.com/opensearch-project/opensearch-go/v5/cmd/osupgrade-v4-to-v5/internal/surface"

// typemap_v4_to_v5.go is the hand-authored v4 -> v5 type-rename map: the human
// judgment that cannot be auto-derived, because a vanished v4 type could have
// been renamed OR removed, and field-set similarity is too ambiguous to tell
// which v5 type a v4 type became (e.g. DocumentGetReq and GetSourceReq share
// nearly identical fields).
//
// Every entry is mechanically verified against the committed surfaces by
// TestTypeMapAgainstSurfaces: the v4 type must be absent from the v5 surface
// under its old name, and the v5 target must be present. A stale or wrong entry
// fails that test, so the map cannot silently drift from the real package types.
//
// Same-name survivors (types present in both versions under the same name) need
// no entry; the surface diff handles them automatically. This map is only for
// types whose NAME changed between v4 and v5.

const (
	v4api = "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	v5api = "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// typeRenamesV4toV5 lists v4 opensearchapi types whose name changed in v5.
// Derived by diffing the osv4 consumer's referenced types against the pinned v5
// surface (the DocumentGetReq -> GetReq family); extend as new consumers surface
// additional renamed types.
var typeRenamesV4toV5 = []surface.TypeRename{
	{V4PkgPath: v4api, V4Name: "DocumentGetReq", V5PkgPath: v5api, V5Name: "GetReq"},
	{V4PkgPath: v4api, V4Name: "DocumentGetResp", V5PkgPath: v5api, V5Name: "GetResp"},
	{V4PkgPath: v4api, V4Name: "DocumentDeleteByQueryReq", V5PkgPath: v5api, V5Name: "DeleteByQueryReq"},
	{V4PkgPath: v4api, V4Name: "DocumentDeleteByQueryResp", V5PkgPath: v5api, V5Name: "DeleteByQueryResp"},
	{V4PkgPath: v4api, V4Name: "IndicesCountReq", V5PkgPath: v5api, V5Name: "CountReq"},
	{V4PkgPath: v4api, V4Name: "IndicesCountResp", V5PkgPath: v5api, V5Name: "CountResp"},
	{V4PkgPath: v4api, V4Name: "ScrollGetReq", V5PkgPath: v5api, V5Name: "ScrollReq"},
	{V4PkgPath: v4api, V4Name: "ScrollGetResp", V5PkgPath: v5api, V5Name: "ScrollResp"},
}
