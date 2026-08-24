// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestConvertToIR_BasicStructure(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpec(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(specPath, nil, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 2)

	reg := newTypeRegistry(opensearchAPIPkgName)
	spec := convertToIR(ops, reg)

	require.Len(t, spec.Operations, 2)
	require.NotNil(t, spec.Registry)

	// Verify cluster.health operation.
	var healthOp *ir.Operation
	for _, op := range spec.Operations {
		if op.Group == "cluster.health" {
			healthOp = op
			break
		}
	}
	require.NotNil(t, healthOp, "cluster.health operation not found")
	require.Equal(t, "ClusterHealth", healthOp.TypePrefix)
	require.Equal(t, "Returns cluster health.", healthOp.Description)
	require.Empty(t, healthOp.PathFields)
	require.True(t, healthOp.IsPointerReq)

	// Verify indices.refresh operation.
	var refreshOp *ir.Operation
	for _, op := range spec.Operations {
		if op.Group == "indices.refresh" {
			refreshOp = op
			break
		}
	}
	require.NotNil(t, refreshOp, "indices.refresh operation not found")
	require.Equal(t, "IndicesRefresh", refreshOp.TypePrefix)
	require.Len(t, refreshOp.PathFields, 1)
	// The array-capable "index" path parameter is pluralized to Indices.
	require.Equal(t, "Indices", refreshOp.PathFields[0].GoName)
	require.True(t, refreshOp.PathFields[0].IsList)
}

func TestConvertToIR_QueryParams(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpecWithQueryParams(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(specPath, nil, VersionRange{})
	require.NoError(t, err)
	require.NotEmpty(t, ops)

	reg := newTypeRegistry(opensearchAPIPkgName)
	spec := convertToIR(ops, reg)

	op := spec.Operations[0]
	require.NotEmpty(t, op.QueryParams)

	// Find the timeout param (should be Duration kind).
	var timeoutParam *ir.QueryParam
	for i := range op.QueryParams {
		if op.QueryParams[i].GoName == "Timeout" {
			timeoutParam = &op.QueryParams[i]
			break
		}
	}
	if timeoutParam != nil {
		require.Equal(t, ir.ParamDuration, timeoutParam.Kind)
	}
}

func TestConvertToIR_TypeRegistry(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpecWithResponseSchema(t)
	ops, spec, _, _, err := extractOperations(specPath, nil, VersionRange{})
	require.NoError(t, err)

	reg := newTypeRegistry(opensearchAPIPkgName)
	populateResponseTypes(ops, spec, reg, VersionRange{})

	irSpec := convertToIR(ops, reg)

	// Types from the legacy registry should be converted.
	require.NotEmpty(t, irSpec.Types)

	// Verify we can look up by name.
	for _, typ := range irSpec.Types {
		found, ok := irSpec.Registry.LookupByName(typ.Name)
		require.True(t, ok, "type %s not found in IR registry", typ.Name)
		require.Equal(t, typ.Name, found.Name)
	}
}

func TestClassifyParamKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		param apiQueryParam
		want  ir.ParamKind
	}{
		{name: "string", param: apiQueryParam{GoType: "string"}, want: ir.ParamString},
		{name: "bool", param: apiQueryParam{IsBool: true, GoType: "*bool"}, want: ir.ParamBool},
		{name: "int", param: apiQueryParam{IsInt: true, GoType: "int"}, want: ir.ParamInt},
		{name: "duration", param: apiQueryParam{IsDuration: true, GoType: "time.Duration"}, want: ir.ParamDuration},
		{name: "list", param: apiQueryParam{IsList: true, GoType: "[]string"}, want: ir.ParamList},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyParamKind(tt.param)
			require.Equal(t, tt.want, got)
		})
	}
}

// valuesToConsts wraps bare wire values as constEnumValue entries (value only,
// no doc/version metadata) for convertEnumMembers, mirroring how the int-backed
// enum path supplies them.
func valuesToConsts(values []string) []constEnumValue {
	if values == nil {
		return nil
	}
	consts := make([]constEnumValue, len(values))
	for i, v := range values {
		consts[i] = constEnumValue{Value: v}
	}
	return consts
}

func TestConvertEnumMembers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		typeName  string
		values    []string
		wantNames []string // parallel to values; nil values -> nil result
	}{
		{
			name:     "simple values",
			typeName: "RestStatus",
			values:   []string{"OK", "NOT_FOUND"},
			// "OK" titlecases to "Ok" (ok is not an acronym; matches SecurityOk).
			wantNames: []string{"RestStatusOk", "RestStatusNotFound"},
		},
		{
			name:     "acronyms expand via titleSegment",
			typeName: "RestStatus",
			values:   []string{"HTTP_VERSION_NOT_SUPPORTED", "REQUEST_URI_TOO_LONG"},
			wantNames: []string{
				"RestStatusHTTPVersionNotSupported",
				"RestStatusRequestURITooLong",
			},
		},
		{
			name:      "nil values yields nil",
			typeName:  "RestStatus",
			values:    nil,
			wantNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := convertEnumMembers(tt.typeName, valuesToConsts(tt.values), newSet(tt.typeName+"Unknown"))
			if tt.wantNames == nil {
				require.Nil(t, got)
				return
			}
			want := make([]ir.EnumMember, len(tt.values))
			for i, v := range tt.values {
				want[i] = ir.EnumMember{ConstName: tt.wantNames[i], Value: v}
			}
			require.Equal(t, want, got)
		})
	}
}

// TestConvertEnumMembers_Panics covers the guards that fail generation loudly
// rather than emit uncompilable Go: values that collapse to the same const
// name, a value that yields an invalid identifier, and a degenerate value that
// collides with the type name or the <Type>Unknown sentinel.
func TestConvertEnumMembers_Panics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		typeName string
		values   []string
	}{
		{
			name:     "duplicate const names collide",
			typeName: "RestStatus",
			values:   []string{"FOO_BAR", "FOO__BAR"}, // both -> RestStatusFooBar
		},
		{
			name:     "value with no identifier characters",
			typeName: "RestStatus",
			values:   []string{"%%%"}, // -> bare "RestStatus", collides with type name
		},
		{
			name:     "empty value collides with type name",
			typeName: "RestStatus",
			values:   []string{""}, // -> "RestStatus" == type name
		},
		{
			name:     "value mapping to Unknown sentinel",
			typeName: "RestStatus",
			values:   []string{"UNKNOWN"}, // -> "RestStatusUnknown" == sentinel
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Panics(t, func() {
				convertEnumMembers(tt.typeName, valuesToConsts(tt.values), newSet(tt.typeName+"Unknown"))
			})
		})
	}
}
