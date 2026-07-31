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

		// OpenAPI 3.1 nullable scalars: type: ["null", <primitive>]. Type.Is
		// matches only single-element sets, so without nullablePrimitiveGoType
		// these would fall through to json.RawMessage (the CAT-record bug).
		// The Go type is the bare primitive; the caller adds the pointer via
		// isNullableSchema.
		{name: "nullable string", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "string"}}, want: "string"},
		{name: "nullable string (null last)", schema: &openapi3.Schema{Type: &openapi3.Types{"string", "null"}}, want: "string"},
		{name: "nullable integer", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "integer"}}, want: "int"},
		{name: "nullable int64", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "integer"}, Format: "int64"}, want: "int64"},
		{name: "nullable number", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "number"}}, want: "float64"},
		{name: "nullable boolean", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "boolean"}}, want: "bool"},
		// Not a nullable-primitive: a genuine multi-type union stays raw.
		{name: "number-or-string union stays raw", schema: &openapi3.Schema{Type: &openapi3.Types{"number", "string"}}, want: "json.RawMessage"},
		// Nullable non-primitives are NOT resolved by nullablePrimitiveGoType:
		// [null, object] / [null, array] have no single Go primitive, so they
		// fall through to json.RawMessage rather than being mis-typed.
		{name: "nullable object stays raw", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "object"}}, want: "json.RawMessage"},
		{name: "nullable array stays raw", schema: &openapi3.Schema{Type: &openapi3.Types{"null", "array"}}, want: "json.RawMessage"},
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

// TestWalkerStringEnum covers the x-enum-name opt-in: a string schema carrying
// the marker plus an enum: constraint resolves to a registered int-backed iota
// enum type; without the marker (or without values) it stays a plain string.
func TestWalkerStringEnum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		enum      []any  // schema enum: values
		marker    string // x-enum-name extension value ("" = absent)
		want      string // expected Go type returned by the walker
		wantEnum  bool   // expect an enum type registered under _common___<marker>
		wantPanic bool   // expect the walk to panic (invalid marker identifier)
	}{
		{
			name:     "marker plus values registers enum type",
			enum:     []any{"OK", "NOT_FOUND"},
			marker:   "RestStatus",
			want:     "RestStatus",
			wantEnum: true,
		},
		{
			name:   "no marker stays string",
			enum:   []any{"OK", "NOT_FOUND"}, // values but no x-enum-name marker
			marker: "",
			want:   "string",
		},
		{
			name:   "marker but no values stays string",
			enum:   nil, // marker present but empty enum
			marker: "RestStatus",
			want:   "string",
		},
		// An x-enum-name that is not a valid Go identifier is a spec defect that
		// would emit uncompilable Go; the walker panics rather than proceed.
		{name: "marker with space panics", enum: []any{"OK"}, marker: "rest status", wantPanic: true},
		{name: "marker leading digit panics", enum: []any{"OK"}, marker: "123Status", wantPanic: true},
		{name: "marker with hyphen panics", enum: []any{"OK"}, marker: "rest-status", wantPanic: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			schema := openapi3.NewStringSchema()
			schema.Enum = tt.enum
			if tt.marker != "" {
				schema.Extensions = map[string]any{extEnumName: tt.marker}
			}

			walk := func() string {
				return w.walkSchema(&openapi3.SchemaRef{Value: schema}, "security._common___Ok", "security", true)
			}
			if tt.wantPanic {
				require.Panics(t, func() { _ = walk() })
				return
			}

			got := walk()
			require.Equal(t, tt.want, got)

			reg2, ok := reg.lookup("_common___" + tt.marker)
			if !tt.wantEnum {
				require.False(t, ok, "no enum type should be registered")
				return
			}
			require.True(t, ok, "enum type should be registered under its _common key")
			require.True(t, reg2.IsEnum)
			require.True(t, reg2.IsShared)
			require.Equal(t, []string{"OK", "NOT_FOUND"}, reg2.EnumValues)
		})
	}
}

// TestWalkerStringEnumShared covers a marker referenced by more than one field:
// identical value sets register once and are reused; conflicting value sets are
// a spec defect and panic rather than letting the second field silently inherit
// the first enum's values.
func TestWalkerStringEnumShared(t *testing.T) {
	t.Parallel()

	type walk struct {
		key    string // schema key for this walk
		values []any  // enum: values for the marker "RestStatus"
	}
	tests := []struct {
		name      string
		walks     []walk
		want      string // expected Go type from every non-panicking walk
		wantPanic bool   // expect the final walk to panic
	}{
		{
			name: "same values shared across fields registers once",
			walks: []walk{
				{key: "security._common___Ok", values: []any{"OK"}},
				{key: "security._common___Created", values: []any{"OK"}},
			},
			want: "RestStatus",
		},
		{
			name: "conflicting values panics",
			walks: []walk{
				{key: "security._common___Ok", values: []any{"OK", "NOT_FOUND"}},
				{key: "security._common___Created", values: []any{"OK", "CREATED"}},
			},
			want:      "RestStatus",
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newTypeRegistry(opensearchAPIPkgName)
			w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

			schemaFor := func(values []any) *openapi3.SchemaRef {
				s := openapi3.NewStringSchema()
				s.Enum = values
				s.Extensions = map[string]any{extEnumName: "RestStatus"}
				return &openapi3.SchemaRef{Value: s}
			}

			for i, wk := range tt.walks {
				last := i == len(tt.walks)-1
				do := func() string { return w.walkSchema(schemaFor(wk.values), wk.key, "security", true) }
				if last && tt.wantPanic {
					require.Panics(t, func() { _ = do() })
					return
				}
				require.Equal(t, tt.want, do())
			}
		})
	}
}

// TestWalkerStringEnumViaRef covers the x-enum-name opt-in arriving through a
// component $ref rather than an inline string. A $ref'd string schema bypasses
// resolveInlineSchema's inline-string branch and reaches resolvePropertylessSchema,
// which must consult the marker before the plain-primitive fallback; otherwise
// the enum marker is silently lost and the field emits a plain string.
func TestWalkerStringEnumViaRef(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	enumSchema := openapi3.NewStringSchema()
	enumSchema.Enum = []any{"OK", "NOT_FOUND"}
	enumSchema.Extensions = map[string]any{extEnumName: "RestStatus"}

	ref := &openapi3.SchemaRef{
		Ref:   "#/components/schemas/_common___RestStatus",
		Value: enumSchema,
	}
	got := w.walkSchema(ref, "security___Status", "security", false)
	require.Equal(t, "RestStatus", got)

	registered, ok := reg.lookup("_common___RestStatus")
	require.True(t, ok, "enum type should be registered even when reached via $ref")
	require.True(t, registered.IsEnum)
	require.Equal(t, []string{"OK", "NOT_FOUND"}, registered.EnumValues)
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

// TestWalkerRefUnionNameCollision pins the key a $ref'd oneOf union registers
// under, across the three cases resolveRefUnion distinguishes.
//
// The default is the REFERENCED schema's own key: a $ref'd schema is a named
// schema with its own identity, and keying it by the caller's field path both
// mangled the name (_common.analysis___Analyzer emitted as
// IndicesIndexSettingsAnalysisAnalyzerValue) and discarded the identity carrying
// the schema's discriminator.
//
// The parent-scoped key is the fallback for a name clash, and it in turn falls
// back to the referenced key when it would collide with the PARENT struct's own
// name -- the tasks.list bug, where the union registers first and the parent is
// then silently dropped, degrading the whole parent response to json.RawMessage.
// The parent-name check compares BOTH the non-resp (schemaTypeName(_, false)) and
// resp-body (schemaTypeName(_, true)) forms, because for non-_common keys those
// differ; the cases below exercise each operand.
func TestWalkerRefUnionNameCollision(t *testing.T) {
	t.Parallel()

	const (
		infosKey = "tasks._common___TaskInfos"
		group    = "tasks._common"
	)

	tests := []struct {
		name string
		// parentKey is the response schema key.
		parentKey string
		// field is the parent property name holding the $ref to the oneOf.
		field string
		// preRegisterName, when non-empty, is a Go type name to occupy before the
		// walk, forcing the referenced schema's own key to be unavailable.
		preRegisterName string
		// matchForm records which parent-name form the parent-scoped union name
		// collides with: "false" (non-resp), "true" (resp-body), or "none".
		matchForm string
		// wantParentName is the parent struct's resolved Go name.
		wantParentName string
		// wantUnionKey is the registry key the union must register under.
		wantUnionKey string
		// wantFieldType is the parent field's Go type expression.
		wantFieldType string
	}{
		{
			// The default: the referenced schema keeps its own identity, so the
			// union is named for the schema rather than for the field that
			// referenced it. The parent-scoped name would not have collided here,
			// which is exactly the case that used to take it.
			name:           "ref key is the default",
			parentKey:      "tasks._common___TaskListResponseBase",
			field:          "result",
			matchForm:      "none",
			wantParentName: "TasksTaskListRespBase",
			wantUnionKey:   infosKey,
			wantFieldType:  "*TasksTaskInfos",
		},
		{
			// The referenced schema's name is taken by an unrelated schema, so
			// registering under it would be dropped by the registry and the union
			// would degrade to json.RawMessage. Fall back to the parent-scoped key,
			// which here does not collide with the parent's own name.
			name:            "taken ref name falls back to parent-scoped key",
			parentKey:       "tasks._common___TaskListResponseBase",
			field:           "result",
			preRegisterName: "TasksTaskInfos",
			matchForm:       "none",
			wantParentName:  "TasksTaskListRespBase",
			wantUnionKey:    "tasks._common___TaskListResponseBase.result",
			wantFieldType:   "*TasksTaskListRespBaseResult",
		},
		{
			// Ref name taken AND the parent-scoped name equals the parent struct's
			// own name in its non-resp form (a _common parent, where both forms are
			// equal): the FIRST operand of the parent-name check. Taking the
			// parent-scoped key would drop the parent, so the referenced key is used
			// despite being taken -- the union is lost rather than the parent.
			name:            "taken ref name plus non-resp parent collision keeps ref key",
			parentKey:       "tasks._common___TaskListResponseBase",
			field:           "tasks",
			preRegisterName: "TasksTaskInfos",
			matchForm:       "false",
			wantParentName:  "TasksTaskListRespBase",
			wantUnionKey:    infosKey,
			wantFieldType:   "*TasksTaskInfos",
		},
		{
			// Same, isolating the SECOND operand: a non-_common parent whose two name
			// forms differ (TasksResponse vs TasksResp), where the parent-scoped
			// union name (TasksResp) matches ONLY the resp-body form.
			name:            "taken ref name plus resp-body parent collision keeps ref key",
			parentKey:       "tasks___Response",
			field:           "tasks",
			preRegisterName: "TasksTaskInfos",
			matchForm:       "true",
			wantParentName:  "TasksResp",
			wantUnionKey:    infosKey,
			wantFieldType:   "*TasksTaskInfos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Verify the precondition so each case is a real regression test
			// rather than a tautology: confirm which name form (if any) the
			// parent-scoped union name actually collides with.
			unionName := schemaTypeName(tt.parentKey+"."+tt.field, false)
			matchFalse := unionName == schemaTypeName(tt.parentKey, false)
			matchTrue := unionName == schemaTypeName(tt.parentKey, true)
			switch tt.matchForm {
			case "false":
				require.True(t, matchFalse, "expected collision on non-resp form")
			case "true":
				require.True(t, matchTrue && !matchFalse, "expected collision on resp-body form only")
			case "none":
				require.False(t, matchFalse || matchTrue, "expected no collision")
			}

			// The referenced oneOf schema (array-of-object | map).
			infos := &openapi3.Schema{
				OneOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{
						Type:  &openapi3.Types{"array"},
						Items: &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
					}},
					{Value: &openapi3.Schema{
						Type: &openapi3.Types{"object"},
						AdditionalProperties: openapi3.AdditionalProperties{
							Schema: &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
						},
					}},
				},
			}
			infosRef := &openapi3.SchemaRef{
				Ref:   "#/components/schemas/" + infosKey,
				Value: infos, // pre-resolved, as the loader would leave it
			}

			parent := openapi3.NewObjectSchema()
			parent.Properties = openapi3.Schemas{tt.field: infosRef}

			spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
				tt.parentKey: &openapi3.SchemaRef{Value: parent},
				infosKey:     infosRef,
			}}}

			reg := newTypeRegistry(opensearchAPIPkgName)
			if tt.preRegisterName != "" {
				_, ok := reg.register(&goType{Name: tt.preRegisterName, SchemaRef: "unrelated___Squatter"})
				require.True(t, ok, "pre-registration must succeed")
			}
			w := &walker{registry: reg, spec: spec, inFlight: make(map[string]struct{})}

			parentName := w.walkSchema(spec.Components.Schemas[tt.parentKey], tt.parentKey, group, true)

			// The parent struct survives as a typed struct in every case.
			require.Equal(t, tt.wantParentName, parentName)
			parentType, ok := reg.lookup(tt.parentKey)
			require.True(t, ok, "parent struct must be registered, not silently dropped")
			require.False(t, parentType.IsUnion)
			require.Len(t, parentType.Fields, 1)

			// The field is typed and the union keyed as expected.
			field := parentType.Fields[0]
			require.Equal(t, tt.field, field.JSONName)
			require.Equal(t, tt.wantFieldType, field.GoType)

			if tt.preRegisterName != "" && tt.matchForm != "none" {
				// The union lost the name race to the squatter, which is the lesser
				// evil: the parent struct survives. The registry records the loss.
				require.NotEmpty(t, reg.collisions, "the dropped union must be reported")
				return
			}
			unionType, ok := reg.lookup(tt.wantUnionKey)
			require.True(t, ok, "union must be registered under %q", tt.wantUnionKey)
			require.True(t, unionType.IsUnion)
			require.Empty(t, reg.collisions)
		})
	}
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

func TestWalkerUnderscoreCollisionDisambiguation(t *testing.T) {
	t.Parallel()

	reg := newTypeRegistry(opensearchAPIPkgName)
	w := &walker{registry: reg, spec: &openapi3.T{}, inFlight: make(map[string]struct{})}

	// baseGoName strips leading underscores, so all three properties want the
	// Go name "Score". The non-underscore field keeps it; the underscore-
	// prefixed siblings disambiguate to unique "Raw"-suffixed names.
	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{
		"score":   &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()},
		"_score":  &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()},
		"__score": &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()},
	}

	ref := &openapi3.SchemaRef{Value: schema}
	got := w.walkSchema(ref, "test___ScoreCollision", "test", false)
	require.Equal(t, "TestScoreCollision", got)

	registered, ok := reg.lookupByName("TestScoreCollision")
	require.True(t, ok)
	require.Len(t, registered.Fields, 3)

	byJSON := make(map[string]string, len(registered.Fields))
	seenGo := make(map[string]string, len(registered.Fields))
	for _, f := range registered.Fields {
		if prev, dup := seenGo[f.GoName]; dup {
			t.Fatalf("duplicate Go field name %q (both %q and %q)", f.GoName, prev, f.JSONName)
		}
		seenGo[f.GoName] = f.JSONName
		byJSON[f.JSONName] = f.GoName
	}

	require.Equal(t, "Score", byJSON["score"], "non-underscore field claims the bare name")
	require.Equal(t, "ScoreRaw", byJSON["__score"])
	require.Equal(t, "ScoreRaw2", byJSON["_score"])
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

func TestResolveSchemaAlias(t *testing.T) {
	t.Parallel()

	// Build a spec with: a two-hop alias chain (A -> B -> C, C terminal),
	// a self-cycle (Loop -> Loop), and a mutual cycle (Ping <-> Pong).
	alias := func(target string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + target, Value: openapi3.NewObjectSchema()}
	}
	spec := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"grp___A":    alias("grp___B"),
		"grp___B":    alias("grp___C"),
		"grp___C":    &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
		"grp___Loop": alias("grp___Loop"),
		"grp___Ping": alias("grp___Pong"),
		"grp___Pong": alias("grp___Ping"),
		// A component whose Ref is not a #/components/schemas/ ref (e.g. an
		// external or otherwise unparseable ref): refToSchemaKey returns "",
		// so resolution stops and returns the input key unchanged.
		"grp___External": {Ref: "https://example.com/schema.json#/X", Value: openapi3.NewObjectSchema()},
	}}}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "two-hop alias resolves to terminal", key: "grp___A", want: "grp___C"},
		{name: "one-hop alias resolves to terminal", key: "grp___B", want: "grp___C"},
		{name: "terminal schema unchanged", key: "grp___C", want: "grp___C"},
		{name: "unknown key unchanged", key: "grp___Missing", want: "grp___Missing"},
		{name: "self cycle returns input", key: "grp___Loop", want: "grp___Loop"},
		{name: "mutual cycle terminates", key: "grp___Ping", want: "grp___Ping"},
		{name: "non-schema ref returns input", key: "grp___External", want: "grp___External"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resolveSchemaAlias(tt.key, spec))
		})
	}
}

func TestResolveSchemaAliasNilSpec(t *testing.T) {
	t.Parallel()
	require.Equal(t, "x", resolveSchemaAlias("x", nil))
	require.Equal(t, "x", resolveSchemaAlias("x", &openapi3.T{}))
}
