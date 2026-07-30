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

// genericParamSchema is a schema marked as an erased type parameter, the spec's
// TDocument/TBucket spelling.
func genericParamSchema() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{
		Extensions: map[string]any{extGenericTypeParam: true},
	}}
}

// newOverrideWalker builds a walker over a spec holding the given named schemas.
func newOverrideWalker(schemas openapi3.Schemas) *walker {
	return &walker{
		registry: newTypeRegistry(opensearchAPIPkgName),
		spec:     &openapi3.T{Components: &openapi3.Components{Schemas: schemas}},
		inFlight: make(set[string]),
	}
}

// TestRedundantOverrideTagsSelectsErasures pins the classifier that decides which
// allOf overrides are dropped. The distinction is the whole basis of the fix: an
// override substituting an erased type parameter conveys nothing and must be
// dropped so the base's field is promoted, while one naming a concrete schema is a
// real narrowing and must be kept. Both render as the same duplicate JSON tag, so
// only the spec can tell them apart.
func TestRedundantOverrideTagsSelectsErasures(t *testing.T) {
	t.Parallel()

	// base declares "hits"; the override redeclares it.
	base := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Properties: openapi3.Schemas{
			"hits":      {Value: openapi3.NewStringSchema()},
			"max_score": {Value: openapi3.NewFloat64Schema()},
		},
	}}
	schemas := openapi3.Schemas{
		"pkg___Base":     base,
		"pkg___TDoc":     genericParamSchema(),
		"pkg___Concrete": {Value: &openapi3.Schema{Properties: openapi3.Schemas{"key": {Value: openapi3.NewStringSchema()}}}},
	}

	tests := []struct {
		name     string
		override *openapi3.SchemaRef
		wantDrop bool
	}{
		{
			name:     "substitutes an erased type parameter",
			override: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___TDoc"},
			wantDrop: true,
		},
		{
			name:     "substitutes an empty schema",
			override: &openapi3.SchemaRef{Value: &openapi3.Schema{}},
			wantDrop: true,
		},
		{
			name: "array of the erased parameter",
			override: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:  &openapi3.Types{openapi3.TypeArray},
				Items: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___TDoc"},
			}},
			wantDrop: true,
		},
		{
			name:     "names a concrete schema",
			override: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Concrete"},
			wantDrop: false,
		},
		{
			name: "array of a concrete schema",
			override: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:  &openapi3.Types{openapi3.TypeArray},
				Items: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Concrete"},
			}},
			wantDrop: false,
		},
		{
			// The bucket-aggregation shape: allOf of the erased base plus a
			// concrete narrowing. One concrete branch makes it a real override.
			name: "erased base plus concrete branch is a narrowing",
			override: &openapi3.SchemaRef{Value: &openapi3.Schema{
				AllOf: openapi3.SchemaRefs{
					{Ref: "#/components/schemas/pkg___TDoc"},
					{Ref: "#/components/schemas/pkg___Concrete"},
				},
			}},
			wantDrop: false,
		},
		{
			name:     "declares a primitive type",
			override: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			wantDrop: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newOverrideWalker(schemas)
			schema := &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Ref: "#/components/schemas/pkg___Base"},
				{Value: &openapi3.Schema{Properties: openapi3.Schemas{"hits": tt.override}}},
			}}

			got := w.redundantOverrideTags(schema)
			require.Equal(t, tt.wantDrop, got["hits"],
				"drop verdict for the redeclared %q tag", "hits")
			// Siblings the override does not touch are never dropped.
			require.False(t, got["max_score"])
		})
	}
}

// TestRedundantOverrideTagsKeepsNewProperties covers the case that separates
// "redundant" from merely "erased": an erased property the base does NOT declare
// is that field's only declaration, so dropping it would remove the field.
func TestRedundantOverrideTagsKeepsNewProperties(t *testing.T) {
	t.Parallel()

	schemas := openapi3.Schemas{
		"pkg___Base": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"other": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___TDoc": genericParamSchema(),
	}
	w := newOverrideWalker(schemas)

	schema := &openapi3.Schema{AllOf: openapi3.SchemaRefs{
		{Ref: "#/components/schemas/pkg___Base"},
		{Value: &openapi3.Schema{Properties: openapi3.Schemas{
			// Erased, but introduced here: the base has no "value".
			"value": {Ref: "#/components/schemas/pkg___TDoc"},
		}}},
	}}

	require.Empty(t, w.redundantOverrideTags(schema),
		"an erased property the base does not declare is the field's only declaration")
}

// TestRedundantOverrideTagsIgnoresNonComposedSchemas guards the cheap exits: a
// schema with fewer than two allOf members has no base to shadow.
func TestRedundantOverrideTagsIgnoresNonComposedSchemas(t *testing.T) {
	t.Parallel()

	w := newOverrideWalker(openapi3.Schemas{"pkg___TDoc": genericParamSchema()})

	for _, tt := range []struct {
		name   string
		schema *openapi3.Schema
	}{
		{name: "no allOf", schema: &openapi3.Schema{Properties: openapi3.Schemas{
			"hits": {Ref: "#/components/schemas/pkg___TDoc"},
		}}},
		{name: "single-member allOf", schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Value: &openapi3.Schema{Properties: openapi3.Schemas{
				"hits": {Ref: "#/components/schemas/pkg___TDoc"},
			}}},
		}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, w.redundantOverrideTags(tt.schema))
		})
	}
}

// TestConveysNoConcreteTypeStopsAtConcreteSchemas pins the descent rule. A named
// concrete schema is concrete regardless of what its own fields reference;
// descending into them would follow the aggregation graph until it reached an
// erased leaf, which would misclassify nearly every override as redundant.
func TestConveysNoConcreteTypeStopsAtConcreteSchemas(t *testing.T) {
	t.Parallel()

	// Concrete names a field whose type is itself erased.
	schemas := openapi3.Schemas{
		"pkg___TDoc": genericParamSchema(),
		"pkg___Concrete": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"inner": {Ref: "#/components/schemas/pkg___TDoc"},
		}}},
	}
	w := newOverrideWalker(schemas)

	require.False(t,
		w.conveysNoConcreteType(&openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Concrete"}, make(set[string])),
		"a concrete schema stays concrete even when its own fields are erased")
}

// cyclicSchemas returns a mutually recursive allOf pair: A composes B and B
// composes A. Neither declares anything, so nothing stops the descent on
// content; only the visited set can.
func cyclicSchemas() openapi3.Schemas {
	return openapi3.Schemas{
		"pkg___A": {Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/pkg___B"},
		}}},
		"pkg___B": {Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/pkg___A"},
		}}},
	}
}

// TestConveysNoConcreteTypeTerminatesOnCycle pins the cycle guard. A $ref is the
// only way the descent can revisit a schema, so recording the refs in flight is
// what makes a cyclic spec terminate. A repeat resolves as concrete, matching the
// treatment of a $ref that cannot be resolved at all: the field is kept.
func TestConveysNoConcreteTypeTerminatesOnCycle(t *testing.T) {
	t.Parallel()

	w := newOverrideWalker(cyclicSchemas())

	require.False(t,
		w.conveysNoConcreteType(&openapi3.SchemaRef{Ref: "#/components/schemas/pkg___A"}, make(set[string])),
		"a cycle resolves as concrete so the field is kept")
}

// TestDeclaresPropertyTerminatesOnCycle pins the cycle guard on the shadow
// probe. Unlike conveysNoConcreteType this walk has no namesConcreteSchema-style
// stopping rule, so without the visited set a mutually recursive allOf pair
// recurses until the stack overflows.
func TestDeclaresPropertyTerminatesOnCycle(t *testing.T) {
	t.Parallel()

	w := newOverrideWalker(cyclicSchemas())

	require.False(t,
		w.declaresProperty(&openapi3.SchemaRef{Ref: "#/components/schemas/pkg___A"}, "hits", make(set[string])),
		"a cycle declaring nothing terminates with no declaration found")
}

// TestDeclaresPropertyProbesSiblingsIndependently pins why redundantOverrideTags
// gives each sibling probe a fresh visited set. The probes are independent paths
// through the same schema graph; sharing one set would let a schema reached via
// the first sibling suppress the answer for the second, so an override that does
// shadow a base declaration would read as introducing the field.
func TestDeclaresPropertyProbesSiblingsIndependently(t *testing.T) {
	t.Parallel()

	// Both branches reach Base, so a shared visited set would mark it seen on the
	// first probe and make the second miss the declaration.
	schemas := openapi3.Schemas{
		"pkg___Base": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"hits": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___Left": {Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/pkg___Base"},
		}}},
		"pkg___Right": {Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/pkg___Base"},
		}}},
	}
	w := newOverrideWalker(schemas)

	for _, branch := range []string{"pkg___Left", "pkg___Right"} {
		require.True(t,
			w.declaresProperty(&openapi3.SchemaRef{Ref: "#/components/schemas/" + branch}, "hits", make(set[string])),
			"each sibling branch reaches the declaration on its own probe: %s", branch)
	}
}

// TestNarrowedUnionMember pins the distinction between an allOf that composes
// structs and an allOf that narrows a union.
//
// The spec instantiates a generic union by allOf-ing the erased base with a
// oneOf that restates the branches over the concrete type argument. Merging that
// as a struct discards the narrowing (a oneOf member contributes no properties),
// which degraded every aggregation `buckets` union to json.RawMessage and left
// the concrete bucket types unemitted.
func TestNarrowedUnionMember(t *testing.T) {
	t.Parallel()

	oneOfBranches := func() openapi3.SchemaRefs {
		return openapi3.SchemaRefs{
			{Value: &openapi3.Schema{
				Type:                 &openapi3.Types{openapi3.TypeObject},
				AdditionalProperties: openapi3.AdditionalProperties{Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Bucket"}},
			}},
			{Value: &openapi3.Schema{
				Type:  &openapi3.Types{openapi3.TypeArray},
				Items: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Bucket"},
			}},
		}
	}

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			// The shape the aggregation buckets use. The loader resolves the $ref,
			// so the base member carries its own oneOf too -- it must be skipped by
			// Ref, or it looks like a second narrowing and the whole thing is
			// rejected.
			name: "base $ref plus inline oneOf narrowing",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{
					Ref:   "#/components/schemas/pkg___Buckets",
					Value: &openapi3.Schema{OneOf: oneOfBranches()},
				},
				{Value: &openapi3.Schema{OneOf: oneOfBranches()}},
			}},
			want: true,
		},
		{
			name: "anyOf narrowing is equivalent",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Ref: "#/components/schemas/pkg___Buckets"},
				{Value: &openapi3.Schema{AnyOf: oneOfBranches()}},
			}},
			want: true,
		},
		{
			// A member contributing properties makes this a real composition; the
			// struct merge is correct and must not be bypassed.
			name: "member with properties is a struct composition",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Ref: "#/components/schemas/pkg___Base"},
				{Value: &openapi3.Schema{
					Type:       &openapi3.Types{openapi3.TypeObject},
					Properties: openapi3.Schemas{"extra": {Value: openapi3.NewStringSchema()}},
				}},
			}},
			want: false,
		},
		{
			name: "two inline narrowings are ambiguous",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Value: &openapi3.Schema{OneOf: oneOfBranches()}},
				{Value: &openapi3.Schema{OneOf: oneOfBranches()}},
			}},
			want: false,
		},
		{
			name: "no oneOf member at all",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Ref: "#/components/schemas/pkg___Base"},
				{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}}},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			narrowed, ok := narrowedUnionMember(tt.schema)
			require.Equal(t, tt.want, ok)
			if tt.want {
				require.NotNil(t, narrowed, "the narrowing member must be returned")
				require.NotEmpty(t, append(narrowed.OneOf, narrowed.AnyOf...),
					"the returned member is the one carrying the branches")
			}
		})
	}
}

// TestConveysNoConcreteTypeDescendsContainers covers the descent itself: which
// container shapes delegate to their element type and which declarations stop
// the descent with a concrete verdict. Every "true" here is a substitution the
// caller may drop; every "false" is a field that must be preserved.
func TestConveysNoConcreteTypeDescendsContainers(t *testing.T) {
	t.Parallel()

	schemas := openapi3.Schemas{
		"pkg___TDoc":  genericParamSchema(),
		"pkg___Empty": {Value: &openapi3.Schema{}},
		"pkg___Concrete": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"key": {Value: openapi3.NewStringSchema()},
		}}},
	}

	erasedRef := &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___TDoc"}
	concreteRef := &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Concrete"}

	tests := []struct {
		name string
		prop *openapi3.SchemaRef
		want bool
	}{
		{
			name: "a nil property conveys a concrete type by default",
			prop: nil,
			want: false,
		},
		{
			name: "a SchemaRef with neither ref nor value is undecidable",
			prop: &openapi3.SchemaRef{},
			want: false,
		},
		{
			// An unresolvable $ref names a type this generator cannot inspect;
			// guessing "erased" would drop a field on no evidence.
			name: "a $ref absent from the spec is treated as concrete",
			prop: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Missing"},
			want: false,
		},
		{
			// A named schema that declares nothing is an alias for nothing, so
			// the descent continues through it and reaches the same verdict.
			name: "a $ref to an empty named schema is still erased",
			prop: &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Empty"},
			want: true,
		},
		{
			name: "map of the erased parameter",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:                 &openapi3.Types{openapi3.TypeObject},
				AdditionalProperties: openapi3.AdditionalProperties{Schema: erasedRef},
			}},
			want: true,
		},
		{
			name: "map of a concrete schema",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:                 &openapi3.Types{openapi3.TypeObject},
				AdditionalProperties: openapi3.AdditionalProperties{Schema: concreteRef},
			}},
			want: false,
		},
		{
			// The SearchResult.hits override: an inline object whose only
			// property restates _source as the empty schema.
			name: "inline object whose every property is erased",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{openapi3.TypeObject},
				Properties: openapi3.Schemas{
					"_source": {Value: &openapi3.Schema{}},
				},
			}},
			want: true,
		},
		{
			name: "inline object with one concrete property is a narrowing",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{openapi3.TypeObject},
				Properties: openapi3.Schemas{
					"_source": {Value: &openapi3.Schema{}},
					"score":   {Value: openapi3.NewFloat64Schema()},
				},
			}},
			want: false,
		},
		{
			name: "allOf of only erased members conveys nothing",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				erasedRef,
				{Value: &openapi3.Schema{}},
			}}},
			want: true,
		},
		{
			// An enumeration is a real Go type even though it declares no
			// properties and, on some spec paths, no type either.
			name: "an enumeration is concrete",
			prop: &openapi3.SchemaRef{Value: &openapi3.Schema{Enum: []any{"asc", "desc"}}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newOverrideWalker(schemas)
			require.Equal(t, tt.want, w.conveysNoConcreteType(tt.prop, make(set[string])))
		})
	}
}

// TestNamesConcreteSchema pins the stop condition for the descent: anything a Go
// type can carry makes a named schema concrete, and only the erasure marker
// overrides that.
func TestNamesConcreteSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			name:   "the erasure marker wins over anything else declared",
			schema: &openapi3.Schema{Extensions: map[string]any{extGenericTypeParam: true}},
			want:   false,
		},
		{
			name:   "properties",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{"a": {Value: openapi3.NewStringSchema()}}},
			want:   true,
		},
		{
			name:   "allOf",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
			want:   true,
		},
		{
			name:   "oneOf",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
			want:   true,
		},
		{
			name:   "anyOf",
			schema: &openapi3.Schema{AnyOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
			want:   true,
		},
		{
			name:   "enum",
			schema: &openapi3.Schema{Enum: []any{"asc"}},
			want:   true,
		},
		{
			name:   "array items",
			schema: &openapi3.Schema{Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
			want:   true,
		},
		{
			name: "additionalProperties schema",
			schema: &openapi3.Schema{AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			}},
			want: true,
		},
		{
			name:   "a declared primitive type",
			schema: openapi3.NewStringSchema(),
			want:   true,
		},
		{
			// `type: object` with nothing else is the spec's spelling for an
			// erased type argument, not a Go type.
			name:   "a bare object declaration is not concrete",
			schema: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}},
			want:   false,
		},
		{
			name:   "an empty schema is not concrete",
			schema: &openapi3.Schema{},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, namesConcreteSchema(tt.schema))
		})
	}
}

// TestDeclaresProperty pins the search for the declaration an override shadows.
// It follows only the paths resolveAllOf and collectFields build fields from, so
// a property found here is genuinely reachable by Go field promotion.
func TestDeclaresProperty(t *testing.T) {
	t.Parallel()

	schemas := openapi3.Schemas{
		"pkg___Base": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"hits": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___Composed": {Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/pkg___Base"},
		}}},
		"pkg___Unrelated": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"other": {Value: openapi3.NewStringSchema()},
		}}},
	}

	tests := []struct {
		name string
		ref  *openapi3.SchemaRef
		// visited pre-seeds the refs already on the caller's path.
		visited set[string]
		want    bool
	}{
		{
			name: "a nil member declares nothing",
			ref:  nil,
		},
		{
			name: "a $ref absent from the spec declares nothing",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Missing"},
		},
		{
			name: "a SchemaRef with neither ref nor value declares nothing",
			ref:  &openapi3.SchemaRef{},
		},
		{
			name: "an inline member declaring the property",
			ref: &openapi3.SchemaRef{Value: &openapi3.Schema{Properties: openapi3.Schemas{
				"hits": {Value: openapi3.NewStringSchema()},
			}}},
			want: true,
		},
		{
			name: "a $ref whose target declares the property",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Base"},
			want: true,
		},
		{
			// The spec chains these: the shadowed declaration can sit one allOf
			// hop below the member the override is compared against.
			name: "a $ref whose target composes the declaring schema",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Composed"},
			want: true,
		},
		{
			name: "a schema declaring only other properties",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Unrelated"},
		},
		{
			// A ref already on the path is not re-entered, so the answer is "not
			// declared" and the override is kept. That is the safe direction: a
			// cyclic spec terminates instead of dropping a field.
			name:    "a $ref already on the path is not re-entered",
			ref:     &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Base"},
			visited: newSet("pkg___Base"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newOverrideWalker(schemas)
			visited := tt.visited
			if visited == nil {
				visited = make(set[string])
			}
			require.Equal(t, tt.want, w.declaresProperty(tt.ref, "hits", visited))
		})
	}
}

// collapseSchemas returns the named schemas the collapse tests compose against:
// a base declaring "hits", a second base to compose with, a concrete narrowing
// target, and the spec's erased type parameter.
func collapseSchemas() openapi3.Schemas {
	return openapi3.Schemas{
		"pkg___Base": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"hits": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___Other": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"other": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___Concrete": {Value: &openapi3.Schema{Properties: openapi3.Schemas{
			"key": {Value: openapi3.NewStringSchema()},
		}}},
		"pkg___TDoc": genericParamSchema(),
	}
}

// TestCollapsesToBase pins the judgment that erases a wrapper type in favor of
// its allOf base. It is the load-bearing decision of the collapse work in both
// directions: too eager and a real narrowing is thrown away along with the
// fields only the wrapper reaches, too shy and every readable alias the spec
// writes emits a separate nominal type whose entire content is the embedded base.
func TestCollapsesToBase(t *testing.T) {
	t.Parallel()

	baseRef := &openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Base"}

	tests := []struct {
		name string
		// wantBase is "" when no collapse applies.
		schema   *openapi3.Schema
		wantBase string
	}{
		{
			// AdjacencyMatrixAggregate over the mangled generic instantiation.
			name:     "a bare $ref rename collapses onto its target",
			schema:   &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef}},
			wantBase: "pkg___Base",
		},
		{
			name: "a re-export with an empty override collapses",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Type:       &openapi3.Types{openapi3.TypeObject},
				Properties: openapi3.Schemas{},
			}}}},
			wantBase: "pkg___Base",
		},
		{
			// HitsMetadataJsonValue over HitsMetadata: the override restates
			// "hits" to substitute the erased type argument, so it conveys
			// nothing the base does not already carry.
			name: "an erased narrowing of a base property collapses",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Properties: openapi3.Schemas{
					"hits": {Ref: "#/components/schemas/pkg___TDoc"},
				},
			}}}},
			wantBase: "pkg___Base",
		},
		{
			// MultiBucketAggregateBaseAdjacencyMatrixBucket: the override
			// narrows to a schema carrying strictly more than the base, so the
			// wrapper is the informative type and must survive.
			name: "a concrete narrowing does not collapse",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Properties: openapi3.Schemas{
					"hits": {Ref: "#/components/schemas/pkg___Concrete"},
				},
			}}}},
		},
		{
			// The property is erased, but the base never declares it, so it is
			// this field's only declaration; collapsing would remove the field.
			name: "an override introducing a new erased property does not collapse",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Properties: openapi3.Schemas{
					"extra": {Ref: "#/components/schemas/pkg___TDoc"},
				},
			}}}},
		},
		{
			name: "a member adding a required field constrains the base",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, {Value: &openapi3.Schema{
				Type:     &openapi3.Types{openapi3.TypeObject},
				Required: []string{"hits"},
			}}}},
		},
		{
			name:   "a nil member makes the shape undecidable",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{baseRef, nil}},
		},
		{
			name: "two $ref members compose distinct schemas",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				baseRef,
				{Ref: "#/components/schemas/pkg___Other"},
			}},
			wantBase: "",
		},
		{
			name: "a member with neither ref nor value is skipped",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				baseRef,
				{},
			}},
			wantBase: "pkg___Base",
		},
		{
			name: "an allOf with no $ref member has nothing to collapse onto",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: &openapi3.Schema{
				Type: &openapi3.Types{openapi3.TypeObject},
			}}}},
		},
		{
			name: "a schema without an allOf is not a wrapper",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{
				"hits": {Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name: "a wrapper declaring its own properties adds something",
			schema: &openapi3.Schema{
				AllOf:      openapi3.SchemaRefs{baseRef},
				Properties: openapi3.Schemas{"extra": {Value: openapi3.NewStringSchema()}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newOverrideWalker(collapseSchemas())

			got, ok := w.collapsesToBase(tt.schema)

			require.Equal(t, tt.wantBase != "", ok)
			require.Equal(t, tt.wantBase, got)
		})
	}
}

// TestOnlyRedundantProperties pins the gate collapsesToBase applies to each
// inline allOf member. A member may only be ignored when everything it declares
// is a property already known to restate the base's; anything else it says --
// a type, a composition, a value constraint, a new required field -- is content
// that would be lost by collapsing.
func TestOnlyRedundantProperties(t *testing.T) {
	t.Parallel()

	redundant := map[string]bool{"hits": true}

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			name:   "an empty member says nothing",
			schema: &openapi3.Schema{},
			want:   true,
		},
		{
			name:   "a bare object declaration says nothing",
			schema: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}},
			want:   true,
		},
		{
			name: "every property is a known redundant override",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{
				"hits": {Value: &openapi3.Schema{}},
			}},
			want: true,
		},
		{
			name: "a property not marked redundant is real content",
			schema: &openapi3.Schema{Properties: openapi3.Schemas{
				"hits":  {Value: &openapi3.Schema{}},
				"extra": {Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name:   "composing further schemas",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "a oneOf branch set",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "an anyOf branch set",
			schema: &openapi3.Schema{AnyOf: openapi3.SchemaRefs{{Value: openapi3.NewStringSchema()}}},
		},
		{
			name:   "array items",
			schema: &openapi3.Schema{Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
		},
		{
			name: "an additionalProperties schema",
			schema: &openapi3.Schema{AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			}},
		},
		{
			name:   "an enumeration",
			schema: &openapi3.Schema{Enum: []any{"asc"}},
		},
		{
			name:   "a non-object type",
			schema: openapi3.NewStringSchema(),
		},
		{
			name: "a newly required field",
			schema: &openapi3.Schema{
				Type:     &openapi3.Types{openapi3.TypeObject},
				Required: []string{"hits"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, onlyRedundantProperties(tt.schema, redundant))
		})
	}
}

// TestResolveCollapsedBaseAliasesTheWrapper pins what the collapse leaves
// behind: one emitted type, registered under the BASE's key and name, with the
// wrapper's ref aliased onto it. The alias is what keeps every later lookup by
// the wrapper's ref working -- notably the response-body classifier, which would
// otherwise degrade the response to raw JSON.
func TestResolveCollapsedBaseAliasesTheWrapper(t *testing.T) {
	t.Parallel()

	const (
		alias = "pkg___Alias"
		base  = "pkg___Base"
	)
	schemas := collapseSchemas()
	schemas[alias] = &openapi3.SchemaRef{Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
		{Ref: "#/components/schemas/" + base},
	}}}
	w := newOverrideWalker(schemas)

	got, ok := w.resolveCollapsedBase(schemas[alias].Value, alias, "pkg", false)

	require.True(t, ok)
	require.Equal(t, "PkgBase", got, "the emitted type keeps the base's own name")

	baseType, found := w.registry.lookup(base)
	require.True(t, found)
	aliasType, found := w.registry.lookup(alias)
	require.True(t, found, "the wrapper's ref must still resolve to a registered type")
	require.Same(t, baseType, aliasType)
	require.Len(t, w.registry.all(), 1, "the wrapper must not be emitted as its own type")
}

// TestResolveCollapsedBaseDeclines covers the cases that must leave the wrapper
// alone, each for a different reason than "the shapes differ".
func TestResolveCollapsedBaseDeclines(t *testing.T) {
	t.Parallel()

	const (
		alias = "pkg___Alias"
		base  = "pkg___Base"
	)
	aliasSchema := func(target string) *openapi3.Schema {
		return &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/" + target},
		}}
	}

	tests := []struct {
		name       string
		schema     *openapi3.Schema
		isRespBody bool
		// setup adjusts the walker before the call.
		setup func(w *walker)
	}{
		{
			name:   "a nil schema",
			schema: nil,
		},
		{
			// An operation's Resp inlines whatever its ref resolves to, so
			// collapsing one would stop emitting the base as a standalone type
			// and break every other schema that names it.
			name:       "a response body is exempt",
			schema:     aliasSchema(base),
			isRespBody: true,
		},
		{
			name:   "a base already being walked would recurse",
			schema: aliasSchema(base),
			setup:  func(w *walker) { w.inFlight.add(base) },
		},
		{
			name:   "a base absent from the spec cannot be resolved",
			schema: aliasSchema("pkg___Missing"),
		},
		{
			// The base is itself an erased type parameter, so it resolves to
			// json.RawMessage; aliasing onto that would discard the wrapper's
			// name for no type at all.
			name:   "a base that erases to json.RawMessage",
			schema: aliasSchema("pkg___TDoc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newOverrideWalker(collapseSchemas())
			if tt.setup != nil {
				tt.setup(w)
			}

			got, ok := w.resolveCollapsedBase(tt.schema, alias, "pkg", tt.isRespBody)

			require.False(t, ok)
			require.Empty(t, got)
			_, aliased := w.registry.lookup(alias)
			require.False(t, aliased, "a declined collapse must not alias the wrapper's ref")
		})
	}
}
