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

// constProp builds a `<name>: {type: string, enum: [value]}` property map, the
// spelling the OpenSearch spec uses to pin a branch's discriminator value.
func constProp(name, value string) openapi3.Schemas {
	return openapi3.Schemas{
		name: {Value: &openapi3.Schema{
			Type: &openapi3.Types{openapi3.TypeString},
			Enum: []any{value},
		}},
	}
}

// refBranch builds a $ref-bearing branch whose Value is pre-resolved, as the
// loader leaves it.
func refBranch(key string, schema *openapi3.Schema) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Ref: "#/components/schemas/" + key, Value: schema}
}

func TestResolveDiscriminatorConst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ref    *openapi3.SchemaRef
		prop   string
		want   string
		wantOK bool
	}{
		{
			name: "const on the branch itself",
			ref:  refBranch("k", &openapi3.Schema{Properties: constProp("type", "keyword")}),
			prop: "type", want: "keyword", wantOK: true,
		},
		{
			name: "JSON Schema const spelling",
			ref: refBranch("k", &openapi3.Schema{Properties: openapi3.Schemas{
				"type": {Value: &openapi3.Schema{Const: "keyword"}},
			}}),
			prop: "type", want: "keyword", wantOK: true,
		},
		{
			// KeywordProperty's shape: the narrowing override sits in an allOf
			// member alongside the base, not on the branch root.
			name: "const on an allOf override member",
			ref: refBranch("k", &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				refBranch("base", &openapi3.Schema{Properties: openapi3.Schemas{
					"doc_values": {Value: openapi3.NewBoolSchema()},
				}}),
				{Value: &openapi3.Schema{Properties: constProp("type", "keyword")}},
			}}),
			prop: "type", want: "keyword", wantOK: true,
		},
		{
			// The value often sits several allOf levels up, on a base schema.
			name: "const resolved transitively through nested allOf",
			ref: refBranch("outer", &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				refBranch("mid", &openapi3.Schema{AllOf: openapi3.SchemaRefs{
					refBranch("inner", &openapi3.Schema{Properties: constProp("type", "date")}),
				}}),
			}}),
			prop: "type", want: "date", wantOK: true,
		},
		{
			name: "property absent",
			ref:  refBranch("k", &openapi3.Schema{Properties: constProp("other", "x")}),
			prop: "type", wantOK: false,
		},
		{
			// A multi-value enum is not a constant: two branches could both
			// accept the same value, so the branch is not identifiable.
			name: "multi-value enum is not a constant",
			ref: refBranch("k", &openapi3.Schema{Properties: openapi3.Schemas{
				"type": {Value: &openapi3.Schema{Enum: []any{"a", "b"}}},
			}}),
			prop: "type", wantOK: false,
		},
		{
			name: "non-string enum value",
			ref: refBranch("k", &openapi3.Schema{Properties: openapi3.Schemas{
				"type": {Value: &openapi3.Schema{Enum: []any{42}}},
			}}),
			prop: "type", wantOK: false,
		},
		{
			name: "nil ref",
			ref:  nil,
			prop: "type", wantOK: false,
		},
		{
			name: "unresolved ref value",
			ref:  &openapi3.SchemaRef{Ref: "#/components/schemas/missing"},
			prop: "type", wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := resolveDiscriminatorConst(tt.ref, tt.prop, make(set[string]))
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestResolveDiscriminatorConstCycle proves the visited set terminates a
// mutually recursive allOf pair. Without it the walk recurses until the stack
// overflows: there is no other stopping rule on the descent.
func TestResolveDiscriminatorConstCycle(t *testing.T) {
	t.Parallel()

	// a -> allOf[b] -> allOf[a], neither declaring the discriminator property.
	a := &openapi3.Schema{}
	b := &openapi3.Schema{}
	a.AllOf = openapi3.SchemaRefs{refBranch("b", b)}
	b.AllOf = openapi3.SchemaRefs{refBranch("a", a)}

	got, ok := resolveDiscriminatorConst(refBranch("a", a), "type", make(set[string]))
	require.False(t, ok, "a cyclic allOf chain resolves to no value")
	require.Empty(t, got)
}

// TestResolveDiscriminatorConstCycleWithValue pins that the cycle guard stops
// the descent WITHOUT hiding a value reachable before the cycle closes.
func TestResolveDiscriminatorConstCycleWithValue(t *testing.T) {
	t.Parallel()

	a := &openapi3.Schema{}
	b := &openapi3.Schema{Properties: constProp("type", "keyword")}
	a.AllOf = openapi3.SchemaRefs{refBranch("b", b)}
	b.AllOf = openapi3.SchemaRefs{refBranch("a", a)}

	got, ok := resolveDiscriminatorConst(refBranch("a", a), "type", make(set[string]))
	require.True(t, ok)
	require.Equal(t, "keyword", got)
}

func TestDiscriminatorValues(t *testing.T) {
	t.Parallel()

	twoBranches := func() openapi3.SchemaRefs {
		return openapi3.SchemaRefs{
			refBranch("kw", &openapi3.Schema{Properties: constProp("type", "keyword")}),
			refBranch("dt", &openapi3.Schema{Properties: constProp("type", "date")}),
		}
	}

	tests := []struct {
		name        string
		schema      *openapi3.Schema
		wantOK      bool
		wantValues  map[int]string
		wantProp    string
		wantDefault string
	}{
		{
			name: "every branch resolves uniquely",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{PropertyName: "type"},
				OneOf:         twoBranches(),
			},
			wantOK:     true,
			wantProp:   "type",
			wantValues: map[int]string{0: "keyword", 1: "date"},
		},
		{
			name: "x-default is carried through",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{
					PropertyName: "type",
					Extensions:   map[string]any{extDiscriminatorDefault: "date"},
				},
				OneOf: twoBranches(),
			},
			wantOK:      true,
			wantProp:    "type",
			wantDefault: "date",
			wantValues:  map[int]string{0: "keyword", 1: "date"},
		},
		{
			// A default naming a value no branch claims would decode into
			// nothing, so the whole discriminator is refused.
			name: "x-default naming no branch refuses the discriminator",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{
					PropertyName: "type",
					Extensions:   map[string]any{extDiscriminatorDefault: "nonexistent"},
				},
				OneOf: twoBranches(),
			},
			wantOK: false,
		},
		{
			// discriminator.mapping wins over the branch's own const, and
			// supplies a value for a branch that declares none.
			name: "explicit mapping supplies the value",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{
					PropertyName: "type",
					Mapping: map[string]openapi3.MappingRef{
						"mapped_a": {Ref: "#/components/schemas/a"},
						"mapped_b": {Ref: "#/components/schemas/b"},
					},
				},
				OneOf: openapi3.SchemaRefs{
					refBranch("a", &openapi3.Schema{}),
					refBranch("b", &openapi3.Schema{}),
				},
			},
			wantOK:     true,
			wantProp:   "type",
			wantValues: map[int]string{0: "mapped_a", 1: "mapped_b"},
		},
		{
			// All-or-nothing: one unresolvable branch disqualifies the union, so
			// the decoder never has to guess.
			name: "one unresolvable branch refuses the whole union",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{PropertyName: "type"},
				OneOf: openapi3.SchemaRefs{
					refBranch("kw", &openapi3.Schema{Properties: constProp("type", "keyword")}),
					refBranch("none", &openapi3.Schema{}),
				},
			},
			wantOK: false,
		},
		{
			// Two branches claiming one value cannot both be selected.
			name: "duplicate values refuse the whole union",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{PropertyName: "type"},
				OneOf: openapi3.SchemaRefs{
					refBranch("a", &openapi3.Schema{Properties: constProp("type", "same")}),
					refBranch("b", &openapi3.Schema{Properties: constProp("type", "same")}),
				},
			},
			wantOK: false,
		},
		{
			name:   "no discriminator declared",
			schema: &openapi3.Schema{OneOf: twoBranches()},
			wantOK: false,
		},
		{
			name: "discriminator with empty propertyName",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{},
				OneOf:         twoBranches(),
			},
			wantOK: false,
		},
		{
			name: "single branch is not a union",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{PropertyName: "type"},
				OneOf: openapi3.SchemaRefs{
					refBranch("kw", &openapi3.Schema{Properties: constProp("type", "keyword")}),
				},
			},
			wantOK: false,
		},
		{
			// Null branches carry no discriminator and are handled by pointer
			// semantics, so they neither resolve nor shift the ordinals of the
			// branches that follow.
			name: "null branch is skipped without consuming an ordinal",
			schema: &openapi3.Schema{
				Discriminator: &openapi3.Discriminator{PropertyName: "type"},
				OneOf: openapi3.SchemaRefs{
					{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeNull}}},
					refBranch("kw", &openapi3.Schema{Properties: constProp("type", "keyword")}),
					refBranch("dt", &openapi3.Schema{Properties: constProp("type", "date")}),
				},
			},
			wantOK:     true,
			wantProp:   "type",
			wantValues: map[int]string{0: "keyword", 1: "date"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			branches := tt.schema.OneOf
			if len(branches) == 0 {
				branches = tt.schema.AnyOf
			}
			disc, values, ok := discriminatorValues(tt.schema, branches)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				require.Nil(t, disc)
				require.Nil(t, values)
				return
			}
			require.Equal(t, tt.wantProp, disc.PropertyName)
			require.Equal(t, tt.wantDefault, disc.DefaultValue)
			require.Equal(t, tt.wantValues, values)
		})
	}
}
