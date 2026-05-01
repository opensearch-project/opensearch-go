// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestExtractOperations(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)

	ops, err := extractOperations(spec, nil)
	require.NoError(t, err)
	require.NotEmpty(t, ops)

	// Should have at least cluster.health and indices.refresh.
	groupNames := make(map[string]bool)
	for _, op := range ops {
		groupNames[op.Group] = true
	}
	require.True(t, groupNames["cluster.health"])
	require.True(t, groupNames["indices.refresh"])
}

func TestExtractOperations_Filter(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)

	filter := map[string]bool{"cluster.health": true}
	ops, err := extractOperations(spec, filter)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	require.Equal(t, "cluster.health", ops[0].Group)
}

func TestExtractOperations_SkipsIgnorable(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithIgnorable(t)

	ops, err := extractOperations(spec, nil)
	require.NoError(t, err)

	for _, op := range ops {
		require.NotEqual(t, "ignorable.endpoint", op.Group)
	}
}

func TestBuildAPIOperation_Metadata(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)
	ops, err := extractOperations(spec, map[string]bool{"cluster.health": true})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Equal(t, "ClusterHealth", op.TypePrefix)
	require.Equal(t, "ClusterHealthPath", op.PathBuilderName)
	require.Equal(t, "http.MethodGet", op.HTTPMethod)
	require.Equal(t, "GET", op.HTTPVerb)
	require.Contains(t, op.PrimaryPath, "/_cluster/health")
	require.Equal(t, "Returns cluster health.", op.Description)
	require.Equal(t, "1.0", op.VersionAdded)
	require.False(t, op.Deprecated)
	require.False(t, op.HasBody)
}

func TestBuildAPIOperation_WithBody(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)
	ops, err := extractOperations(spec, map[string]bool{"indices.refresh": true})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Equal(t, "IndicesRefresh", op.TypePrefix)
	require.Equal(t, "http.MethodPost", op.HTTPMethod)
	require.Equal(t, "POST", op.HTTPVerb)
}

func TestBuildAPIOperation_Deprecated(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithDeprecated(t)
	ops, err := extractOperations(spec, map[string]bool{"old.api": true})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.True(t, op.Deprecated)
	require.Equal(t, "Use new.api instead.", op.DeprecationMsg)
	require.Equal(t, "2.0", op.VersionDeprecated)
}

func TestBuildAPIOperation_PathFields(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)
	ops, err := extractOperations(spec, map[string]bool{"indices.refresh": true})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Len(t, op.PathFields, 1)
	require.Equal(t, "Index", op.PathFields[0].GoName)
	require.True(t, op.PathFields[0].IsList)
}

func TestBuildAPIOperation_QueryParams(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithQueryParams(t)
	ops, err := extractOperations(spec, map[string]bool{"search": true})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.NotEmpty(t, op.QueryParams)

	paramMap := make(map[string]apiQueryParam)
	for _, p := range op.QueryParams {
		paramMap[p.ParamName] = p
	}

	// Boolean param.
	require.True(t, paramMap["allow_partial_results"].IsBool)
	require.Equal(t, "bool", paramMap["allow_partial_results"].GoType)

	// Integer param.
	require.True(t, paramMap["size"].IsInt)
	require.Equal(t, "int", paramMap["size"].GoType)

	// Duration param.
	require.True(t, paramMap["timeout"].IsDuration)
	require.Equal(t, "time.Duration", paramMap["timeout"].GoType)

	// List param.
	require.True(t, paramMap["expand_wildcards"].IsList)
	require.Equal(t, "[]string", paramMap["expand_wildcards"].GoType)

	// Global params should be excluded.
	_, hasPretty := paramMap["pretty"]
	require.False(t, hasPretty)
}

func TestIsGlobalParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{"pretty", true},
		{"human", true},
		{"error_trace", true},
		{"source", true},
		{"filter_path", true},
		{"timeout", false},
		{"index", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isGlobalParam(tt.name))
		})
	}
}

func TestClassifyParamSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schema     *openapi3.Schema
		ref        *openapi3.ParameterRef
		wantType   string
		isDuration bool
		isBool     bool
		isList     bool
		isInt      bool
	}{
		{
			name:   "string type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"string"}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
		},
		{
			name:   "boolean type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"boolean"}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isBool: true,
		},
		{
			name:  "integer type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
			ref:   &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isInt: true,
		},
		{
			name:  "number type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"number"}},
			ref:   &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isInt: true,
		},
		{
			name:   "array type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"array"}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isList: true,
		},
		{
			name:   "oneOf with array",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}}}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isList: true,
		},
		{
			name:       "duration via pattern",
			schema:     &openapi3.Schema{Pattern: "([0-9]+)(?:d|h|m|s|ms|micros|nanos)"},
			ref:        &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isDuration: true,
		},
		{
			name:   "duration via ref",
			schema: &openapi3.Schema{},
			ref: &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/Duration"},
			}},
			isDuration: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			goType, isDur, isBool, isList, isInt := classifyParamSchema(tt.schema, tt.ref)

			require.Equal(t, tt.isDuration, isDur, "isDuration")
			require.Equal(t, tt.isBool, isBool, "isBool")
			require.Equal(t, tt.isList, isList, "isList")
			require.Equal(t, tt.isInt, isInt, "isInt")

			switch {
			case tt.isDuration:
				require.Equal(t, "time.Duration", goType)
			case tt.isBool:
				require.Equal(t, "bool", goType)
			case tt.isInt:
				require.Equal(t, "int", goType)
			case tt.isList:
				require.Equal(t, "[]string", goType)
			default:
				require.Equal(t, "string", goType)
			}
		})
	}
}

func TestIsDurationSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		ref    *openapi3.ParameterRef
		want   bool
	}{
		{
			name:   "nanos pattern",
			schema: &openapi3.Schema{Pattern: "([0-9]+)(?:nanos|ms|s|m|h)"},
			ref:    nil,
			want:   true,
		},
		{
			name:   "no pattern",
			schema: &openapi3.Schema{},
			ref:    nil,
			want:   false,
		},
		{
			name:   "Duration ref",
			schema: &openapi3.Schema{},
			ref: &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/Duration"},
			}},
			want: true,
		},
		{
			name:   "non-Duration ref",
			schema: &openapi3.Schema{},
			ref: &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/SomeOther"},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isDurationSchema(tt.schema, tt.ref))
		})
	}
}

func TestHasOneOfType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		schema   *openapi3.Schema
		typeName string
		want     bool
	}{
		{
			name:     "oneOf match",
			schema:   &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"array"}}}}},
			typeName: "array",
			want:     true,
		},
		{
			name:     "anyOf match",
			schema:   &openapi3.Schema{AnyOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}},
			typeName: "string",
			want:     true,
		},
		{
			name:     "no match",
			schema:   &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{Type: &openapi3.Types{"boolean"}}}}},
			typeName: "array",
			want:     false,
		},
		{
			name:     "empty schema",
			schema:   &openapi3.Schema{},
			typeName: "array",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, hasOneOfType(tt.schema, tt.typeName))
		})
	}
}

// --- Test helper spec builders ---

func buildTestSpec(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_cluster/health": map[string]any{
				"get": map[string]any{
					"x-operation-group": "cluster.health",
					"x-version-added":   "1.0",
					"description":       "Returns cluster health.",
					"responses":         map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
			"/{index}/_refresh": map[string]any{
				"post": map[string]any{
					"x-operation-group": "indices.refresh",
					"x-version-added":   "1.0",
					"parameters": []any{
						map[string]any{
							"name":   "index",
							"in":     "path",
							"schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
					"responses": map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

func buildTestSpecWithIgnorable(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_cluster/health": map[string]any{
				"get": map[string]any{
					"x-operation-group": "cluster.health",
					"responses":         map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
			"/_ignorable": map[string]any{
				"get": map[string]any{
					"x-operation-group": "ignorable.endpoint",
					"x-ignorable":      true,
					"responses":         map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

func buildTestSpecWithDeprecated(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_old": map[string]any{
				"get": map[string]any{
					"x-operation-group":    "old.api",
					"x-version-added":      "1.0",
					"x-version-deprecated": "2.0",
					"x-deprecation-message": "Use new.api instead.",
					"deprecated":           true,
					"responses":            map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

func buildTestSpecWithQueryParams(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_search": map[string]any{
				"post": map[string]any{
					"x-operation-group": "search",
					"x-version-added":   "1.0",
					"parameters": []any{
						map[string]any{"name": "allow_partial_results", "in": "query", "schema": map[string]any{"type": "boolean"}},
						map[string]any{"name": "size", "in": "query", "schema": map[string]any{"type": "integer"}},
						map[string]any{"name": "timeout", "in": "query", "schema": map[string]any{"type": "string", "pattern": "([0-9]+)(?:d|h|m|s|ms|micros|nanos)", "x-data-type": "time"}},
						map[string]any{"name": "expand_wildcards", "in": "query", "schema": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}},
						map[string]any{"name": "pretty", "in": "query", "schema": map[string]any{"type": "boolean"}},
					},
					"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
					"responses":   map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

func writeTestSpec(t *testing.T, spec map[string]any) string {
	t.Helper()
	data, err := json.Marshal(spec)
	require.NoError(t, err)

	path := t.TempDir() + "/spec.json"
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}
