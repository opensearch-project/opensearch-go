// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// An allOf that adds nothing to its base collapses onto that base
// (see walker.collapsesToBase), which leaves two spec names describing one Go
// type. The walk keeps the base's name because it is the one already registered,
// but the collapsed schema usually carries the friendlier name: the spec writes
// `AdjacencyMatrixAggregate: {allOf: [$ref MultiBucketAggregateBaseAdjacencyMatrixBucket]}`
// precisely to give the mangled generic instantiation a readable alias. Keeping
// the base's name discards it, so `AsAdjacencyMatrix()` returns
// CommonAggregationsMultiBucketAggregateBaseAdjacencyMatrixBucket.
//
// The rename cannot happen during the walk. Type references are plain Go type
// strings on already-emitted fields and branches, so renaming a type mid-walk
// leaves earlier references pointing at a name that no longer exists -- and the
// spec chains these collapses (RangeAggregate -> RangeAggregateBase ->
// MultiBucketAggregateBaseRangeBucket), so siblings resolve through the old name
// before the rename would fire. renameCollapsedAliases therefore runs after every
// walk pass and rewrites the registry and every reference together.

// renameCollapsedAliases renames each collapsed type to its alias's name and
// rewrites all references, for targets reachable from exactly one alias.
//
// A target with several aliases has no single better name -- eight schemas from
// AvgAggregate to WeightedAvgAggregate all collapse onto
// SingleMetricAggregateBase -- so those keep the base's name.
func renameCollapsedAliases(spec *openapi3.T, registry *typeRegistry) {
	if spec == nil || spec.Components == nil || registry == nil {
		return
	}

	renames := plannedRenames(spec, registry)
	if len(renames) == 0 {
		return
	}
	for _, r := range renames {
		registry.rename(r.typ, r.newName)
	}
	registry.rewriteTypeRefs(renames)
}

// typeRename pairs a registered type with the name it should take.
type typeRename struct {
	typ     *goType
	oldName string
	newName string
}

// plannedRenames resolves the collapse graph and returns the renames to apply,
// sorted by old name so generation stays deterministic.
//
// A rename is planned only when the target is reachable from exactly one alias,
// the alias derives a Go name that is currently unused, and both the target and
// the alias resolved to the same registered type (which is what aliasRef
// records -- if they differ, the collapse did not happen and the names are
// independent).
func plannedRenames(spec *openapi3.T, registry *typeRegistry) []typeRename {
	aliases := collapseAliases(spec)

	byTarget := make(map[string][]string, len(aliases))
	for alias, target := range aliases {
		byTarget[target] = append(byTarget[target], alias)
	}

	var renames []typeRename
	taken := make(set[string])
	for target, group := range byTarget {
		if len(group) != 1 {
			// Several aliases collapse here; no single name is the better one.
			continue
		}
		alias := group[0]

		typ, ok := registry.lookup(target)
		if !ok {
			continue
		}
		// The alias must have collapsed onto this very type, not merely share a
		// prefix: aliasRef points both refs at one *goType.
		if aliasType, ok := registry.lookup(alias); !ok || aliasType != typ {
			continue
		}

		// The alias's name only wins when the spec does not lean on the target's
		// name more heavily. SearchResponse aliases SearchResult, but SearchResult
		// has 9 $ref sites against SearchResponse's 3 -- promoting the alias would
		// retire the name the spec (and callers) actually use.
		if refCount(spec, target, alias) > refCount(spec, alias, target) {
			continue
		}

		newName := schemaTypeName(alias, typ.IsResp)
		if newName == "" || newName == typ.Name || taken.has(newName) {
			continue
		}
		if _, clash := registry.lookupByName(newName); clash {
			continue
		}
		taken.add(newName)
		renames = append(renames, typeRename{typ: typ, oldName: typ.Name, newName: newName})

		// Types keyed under the collapsed schema (a nested union or object, keyed
		// "<parentKey>.<field>") derived their names from the old prefix while the
		// walk was running. Carry them along so the whole family reads from the
		// alias rather than half from each name.
		renames = append(renames, descendantRenames(registry, target, typ.Name, newName, taken)...)
	}

	sort.Slice(renames, func(i, j int) bool { return renames[i].oldName < renames[j].oldName })
	return renames
}

// refCount counts $ref sites for key across schemas, request bodies, and
// responses -- everywhere a name can be depended upon.
//
// skip names one schema to ignore, always the other half of the collapse pair: the
// alias's $ref to its target IS the collapse, so counting it would make every
// target look busier than its alias and block every rename.
func refCount(spec *openapi3.T, key, skip string) int {
	n := 0
	for name, sch := range spec.Components.Schemas {
		if name == key || name == skip {
			continue
		}
		n += countRefsTo(sch, key, 0)
	}
	for _, body := range spec.Components.RequestBodies {
		if body == nil || body.Value == nil {
			continue
		}
		for _, media := range body.Value.Content {
			if media != nil {
				n += countRefsTo(media.Schema, key, 0)
			}
		}
	}
	for _, resp := range spec.Components.Responses {
		if resp == nil || resp.Value == nil {
			continue
		}
		for _, media := range resp.Value.Content {
			if media != nil {
				n += countRefsTo(media.Schema, key, 0)
			}
		}
	}
	return n
}

// countRefsTo counts $ref occurrences of key within ref, descending through
// composition, containers, and properties but never through a $ref itself.
func countRefsTo(ref *openapi3.SchemaRef, key string, depth int) int {
	if ref == nil || depth > maxOverrideDepth {
		return 0
	}
	if ref.Ref != "" {
		if refToSchemaKey(ref.Ref) == key {
			return 1
		}
		return 0
	}
	s := ref.Value
	if s == nil {
		return 0
	}
	n := 0
	for _, group := range [][]*openapi3.SchemaRef{s.AllOf, s.OneOf, s.AnyOf} {
		for _, sub := range group {
			n += countRefsTo(sub, key, depth+1)
		}
	}
	n += countRefsTo(s.Items, key, depth+1)
	n += countRefsTo(s.AdditionalProperties.Schema, key, depth+1)
	for _, p := range s.Properties {
		n += countRefsTo(p, key, depth+1)
	}
	return n
}

// descendantRenames renames types the walk keyed beneath the collapsed schema.
//
// A nested union or object is registered under "<parentKey>.<field>", and its Go
// name was derived from the parent's old name while the walk ran. Matching on the
// schema KEY rather than the name prefix is essential: SearchResultJSONValue is a
// separate spec schema that merely shares a prefix with SearchResult, and renaming
// it as a descendant would drag an unrelated family along.
func descendantRenames(registry *typeRegistry, targetRef, oldName, newName string, taken set[string]) []typeRename {
	keyPrefix := targetRef + "."
	var out []typeRename
	for _, t := range registry.all() {
		if t.SchemaRef == targetRef || !strings.HasPrefix(t.SchemaRef, keyPrefix) {
			continue
		}
		if !strings.HasPrefix(t.Name, oldName) || t.Name == oldName {
			continue
		}
		candidate := newName + strings.TrimPrefix(t.Name, oldName)
		if taken.has(candidate) {
			continue
		}
		if _, clash := registry.lookupByName(candidate); clash {
			continue
		}
		taken.add(candidate)
		out = append(out, typeRename{typ: t, oldName: t.Name, newName: candidate})
	}
	return out
}

// collapseAliases maps each schema key that collapses onto another to the
// terminal target of its chain. The spec chains these (RangeAggregate ->
// RangeAggregateBase -> MultiBucketAggregateBaseRangeBucket), so every alias in a
// chain resolves to the same end, which is what makes the one-alias test
// meaningful.
func collapseAliases(spec *openapi3.T) map[string]string {
	direct := make(map[string]string)
	for key, sch := range spec.Components.Schemas {
		if sch == nil || sch.Value == nil {
			continue
		}
		if target, ok := collapseTargetOf(sch.Value); ok {
			direct[key] = target
		}
	}

	terminal := make(map[string]string, len(direct))
	for alias := range direct {
		key := alias
		seen := set[string]{alias: {}}
		for {
			next, ok := direct[key]
			if !ok || seen.has(next) {
				break
			}
			seen.add(next)
			key = next
		}
		if key != alias {
			terminal[alias] = key
		}
	}
	return terminal
}

// collapseTargetOf returns the schema key an allOf collapses onto, mirroring
// walker.collapsesToBase without needing a walker: exactly one $ref member and no
// member contributing properties or constraints of its own.
func collapseTargetOf(schema *openapi3.Schema) (string, bool) {
	if len(schema.AllOf) == 0 || len(schema.Properties) > 0 {
		return "", false
	}
	var target string
	for _, member := range schema.AllOf {
		if member == nil {
			return "", false
		}
		if member.Ref != "" {
			if target != "" {
				return "", false
			}
			target = refToSchemaKey(member.Ref)
			continue
		}
		if member.Value == nil {
			continue
		}
		if !contributesNothing(member.Value) {
			return "", false
		}
	}
	if target == "" {
		return "", false
	}
	return target, true
}

// contributesNothing reports whether an inline allOf member adds no properties,
// composition, or value constraints.
func contributesNothing(s *openapi3.Schema) bool {
	if len(s.Properties) > 0 || len(s.AllOf) > 0 || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return false
	}
	if s.Items != nil || s.AdditionalProperties.Schema != nil || len(s.Enum) > 0 || len(s.Required) > 0 {
		return false
	}
	return s.Type == nil || s.Type.Includes(openapi3.TypeObject)
}

// rewriteTypeRefs rewrites every field and branch type expression that names a
// renamed type. Type references are plain strings, so this is the counterpart to
// renaming the registry entry: without it the emitted code references names that
// no longer exist.
func (r *typeRegistry) rewriteTypeRefs(renames []typeRename) {
	if len(renames) == 0 {
		return
	}
	repl := make(map[string]string, len(renames))
	for _, rn := range renames {
		repl[rn.oldName] = rn.newName
	}

	// A type expression is wrappers plus a base name ("[]Foo", "*pkg.Foo",
	// "map[string]Foo"). unwrapTypeName gives the base; the prefix is whatever
	// precedes it, so splicing on the base's last occurrence preserves wrappers
	// and any package qualifier.
	rewrite := func(goType string) string {
		base := unwrapTypeName(goType)
		if base == "" {
			return goType
		}
		to, ok := repl[base]
		if !ok {
			return goType
		}
		i := strings.LastIndex(goType, base)
		if i < 0 {
			return goType
		}
		return goType[:i] + to + goType[i+len(base):]
	}

	for _, t := range r.byRef {
		for i := range t.Fields {
			t.Fields[i].GoType = rewrite(t.Fields[i].GoType)
		}
		for i := range t.Branches {
			t.Branches[i].GoType = rewrite(t.Branches[i].GoType)
		}
	}
}
