// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

// constBranch builds a {type: string, const: v} oneOf branch, optionally
// carrying version extensions, mirroring the bundled spec's NodeRole shape.
func constBranch(v, desc string, ext map[string]any) *openapi3.SchemaRef {
	s := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Const:       v,
		Description: desc,
	}
	if ext != nil {
		s.Extensions = ext
	}
	return &openapi3.SchemaRef{Value: s}
}

// constValueList projects the wire values out of a []constEnumValue in order,
// for asserting against an expected value slice.
func constValueList(consts []constEnumValue) []string {
	values := make([]string, len(consts))
	for i, cv := range consts {
		values[i] = cv.Value
	}
	return values
}

func TestParseConstOneOf(t *testing.T) {
	t.Parallel()

	t.Run("collects values and descriptions in order", func(t *testing.T) {
		t.Parallel()
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			constBranch("data_hot", "hot data", nil),
			constBranch("ml", "machine learning", nil),
		}}
		got, ok := parseConstOneOf("NodeRole", "_common___NodeRole", schema, VersionRange{}, nil, nil)
		require.True(t, ok)
		require.Equal(t, []constEnumValue{
			{Value: "data_hot", Description: "hot data"},
			{Value: "ml", Description: "machine learning"},
		}, got)
	})

	t.Run("dedups values colliding on the same const name, keeping the first", func(t *testing.T) {
		t.Parallel()
		// NodeRole lists `search` twice across a version boundary; at the default
		// (all) range both survive, so the const-name dedup must keep one.
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			constBranch("search", "pre-3.0 search", map[string]any{extVersionRemoved: "3.0"}),
			constBranch("search", "3.0 search", map[string]any{extVersionAdded: "3.0"}),
		}}
		var warn strings.Builder
		got, ok := parseConstOneOf("NodeRole", "_common___NodeRole", schema, VersionRange{}, nil, &warn)
		require.True(t, ok)
		require.Len(t, got, 1)
		require.Equal(t, "search", got[0].Value)
		require.Equal(t, "pre-3.0 search", got[0].Description)
		// True duplicate (identical value): no distinct-value warning.
		require.Empty(t, warn.String())
	})

	t.Run("distinct casings collide; canonical wins and the drop is reported", func(t *testing.T) {
		t.Parallel()
		// TranslogDurability lists ASYNC and async as distinct values that
		// PascalCase to the same const; the first-listed (ASYNC) wins.
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			constBranch("ASYNC", "upper", nil),
			constBranch("async", "lower", nil),
		}}
		var warn strings.Builder
		got, ok := parseConstOneOf("IndicesTranslogDurability", "indices._common___TranslogDurability", schema, VersionRange{}, nil, &warn)
		require.True(t, ok)
		require.Len(t, got, 1)
		require.Equal(t, "ASYNC", got[0].Value)
		require.Contains(t, warn.String(), `value "async" dropped`)
	})

	t.Run("version-filters branches outside the range", func(t *testing.T) {
		t.Parallel()
		// warm was added in 3.0; a max-version below that drops it.
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			constBranch("data", "data", nil),
			constBranch("warm", "warm", map[string]any{extVersionAdded: "3.0"}),
		}}
		vr, err := ParseVersionRange("epoch", "2.19", "epoch", false)
		require.NoError(t, err)
		got, ok := parseConstOneOf("NodeRole", "_common___NodeRole", schema, vr, nil, nil)
		require.True(t, ok)
		require.Len(t, got, 1)
		require.Equal(t, "data", got[0].Value)
	})

	t.Run("rejects non-const shapes", func(t *testing.T) {
		t.Parallel()
		// A union of plain string + integer is not a const-oneOf.
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			{Value: openapi3.NewStringSchema()},
			{Value: openapi3.NewIntegerSchema()},
		}}
		_, ok := parseConstOneOf("X", "x___X", schema, VersionRange{}, nil, nil)
		require.False(t, ok)
	})

	t.Run("honors the deny-list", func(t *testing.T) {
		t.Parallel()
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{constBranch("a", "", nil)}}
		key := "x___Denied"
		// Clone the real deny-list as a starting point and add to the copy, so the
		// test exercises deny behavior without mutating the package global (which
		// other parallel tests read).
		deny := stringEnumDenyList.clone()
		deny.add(key)
		_, ok := parseConstOneOf("Denied", key, schema, VersionRange{}, deny, nil)
		require.False(t, ok)
	})
}

// TestWalkerStringEnumConst pins the walker path: a $ref to a const-oneOf schema
// registers a shared, string-backed enum and the field resolves to its Go name.
func TestWalkerStringEnumConst(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	spec := &openapi3.T{
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{
				"_common___NodeRole": &openapi3.SchemaRef{Value: &openapi3.Schema{
					Description: "The role assigned to the node.",
					OneOf: openapi3.SchemaRefs{
						constBranch("data", "data node", nil),
						constBranch("search", "pre-3.0", map[string]any{extVersionRemoved: "3.0"}),
						constBranch("search", "3.0", map[string]any{extVersionAdded: "3.0"}),
					},
				}},
			},
		},
	}
	w := &walker{registry: reg, spec: spec, inFlight: make(map[string]struct{})}

	ref := &openapi3.SchemaRef{Ref: "#/components/schemas/_common___NodeRole", Value: spec.Components.Schemas["_common___NodeRole"].Value}
	got := w.walkSchema(ref, "somefield", "nodes", true)
	require.Equal(t, "NodeRole", got)

	registered, ok := reg.lookup("_common___NodeRole")
	require.True(t, ok)
	require.True(t, registered.IsStringEnum)
	require.True(t, registered.IsShared)
	// The duplicate `search` collapses to a single value. EnumConsts preserves
	// declaration order, so compare the projected values directly (no sorting).
	require.Equal(t, []string{"data", "search"}, constValueList(registered.EnumConsts))
	require.Equal(t, "The role assigned to the node.", registered.Comment)
}

// TestTypeQueryParamEnums pins the query-param path: a $ref query param to a
// const-oneOf schema is retyped to the enum's Go name, marked for the string()
// serialization cast, and the enum type is registered (even when the enum is
// reached only through a query parameter).
func TestTypeQueryParamEnums(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	spec := &openapi3.T{
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{
				"_common___TimeUnit": &openapi3.SchemaRef{Value: &openapi3.Schema{
					OneOf: openapi3.SchemaRefs{
						constBranch("d", "days", nil),
						constBranch("h", "hours", nil),
					},
				}},
			},
		},
	}
	ops := []apiOperation{{
		Group: "cat.nodes",
		QueryParams: []apiQueryParam{
			{ParamName: "time", GoName: "Time", GoType: "string", SchemaRef: "_common___TimeUnit"},
			{ParamName: "bytes", GoName: "Bytes", GoType: "string", SchemaRef: ""},
		},
	}}

	typeQueryParamEnums(ops, spec, reg, VersionRange{})

	time := ops[0].QueryParams[0]
	require.Equal(t, "TimeUnit", time.GoType)
	require.True(t, time.IsStringEnum)

	// A param without a const-oneOf ref is left untouched.
	bytes := ops[0].QueryParams[1]
	require.Equal(t, "string", bytes.GoType)
	require.False(t, bytes.IsStringEnum)

	// The enum type is registered as a shared string enum.
	registered, ok := reg.lookup("_common___TimeUnit")
	require.True(t, ok)
	require.True(t, registered.IsStringEnum)
	require.Equal(t, []string{"d", "h"}, constValueList(registered.EnumConsts))
}

// enumBranch builds a {type: string, enum: [values...]} oneOf branch, the
// bundled spec's ByteUnit/HealthStatus spelling for several wire values in one
// branch.
func enumBranch(values ...string) *openapi3.SchemaRef {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	return &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeString},
		Enum: enum,
	}}
}

// boolBranch builds the {type: boolean} oneOf branch Refresh carries next to its
// string consts.
func boolBranch() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeBoolean}}}
}

// TestParseParamEnum pins the query-param-only enum shapes: a top-level string
// enum, oneOf branches carrying enum lists, and a boolean branch next to string
// consts. parseConstOneOf must keep rejecting all of them.
func TestParseParamEnum(t *testing.T) {
	t.Parallel()

	topLevel := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeString},
		Enum: []any{"asc", "desc"},
	}
	byteUnit := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		constBranch("b", "bytes", nil),
		enumBranch("kb", "k"),
	}}
	refresh := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		boolBranch(),
		constBranch("false", "", nil),
		constBranch("true", "", nil),
		constBranch("wait_for", "", nil),
	}}
	health := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		enumBranch("green", "GREEN"),
		enumBranch("red"),
	}}

	boolOnly := &openapi3.Schema{OneOf: openapi3.SchemaRefs{boolBranch()}}

	tests := []struct {
		name     string
		schema   *openapi3.Schema
		wantOK   bool
		want     []string
		wantWarn string
	}{
		{name: "top-level enum", schema: topLevel, wantOK: true, want: []string{"asc", "desc"}},
		{name: "enum-carrying oneOf branches", schema: byteUnit, wantOK: true, want: []string{"b", "kb", "k"}},
		{name: "boolean branch skipped", schema: refresh, wantOK: true, want: []string{"false", "true", "wait_for"}},
		{name: "case-only duplicate keeps the first", schema: health, wantOK: true, want: []string{"green", "red"}, wantWarn: `"GREEN" dropped`},
		{name: "boolean-only schema is not an enum", schema: boolOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var warn strings.Builder
			got, ok := parseParamEnum("X", "x___X", tt.schema, VersionRange{}, nil, &warn)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			require.Equal(t, tt.want, constValueList(got))
			if tt.wantWarn != "" {
				require.Contains(t, warn.String(), tt.wantWarn)
			}

			_, strictOK := parseConstOneOf("X", "x___X", tt.schema, VersionRange{}, nil, nil)
			require.False(t, strictOK)
		})
	}
}

// refreshEnumSpec returns a spec holding the bool-or-const Refresh enum, a
// shape only query params accept.
func refreshEnumSpec() *openapi3.T {
	return &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"_common___Refresh": &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			boolBranch(),
			constBranch("true", "", nil),
			constBranch("wait_for", "", nil),
		}}},
	}}}
}

// TestTypeQueryParamEnums_Aliases pins the alias path: enum shapes only
// query params accept become `type X = string` aliases, and an
// array-or-single enum param becomes a slice of the alias.
func TestTypeQueryParamEnums_Aliases(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	arrayOf := func(key string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{openapi3.TypeArray},
			Items: &openapi3.SchemaRef{Ref: "#/components/schemas/" + key, Value: nil},
		}}
	}
	expand := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		constBranch("open", "", nil),
		constBranch("closed", "", nil),
	}}
	spec := refreshEnumSpec()
	spec.Components.Schemas["_common___ExpandWildcard"] = &openapi3.SchemaRef{Value: expand}
	spec.Components.Schemas["_common___ExpandWildcards"] = &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		{Ref: "#/components/schemas/_common___ExpandWildcard", Value: expand},
		arrayOf("_common___ExpandWildcard"),
	}}}
	ops := []apiOperation{{
		Group: "indices.refresh",
		QueryParams: []apiQueryParam{
			{ParamName: "refresh", GoName: "Refresh", GoType: "string", SchemaRef: "_common___Refresh"},
			{ParamName: "expand_wildcards", GoName: "ExpandWildcards", GoType: "[]string", IsList: true, SchemaRef: "_common___ExpandWildcards"},
		},
	}}

	typeQueryParamEnums(ops, spec, reg, VersionRange{})

	refresh := ops[0].QueryParams[0]
	require.Equal(t, "Refresh", refresh.GoType)
	require.True(t, refresh.IsStringEnum)
	registered, ok := reg.lookup("_common___Refresh")
	require.True(t, ok)
	require.True(t, registered.IsAlias)
	require.Equal(t, []string{"true", "wait_for"}, constValueList(registered.EnumConsts))

	expandParam := ops[0].QueryParams[1]
	require.Equal(t, "[]ExpandWildcard", expandParam.GoType)
	require.True(t, expandParam.IsList)
	elem, ok := reg.lookup("_common___ExpandWildcard")
	require.True(t, ok)
	require.True(t, elem.IsAlias)
}

// TestTypeQueryParamEnums_StrictScalarStaysNamed pins that a strict-shape enum
// reached only through a scalar param keeps its named type: only new shapes and
// list elements become aliases.
func TestTypeQueryParamEnums_StrictScalarStaysNamed(t *testing.T) {
	t.Parallel()

	named := newTypeRegistry(opensearchAPIPkgName)
	namedSpec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"_common___TimeUnit": &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{constBranch("d", "", nil)}}},
	}}}
	namedOps := []apiOperation{{Group: "cat.nodes", QueryParams: []apiQueryParam{
		{ParamName: "time", GoName: "Time", GoType: "string", SchemaRef: "_common___TimeUnit"},
	}}}
	typeQueryParamEnums(namedOps, namedSpec, named, VersionRange{})
	timeUnit, ok := named.lookup("_common___TimeUnit")
	require.True(t, ok)
	require.False(t, timeUnit.IsAlias)
}

// TestResolveStringEnumConst_ResponseWalkUnchanged pins that a response/body
// walk of a param-only enum shape keeps its plain string type.
func TestResolveStringEnumConst_ResponseWalkUnchanged(t *testing.T) {
	t.Parallel()

	spec := refreshEnumSpec()
	w := &walker{registry: newTypeRegistry(opensearchAPIPkgName), spec: spec, inFlight: make(map[string]struct{})}
	_, isEnum := w.resolveStringEnumConst("_common___Refresh", spec.Components.Schemas["_common___Refresh"].Value, "indices")
	require.False(t, isEnum)
}

// TestTypeQueryParamEnums_GlobalFormat pins that an enum referenced only by a
// global query parameter (dropped from op.QueryParams because it lives on the
// shared DebugParams struct) is still registered as a shared string alias, and
// that no per-operation param is added or retyped.
func TestTypeQueryParamEnums_GlobalFormat(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	formatEnum := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeString},
		Enum: []any{"text", "json"},
	}
	formatParam := &openapi3.ParameterRef{Value: &openapi3.Parameter{
		Name: "format",
		In:   "query",
		Schema: &openapi3.SchemaRef{
			Ref:   "#/components/schemas/_common___CatResponseFormat",
			Value: formatEnum,
		},
	}}
	// A non-global query param with a $ref schema must not be registered by the
	// global pass.
	otherParam := &openapi3.ParameterRef{Value: &openapi3.Parameter{
		Name: "other",
		In:   "query",
		Schema: &openapi3.SchemaRef{
			Ref:   "#/components/schemas/_common___Other",
			Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Enum: []any{"a"}},
		},
	}}
	paths := openapi3.NewPaths()
	paths.Set("/_cat/example", &openapi3.PathItem{
		Parameters: openapi3.Parameters{formatParam},
		Get:        &openapi3.Operation{Parameters: openapi3.Parameters{otherParam}},
	})
	spec := &openapi3.T{
		Paths: paths,
		Components: &openapi3.Components{Schemas: openapi3.Schemas{
			"_common___CatResponseFormat": &openapi3.SchemaRef{Value: formatEnum},
			"_common___Other":             otherParam.Value.Schema,
		}},
	}
	ops := []apiOperation{{Group: "cat.example"}}

	typeQueryParamEnums(ops, spec, reg, VersionRange{})

	require.Empty(t, ops[0].QueryParams)
	registered, ok := reg.lookup("_common___CatResponseFormat")
	require.True(t, ok)
	require.Equal(t, "CatRespFormat", registered.Name)
	require.True(t, registered.IsShared)
	require.True(t, registered.IsAlias)
	require.Equal(t, []string{"text", "json"}, constValueList(registered.EnumConsts))
	_, ok = reg.lookup("_common___Other")
	require.False(t, ok)
}

// TestTypeQueryParamEnums_GlobalFormatOrder pins that global-param enums are
// registered in sorted path order, so emission order does not depend on Go's
// randomized map iteration. Paths are set in reverse so insertion order cannot
// pass the test by accident; the loop repeats to give a random order a chance
// to show.
func TestTypeQueryParamEnums_GlobalFormatOrder(t *testing.T) {
	t.Parallel()

	formatParam := func(key string) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name:   "format",
			In:     "query",
			Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + key},
		}}
	}
	keys := []string{"_common___AFormat", "_common___BFormat", "_common___CFormat", "_common___DFormat"}
	schemas := openapi3.Schemas{}
	paths := openapi3.NewPaths()
	for i, key := range slices.Backward(keys) {
		schemas[key] = &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type: &openapi3.Types{openapi3.TypeString},
			Enum: []any{"x"},
		}}
		paths.Set(fmt.Sprintf("/p%d", i), &openapi3.PathItem{Parameters: openapi3.Parameters{formatParam(key)}})
	}
	spec := &openapi3.T{Paths: paths, Components: &openapi3.Components{Schemas: schemas}}

	for range 20 {
		reg := newTypeRegistry(opensearchAPIPkgName)
		typeQueryParamEnums(nil, spec, reg, VersionRange{})
		require.Equal(t, keys, reg.order)
	}
}
