// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/renameio/v2/maybe"
	"github.com/stretchr/testify/require"
)

func TestExtractOperations(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)

	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, nil, VersionRange{})
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
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, filter, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)
	require.Equal(t, "cluster.health", ops[0].Group)
}

func TestExtractOperations_SkipsIgnorable(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithIgnorable(t)

	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, nil, VersionRange{})
	require.NoError(t, err)

	for _, op := range ops {
		require.NotEqual(t, "ignorable.endpoint", op.Group)
	}
}

func TestBuildAPIOperation_Metadata(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"cluster.health": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Equal(t, "ClusterHealth", op.TypePrefix)
	require.Equal(t, "ClusterHealthPath", op.PathBuilderName)
	require.Equal(t, []string{http.MethodGet}, op.HTTPMethods)
	require.Contains(t, op.PrimaryPath, "/_cluster/health")
	require.Equal(t, "Returns cluster health.", op.Description)
	require.Equal(t, "1.0", op.VersionAdded)
	require.False(t, op.Deprecated)
	require.False(t, op.HasBody)
}

func TestBuildAPIOperation_WithBody(t *testing.T) {
	t.Parallel()

	spec := buildTestSpec(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"indices.refresh": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Equal(t, "IndicesRefresh", op.TypePrefix)
	require.Equal(t, []string{http.MethodPost}, op.HTTPMethods)
}

func TestBuildAPIOperation_Deprecated(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithDeprecated(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"old.api": true}, VersionRange{})
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
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"indices.refresh": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Len(t, op.PathFields, 1)
	// The array-capable "index" path parameter is pluralized to Indices.
	require.Equal(t, "Indices", op.PathFields[0].GoName)
	require.True(t, op.PathFields[0].IsList)
}

func TestBuildAPIOperation_QueryParams(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithQueryParams(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"search": true}, VersionRange{})
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
	require.Equal(t, "*bool", paramMap["allow_partial_results"].GoType)

	// Integer param.
	require.True(t, paramMap["size"].IsInt)
	require.Equal(t, "int", paramMap["size"].GoType)

	// Duration param (non-shared; timeout is now a shared param).
	require.True(t, paramMap["scroll"].IsDuration)
	require.Equal(t, "time.Duration", paramMap["scroll"].GoType)

	// List param.
	require.True(t, paramMap["expand_wildcards"].IsList)
	require.Equal(t, "[]string", paramMap["expand_wildcards"].GoType)

	// Global params should be excluded.
	_, hasPretty := paramMap["pretty"]
	require.False(t, hasPretty)

	// Shared timeout params should be excluded.
	_, hasTimeout := paramMap["timeout"]
	require.False(t, hasTimeout)
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
		{"format", true},
		{"help", true},
		{"v", true},
		{"s", true},
		{"h", true},
		{"timeout", true},
		{"cluster_manager_timeout", true},
		{"master_timeout", true},
		{"index", false},
		{"level", false},
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
			name:   "integer type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isInt:  true,
		},
		{
			name:   "number type",
			schema: &openapi3.Schema{Type: &openapi3.Types{"number"}},
			ref:    &openapi3.ParameterRef{Value: &openapi3.Parameter{}},
			isInt:  true,
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
				require.Equal(t, "*bool", goType)
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
					"x-ignorable":       true,
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
					"x-operation-group":     "old.api",
					"x-version-added":       "1.0",
					"x-version-deprecated":  "2.0",
					"x-deprecation-message": "Use new.api instead.",
					"deprecated":            true,
					"responses":             map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

func TestBuildAPIOperation_PathFieldUnion(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithMultipleVariants(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"indices.refresh": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	op := ops[0]
	require.Len(t, op.PathFields, 1)
	// The array-capable "index" path parameter is pluralized to Indices.
	require.Equal(t, "Indices", op.PathFields[0].GoName)
	require.True(t, op.PathFields[0].IsList)
}

func TestBuildAPIOperation_ResponseRef(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithResponseSchema(t)
	//nolint:dogsled // test only cares about ops + err
	ops, _, _, _, err := extractOperations(spec, map[string]bool{"cluster.health": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	require.Equal(t, "cluster.health___HealthResponseBody", ops[0].ResponseRef)
}

func TestPopulateResponseTypes(t *testing.T) {
	t.Parallel()

	spec := buildTestSpecWithResponseSchema(t)
	ops, loadedSpec, _, _, err := extractOperations(spec, map[string]bool{"cluster.health": true}, VersionRange{})
	require.NoError(t, err)
	require.Len(t, ops, 1)

	registry := newTypeRegistry(opensearchAPIPkgName)
	populateResponseTypes(ops, loadedSpec, registry, VersionRange{})

	require.NotEmpty(t, ops[0].RespFields)

	fieldMap := make(map[string]goField)
	for _, f := range ops[0].RespFields {
		fieldMap[f.JSONName] = f
	}
	require.Equal(t, "string", fieldMap["cluster_name"].GoType)
	require.Equal(t, "string", fieldMap["status"].GoType)
	require.Equal(t, "*bool", fieldMap["timed_out"].GoType)
}

func buildTestSpecWithMultipleVariants(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_refresh": map[string]any{
				"post": map[string]any{
					"x-operation-group": "indices.refresh",
					"x-version-added":   "1.0",
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

func buildTestSpecWithResponseSchema(t *testing.T) string {
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
					"responses": map[string]any{
						"200": map[string]any{
							"description": "OK",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/cluster.health___HealthResponseBody",
									},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"cluster.health___HealthResponseBody": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cluster_name":    map[string]any{"type": "string"},
						"status":          map[string]any{"type": "string"},
						"timed_out":       map[string]any{"type": "boolean"},
						"number_of_nodes": map[string]any{"type": "integer"},
					},
					"required": []any{"cluster_name", "status"},
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
						map[string]any{
							"name":   "allow_partial_results",
							"in":     "query",
							"schema": map[string]any{"type": "boolean"},
						},
						map[string]any{
							"name":   "size",
							"in":     "query",
							"schema": map[string]any{"type": "integer"},
						},
						map[string]any{
							"name": "timeout",
							"in":   "query",
							"schema": map[string]any{
								"type":        "string",
								"pattern":     "([0-9]+)(?:d|h|m|s|ms|micros|nanos)",
								"x-data-type": "time",
							},
						},
						map[string]any{
							"name": "scroll",
							"in":   "query",
							"schema": map[string]any{
								"type":        "string",
								"pattern":     "([0-9]+)(?:d|h|m|s|ms|micros|nanos)",
								"x-data-type": "time",
							},
						},
						map[string]any{
							"name": "expand_wildcards",
							"in":   "query",
							"schema": map[string]any{
								"oneOf": []any{
									map[string]any{"type": "string"},
									map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								},
							},
						},
						map[string]any{
							"name":   "pretty",
							"in":     "query",
							"schema": map[string]any{"type": "boolean"},
						},
					},
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					},
					"responses": map[string]any{"200": map[string]any{"description": "OK"}},
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
	require.NoError(t, maybe.WriteFile(path, data, 0o600))
	return path
}
