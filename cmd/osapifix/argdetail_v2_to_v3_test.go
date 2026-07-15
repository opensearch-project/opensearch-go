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

func TestArgDetailSeedOpsPresent(t *testing.T) {
	ping, ok := argDetailV2toV3["Ping"]
	require.True(t, ok, "Ping arg-detail missing")
	require.Empty(t, ping.Positionals, "Ping has no positional args")
	require.Equal(t, destContext, ping.Options["WithContext"].Kind)
	require.Equal(t, destDropped, ping.Options["WithFilterPath"].Kind)

	exists, ok := argDetailV2toV3["Indices.Exists"]
	require.True(t, ok, "Indices.Exists arg-detail missing")
	require.Equal(t, "Indices", exists.Positionals[0].ReqField)
	require.Equal(t, destParams, exists.Options["WithAllowNoIndices"].Kind)
	require.Equal(t, "AllowNoIndices", exists.Options["WithAllowNoIndices"].Field)
}

// TestArgDetailV2toV3AgainstSurfaces is the drift guard: for each seed op,
// every v3 destination field named in the arg-detail must exist on the op's v3
// Req or Params struct, and every destParams/destReqField option must have a
// backing field on the v2 Request struct. Mirrors TestCallMapV2toV3AgainstSurfaces.
func TestArgDetailV2toV3AgainstSurfaces(t *testing.T) {
	v2Snap, err := decodeSurface(2)
	require.NoError(t, err)
	v3Snap, err := decodeSurface(3)
	require.NoError(t, err)

	// v3 Req name per op comes from the call map (single source of truth).
	reqNameByPath := make(map[string]string, len(callMapV2toV3))
	for _, e := range callMapV2toV3 {
		if e.V3Req != "" {
			reqNameByPath[pathString(e.V2Path)] = e.V3Req
		}
	}

	fieldSet := func(snap *apirev.Snapshot, pkg, structName string) map[string]bool {
		st, ok := lookupStruct(snap, pkg, structName)
		require.Truef(t, ok, "struct %s.%s not found in surface", pkg, structName)
		m := make(map[string]bool, len(st.Fields))
		for _, f := range st.Fields {
			m[f.Name] = true
		}
		return m
	}

	// fieldPtr maps field name -> IsPointer for the named struct, so the arg-detail
	// IsPtr flag can be checked against the real v3 field pointer-ness (the drift
	// guard that catches a *bool field emitting a bare value).
	fieldPtr := func(snap *apirev.Snapshot, pkg, structName string) map[string]bool {
		st, ok := lookupStruct(snap, pkg, structName)
		require.Truef(t, ok, "struct %s.%s not found in surface", pkg, structName)
		m := make(map[string]bool, len(st.Fields))
		for _, f := range st.Fields {
			m[f.Name] = f.IsPointer
		}
		return m
	}

	// fieldType maps field name -> declared type string for the named struct, so
	// the Params-field convention (a destParams op emits Req{Params: <Op>Params{}})
	// can be checked against the real v3 Req field type.
	fieldType := func(snap *apirev.Snapshot, pkg, structName string) map[string]string {
		st, ok := lookupStruct(snap, pkg, structName)
		require.Truef(t, ok, "struct %s.%s not found in surface", pkg, structName)
		m := make(map[string]string, len(st.Fields))
		for _, f := range st.Fields {
			m[f.Name] = f.Type
		}
		return m
	}

	for path, detail := range argDetailV2toV3 {
		reqName := reqNameByPath[path]
		require.NotEmptyf(t, reqName, "no V3Req in call map for %s", path)

		// Derive Params name: "PingReq" -> "PingParams", "IndicesExistsReq" -> "IndicesExistsParams".
		paramsName := strings.TrimSuffix(reqName, "Req") + "Params"

		reqFields := fieldSet(v3Snap, v3CallMapAPIPkg, reqName)
		paramsFields := fieldSet(v3Snap, v3CallMapAPIPkg, paramsName)
		paramsPtr := fieldPtr(v3Snap, v3CallMapAPIPkg, paramsName)
		reqFieldType := fieldType(v3Snap, v3CallMapAPIPkg, reqName)

		// v2 Request struct name: op path -> e.g. "Ping" -> "PingRequest", "Indices.Exists" -> "IndicesExistsRequest".
		v2ReqName := strings.ReplaceAll(path, ".", "") + "Request"
		v2Fields := fieldSet(v2Snap, v2CallMapRootPkg+"/opensearchapi", v2ReqName)

		// If the op carries any destParams option, the emit nests them under a Req
		// field literally named "Params" typed <Op>Params (rewrite_idiom2.go). The
		// v3-field checks below confirm each param lands on <Op>Params, but not that
		// the Req actually HAS a Params field of that type - a convention the emit
		// leans on. Guard it here so a future op whose Req names that field
		// differently is caught at test time, not as non-compiling emitted output.
		hasParamsOption := false
		for _, dest := range detail.Options {
			if dest.Kind == destParams {
				hasParamsOption = true
				break
			}
		}
		if hasParamsOption {
			require.Truef(t, reqFields["Params"],
				"%s carries a destParams option but %s has no Params field", path, reqName)
			wantType := v3CallMapAPIPkg + "." + paramsName
			require.Equalf(t, wantType, reqFieldType["Params"],
				"%s Req field Params is %q, want %q (the <Op>Params the emit nests)", path, reqFieldType["Params"], wantType)
		}

		for i, p := range detail.Positionals {
			require.Truef(t, reqFields[p.ReqField],
				"%s positional[%d] -> %s.%s missing in v3", path, i, reqName, p.ReqField)
			// Positionals are constructor args (e.g. []string{"index"} -> Indices),
			// not struct fields on v2 Request, so no v2-side field check here.
		}

		for opt, dest := range detail.Options {
			switch dest.Kind {
			case destParams:
				require.Truef(t, paramsFields[dest.Field],
					"%s %s -> %s.%s missing in v3", path, opt, paramsName, dest.Field)
				// The arg-detail IsPtr flag must match the real v3 field pointer-ness:
				// a *bool field must be wrapped in opensearchapi.ToPointer, a value field
				// must not. This catches pointer-ness drift in the v3 Params struct.
				require.Equalf(t, paramsPtr[dest.Field], dest.IsPtr,
					"%s %s -> %s.%s IsPtr=%v but v3 field IsPointer=%v", path, opt, paramsName, dest.Field, dest.IsPtr, paramsPtr[dest.Field])
				// v2 field name matches the dest field name for all seed-op params options.
				require.Truef(t, v2Fields[dest.Field],
					"%s %s -> v2 %s.%s missing in v2 surface", path, opt, v2ReqName, dest.Field)
			case destReqField:
				require.Truef(t, reqFields[dest.Field],
					"%s %s -> %s.%s missing in v3", path, opt, reqName, dest.Field)
				// destReqField options (e.g. Body) may not have a same-named v2 field;
				// v3-side guard is sufficient here.
			case destContext, destDropped, destMarker:
				// no struct field to validate.
			}
		}
	}
}
