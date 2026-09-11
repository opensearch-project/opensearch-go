// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"github.com/getkin/kin-openapi/openapi3"
)

// The spec models several responses as generic types: HitsMetadata[TDocument],
// GetResult[TDocument], MultiBucketAggregateBase[TBucket]. It instantiates them
// with an allOf whose first member $refs the generic base and whose second member
// redeclares one property to substitute the type argument.
//
// Go has no generics here, so the substituting property renders as a struct field
// alongside the embedded base, carrying the same JSON tag as the base's field.
// encoding/json resolves a duplicate tag at differing depths in favor of the
// shallower one, so the redeclaration always wins and the embedded base's field
// is never populated.
//
// That is correct when the substitution names a concrete schema:
// MultiBucketAggregateBaseAdjacencyMatrixBucket narrows buckets to
// AdjacencyMatrixBucket, which carries strictly more than the erased base, so
// having it win is the entire purpose of the override.
//
// It is wrong when the substitution is itself a type parameter or an empty
// schema. SearchResult.hits and GetResult._source narrow to TDocument, which
// erases to json.RawMessage: the redeclaration conveys nothing while displacing
// the base's whole envelope (_id, _seq_no, _primary_term, sort). Callers cannot
// recover the lost fields from the typed response at all.
//
// redundantOverrideTags identifies the second kind so callers can skip the
// property and let Go promote the base's declaration instead. The test is on the
// spec rather than the emitted Go, because both kinds emit the same duplicate
// tag; for unions they differ only in a branch's payload type.

// conveysNoConcreteType reports whether prop substitutes nothing a Go type can
// express: a generic type parameter, an empty schema, or a container of those.
//
// It stops at any $ref naming a concrete schema without descending into that
// schema's own fields. A concrete type is concrete regardless of what its fields
// eventually reference, and descending would follow the aggregation graph into
// cycles and reach an erased leaf from almost anywhere.
//
// visited carries the $ref keys already on the current path. Inline subschemas
// form a finite tree, so only a $ref can revisit a schema; recording the ones in
// flight makes the traversal terminate on a cyclic spec. A repeat resolves as
// concrete, which keeps the field, matching the treatment of a $ref that cannot
// be resolved at all.
func (w *walker) conveysNoConcreteType(prop *openapi3.SchemaRef, visited set[string]) bool {
	if prop == nil {
		return false
	}

	if prop.Ref != "" {
		key := refToSchemaKey(prop.Ref)
		target, ok := w.spec.Components.Schemas[key]
		if !ok || target.Value == nil {
			// A $ref we cannot resolve names some type we cannot inspect.
			// Treat it as concrete so the field is preserved.
			return false
		}
		if extensionBool(target.Value.Extensions, extGenericTypeParam) {
			return true
		}
		if namesConcreteSchema(target.Value) {
			return false
		}
		if visited.has(key) {
			return false
		}
		visited.add(key)
		defer delete(visited, key)
		return w.conveysNoConcreteType(target, visited)
	}

	s := prop.Value
	if s == nil {
		return false
	}

	// Composition conveys nothing only when every branch conveys nothing: one
	// concrete branch is enough to make the override a real narrowing.
	for _, group := range [][]*openapi3.SchemaRef{s.AllOf, s.OneOf, s.AnyOf} {
		if len(group) == 0 {
			continue
		}
		for _, sub := range group {
			if !w.conveysNoConcreteType(sub, visited) {
				return false
			}
		}
		return true
	}

	// Containers delegate to their element type.
	if s.Items != nil {
		return w.conveysNoConcreteType(s.Items, visited)
	}
	if s.AdditionalProperties.Schema != nil {
		return w.conveysNoConcreteType(s.AdditionalProperties.Schema, visited)
	}
	if len(s.Properties) > 0 {
		for _, p := range s.Properties {
			if !w.conveysNoConcreteType(p, visited) {
				return false
			}
		}
		return true
	}
	if len(s.Enum) > 0 {
		return false
	}

	// Nothing left to inspect. A bare `{}` or `type: object` is the spec's
	// spelling for an erased type argument; any other declared type is real.
	return s.Type == nil || s.Type.Includes(openapi3.TypeObject)
}

// namesConcreteSchema reports whether s declares anything a Go type can carry.
// Used to stop the descent at named schemas.
func namesConcreteSchema(s *openapi3.Schema) bool {
	if extensionBool(s.Extensions, extGenericTypeParam) {
		return false
	}
	if len(s.Properties) > 0 || len(s.AllOf) > 0 || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return true
	}
	if len(s.Enum) > 0 || s.Items != nil || s.AdditionalProperties.Schema != nil {
		return true
	}
	return s.Type != nil && !s.Type.Includes(openapi3.TypeObject)
}

// declaresProperty reports whether the allOf chain rooted at ref declares
// jsonName. Only $ref members and inline properties are followed, mirroring how
// resolveAllOf and collectFields build the embedded field set.
//
// visited carries the $ref keys already on the current path. Without it a
// mutually recursive allOf pair overflows the stack, since this walk has no
// namesConcreteSchema-style stopping rule to halt the descent.
func (w *walker) declaresProperty(ref *openapi3.SchemaRef, jsonName string, visited set[string]) bool {
	if ref == nil {
		return false
	}

	s := ref.Value
	if ref.Ref != "" {
		key := refToSchemaKey(ref.Ref)
		target, ok := w.spec.Components.Schemas[key]
		if !ok {
			return false
		}
		if visited.has(key) {
			return false
		}
		visited.add(key)
		defer delete(visited, key)
		s = target.Value
	}
	if s == nil {
		return false
	}

	if _, ok := s.Properties[jsonName]; ok {
		return true
	}
	for _, sub := range s.AllOf {
		if w.declaresProperty(sub, jsonName, visited) {
			return true
		}
	}
	return false
}

// narrowedUnionMember reports whether an allOf is a narrowed union rather than a
// struct merge, returning the member that carries the narrowing.
//
// The spec instantiates a generic union by allOf-ing the erased base with a
// oneOf/anyOf that restates the same branches over the concrete type argument:
//
//	buckets:
//	  allOf:
//	    - $ref: Buckets                     # oneOf[keyed, array] over TBucket
//	    - oneOf:
//	        - {type: object, additionalProperties: {$ref: AdjacencyMatrixBucket}}
//	        - {type: array,  items: {$ref: AdjacencyMatrixBucket}}
//
// Merging that as a struct discards the narrowing entirely: a oneOf member
// contributes no properties, so the result degrades to the erased base and the
// concrete bucket type is never emitted. The narrowing member IS the type; the
// $ref member only names what it narrows.
//
// Applies only when no member contributes properties -- with properties present
// the allOf is a genuine composition and the struct merge is correct.
func narrowedUnionMember(schema *openapi3.Schema) (*openapi3.Schema, bool) {
	var narrowed *openapi3.Schema
	for _, member := range schema.AllOf {
		if member == nil || member.Value == nil {
			continue
		}
		// A $ref member names the base being narrowed. The loader resolves it, so
		// its Value carries the base's own branches -- skip it by Ref, not by a
		// nil Value, or the base's oneOf is mistaken for a second narrowing.
		if member.Ref != "" {
			continue
		}
		s := member.Value
		if len(s.Properties) > 0 {
			return nil, false
		}
		if len(s.OneOf) == 0 && len(s.AnyOf) == 0 {
			continue
		}
		if narrowed != nil {
			// Two inline narrowing members: not a single union to resolve.
			return nil, false
		}
		narrowed = s
	}
	return narrowed, narrowed != nil
}

// collapsesToBase reports whether schema describes the same shape as its allOf
// base, so callers can resolve to the base instead of emitting a wrapper.
//
// That holds when the allOf has exactly one $ref member and every other member
// contributes nothing: no properties beyond redundant overrides, no type or value
// constraints of its own. Three spec shapes reach it:
//
//   - a generic instantiation that substitutes nothing a Go type can carry
//     (GetResult[TDocument] over GetResultBase, HitsMetadataJsonValue over
//     HitsMetadata);
//   - a re-export with an empty override,
//     `allOf: [$ref, {type: object, properties: {}}]`;
//   - a bare rename, `allOf: [$ref]` with no second member at all, which the
//     spec uses to give a generic instantiation a friendly name
//     (AdjacencyMatrixAggregate over MultiBucketAggregateBaseAdjacencyMatrixBucket).
//
// Emitting a distinct Go type for any of them produces a struct whose only
// content is the embedded base: a separate nominal type that conveys nothing and
// is not assignable to the base.
//
// Returns the base's schema key and true when the collapse applies.
func (w *walker) collapsesToBase(schema *openapi3.Schema) (string, bool) {
	if len(schema.AllOf) == 0 || len(schema.Properties) > 0 {
		return "", false
	}

	var baseRef string
	for _, member := range schema.AllOf {
		if member == nil {
			return "", false
		}
		if member.Ref != "" {
			if baseRef != "" {
				// Two bases: a real composition of distinct schemas.
				return "", false
			}
			baseRef = refToSchemaKey(member.Ref)
			continue
		}
		if member.Value == nil {
			continue
		}
		if !onlyRedundantProperties(member.Value, w.redundantOverrideTags(schema)) {
			return "", false
		}
	}
	if baseRef == "" {
		return "", false
	}
	return baseRef, true
}

// onlyRedundantProperties reports whether an inline allOf member contributes
// nothing beyond properties already known to be redundant overrides. A member that
// declares a non-object type, composes further schemas, constrains values, or adds
// required fields is contributing something real even when its properties are all
// redundant.
func onlyRedundantProperties(s *openapi3.Schema, redundant map[string]bool) bool {
	if len(s.AllOf) > 0 || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return false
	}
	if s.Items != nil || s.AdditionalProperties.Schema != nil || len(s.Enum) > 0 {
		return false
	}
	if s.Type != nil && !s.Type.Includes(openapi3.TypeObject) {
		return false
	}
	if len(s.Required) > 0 {
		return false
	}
	for name := range s.Properties {
		if !redundant[name] {
			return false
		}
	}
	return true
}

// resolveCollapsedBase resolves schema to its allOf base when the two describe the
// same shape, aliasing schemaKey to the base's type so later lookups by either ref
// find it. Returns the base's Go type name and true on success.
//
// Response bodies are exempt. An operation's Resp is built by inlining the fields
// of whatever type its ref resolves to, so collapsing a response schema would
// inline the base into the Resp and stop emitting the base as a standalone type --
// breaking any other schema that names it (e.g. mget's union branch on GetResult,
// whose base GetResultBase is also referenced directly).
func (w *walker) resolveCollapsedBase(schema *openapi3.Schema, schemaKey, group string, isRespBody bool) (string, bool) {
	if schema == nil || isRespBody {
		return "", false
	}
	baseKey, ok := w.collapsesToBase(schema)
	if !ok || w.inFlight.has(baseKey) {
		return "", false
	}

	target, found := w.spec.Components.Schemas[baseKey]
	if !found || target.Value == nil {
		return "", false
	}

	// Resolve the base under its OWN key so it is registered and named as itself.
	resolved := w.resolveNamedSchema(baseKey, target.Value, group, isRespBody)
	if resolved == goTypeRawMessage {
		return "", false
	}

	// The emitted type keeps the base's name even when that name carries the
	// spec's "Base" suffix. Renaming to the collapsed schema's name looks
	// tempting -- GetResultBase reads as internal next to GetResult -- but both
	// names are referenced independently in the spec (MemoryStatsBase twice
	// against MemoryStats once), so promoting either one orphans real references
	// to the other. The suffix is the spec's, not this generator's.
	if base, regd := w.registry.lookup(baseKey); regd {
		// Point this schema's ref at the base's type so every lookup by the
		// collapsed ref still resolves to a registered type.
		w.registry.aliasRef(schemaKey, base)
	}
	return resolved, true
}

// redundantOverrideTags returns the JSON names of properties that an allOf member
// redeclares without narrowing: the property conveys no concrete type and another
// member's chain already declares it. Callers skip these so the base declaration
// is reached by Go field promotion.
func (w *walker) redundantOverrideTags(schema *openapi3.Schema) map[string]bool {
	if len(schema.AllOf) < 2 {
		return nil
	}

	var redundant map[string]bool
	for _, member := range schema.AllOf {
		if member == nil || member.Value == nil || len(member.Value.Properties) == 0 {
			continue
		}
		for name, prop := range member.Value.Properties {
			if !w.conveysNoConcreteType(prop, make(set[string])) {
				continue
			}
			// Only a redeclaration is redundant. The same property on a member
			// that introduces it is the field's only declaration; dropping it
			// would remove the field entirely.
			//
			// Each sibling gets a fresh visited set: the probes are independent
			// paths, and sharing one would let a schema reached through member A
			// suppress the answer for member B.
			shadowsBase := false
			for _, other := range schema.AllOf {
				if other == member {
					continue
				}
				if w.declaresProperty(other, name, make(set[string])) {
					shadowsBase = true
					break
				}
			}
			if !shadowsBase {
				continue
			}
			if redundant == nil {
				redundant = make(map[string]bool)
			}
			redundant[name] = true
		}
	}
	return redundant
}
