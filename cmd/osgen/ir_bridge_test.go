// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestConvertToIR_BasicStructure(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpec(t)
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
	require.Equal(t, "Index", refreshOp.PathFields[0].GoName)
	require.True(t, refreshOp.PathFields[0].IsList)
}

func TestConvertToIR_QueryParams(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpecWithQueryParams(t)
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
		{name: "bool", param: apiQueryParam{IsBool: true, GoType: "bool"}, want: ir.ParamBool},
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
