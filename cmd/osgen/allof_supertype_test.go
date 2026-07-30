// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"strings"
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

			got := w.redundantOverrideTags(schema, "pkg___Derived")
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

	require.Empty(t, w.redundantOverrideTags(schema, "pkg___Derived"),
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
			require.Empty(t, w.redundantOverrideTags(tt.schema, "pkg___Derived"))
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
		w.conveysNoConcreteType(&openapi3.SchemaRef{Ref: "#/components/schemas/pkg___Concrete"}, 0, func() {}),
		"a concrete schema stays concrete even when its own fields are erased")
}

// TestConveysNoConcreteTypeReportsDepthExceeded checks the diagnostic: a chain
// deeper than maxOverrideDepth reports once and resolves to "concrete", which
// preserves the field rather than dropping one on an undecided answer.
//
// The chain is built from inline containers. A $ref chain cannot reach the bound,
// because any named schema declaring items/properties is concrete and stops the
// descent immediately; only anonymous nesting recurses far enough.
func TestConveysNoConcreteTypeReportsDepthExceeded(t *testing.T) {
	t.Parallel()

	// Nest inline arrays past the bound, bottoming out in an erased parameter so
	// nothing short-circuits the descent as concrete on the way down.
	deepest := genericParamSchema()
	nested := deepest
	for range maxOverrideDepth + 2 {
		nested = &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{openapi3.TypeArray},
			Items: nested,
		}}
	}

	w := newOverrideWalker(openapi3.Schemas{})

	called := 0
	got := w.conveysNoConcreteType(nested, 0, func() { called++ })

	require.False(t, got, "an undecided chain resolves to concrete so the field is kept")
	require.Positive(t, called, "exceeding the depth bound must be reported")
}

// TestWarnOverrideDepthDedupes checks that the diagnostic reports each
// schema/property pair once, so a deep chain revisited during the walk does not
// flood the generator output.
func TestWarnOverrideDepthDedupes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w := newOverrideWalker(openapi3.Schemas{})
	w.warnOut = &buf

	w.warnOverrideDepth("pkg___Derived", "hits")
	w.warnOverrideDepth("pkg___Derived", "hits")
	w.warnOverrideDepth("pkg___Derived", "other")

	require.Equal(t, 1, strings.Count(buf.String(), `property "hits"`))
	require.Equal(t, 1, strings.Count(buf.String(), `property "other"`))
	require.Contains(t, buf.String(), "pkg___Derived")
}
