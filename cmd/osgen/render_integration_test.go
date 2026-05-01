// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender(t *testing.T) {
	t.Parallel()

	builders := []builder{
		{
			StructName:  "ClusterHealthPath",
			Comment:     "ClusterHealthPath builds URL paths for the cluster.health operation.",
			Group:       "cluster.health",
			Description: "Returns cluster health.",
			Fields:      nil,
			Ops: []emitOp{
				{Kind: opLit, Value: "_cluster"},
				{Kind: opLit, Value: "health"},
			},
		},
		{
			StructName: "IndicesRefreshPath",
			Comment:    "IndicesRefreshPath builds URL paths for the indices.refresh operation.",
			Group:      "indices.refresh",
			Fields: []builderField{
				{Name: "Index", Param: "index", Required: false, IsList: true},
			},
			Ops: []emitOp{
				{Kind: opIfList, Value: "Index"},
				{Kind: opList, Value: "Index"},
				{Kind: opEnd},
				{Kind: opLit, Value: "_refresh"},
			},
		},
	}

	src, err := render(builders, "path", true)
	require.NoError(t, err)
	require.Contains(t, src, "package path")
	require.Contains(t, src, "ClusterHealthPath")
	require.Contains(t, src, "IndicesRefreshPath")
	require.Contains(t, src, "func (p ClusterHealthPath) Build()")
	require.Contains(t, src, "func (p IndicesRefreshPath) Build()")

	// Output should be valid Go.
	assertValidGo(t, src)
}

func TestRender_RequiredField(t *testing.T) {
	t.Parallel()

	builders := []builder{
		{
			StructName: "IndicesDeletePath",
			Comment:    "IndicesDeletePath builds URL paths.",
			Group:      "indices.delete",
			Fields: []builderField{
				{Name: "Index", Param: "index", Required: true, IsList: true},
			},
			Ops: []emitOp{
				{Kind: opList, Value: "Index"},
			},
		},
	}

	src, err := render(builders, "path", true)
	require.NoError(t, err)
	require.Contains(t, src, "errRequired")
	require.Contains(t, src, `IndicesDeletePath.Index`)
	assertValidGo(t, src)
}

func TestRender_Unexported(t *testing.T) {
	t.Parallel()

	builders := []builder{
		{
			StructName: "TestPath",
			Comment:    "TestPath builds URL paths.",
			Group:      "test",
			Fields:     nil,
			Ops:        []emitOp{{Kind: opLit, Value: "_test"}},
		},
	}

	src, err := render(builders, "path", false)
	require.NoError(t, err)
	require.Contains(t, src, "func (p TestPath) build()")
	assertValidGo(t, src)
}

func TestRenderAPIFile(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:           "cluster.health",
		TypePrefix:      "ClusterHealth",
		PathBuilderName: "ClusterHealthPath",
		HTTPMethods:     []string{http.MethodGet},
		PrimaryPath:     "/_cluster/health",
		Description:     "Returns basic cluster health info.",
		VersionAdded:    "1.0",
		HasBody:         false,
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "package osapi")
	require.Contains(t, src, "ClusterHealthReq")
	require.Contains(t, src, "ClusterHealthResp")
	require.Contains(t, src, "GET /_cluster/health")
	require.Contains(t, src, "Available: >= 1.0.0")
	assertValidGo(t, src)
}

func TestRenderAPIFile_WithBody(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:           "search",
		TypePrefix:      "Search",
		PathBuilderName: "SearchPath",
		HTTPMethods:     []string{http.MethodGet, http.MethodPost},
		PrimaryPath:     "/_search",
		HasBody:         true,
		PathFields: []apiPathField{
			{GoName: "Index", IsList: true},
		},
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "io.Reader")
	require.Contains(t, src, "Body")
	assertValidGo(t, src)
}

func TestRenderAPIFile_Deprecated(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:             "old.endpoint",
		TypePrefix:        "OldEndpoint",
		PathBuilderName:   "OldEndpointPath",
		HTTPMethods:       []string{http.MethodGet},
		PrimaryPath:       "/_old",
		VersionAdded:      "1.0",
		VersionDeprecated: "2.0",
		Deprecated:        true,
		DeprecationMsg:    "Use new_endpoint instead.",
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "Deprecated: since 2.0.0.")
	require.Contains(t, src, "Available >= 1.0.0")
	require.Contains(t, src, "new_endpoint")
	assertValidGo(t, src)
}

func TestRenderAPIFile_Params(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:           "search",
		TypePrefix:      "Search",
		PathBuilderName: "SearchPath",
		HTTPMethods:     []string{http.MethodGet, http.MethodPost},
		PrimaryPath:     "/_search",
		VersionAdded:    "1.0",
		HasBody:         true,
		PathFields:      []apiPathField{{GoName: "Index", IsList: true}},
		QueryParams: []apiQueryParam{
			{GoName: "Size", ParamName: "size", GoType: "int", IsInt: true},
			{GoName: "Timeout", ParamName: "timeout", GoType: "time.Duration", IsDuration: true},
			{GoName: "AllowPartialResults", ParamName: "allow_partial_results", GoType: "bool", IsBool: true},
			{GoName: "ExpandWildcards", ParamName: "expand_wildcards", GoType: "[]string", IsList: true},
			{GoName: "Q", ParamName: "q", GoType: "string"},
		},
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "package osapi")
	require.Contains(t, src, "SearchParams")
	require.Contains(t, src, "Available: >= 1.0.0")
	require.Contains(t, src, `"strconv"`)
	require.Contains(t, src, `"time"`)
	require.Contains(t, src, `"strings"`)
	require.Contains(t, src, "formatDuration")
	require.Contains(t, src, "strconv.Itoa")
	assertValidGo(t, src)
}

func TestRenderAPIFile_ParamsDeprecated(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:             "old",
		TypePrefix:        "Old",
		PathBuilderName:   "OldPath",
		HTTPMethods:       []string{http.MethodGet},
		PrimaryPath:       "/_old",
		VersionAdded:      "1.0",
		VersionDeprecated: "3.0",
		Deprecated:        true,
		DeprecationMsg:    "Removed.",
		QueryParams:       []apiQueryParam{},
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "Deprecated: since 3.0.0.")
	require.Contains(t, src, "Available >= 1.0.0")
	require.Contains(t, src, "Removed.")
	assertValidGo(t, src)
}

func TestRenderCompatFile(t *testing.T) {
	t.Parallel()

	src, err := renderCompatFile("knn", false)
	require.NoError(t, err)
	require.Contains(t, src, "package knn")
	require.Contains(t, src, "Inspect")
	assertValidGo(t, src)
}

func TestRenderAPIFile_TypedResponse(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:           "cluster.health",
		TypePrefix:      "ClusterHealth",
		PathBuilderName: "ClusterHealthPath",
		HTTPMethods:     []string{http.MethodGet},
		PrimaryPath:     "/_cluster/health",
		VersionAdded:    "1.0",
		HasBody:         false,
		RespFields: []goField{
			{GoName: "ClusterName", JSONName: "cluster_name", GoType: "string"},
			{GoName: "Status", JSONName: "status", GoType: "string"},
			{GoName: "TimedOut", JSONName: "timed_out", GoType: "*bool", IsPointer: true, OmitEmpty: true},
			{GoName: "NumberOfNodes", JSONName: "number_of_nodes", GoType: "*int", IsPointer: true, OmitEmpty: true},
		},
		SiblingTypes: []*goType{
			{
				Name:    "ClusterHealthIndexStats",
				Comment: "Per-index health statistics.",
				Fields: []goField{
					{GoName: "Status", JSONName: "status", GoType: "string"},
					{GoName: "NumberOfShards", JSONName: "number_of_shards", GoType: "int"},
				},
			},
		},
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, "ClusterHealthResp")
	require.Contains(t, src, "ClusterName")
	require.Contains(t, src, `json:"cluster_name"`)
	require.Contains(t, src, "TimedOut")
	require.Contains(t, src, "*bool")
	require.Contains(t, src, `json:"timed_out,omitempty"`)
	require.Contains(t, src, "ClusterHealthIndexStats")
	require.Contains(t, src, "NumberOfShards")
	require.Contains(t, src, "response")
	require.Contains(t, src, "*opensearch.Response")
	require.Contains(t, src, "Inspect()")
	assertValidGo(t, src)
}

func TestRenderAPIFile_JSONRawMessage(t *testing.T) {
	t.Parallel()

	op := apiOperation{
		Group:           "search",
		TypePrefix:      "Search",
		PathBuilderName: "SearchPath",
		HTTPMethods:     []string{http.MethodGet, http.MethodPost},
		PrimaryPath:     "/_search",
		HasBody:         true,
		RespFields: []goField{
			{GoName: "Hits", JSONName: "hits", GoType: "json.RawMessage"},
		},
	}

	src, err := renderAPIFile(op, "osapi", newTypeRegistry("osapi"))
	require.NoError(t, err)
	require.Contains(t, src, `"encoding/json"`)
	require.Contains(t, src, "json.RawMessage")
	assertValidGo(t, src)
}

func TestRenderSharedTypesFile(t *testing.T) {
	t.Parallel()

	types := []*goType{
		{
			Name:    "ShardStatistics",
			Comment: "Shard-level statistics.",
			Fields: []goField{
				{GoName: "Total", JSONName: "total", GoType: "int"},
				{GoName: "Successful", JSONName: "successful", GoType: "int"},
				{GoName: "Failed", JSONName: "failed", GoType: "int"},
				{GoName: "Failures", JSONName: "failures", GoType: "[]ShardFailure", OmitEmpty: true},
			},
		},
		{
			Name: "ErrorCause",
			Fields: []goField{
				{GoName: "Type", JSONName: "type", GoType: "string"},
				{GoName: "Reason", JSONName: "reason", GoType: "string"},
				{GoName: "CausedBy", JSONName: "caused_by", GoType: "*ErrorCause", IsPointer: true, OmitEmpty: true},
			},
		},
	}

	src, err := renderSharedTypesFile(types, "osapi")
	require.NoError(t, err)
	require.Contains(t, src, "package osapi")
	require.Contains(t, src, "ShardStatistics")
	require.Contains(t, src, "ErrorCause")
	require.Contains(t, src, `json:"total"`)
	require.Contains(t, src, `json:"caused_by,omitempty"`)
	assertValidGo(t, src)
}

func TestGenerateTests(t *testing.T) {
	t.Parallel()

	builders := []builder{
		{
			StructName: "SearchPath",
			Group:      "search",
			Fields: []builderField{
				{Name: "Index", Param: "index", Required: false, IsList: true},
			},
			Ops: []emitOp{
				{Kind: opIfList, Value: "Index"},
				{Kind: opList, Value: "Index"},
				{Kind: opEnd},
				{Kind: opLit, Value: "_search"},
			},
		},
	}

	src, err := generateTests(builders, "path")
	require.NoError(t, err)
	require.Contains(t, src, "package path")
	require.Contains(t, src, "TestSearchPath_Build")
	require.Contains(t, src, "t.Parallel()")
	assertValidGo(t, src)
}

func TestGenerateTests_RequiredFields(t *testing.T) {
	t.Parallel()

	builders := []builder{
		{
			StructName: "IndicesDeletePath",
			Group:      "indices.delete",
			Fields: []builderField{
				{Name: "Index", Param: "index", Required: true, IsList: true},
			},
			Ops: []emitOp{
				{Kind: opList, Value: "Index"},
			},
		},
	}

	src, err := generateTests(builders, "path")
	require.NoError(t, err)
	require.Contains(t, src, "required fields empty")
	require.Contains(t, src, "wantErr: true")
	assertValidGo(t, src)
}

func TestSimulateBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		b      builder
		values map[string][]string
		want   string
	}{
		{
			name: "no ops",
			b:    builder{},
			want: "/",
		},
		{
			name: "literals only",
			b: builder{
				Ops: []emitOp{
					{Kind: opLit, Value: "_cluster"},
					{Kind: opLit, Value: "health"},
				},
			},
			want: "/_cluster/health",
		},
		{
			name: "field present",
			b: builder{
				Ops: []emitOp{
					{Kind: opField, Value: "Index"},
					{Kind: opLit, Value: "_refresh"},
				},
			},
			values: map[string][]string{"Index": {"my-index"}},
			want:   "/my-index/_refresh",
		},
		{
			name: "field absent",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfStr, Value: "Index"},
					{Kind: opField, Value: "Index"},
					{Kind: opEnd},
					{Kind: opLit, Value: "_refresh"},
				},
			},
			values: nil,
			want:   "/_refresh",
		},
		{
			name: "list present",
			b: builder{
				Ops: []emitOp{
					{Kind: opList, Value: "Index"},
					{Kind: opLit, Value: "_refresh"},
				},
			},
			values: map[string][]string{"Index": {"a", "b"}},
			want:   "/a,b/_refresh",
		},
		{
			name: "ifList false skips body",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfList, Value: "Index"},
					{Kind: opList, Value: "Index"},
					{Kind: opEnd},
					{Kind: opLit, Value: "_refresh"},
				},
			},
			values: nil,
			want:   "/_refresh",
		},
		{
			name: "elseIfStr taken",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfStr, Value: "A"},
					{Kind: opField, Value: "A"},
					{Kind: opElseIfStr, Value: "B"},
					{Kind: opField, Value: "B"},
					{Kind: opEnd},
				},
			},
			values: map[string][]string{"B": {"val-b"}},
			want:   "/val-b",
		},
		{
			name: "elseIfList taken",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfStr, Value: "A"},
					{Kind: opField, Value: "A"},
					{Kind: opElseIfList, Value: "B"},
					{Kind: opList, Value: "B"},
					{Kind: opEnd},
				},
			},
			values: map[string][]string{"B": {"x", "y"}},
			want:   "/x,y",
		},
		{
			name: "else taken",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfStr, Value: "A"},
					{Kind: opField, Value: "A"},
					{Kind: opElse},
					{Kind: opLit, Value: "_default"},
					{Kind: opEnd},
				},
			},
			values: nil,
			want:   "/_default",
		},
		{
			name: "nested inactive suppresses inner ops",
			b: builder{
				Ops: []emitOp{
					{Kind: opIfStr, Value: "A"},
					{Kind: opIfStr, Value: "B"},
					{Kind: opField, Value: "B"},
					{Kind: opEnd},
					{Kind: opEnd},
					{Kind: opLit, Value: "_end"},
				},
			},
			values: nil,
			want:   "/_end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := simulateBuild(tt.b, tt.values)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildStructLiteral(t *testing.T) {
	t.Parallel()

	b := builder{
		StructName: "SearchPath",
		Fields: []builderField{
			{Name: "Index", IsList: true},
			{Name: "Q", IsList: false},
		},
	}

	values := map[string][]string{
		"Index": {"a", "b"},
		"Q":     {"test"},
	}

	got := buildStructLiteral(b, values)
	require.Contains(t, got, "SearchPath{")
	require.Contains(t, got, `Index: []string{"a", "b"}`)
	require.Contains(t, got, `Q: "test"`)
}

func TestMethodName(t *testing.T) {
	t.Parallel()

	exported := renderData{Exported: true}
	require.Equal(t, "Build", exported.MethodName())

	unexported := renderData{Exported: false}
	require.Equal(t, "build", unexported.MethodName())
}

func TestHasRequired(t *testing.T) {
	t.Parallel()

	noReq := renderData{Builders: []builder{{Fields: []builderField{{Required: false}}}}}
	require.False(t, noReq.HasRequired())

	hasReq := renderData{Builders: []builder{{Fields: []builderField{{Required: true}}}}}
	require.True(t, hasReq.HasRequired())
}

func TestExtractLines(t *testing.T) {
	t.Parallel()

	input := "line1\nline2\nline3\nline4\nline5"
	got := extractLines(input, 1, 3)
	require.Contains(t, got, "2: line2")
	require.Contains(t, got, "3: line3")
}

// assertValidGo verifies that the source string is syntactically valid Go.
func assertValidGo(t *testing.T, src string) {
	t.Helper()

	// First verify it's formatted.
	formatted, err := format.Source([]byte(src))
	require.NoError(t, err, "source should be gofmt-valid")
	require.Equal(t, src, string(formatted), "source should be pre-formatted")

	// Then verify it parses.
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "test.go", src, parser.AllErrors)
	require.NoError(t, err, "source should parse as valid Go")
}
