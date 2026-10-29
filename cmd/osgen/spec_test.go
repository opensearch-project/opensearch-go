// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

func TestOperationGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *openapi3.Operation
		want string
	}{
		{name: "nil operation", op: nil, want: ""},
		{name: "nil extensions", op: &openapi3.Operation{}, want: ""},
		{name: "missing key", op: &openapi3.Operation{
			Extensions: map[string]any{"x-other": "val"},
		}, want: ""},
		{name: "string value", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: "indices.create"},
		}, want: "indices.create"},
		{name: "json.RawMessage value", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: json.RawMessage(`"cluster.health"`)},
		}, want: "cluster.health"},
		{name: "invalid json.RawMessage", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: json.RawMessage(`not-json`)},
		}, want: ""},
		{name: "unexpected type", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: 42},
		}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, operationGroup(tt.op))
		})
	}
}

func TestDeprecationMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *openapi3.Operation
		want string
	}{
		{name: "nil operation", op: nil, want: ""},
		{name: "nil extensions", op: &openapi3.Operation{}, want: ""},
		{name: "missing key", op: &openapi3.Operation{
			Extensions: map[string]any{},
		}, want: ""},
		{name: "string value", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: "Use v2 API instead."},
		}, want: "Use v2 API instead."},
		{name: "json.RawMessage value", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: json.RawMessage(`"Removed in 3.0."`)},
		}, want: "Removed in 3.0."},
		{name: "invalid json.RawMessage", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: json.RawMessage(`{bad}`)},
		}, want: ""},
		{name: "unexpected type", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: true},
		}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, deprecationMessage(tt.op))
		})
	}
}

func TestExtensionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want string
	}{
		{name: "nil map", ext: nil, key: "x-foo", want: ""},
		{name: "missing key", ext: map[string]any{}, key: "x-foo", want: ""},
		{name: "string value", ext: map[string]any{"x-foo": "bar"}, key: "x-foo", want: "bar"},
		{name: "json.RawMessage", ext: map[string]any{"x-foo": json.RawMessage(`"baz"`)}, key: "x-foo", want: "baz"},
		{name: "invalid json.RawMessage", ext: map[string]any{"x-foo": json.RawMessage(`123`)}, key: "x-foo", want: ""},
		{name: "wrong type int", ext: map[string]any{"x-foo": 99}, key: "x-foo", want: ""},
		{name: "wrong type bool", ext: map[string]any{"x-foo": false}, key: "x-foo", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extensionString(tt.ext, tt.key))
		})
	}
}

func TestExtensionBool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want bool
	}{
		{name: "nil map", ext: nil, key: "x-flag", want: false},
		{name: "missing key", ext: map[string]any{}, key: "x-flag", want: false},
		{name: "bool true", ext: map[string]any{"x-flag": true}, key: "x-flag", want: true},
		{name: "bool false", ext: map[string]any{"x-flag": false}, key: "x-flag", want: false},
		{name: "json.RawMessage true", ext: map[string]any{"x-flag": json.RawMessage(`true`)}, key: "x-flag", want: true},
		{name: "json.RawMessage false", ext: map[string]any{"x-flag": json.RawMessage(`false`)}, key: "x-flag", want: false},
		{name: "invalid json.RawMessage", ext: map[string]any{"x-flag": json.RawMessage(`"yes"`)}, key: "x-flag", want: false},
		{name: "wrong type string", ext: map[string]any{"x-flag": "true"}, key: "x-flag", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extensionBool(tt.ext, tt.key))
		})
	}
}

func TestExtensionStringSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want []string
	}{
		{name: "nil map", ext: nil, key: "x-list", want: nil},
		{name: "missing key", ext: map[string]any{}, key: "x-list", want: nil},
		{name: "json.RawMessage slice", ext: map[string]any{
			"x-list": json.RawMessage(`["a","b","c"]`),
		}, key: "x-list", want: []string{"a", "b", "c"}},
		{name: "json.RawMessage empty", ext: map[string]any{
			"x-list": json.RawMessage(`[]`),
		}, key: "x-list", want: []string{}},
		{name: "json.RawMessage invalid", ext: map[string]any{
			"x-list": json.RawMessage(`not-json`),
		}, key: "x-list", want: nil},
		{name: "[]any with strings", ext: map[string]any{
			"x-list": []any{"foo", "bar"},
		}, key: "x-list", want: []string{"foo", "bar"}},
		{name: "[]any with mixed types", ext: map[string]any{
			"x-list": []any{"foo", 42, "bar"},
		}, key: "x-list", want: []string{"foo", "bar"}},
		{name: "wrong type", ext: map[string]any{
			"x-list": "not-a-slice",
		}, key: "x-list", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extensionStringSlice(tt.ext, tt.key)
			if tt.want == nil {
				require.Nil(t, got)
			} else {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestErrorResponseWrappers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *openapi3.Operation
		want []string
	}{
		{name: "nil operation", op: nil, want: nil},
		{name: "nil extensions", op: &openapi3.Operation{}, want: nil},
		{name: "missing key", op: &openapi3.Operation{
			Extensions: map[string]any{"x-other": "val"},
		}, want: nil},
		{
			name: "json.RawMessage internal refs",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: json.RawMessage(`[` +
						`{"$ref":"#/components/schemas/_common.errors___BulkItems"},` +
						`{"$ref":"#/components/schemas/_common.errors___WriteShards"}` +
						`]`),
				},
			},
			want: []string{"BulkItems", "WriteShards"},
		},
		{
			name: "[]any of map refs",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: []any{
						map[string]any{"$ref": "#/components/schemas/_common.errors___SearchShards"},
					},
				},
			},
			want: []string{"SearchShards"},
		},
		{
			name: "external ref source-form falls back to last path segment",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: json.RawMessage(`[{"$ref":"../schemas/_common.errors.yaml#/components/schemas/BulkItems"}]`),
				},
			},
			want: []string{"BulkItems"},
		},
		{
			name: "skips entries without $ref",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: []any{
						map[string]any{"description": "no ref"},
						map[string]any{"$ref": "#/components/schemas/_common.errors___NodeFailures"},
					},
				},
			},
			want: []string{"NodeFailures"},
		},
		{
			name: "spec casing resolves to the Go-cased wrapper constant",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: json.RawMessage(`[` +
						`{"$ref":"#/components/schemas/_common.errors___PitDeleteItems"},` +
						`{"$ref":"#/components/schemas/_common.errors___PitNodeFailures"},` +
						`{"$ref":"#/components/schemas/_common.errors___NotAWrapper"}` +
						`]`),
				},
			},
			want: []string{"PITDeleteItems", "PITNodeFailures", "NotAWrapper"},
		},
		{
			name: "empty array",
			op: &openapi3.Operation{
				Extensions: map[string]any{extErrorResponses: json.RawMessage(`[]`)},
			},
			want: nil,
		},
		{
			name: "malformed json.RawMessage",
			op: &openapi3.Operation{
				Extensions: map[string]any{extErrorResponses: json.RawMessage(`not-json`)},
			},
			want: nil,
		},
		{
			name: "unexpected scalar value",
			op: &openapi3.Operation{
				Extensions: map[string]any{extErrorResponses: 42},
			},
			want: nil,
		},
		{
			name: "[]any with non-map elements skipped",
			op: &openapi3.Operation{
				Extensions: map[string]any{
					extErrorResponses: []any{"not-a-map", 99},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := errorResponseWrappers(tt.op)
			if tt.want == nil {
				require.Nil(t, got)
			} else {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	const ref = "#/components/schemas/_common.errors___SearchContextMissing"
	wrapper := func(ext map[string]any) *openapi3.T {
		return &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
			"_common.errors___SearchContextMissing": {Value: &openapi3.Schema{Extensions: ext}},
		}}}
	}
	complete := wrapper(map[string]any{
		extErrorStatus:        json.RawMessage(`404`),
		extErrorRootCauseType: json.RawMessage(`"search_context_missing_exception"`),
	})
	withRef := &openapi3.Operation{Extensions: map[string]any{extErrorTypes: json.RawMessage(`[{"$ref":"` + ref + `"}]`)}}

	tests := []struct {
		name string
		op   *openapi3.Operation
		spec *openapi3.T
		want []ir.ErrorType
	}{
		{name: "nil operation", op: nil, spec: complete, want: nil},
		{name: "missing key", op: &openapi3.Operation{}, spec: complete, want: nil},
		{
			name: "reads status and root cause from the wrapper schema",
			op:   withRef,
			spec: complete,
			want: []ir.ErrorType{{Name: "SearchContextMissing", Status: 404, RootCauseType: "search_context_missing_exception"}},
		},
		{
			name: "loaded values that are not json.RawMessage",
			op:   &openapi3.Operation{Extensions: map[string]any{extErrorTypes: []any{map[string]any{"$ref": ref}}}},
			spec: wrapper(map[string]any{extErrorStatus: float64(404), extErrorRootCauseType: "search_context_missing_exception"}),
			want: []ir.ErrorType{{Name: "SearchContextMissing", Status: 404, RootCauseType: "search_context_missing_exception"}},
		},
		{name: "skips a wrapper missing from components", op: withRef, spec: &openapi3.T{Components: &openapi3.Components{}}, want: nil},
		{
			name: "skips a wrapper without a status",
			op:   withRef,
			spec: wrapper(map[string]any{extErrorRootCauseType: json.RawMessage(`"search_context_missing_exception"`)}),
			want: nil,
		},
		{
			name: "skips a wrapper without a root cause type",
			op:   withRef,
			spec: wrapper(map[string]any{extErrorStatus: json.RawMessage(`404`)}),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, errorTypes(tt.op, tt.spec))
		})
	}
}

func TestRefExtensionStringPrefersSibling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  *openapi3.SchemaRef
		want string
	}{
		{name: "nil ref", ref: nil, want: ""},
		{
			name: "sibling only",
			ref: &openapi3.SchemaRef{
				Extensions: map[string]any{extVersionAdded: "2.12"},
				Value:      &openapi3.Schema{},
			},
			want: "2.12",
		},
		{
			name: "resolved schema only",
			ref: &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Extensions: map[string]any{extVersionAdded: "2.10"},
				},
			},
			want: "2.10",
		},
		{
			name: "sibling wins over resolved schema",
			ref: &openapi3.SchemaRef{
				Extensions: map[string]any{extVersionAdded: "2.19"},
				Value: &openapi3.Schema{
					Extensions: map[string]any{extVersionAdded: "2.10"},
				},
			},
			want: "2.19",
		},
		{
			name: "sibling with nil value",
			ref: &openapi3.SchemaRef{
				Extensions: map[string]any{extVersionAdded: "3.0"},
			},
			want: "3.0",
		},
		{
			name: "neither carries the key",
			ref: &openapi3.SchemaRef{
				Extensions: map[string]any{"x-other": "val"},
				Value:      &openapi3.Schema{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, refExtensionString(tt.ref, extVersionAdded))
		})
	}
}

// TestRefExtensionStringReadsLoadedSiblings pins the loader behavior the helper
// exists to work around: kin-openapi overlays a $ref's standard siblings onto the
// resolved schema, so Value.Description sees them, but leaves x-* keys on the
// SchemaRef. Reading only Value.Extensions drops every version annotation the
// spec writes beside a $ref.
func TestRefExtensionStringReadsLoadedSiblings(t *testing.T) {
	t.Parallel()

	const doc = `
openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    Target:
      type: object
      description: target description
    Holder:
      type: object
      properties:
        phase_took:
          x-version-added: '2.12'
          $ref: '#/components/schemas/Target'
`

	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	require.NoError(t, err)

	prop := spec.Components.Schemas["Holder"].Value.Properties["phase_took"]
	require.NotNil(t, prop)

	// The sibling is unreachable from the resolved schema, which is the bug.
	require.Empty(t, extensionString(prop.Value.Extensions, extVersionAdded))
	require.Equal(t, "2.12", refExtensionString(prop, extVersionAdded))

	// The description is overlaid, which is why descriptions never regressed.
	require.Equal(t, "target description", prop.Value.Description)
}

func TestDropRedundantRefTypeConjuncts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		siblings  string // YAML lines written next to the $ref
		wantAllOf int
	}{
		{name: "type repeating the target's is dropped", siblings: "type: string", wantAllOf: 0},
		{name: "a real constraint is kept", siblings: "maxLength: 3", wantAllOf: 1},
		{name: "type plus a constraint is kept", siblings: "type: string\n          maxLength: 3", wantAllOf: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := `openapi: 3.1.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    Status:
      type: string
      enum: [green, red]
    Probe:
      type: object
      properties:
        status:
          description: The probe status.
          $ref: '#/components/schemas/Status'
          ` + tt.siblings + "\n"

			spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
			require.NoError(t, err)
			require.NoError(t, dropRedundantRefTypeConjuncts(spec))

			prop := spec.Components.Schemas["Probe"].Value.Properties["status"]
			require.Len(t, prop.Value.AllOf, tt.wantAllOf)
			require.Equal(t, []string{"string"}, prop.Value.Type.Slice())
			require.Equal(t, "The probe status.", prop.Value.Description)
		})
	}
}

// overrideDoc is a spec in the shape #1113 gives search responses: Result.hits
// composes Meta with an inline override, and Wrapped narrows Result.hits again.
const overrideDoc = `openapi: 3.1.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    Hit:
      type: object
      properties:
        _id: {type: string}
    Meta:
      type: object
      properties:
        total: {type: integer}
        hits:
          type: array
          items: {$ref: '#/components/schemas/Hit'}
      required: [hits]
    Result:
      type: object
      properties:
        hits:
          allOf:
            - $ref: '#/components/schemas/Meta'
            - type: object
              properties:
                hits:
                  type: array
                  items: {$ref: '#/components/schemas/Hit'}
    Wrapped:
      allOf:
        - $ref: '#/components/schemas/Result'
        - type: object
          properties:
            hits:
              description: The narrowed hits.
              type: object
              properties:
                hits:
                  type: array
                  items: {type: string}
`

func TestNormalizeAllOfOverrides(t *testing.T) {
	t.Parallel()

	spec, err := openapi3.NewLoader().LoadFromData([]byte(overrideDoc))
	require.NoError(t, err)
	require.NoError(t, normalizeAllOfOverrides(spec))

	// The override restating Meta.hits exactly is dropped, so Result.hits is Meta.
	resultHits := spec.Components.Schemas["Result"].Value.Properties["hits"].Value
	require.Empty(t, resultHits.AllOf[1].Value.Properties,
		"an override identical to the base declaration conveys nothing")

	// Wrapped.hits keeps the Meta fields it would otherwise shadow: the override
	// becomes the members of Result.hits plus the narrowing itself.
	wrappedHits := spec.Components.Schemas["Wrapped"].Value.AllOf[1].Value.Properties["hits"]
	require.Empty(t, wrappedHits.Ref)
	require.Equal(t, "The narrowed hits.", wrappedHits.Value.Description)
	members := wrappedHits.Value.AllOf
	require.Len(t, members, 3)
	require.Equal(t, "#/components/schemas/Meta", members[0].Ref)
	require.Equal(t, []string{"string"}, members[2].Value.Properties["hits"].Value.Items.Value.Type.Slice())

	// A second pass changes nothing.
	require.NoError(t, normalizeAllOfOverrides(spec))
	require.Len(t, spec.Components.Schemas["Wrapped"].Value.AllOf[1].Value.Properties["hits"].Value.AllOf, 3)
}

func TestNormalizeAllOfOverridesKeepsRealOverrides(t *testing.T) {
	t.Parallel()

	doc := `openapi: 3.1.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    Base:
      type: object
      properties:
        count: {type: integer}
        tags:
          type: array
          items: {type: string}
    Narrowed:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          properties:
            count: {type: integer, format: int32}
            tags:
              type: array
              items: {type: integer}
            extra:
              type: object
              properties:
                a: {type: string}
`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	require.NoError(t, err)
	require.NoError(t, normalizeAllOfOverrides(spec))

	props := spec.Components.Schemas["Narrowed"].Value.AllOf[1].Value.Properties
	require.Len(t, props, 3, "overrides that differ from the base are kept")
	require.Empty(t, props["tags"].Value.AllOf, "only an object override of an object property is merged")
	require.NotEmpty(t, props["extra"].Value.Properties, "a property the base does not declare is left alone")
	require.Empty(t, props["extra"].Value.AllOf)
}

// TestNormalizeAllOfOverridesFoldsErasedRestatements covers the JsonValue
// instantiations: a hit whose _source is substituted with {} restates Hit's
// erased _source, so hits holding it restate the base's hits, and an object
// override made only of such restatements adds nothing and is deleted.
func TestNormalizeAllOfOverridesFoldsErasedRestatements(t *testing.T) {
	t.Parallel()

	doc := `openapi: 3.1.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    TDoc:
      x-is-generic-type-parameter: true
    Hit:
      type: object
      properties:
        _id: {type: string}
        _source: {$ref: '#/components/schemas/TDoc'}
    Meta:
      type: object
      properties:
        hits:
          type: array
          items:
            allOf:
              - $ref: '#/components/schemas/Hit'
              - type: object
                properties:
                  _source: {$ref: '#/components/schemas/TDoc'}
    Result:
      type: object
      properties:
        hits: {$ref: '#/components/schemas/Meta'}
    ResultJsonValue:
      allOf:
        - $ref: '#/components/schemas/Result'
        - type: object
          properties:
            hits:
              type: object
              properties:
                hits:
                  type: array
                  items:
                    allOf:
                      - $ref: '#/components/schemas/Hit'
                      - type: object
                        properties:
                          _source: {}
`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	require.NoError(t, err)
	require.NoError(t, normalizeAllOfOverrides(spec))

	item := spec.Components.Schemas["Meta"].Value.Properties["hits"].Value.Items.Value
	require.Empty(t, item.AllOf[1].Value.Properties, "an erased _source restating Hit's is deleted")

	override := spec.Components.Schemas["ResultJsonValue"].Value.AllOf[1].Value
	require.NotContains(t, override.Properties, "hits", "a hits override that only restates Result.hits is deleted")
}
