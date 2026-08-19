// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"github.com/getkin/kin-openapi/openapi3"
)

// extDiscriminatorDefault names the branch a payload belongs to when the
// discriminator property is absent entirely. The OpenSearch spec carries it on
// the discriminator object (mapping Property declares `x-default: object`,
// because a bare `{"properties":{...}}` is an implicit object mapping). It is
// not part of standard OpenAPI, so kin-openapi surfaces it through the
// discriminator's Extensions map.
const extDiscriminatorDefault = "x-default"

// unionDiscriminator describes how a union's branch is read off the wire when
// the spec declares an OpenAPI `discriminator`. Its presence makes the union
// genuinely wire-discriminated: the generated UnmarshalJSON reads one property
// and decodes exactly one branch, so the union gets a real Type() and a
// discriminant const per branch instead of the undiscriminated lazy surface.
type unionDiscriminator struct {
	// PropertyName is the JSON property holding the branch name
	// (discriminator.propertyName, e.g. "type").
	PropertyName string

	// DefaultValue is the value assumed when PropertyName is absent from the
	// payload (the spec's x-default). Empty when the spec declares none, in
	// which case an absent property is an error rather than a silent guess.
	DefaultValue string
}

// discriminatorValues resolves each branch's discriminator value for a
// oneOf/anyOf schema that declares a `discriminator`. The returned map is keyed
// by the branch's position among non-null branches, matching the Ordinal
// resolveUnionType assigns.
//
// Resolution is all-or-nothing: it returns (nil, nil, false) unless EVERY
// branch resolves to a value and every value is unique. A partially resolvable
// discriminator is worse than none -- the generated decoder would have to guess
// for the unresolved branches -- so the caller falls back to its normal
// (undiscriminated) handling.
//
// A branch's value comes either from the spec's explicit discriminator.mapping
// or, failing that, from the const/enum constraint on the discriminator property
// itself. The latter usually sits on a base schema rather than the branch (the
// property is declared in the allOf override, but the branch reached through
// several levels of allOf), so resolution walks allOf transitively.
func discriminatorValues(schema *openapi3.Schema, branches openapi3.SchemaRefs) (*unionDiscriminator, map[int]string, bool) {
	if schema == nil || schema.Discriminator == nil || schema.Discriminator.PropertyName == "" {
		return nil, nil, false
	}
	prop := schema.Discriminator.PropertyName

	// Invert discriminator.mapping (value -> $ref) into $ref -> value so a
	// branch can be looked up by the ref it carries.
	byRef := make(map[string]string, len(schema.Discriminator.Mapping))
	for value, mapping := range schema.Discriminator.Mapping {
		if mapping.Ref != "" {
			byRef[mapping.Ref] = value
		}
	}

	values := map[int]string{}
	taken := map[string]bool{}
	idx := 0
	for _, branch := range branches {
		if branch == nil {
			continue
		}
		if branch.Value != nil && branch.Value.Type != nil && branch.Value.Type.Is(openapi3.TypeNull) {
			continue // null branches carry no discriminator; pointer semantics handle them
		}
		value, ok := byRef[branch.Ref]
		if !ok {
			value, ok = resolveDiscriminatorConst(branch, prop, make(set[string]))
		}
		if !ok || taken[value] {
			return nil, nil, false
		}
		taken[value] = true
		values[idx] = value
		idx++
	}
	if len(values) < 2 {
		return nil, nil, false
	}

	def := extensionString(schema.Discriminator.Extensions, extDiscriminatorDefault)
	if def != "" && !taken[def] {
		// The default names a value no branch claims, so falling back to it
		// would decode into nothing. Treat the discriminator as unusable rather
		// than emit a decoder with an unreachable default.
		return nil, nil, false
	}

	return &unionDiscriminator{PropertyName: prop, DefaultValue: def}, values, true
}

// resolveDiscriminatorConst finds the single constant value a schema pins its
// discriminator property to, following allOf members transitively. The spec
// spells the constraint as either a one-element `enum` (the OpenSearch style,
// e.g. `type: {type: string, enum: [keyword]}`) or a JSON Schema `const`.
//
// visited carries the $ref keys already on the current path, mirroring
// declaresProperty: a mutually recursive allOf pair would otherwise recurse
// until the stack overflows, since this walk has no other stopping rule. Inline
// members need no guard -- they are a finite tree in the parsed document and
// only a $ref can close a cycle.
//
// Returns ("", false) when the property is absent, is not pinned to a constant,
// or is pinned to more than one value -- in each case the branch is not
// identifiable from the property alone.
func resolveDiscriminatorConst(ref *openapi3.SchemaRef, prop string, visited set[string]) (string, bool) {
	if ref == nil {
		return "", false
	}
	if key := refToSchemaKey(ref.Ref); key != "" {
		if visited.has(key) {
			return "", false
		}
		visited.add(key)
		defer delete(visited, key)
	}

	// The loader resolves every $ref's Value, so the branch schema is reachable
	// without a components lookup.
	schema := ref.Value
	if schema == nil {
		return "", false
	}

	if p, ok := schema.Properties[prop]; ok && p != nil && p.Value != nil {
		if value, found := singleConstValue(p.Value); found {
			return value, true
		}
	}
	// The property may be declared on a base schema several allOf levels up, so
	// keep descending. The first member that pins it wins: a narrowing override
	// is always listed alongside (not beneath) the base it narrows, so there is
	// no deeper member that could contradict a shallower one.
	for _, sub := range schema.AllOf {
		if value, ok := resolveDiscriminatorConst(sub, prop, visited); ok {
			return value, true
		}
	}
	return "", false
}

// singleConstValue returns the lone string value a schema is pinned to, from
// either a one-element enum or a const. Multi-value enums are not constants and
// yield false: a branch whose discriminator accepts several values cannot be
// told apart from a sibling that accepts one of the same values.
func singleConstValue(schema *openapi3.Schema) (string, bool) {
	if schema == nil {
		return "", false
	}
	if c, ok := schema.Const.(string); ok {
		return c, true
	}
	if len(schema.Enum) == 1 {
		if s, ok := schema.Enum[0].(string); ok {
			return s, true
		}
	}
	return "", false
}
