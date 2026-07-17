// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapifix/internal/apirev"
)

// callmap_v2_to_v3_test.go is the drift guard for the hand-authored v2 -> v3
// call map (callmap_v2_to_v3.go). It is the call-map analog of the hop
// disposition/rename guards in delta_test.go: every row must stay true against
// the real v2 and v3 surfaces, so the map cannot silently rot as the surfaces
// are regenerated. It also enforces the map's internal invariants (well-formed
// rows, no duplicate v2 paths, complete coverage of the v2 root client).

const (
	v2CallMapRootPkg = "github.com/opensearch-project/opensearch-go/v2"
	v3CallMapAPIPkg  = "github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

// lookupStruct finds a struct by package path and name in a snapshot. apirev's
// own lookup is unexported, so the call-map guard re-implements the same linear
// scan the hop guards in delta_test.go use.
func lookupStruct(snap *apirev.Snapshot, pkg, name string) (apirev.Struct, bool) {
	for _, st := range snap.Structs {
		if st.PkgPath == pkg && st.Name == name {
			return st, true
		}
	}
	return apirev.Struct{}, false
}

// v2ClientFieldPath reports whether path is a real call path off the v2 root
// opensearch.Client: the first segment is a field on opensearch.Client, and each
// further segment is a field on the opensearchapi sub-client struct named by the
// previous segment's field type. This is exactly the receiver chain the eventual
// rewriter must recognize, so validating it here pins the map to the real types.
func v2ClientFieldPath(snap *apirev.Snapshot, path []string) bool {
	cur, ok := lookupStruct(snap, v2CallMapRootPkg, "Client")
	if !ok {
		return false
	}
	for i, seg := range path {
		f, ok := cur.Field(seg)
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		// Descend into the sub-client struct named by this field's type. Field
		// types are fully qualified and may be pointers (e.g.
		// "*.../opensearchapi.Indices"); the unqualified trailing name is the
		// sub-client struct in the v2 opensearchapi package.
		sub, ok := lookupStruct(snap, v2CallMapRootPkg+"/opensearchapi", unqualifiedTypeName(f.Type))
		if !ok {
			return false
		}
		cur = sub
	}
	return true
}

// unqualifiedTypeName returns the bare type name from a possibly-pointer,
// possibly-qualified types.Type string, e.g.
// "*github.com/.../opensearchapi.Indices" -> "Indices".
func unqualifiedTypeName(t string) string {
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		return t[i+1:]
	}
	return t
}

// TestCallMapV2toV3AgainstSurfaces validates every mapped row against the real
// surfaces: the v2 call path must exist off opensearch.Client, and the v3 target
// request struct must exist in the v3 opensearchapi package. Removed rows carry
// no v3 target; only their v2 path is checked. This is what keeps the map from
// drifting from the packages it migrates between.
func TestCallMapV2toV3AgainstSurfaces(t *testing.T) {
	v2Snap, err := decodeSurface(2)
	require.NoError(t, err, "v2 surface")
	v3Snap, err := decodeSurface(3)
	require.NoError(t, err, "v3 surface")

	hasReq := func(name string) bool {
		_, ok := lookupStruct(v3Snap, v3CallMapAPIPkg, name)
		return ok
	}

	for _, e := range callMapV2toV3 {
		require.Truef(t, v2ClientFieldPath(v2Snap, e.V2Path),
			"v2 call path client.%s not found off opensearch.Client in v2 surface (stale entry?)",
			pathString(e.V2Path))

		if e.Removed {
			require.Empty(t, e.V3Path, "removed op client.%s must have no V3Path", pathString(e.V2Path))
			require.Empty(t, e.V3Req, "removed op client.%s must have no V3Req", pathString(e.V2Path))
			continue
		}

		require.NotEmpty(t, e.V3Path, "mapped op client.%s must have a V3Path", pathString(e.V2Path))
		require.Truef(t, hasReq(e.V3Req),
			"v3 request type opensearchapi.%s (for client.%s -> client.%s) not found in v3 surface (wrong rename?)",
			e.V3Req, pathString(e.V2Path), pathString(e.V3Path))
	}
}

// TestCallMapV2toV3Invariants pins the map's internal shape: no duplicate v2
// paths, and complete coverage of every callable field on the v2 root client
// (so a real consumer never hits an op the map forgot). Transport is the only
// non-API field and is excluded.
func TestCallMapV2toV3Invariants(t *testing.T) {
	seen := make(map[string]bool, len(callMapV2toV3))
	for _, e := range callMapV2toV3 {
		require.NotEmpty(t, e.V2Path, "every entry must have a V2Path")
		key := pathString(e.V2Path)
		require.Falsef(t, seen[key], "duplicate v2 path client.%s", key)
		seen[key] = true
	}

	// Every top-level op field on the v2 root client (excluding Transport and
	// the sub-client structs, which are covered by their own nested rows) must
	// appear as the first segment of some mapped path.
	v2Snap, err := decodeSurface(2)
	require.NoError(t, err, "v2 surface")
	root, ok := lookupStruct(v2Snap, v2CallMapRootPkg, "Client")
	require.True(t, ok, "v2 opensearch.Client present in surface")

	firstSeg := make(map[string]bool)
	for _, e := range callMapV2toV3 {
		firstSeg[e.V2Path[0]] = true
	}
	for _, f := range root.Fields {
		if f.Name == "Transport" {
			continue
		}
		require.Truef(t, firstSeg[f.Name],
			"v2 opensearch.Client field %q is not covered by the call map", f.Name)
	}
}

// pathString renders a call path for messages, e.g. ["Indices","Exists"] ->
// "Indices.Exists".
func pathString(path []string) string {
	return strings.Join(path, ".")
}
