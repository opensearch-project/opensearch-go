// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
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
