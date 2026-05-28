// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestWalkerPrimitiveTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   string
	}{
		{name: "string", schema: openapi3.NewStringSchema(), want: "string"},
		{name: "boolean", schema: openapi3.NewBoolSchema(), want: "bool"},
		{name: "integer", schema: openapi3.NewIntegerSchema(), want: "int"},
		{name: "int64", schema: openapi3.NewInt64Schema(), want: "int64"},
		{name: "float64", schema: openapi3.NewFloat64Schema(), want: "float64"},
		{name: "float32", schema: &openapi3.Schema{Type: &openapi3.Types{"number"}, Format: "float"}, want: "float32"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}
			ref := &openapi3.SchemaRef{Value: tt.schema}
			got := w.walkSchema(ref, "test___Field", "test", false)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestWalkerArrayType(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewArraySchema()
	schema.Items = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}

	ref := &openapi3.SchemaRef{Value: schema}
	got := w.walkSchema(ref, "test___List", "test", false)
	require.Equal(t, "[]string", got)
}

func TestWalkerMapType(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewObjectSchema()
	schema.AdditionalProperties = openapi3.AdditionalProperties{
		Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
	}

	ref := &openapi3.SchemaRef{Value: schema}
	got := w.walkSchema(ref, "test___Map", "test", false)
	require.Equal(t, "map[string]string", got)
}

func TestWalkerObjectWithProperties(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"cluster_name": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		"status":       &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		"timed_out":    &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
		"node_count":   &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
	}
	schema.Required = []string{"cluster_name", "status"}

	ref := &openapi3.SchemaRef{Value: schema}
	got := w.walkSchema(ref, "cluster.health___HealthResponseBody", "cluster.health", true)
	require.Equal(t, "ClusterHealthResp", got)

	registered, ok := reg.lookupByName("ClusterHealthResp")
	require.True(t, ok)
	require.True(t, registered.IsResp)
	require.Len(t, registered.Fields, 4)

	fieldMap := make(map[string]goField)
	for _, f := range registered.Fields {
		fieldMap[f.JSONName] = f
	}

	// Required fields: not pointers.
	require.Equal(t, "string", fieldMap["cluster_name"].GoType)
	require.False(t, fieldMap["cluster_name"].IsPointer)
	require.Equal(t, "string", fieldMap["status"].GoType)
	require.False(t, fieldMap["status"].IsPointer)

	// Optional fields: pointers.
	require.Equal(t, "*bool", fieldMap["timed_out"].GoType)
	require.True(t, fieldMap["timed_out"].IsPointer)
	require.Equal(t, "*int", fieldMap["node_count"].GoType)
	require.True(t, fieldMap["node_count"].IsPointer)
}

func TestWalkerScalarAliasInlined(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	ref := &openapi3.SchemaRef{
		Ref:   "#/components/schemas/_common___Name",
		Value: openapi3.NewStringSchema(),
	}
	got := w.walkSchema(ref, "_common___Name", "test", false)
	require.Equal(t, "string", got)

	// Should NOT be registered as a named type.
	_, ok := reg.lookupByName("Name")
	require.False(t, ok)
}

func TestWalkerNamedRef(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"total":      &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
		"successful": &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
		"failed":     &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
	}
	schema.Required = []string{"total", "successful", "failed"}

	ref := &openapi3.SchemaRef{
		Ref:   "#/components/schemas/_common___ShardStatistics",
		Value: schema,
	}
	got := w.walkSchema(ref, "_common___ShardStatistics", "test", false)
	require.Equal(t, "ShardStatistics", got)

	registered, ok := reg.lookupByName("ShardStatistics")
	require.True(t, ok)
	require.True(t, registered.IsShared)
	require.Len(t, registered.Fields, 3)
}

func TestWalkerDedup(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"total": &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
	}
	schema.Required = []string{"total"}

	// Walk the same ref twice.
	ref := &openapi3.SchemaRef{
		Ref:   "#/components/schemas/_common___ShardStatistics",
		Value: schema,
	}
	got1 := w.walkSchema(ref, "_common___ShardStatistics", "test", false)
	got2 := w.walkSchema(ref, "_common___ShardStatistics", "test", false)
	require.Equal(t, "ShardStatistics", got1)
	require.Equal(t, "ShardStatistics", got2)
	require.Len(t, reg.all(), 1)
}

func TestWalkerAllOf(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	base := openapi3.NewObjectSchema()
	base.Properties = openapi3.Schemas{
		"acknowledged": &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
	}
	base.Required = []string{"acknowledged"}

	extra := openapi3.NewObjectSchema()
	extra.Properties = openapi3.Schemas{
		"shards_acknowledged": &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
	}

	combined := &openapi3.Schema{
		AllOf: openapi3.SchemaRefs{
			{Value: base},
			{Value: extra},
		},
	}

	ref := &openapi3.SchemaRef{Value: combined}
	got := w.walkSchema(ref, "indices.create___CreateResponseBody", "indices.create", true)
	require.Equal(t, "IndicesCreateResp", got)

	registered, ok := reg.lookupByName("IndicesCreateResp")
	require.True(t, ok)
	require.Len(t, registered.Fields, 2)

	fieldMap := make(map[string]goField)
	for _, f := range registered.Fields {
		fieldMap[f.JSONName] = f
	}
	require.Equal(t, "bool", fieldMap["acknowledged"].GoType)
	require.Equal(t, "*bool", fieldMap["shards_acknowledged"].GoType)
}

func TestWalkerOneOfUnion(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{
			{Value: openapi3.NewStringSchema()},
			{Value: openapi3.NewIntegerSchema()},
		},
	}

	ref := &openapi3.SchemaRef{Value: schema}
	got := w.walkSchema(ref, "test___Union", "test", false)
	require.Equal(t, "TestUnion", got)

	registered, ok := reg.lookup("test___Union")
	require.True(t, ok)
	require.True(t, registered.IsUnion)
	require.Len(t, registered.Branches, 2)
	require.Equal(t, "String", registered.Branches[0].Name)
	require.Equal(t, "Int", registered.Branches[1].Name)
}

func TestWalkerCollectionNotPointer(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"names": &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}},
		"counts": &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
			},
		}},
	}
	// Not required - but collections shouldn't become pointers.

	ref := &openapi3.SchemaRef{Value: schema}
	w.walkSchema(ref, "test___Container", "test", false)

	registered, ok := reg.lookupByName("TestContainer")
	require.True(t, ok)

	fieldMap := make(map[string]goField)
	for _, f := range registered.Fields {
		fieldMap[f.JSONName] = f
	}
	require.Equal(t, "[]string", fieldMap["names"].GoType)
	require.False(t, fieldMap["names"].IsPointer)
	require.Equal(t, "map[string]int", fieldMap["counts"].GoType)
	require.False(t, fieldMap["counts"].IsPointer)
}

func TestWalkerCycleDetection(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	// Simulate a self-referential schema (ErrorCause contains itself).
	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"type":   &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		"reason": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		"caused_by": &openapi3.SchemaRef{
			Ref:   "#/components/schemas/_common___ErrorCause",
			Value: schema, // self-reference
		},
	}
	schema.Required = []string{"type", "reason"}

	ref := &openapi3.SchemaRef{
		Ref:   "#/components/schemas/_common___ErrorCause",
		Value: schema,
	}
	got := w.walkSchema(ref, "_common___ErrorCause", "test", false)
	require.Equal(t, "ErrorCause", got)

	registered, ok := reg.lookupByName("ErrorCause")
	require.True(t, ok)
	require.True(t, registered.IsShared)

	fieldMap := make(map[string]goField)
	for _, f := range registered.Fields {
		fieldMap[f.JSONName] = f
	}
	require.Equal(t, "string", fieldMap["type"].GoType)
	require.Equal(t, "string", fieldMap["reason"].GoType)
	// Self-reference becomes a pointer to break the cycle.
	require.Equal(t, "*ErrorCause", fieldMap["caused_by"].GoType)
}

func TestIsSharedSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "common", key: "_common___ShardStatistics", want: true},
		{name: "group._common", key: "nodes._common___NodesResponseBase", want: true},
		{name: "operation specific", key: "cluster.health___HealthResponseBody", want: false},
		{name: "no separator", key: "something", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isSharedSchema(tt.key))
		})
	}
}

func TestRefToSchemaKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "full ref", ref: "#/components/schemas/_common___ShardStatistics", want: "_common___ShardStatistics"},
		{name: "empty", ref: "", want: ""},
		{name: "no prefix", ref: "_common___X", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, refToSchemaKey(tt.ref))
		})
	}
}
