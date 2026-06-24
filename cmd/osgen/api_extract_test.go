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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
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

	// Integer param whose 0 is meaningful for this operation (search size=0
	// returns aggregations with no hits) is promoted to *int so the != 0
	// emission guard cannot drop a deliberate 0.
	require.True(t, paramMap["size"].IsInt)
	require.Equal(t, "*int", paramMap["size"].GoType)

	// Integer param whose 0 is NOT meaningful stays a plain int (not in the
	// per-operation allowlist).
	require.True(t, paramMap["terminate_after"].IsInt)
	require.Equal(t, "int", paramMap["terminate_after"].GoType)

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
							"name":   "terminate_after",
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

// buildTestSpecWithRawMessageFixes builds a spec exercising the two
// json.RawMessage generator fixes end to end:
//
//   - cat.things: a response field typed as the OpenAPI 3.1 nullable form
//     ["null","string"], which must resolve to a typed (pointer) field rather
//     than json.RawMessage.
//   - alias.get: a response whose component schema is a bare $ref alias to a
//     real object schema, which must resolve through the alias to a typed
//     struct rather than degrading the whole response to raw.
func buildTestSpecWithRawMessageFixes(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_cat/things": map[string]any{
				"get": map[string]any{
					"x-operation-group": "cat.things",
					"x-version-added":   "1.0",
					"description":       "Nullable-scalar response field.",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "OK",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/cat.things___ThingsResponse",
									},
								},
							},
						},
					},
				},
			},
			"/_alias/thing": map[string]any{
				"get": map[string]any{
					"x-operation-group": "alias.get",
					"x-version-added":   "1.0",
					"description":       "Bare-$ref alias response.",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "OK",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/alias._common___AliasResponse",
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
				// Nullable-scalar field: ["null","string"].
				"cat.things___ThingsResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"docs_count": map[string]any{"type": []any{"null", "string"}},
					},
				},
				// Bare-$ref alias chain: AliasResponse -> AliasResponseBase (object).
				"alias._common___AliasResponse": map[string]any{
					"$ref": "#/components/schemas/alias._common___AliasResponseBase",
				},
				"alias._common___AliasResponseBase": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"acknowledged": map[string]any{"type": "boolean"},
					},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}

// TestRawMessageFixes_EndToEnd drives the full extract -> populate pipeline and
// asserts the two json.RawMessage fixes produce typed Resp structs end to end:
// the nullable-scalar field becomes a typed (pointer) field, and the bare-$ref
// alias response resolves to its target struct. Both previously degraded to
// json.RawMessage / RespShapeRaw.
func TestRawMessageFixes_EndToEnd(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpecWithRawMessageFixes(t)
	ops, spec, _, _, err := extractOperations(specPath, nil, VersionRange{})
	require.NoError(t, err)

	registry := newTypeRegistry(opensearchAPIPkgName)
	populateResponseTypes(ops, spec, registry, VersionRange{})

	byGroup := make(map[string]*apiOperation, len(ops))
	for i := range ops {
		byGroup[ops[i].Group] = &ops[i]
	}

	t.Run("nullable scalar field is typed, not raw", func(t *testing.T) {
		op := byGroup["cat.things"]
		require.NotNil(t, op)
		require.NotEqual(t, ir.RespShapeRaw, op.RespShape, "response must not degrade to raw")
		require.NotEmpty(t, op.RespFields, "response struct must have fields")

		var docs *goField
		for i := range op.RespFields {
			if op.RespFields[i].JSONName == "docs_count" {
				docs = &op.RespFields[i]
			}
		}
		require.NotNil(t, docs, "docs_count field must be present")
		// ["null","string"] -> nullable string -> *string, not json.RawMessage.
		require.Equal(t, "*string", docs.GoType)
		require.True(t, docs.IsPointer)
		require.NotContains(t, docs.GoType, "json.RawMessage")
	})

	t.Run("bare-$ref alias response resolves to typed struct", func(t *testing.T) {
		op := byGroup["alias.get"]
		require.NotNil(t, op)
		// ResponseRef must have been resolved through the alias to the terminal.
		require.Equal(t, "alias._common___AliasResponseBase", op.ResponseRef)
		require.NotEqual(t, ir.RespShapeRaw, op.RespShape, "response must not degrade to raw")
		require.NotEmpty(t, op.RespFields, "aliased response struct must have fields")

		var acked *goField
		for i := range op.RespFields {
			if op.RespFields[i].JSONName == "acknowledged" {
				acked = &op.RespFields[i]
			}
		}
		require.NotNil(t, acked, "acknowledged field must come through the alias")
		require.Equal(t, "*bool", acked.GoType)
	})
}

// buildTestSpecWithUnregisteredResponses builds a spec whose responses do not
// register as named structs, exercising the classifyRespShape fallback paths:
//
//   - shapeless.get: response is a bare `type: object` with no properties and no
//     additionalProperties -> legitimately RespShapeRaw (e.g. SQLStats-style).
//   - mapresp.get: response is `type: object` with additionalProperties and no
//     named properties -> RespShapeMap.
//   - arrayresp.get: response is `type: array` -> RespShapeArray.
func buildTestSpecWithUnregisteredResponses(t *testing.T) string {
	t.Helper()
	resp := func(schema map[string]any) map[string]any {
		return map[string]any{
			"200": map[string]any{
				"description": "OK",
				"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
			},
		}
	}
	op := func(group string, responses map[string]any) map[string]any {
		return map[string]any{
			"get": map[string]any{
				"x-operation-group": group,
				"x-version-added":   "1.0",
				"description":       group,
				"responses":         responses,
			},
		}
	}
	spec := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			// Inline shapeless object: no properties, no additionalProperties.
			"/_shapeless": op("shapeless.get", resp(map[string]any{"type": "object"})),
			// Inline map: additionalProperties, no named properties.
			"/_mapresp": op("mapresp.get", resp(map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			})),
			// Inline array.
			"/_arrayresp": op("arrayresp.get", resp(map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			})),
		},
	}
	return writeTestSpec(t, spec)
}

// TestClassifyRespShape pins the response-shape fallback for operations whose
// response is not a registered struct. It guards the json.RawMessage work: a
// genuinely shapeless response must classify as RespShapeRaw (not be
// accidentally over-typed), while map and array responses classify as their
// respective shapes. The cases are resolved from a real spec so the schema the
// classifier sees matches what the pipeline produces.
func TestClassifyRespShape(t *testing.T) {
	t.Parallel()

	specPath := buildTestSpecWithUnregisteredResponses(t)
	ops, spec, _, _, err := extractOperations(specPath, nil, VersionRange{})
	require.NoError(t, err)

	byGroup := make(map[string]*apiOperation, len(ops))
	for i := range ops {
		byGroup[ops[i].Group] = &ops[i]
	}

	registry := newTypeRegistry(opensearchAPIPkgName)

	tests := []struct {
		name  string
		group string
		want  ir.RespShape
	}{
		{name: "shapeless object stays raw", group: "shapeless.get", want: ir.RespShapeRaw},
		{name: "additionalProperties is map", group: "mapresp.get", want: ir.RespShapeMap},
		{name: "array response is array", group: "arrayresp.get", want: ir.RespShapeArray},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := byGroup[tt.group]
			require.NotNil(t, op, "operation %q must exist", tt.group)
			// Call the classifier directly: it is reached for any response that
			// does not resolve to a registered struct.
			classifyRespShape(op, spec, registry)
			require.Equal(t, tt.want, op.RespShape)
		})
	}
}
