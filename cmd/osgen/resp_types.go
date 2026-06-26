// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"io"
	"sort"
)

// goField represents a single struct field in a generated Go type.
type goField struct {
	GoName            string // exported field name (e.g. "ClusterName")
	JSONName          string // wire name for json tag (e.g. "cluster_name")
	GoType            string // Go type expression (e.g. "int", "*string", "[]ShardInfo")
	IsPointer         bool   // field uses pointer semantics (optional/version-gated)
	IsEmbed           bool   // field is an embedded type (no field name or json tag)
	OmitEmpty         bool   // json tag includes omitempty
	Comment           string // short doc comment (usually from spec description)
	VersionAdded      string // semver when this field was introduced
	VersionDeprecated string // semver when this field was deprecated
	DeprecationMsg    string // explains what to use instead
}

// unionBranch represents one branch of a oneOf/anyOf discriminated union.
type unionBranch struct {
	Name         string   // Go accessor/const suffix (e.g. "TotalHits", "Int64")
	GoType       string   // Go type of the branch value (e.g. "SearchTotalHits", "int64")
	TokenClass   string   // "object", "array", "number", "string", "bool" for byte-prefix dispatch
	Required     []string // required fields for object validation (try-each heuristic)
	IsRef        bool     // branch came from a $ref (needs registry walk)
	VersionAdded string   // x-version-added from the spec (for try-each ordering)
}

// goType represents a generated Go struct type or discriminated union.
type goType struct {
	Name       string        // Go type name (e.g. "ClusterHealthResp", "ShardStatistics")
	Pkg        string        // full import path (see ir.DefaultCoreImportPath)
	SchemaRef  string        // original spec schema key (e.g. "_common___ShardStatistics")
	Fields     []goField     // struct fields in declaration order
	IsResp     bool          // true for top-level response body types (gets Inspect method)
	IsShared   bool          // true for types emitted to types_gen.go (shared across operations)
	IsUnion    bool          // true for discriminated union types (oneOf/anyOf)
	IsLazy     bool          // lazy-decode union (stores raw, decodes on accessor call)
	IsEnum     bool          // true for int-backed iota enum types (x-enum-name marker)
	Branches   []unionBranch // union branches (only populated when IsUnion)
	EnumValues []string      // allowed wire values (only populated when IsEnum)
	Comment    string        // type doc comment
}

// typeRegistry tracks generated types, deduplicates by schema ref, and
// provides stable iteration order.
type typeRegistry struct {
	byRef  map[string]*goType // schema ref -> type (for dedup)
	byName map[string]*goType // Go name -> type (for collision detection)
	order  []string           // insertion-order schema refs

	// collisions records cases where two distinct schema refs derived the same
	// Go type name. The second registrant is dropped (see register), which
	// silently degrades the dropped type's output (e.g. a response struct
	// falling back to raw json.RawMessage). Surfaced by checkCollisions as a
	// stderr warning at generation time so the naming defect is fixed at the
	// source rather than hidden. Deduplicated by dropped ref (see register).
	collisions []nameCollision

	// CorePkg is the Go package name for the core API output (e.g. "opensearchapi").
	CorePkg string
	// CoreImport is the full import path for CorePkg.
	CoreImport string
}

// nameCollision records two schema refs that mapped to the same Go type name.
type nameCollision struct {
	Name       string // the Go type name both refs produced
	KeptRef    string // schema ref already holding the name
	DroppedRef string // schema ref that was dropped because of the clash
}

func newTypeRegistry(corePkg string) *typeRegistry {
	coreImport := opensearchAPIImport
	if corePkg != opensearchAPIPkgName {
		coreImport = modulePath + "/" + corePkg
	}
	return &typeRegistry{
		byRef:      make(map[string]*goType),
		byName:     make(map[string]*goType),
		CorePkg:    corePkg,
		CoreImport: coreImport,
	}
}

// register adds a type to the registry. If the schema ref is already
// registered, it returns the existing type and false. If the Go name collides
// with a different schema ref, the new type is dropped, the collision is
// recorded once per dropped ref for later reporting, and (nil, false) is
// returned.
func (r *typeRegistry) register(t *goType) (*goType, bool) {
	if existing, ok := r.byRef[t.SchemaRef]; ok {
		return existing, false
	}
	if existing, ok := r.byName[t.Name]; ok {
		r.recordCollision(nameCollision{
			Name:       t.Name,
			KeptRef:    existing.SchemaRef,
			DroppedRef: t.SchemaRef,
		})
		return nil, false
	}
	r.byRef[t.SchemaRef] = t
	r.byName[t.Name] = t
	r.order = append(r.order, t.SchemaRef)
	return t, true
}

// recordCollision appends c unless a collision for the same dropped ref is
// already recorded. A dropped ref can be re-attempted from multiple parent
// fields during the walk; deduping keeps the reported count equal to the number
// of distinct types actually lost.
func (r *typeRegistry) recordCollision(c nameCollision) {
	for _, existing := range r.collisions {
		if existing.DroppedRef == c.DroppedRef {
			return
		}
	}
	r.collisions = append(r.collisions, c)
}

// lookup returns a previously registered type by its schema ref.
func (r *typeRegistry) lookup(ref string) (*goType, bool) {
	t, ok := r.byRef[ref]
	return t, ok
}

// lookupByName returns a previously registered type by its Go name.
func (r *typeRegistry) lookupByName(name string) (*goType, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// checkCollisions reports cases where two distinct schema refs derived the same
// Go type name during registration. Such a collision silently drops the second
// type and degrades its output (e.g. a response struct downgraded to raw
// json.RawMessage). It writes a prominent warning to w for each collision and
// returns the number found (one per distinct dropped ref; see recordCollision),
// so the defect is visible at generation time rather than hidden. The fix
// belongs at the naming source (schemaTypeName or the key passed to register),
// not here.
//
// This is intentionally non-fatal: the spec currently contains several
// long-standing collisions in unrelated groups, and aborting generation would
// block all output until every one is resolved. Surfacing them loudly lets them
// be fixed incrementally.
func (r *typeRegistry) checkCollisions(w io.Writer) int {
	if len(r.collisions) == 0 {
		return 0
	}
	fmt.Fprintf(w, "WARNING: osgen detected %d Go type name collision(s) during type registration.\n", len(r.collisions))
	fmt.Fprintln(w, "Each collision silently drops a type and degrades its generated output")
	fmt.Fprintln(w, "(e.g. a typed response struct downgraded to raw json.RawMessage).")
	for _, c := range r.collisions {
		fmt.Fprintf(w, "  - name %q: kept schema %q, dropped schema %q\n", c.Name, c.KeptRef, c.DroppedRef)
	}
	fmt.Fprintln(w, "Fix the name derivation so the colliding refs map to distinct Go names.")
	return len(r.collisions)
}

// reportCollisions writes the registry's collision report to w and, if any were
// found, a trailing note that generation continued anyway. It is the non-fatal
// wrapper used by generateAPI: collisions are surfaced loudly but do not abort
// generation, since the spec has long-standing collisions in unrelated groups
// (see checkCollisions).
func reportCollisions(w io.Writer, r *typeRegistry) {
	if n := r.checkCollisions(w); n > 0 {
		fmt.Fprintf(w, "osgen: continuing despite %d type name collision(s); generated output for the dropped types is degraded\n", n)
	}
}

// all returns all registered types in insertion order.
func (r *typeRegistry) all() []*goType {
	result := make([]*goType, 0, len(r.order))
	for _, ref := range r.order {
		result = append(result, r.byRef[ref])
	}
	return result
}

// shared returns all shared types sorted by name for stable output.
func (r *typeRegistry) shared() []*goType {
	var result []*goType
	for _, ref := range r.order {
		t := r.byRef[ref]
		if t.IsShared {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// forOperation returns types associated with a specific operation (not shared).
func (r *typeRegistry) forOperation(group string) []*goType {
	var result []*goType
	for _, ref := range r.order {
		t := r.byRef[ref]
		if !t.IsShared && schemaGroup(t.SchemaRef) == group {
			result = append(result, t)
		}
	}
	return result
}

// reachableFrom returns all non-shared, non-Resp types transitively reachable
// from the given schema ref by following field type references and embeds.
func (r *typeRegistry) reachableFrom(startRef string) []*goType {
	visited := make(map[string]bool)
	var result []*goType

	var walk func(ref string)
	walk = func(ref string) {
		t, ok := r.byRef[ref]
		if !ok {
			return
		}
		for _, f := range t.Fields {
			typeName := unwrapTypeName(f.GoType)
			child, ok := r.byName[typeName]
			if !ok || visited[child.SchemaRef] {
				continue
			}
			visited[child.SchemaRef] = true
			if !child.IsResp && !child.IsShared {
				result = append(result, child)
			}
			walk(child.SchemaRef)
		}
		for _, b := range t.Branches {
			typeName := unwrapTypeName(b.GoType)
			child, ok := r.byName[typeName]
			if !ok || visited[child.SchemaRef] {
				continue
			}
			visited[child.SchemaRef] = true
			if !child.IsResp && !child.IsShared {
				result = append(result, child)
			}
			walk(child.SchemaRef)
		}
	}

	walk(startRef)
	return result
}

// promoteSharedDeps walks all shared types and promotes any non-shared type
// they reference to shared. This ensures types used by shared types (e.g.
// SearchFieldCollapse used by InsightsSource) are emitted to types_gen.go.
func (r *typeRegistry) promoteSharedDeps() {
	changed := true
	for changed {
		changed = false
		for _, ref := range r.order {
			t := r.byRef[ref]
			if !t.IsShared {
				continue
			}
			for _, f := range t.Fields {
				typeName := unwrapTypeName(f.GoType)
				child, ok := r.byName[typeName]
				if !ok || child.IsShared || child.IsResp {
					continue
				}
				child.IsShared = true
				child.Pkg = t.Pkg
				changed = true
			}
			for _, b := range t.Branches {
				typeName := unwrapTypeName(b.GoType)
				child, ok := r.byName[typeName]
				if !ok || child.IsShared || child.IsResp {
					continue
				}
				child.IsShared = true
				child.Pkg = t.Pkg
				changed = true
			}
		}
	}
}

// schemaGroup extracts the group portion from a schema ref key.
// e.g. "cluster.health___IndexHealthStats" -> "cluster.health"
func schemaGroup(ref string) string {
	if idx := indexTripleUnderscore(ref); idx >= 0 {
		return ref[:idx]
	}
	return ""
}

func indexTripleUnderscore(s string) int {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '_' && s[i+1] == '_' && s[i+2] == '_' {
			return i
		}
	}
	return -1
}
