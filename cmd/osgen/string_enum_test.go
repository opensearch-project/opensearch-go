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

func TestEnumParser_ConstOneOf(t *testing.T) {
	t.Parallel()

	t.Run("collects values and descriptions in order", func(t *testing.T) {
		t.Parallel()
		schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			constBranch("data_hot", "hot data", nil),
			constBranch("ml", "machine learning", nil),
		}}
		got, shape := enumParser{}.constOneOf("NodeRole", "_common___NodeRole", schema)
		require.Equal(t, enumShapeConst, shape)
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
		got, shape := enumParser{warnW: &warn}.constOneOf("NodeRole", "_common___NodeRole", schema)
		require.Equal(t, enumShapeConst, shape)
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
		got, shape := enumParser{warnW: &warn}.constOneOf("IndicesTranslogDurability", "indices._common___TranslogDurability", schema)
		require.Equal(t, enumShapeConst, shape)
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
		got, shape := enumParser{vr: vr}.constOneOf("NodeRole", "_common___NodeRole", schema)
		require.Equal(t, enumShapeConst, shape)
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
		_, shape := enumParser{}.constOneOf("X", "x___X", schema)
		require.Equal(t, enumShapeNone, shape)
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
		_, shape := enumParser{deny: deny}.constOneOf("Denied", key, schema)
		require.Equal(t, enumShapeNone, shape)
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

// TestEnumParser_ParamShape pins the enumShapeParam spellings: a top-level
// string enum, oneOf branches carrying enum lists, and a boolean branch next to
// string consts. constOneOf must keep rejecting all of them.
func TestEnumParser_ParamShape(t *testing.T) {
	t.Parallel()

	topLevel := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Enum:        []any{"asc", "desc"},
		Description: "Sort order.",
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

	described := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Enum:        []any{"asc", "desc"},
		Description: "Sort order.",
		Extensions: map[string]any{extEnumDescriptions: map[string]any{
			"asc":  "Smallest first.",
			"desc": "Largest first.",
			"up":   "Not a value.",
		}},
	}

	tests := []struct {
		name     string
		schema   *openapi3.Schema
		wantOK   bool
		want     []string
		wantDocs []string
		wantWarn string
	}{
		{
			name: "top-level enum leaves values undocumented", schema: topLevel, wantOK: true,
			want: []string{"asc", "desc"}, wantDocs: []string{"", ""},
		},
		{
			name: "x-enum-descriptions documents each value", schema: described, wantOK: true,
			want: []string{"asc", "desc"}, wantDocs: []string{"Smallest first.", "Largest first."},
			wantWarn: `x-enum-descriptions names "up"`,
		},
		{name: "enum-carrying oneOf branches", schema: byteUnit, wantOK: true, want: []string{"b", "kb", "k"}},
		{name: "boolean branch skipped", schema: refresh, wantOK: true, want: []string{"false", "true", "wait_for"}},
		{name: "case-only duplicate keeps the first", schema: health, wantOK: true, want: []string{"green", "red"}, wantWarn: `"GREEN" dropped`},
		{name: "boolean-only schema is not an enum", schema: boolOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var warn strings.Builder
			got, shape := enumParser{warnW: &warn}.parse("X", "x___X", tt.schema, true)
			ok := shape != enumShapeNone
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			require.Equal(t, tt.want, constValueList(got))
			if tt.wantDocs != nil {
				docs := make([]string, len(got))
				for i, v := range got {
					docs[i] = v.Description
				}
				require.Equal(t, tt.wantDocs, docs)
			}
			if tt.wantWarn != "" {
				require.Contains(t, warn.String(), tt.wantWarn)
			}

			_, strictShape := enumParser{}.constOneOf("X", "x___X", tt.schema)
			require.Equal(t, enumShapeNone, strictShape)
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

// TestTypeQueryParamEnums_ParamShapesAreTyped pins that enum shapes only query
// params accept get a registered enum type, and an array-or-single enum param
// becomes a slice of that type.
func TestTypeQueryParamEnums_ParamShapesAreTyped(t *testing.T) {
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
	require.True(t, registered.IsStringEnum)
	require.Equal(t, []string{"true", "wait_for"}, constValueList(registered.EnumConsts))

	expandParam := ops[0].QueryParams[1]
	require.Equal(t, "[]ExpandWildcard", expandParam.GoType)
	require.True(t, expandParam.IsList)
	elem, ok := reg.lookup("_common___ExpandWildcard")
	require.True(t, ok)
	require.True(t, elem.IsStringEnum)
}

// TestTypeQueryParamEnums_NamedListElement pins that a list param is typed even
// when a response or body walk registered its element enum first.
func TestTypeQueryParamEnums_NamedListElement(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	expand := &openapi3.Schema{OneOf: openapi3.SchemaRefs{constBranch("open", "", nil), constBranch("closed", "", nil)}}
	spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"_common___ExpandWildcard": &openapi3.SchemaRef{Value: expand},
		"_common___ExpandWildcards": &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/_common___ExpandWildcard", Value: expand},
			{Value: &openapi3.Schema{
				Type:  &openapi3.Types{openapi3.TypeArray},
				Items: &openapi3.SchemaRef{Ref: "#/components/schemas/_common___ExpandWildcard"},
			}},
		}}},
	}}}
	body := &walker{registry: reg, spec: spec, inFlight: make(set[string])}
	_, ok := body.resolveStringEnumConst("_common___ExpandWildcard", expand, "")
	require.True(t, ok)

	ops := []apiOperation{{Group: "search", QueryParams: []apiQueryParam{
		{ParamName: "expand_wildcards", GoName: "ExpandWildcards", GoType: "[]string", IsList: true, SchemaRef: "_common___ExpandWildcards"},
	}}}
	typeQueryParamEnums(ops, spec, reg, VersionRange{})
	require.Equal(t, "[]ExpandWildcard", ops[0].QueryParams[0].GoType)
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

// integerBranch builds a {type: integer} oneOf branch.
func integerBranch() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}}}
}

// oneOfBranch wraps branches in a nested oneOf schema, the spelling of the
// spec's StringifiedInteger, WaitForActiveShardOptions and SlicesCalculation.
func oneOfBranch(branches ...*openapi3.SchemaRef) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: branches}}
}

// nullBranch builds a {type: null} oneOf branch.
func nullBranch() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeNull}}}
}

// stringPatternBranch builds the {type: string, pattern} branch next to an
// integer inside StringifiedInteger.
func stringPatternBranch() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Pattern: `^[+-]?\d+$`}}
}

// TestEnumParser_IntOrConst pins the enumShapeIntOrConst shape: exactly
// one integer branch (directly or inside a nested oneOf) beside at least one
// const branch (directly or inside a nested oneOf, null skipped).
func TestEnumParser_IntOrConst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   []string
		wantOK bool
	}{
		{
			name: "wait_for_active_shards shape",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				oneOfBranch(integerBranch(), stringPatternBranch()),
				oneOfBranch(constBranch("all", "", nil), nullBranch()),
			}},
			want:   []string{"all"},
			wantOK: true,
		},
		{
			name: "slices shape",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				integerBranch(),
				oneOfBranch(constBranch("auto", "", nil)),
			}},
			want:   []string{"auto"},
			wantOK: true,
		},
		{
			name: "no integer branch",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				oneOfBranch(constBranch("all", "", nil)),
				constBranch("auto", "", nil),
			}},
		},
		{
			name: "no const branch",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				integerBranch(),
				nullBranch(),
			}},
		},
		{
			name: "two integer branches",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				integerBranch(),
				oneOfBranch(integerBranch(), stringPatternBranch()),
				constBranch("auto", "", nil),
			}},
		},
		{
			name:   "no oneOf",
			schema: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, shape := enumParser{}.intOrConst("X", "_common___X", tt.schema)
			ok := shape != enumShapeNone
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.want, constValueList(got))
			} else {
				require.Nil(t, got)
			}
		})
	}
}

// TestTypeQueryParamEnums_IntOrConst pins that an int-or-keyword query param is
// retyped to an alias that carries a Count constructor, and that the response
// and body walks leave the same schema untyped.
func TestTypeQueryParamEnums_IntOrConst(t *testing.T) {
	t.Parallel()

	schema := &openapi3.Schema{OneOf: openapi3.SchemaRefs{
		oneOfBranch(integerBranch(), stringPatternBranch()),
		oneOfBranch(constBranch("all", "", nil), nullBranch()),
	}}
	spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"_common___WaitForActiveShards": &openapi3.SchemaRef{Value: schema},
	}}}
	reg := newTypeRegistry(opensearchAPIPkgName)
	ops := []apiOperation{{
		Group: "index",
		QueryParams: []apiQueryParam{
			{ParamName: "wait_for_active_shards", GoName: "WaitForActiveShards", GoType: "string", SchemaRef: "_common___WaitForActiveShards"},
		},
	}}

	typeQueryParamEnums(ops, spec, reg, VersionRange{})

	qp := ops[0].QueryParams[0]
	require.Equal(t, "WaitForActiveShards", qp.GoType)
	require.True(t, qp.IsStringEnum)
	registered, ok := reg.lookup("_common___WaitForActiveShards")
	require.True(t, ok)
	require.True(t, registered.HasCount)
	require.Equal(t, []string{"all"}, constValueList(registered.EnumConsts))

	// A non-param walker does not type the schema.
	other := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: other, spec: spec, inFlight: make(set[string]), vrange: VersionRange{}}
	_, typed := w.resolveStringEnumConst("_common___WaitForActiveShards", schema, "index")
	require.False(t, typed)
}

// TestTypeQueryParamEnums_ParamNamesAndDefault pins that each enum type records
// the sorted, deduplicated wire names of the query params that use it, and the
// schema default.
func TestTypeQueryParamEnums_ParamNamesAndDefault(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	spec := &openapi3.T{
		Components: &openapi3.Components{
			Schemas: openapi3.Schemas{
				"_common___Refresh": &openapi3.SchemaRef{Value: &openapi3.Schema{
					Default: "false",
					OneOf: openapi3.SchemaRefs{
						constBranch("true", "", nil),
						constBranch("false", "", nil),
					},
				}},
				"_common___Level": &openapi3.SchemaRef{Value: &openapi3.Schema{
					OneOf: openapi3.SchemaRefs{constBranch("a", "", nil), constBranch("b", "", nil)},
				}},
			},
		},
	}
	ops := []apiOperation{
		{Group: "index", QueryParams: []apiQueryParam{
			{ParamName: "refresh", GoName: "Refresh", GoType: "string", SchemaRef: "_common___Refresh"},
		}},
		{Group: "bulk", QueryParams: []apiQueryParam{
			{ParamName: "refresh", GoName: "Refresh", GoType: "string", SchemaRef: "_common___Refresh"},
			{ParamName: "level", GoName: "Level", GoType: "string", SchemaRef: "_common___Level"},
			{ParamName: "bulk_level", GoName: "BulkLevel", GoType: "string", SchemaRef: "_common___Level"},
		}},
	}

	typeQueryParamEnums(ops, spec, reg, VersionRange{})

	tests := []struct {
		ref         string
		wantParams  []string
		wantDefault string
	}{
		{"_common___Refresh", []string{"refresh"}, "false"},
		{"_common___Level", []string{"bulk_level", "level"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()
			got, ok := reg.lookup(tt.ref)
			require.True(t, ok)
			require.Equal(t, tt.wantParams, got.ParamNames)
			require.Equal(t, tt.wantDefault, got.Default)
		})
	}
}
