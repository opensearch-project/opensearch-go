// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import "sort"

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
	Name      string        // Go type name (e.g. "ClusterHealthResp", "ShardStatistics")
	Pkg       string        // full import path (see ir.DefaultCoreImportPath)
	SchemaRef string        // original spec schema key (e.g. "_common___ShardStatistics")
	Fields    []goField     // struct fields in declaration order
	IsResp    bool          // true for top-level response body types (gets Inspect method)
	IsShared  bool          // true for types emitted to types_gen.go (shared across operations)
	IsUnion   bool          // true for discriminated union types (oneOf/anyOf)
	IsLazy    bool          // lazy-decode union (stores raw, decodes on accessor call)
	Branches  []unionBranch // union branches (only populated when IsUnion)
	Comment   string        // type doc comment
}

// typeRegistry tracks generated types, deduplicates by schema ref, and
// provides stable iteration order.
type typeRegistry struct {
	byRef  map[string]*goType // schema ref -> type (for dedup)
	byName map[string]*goType // Go name -> type (for collision detection)
	order  []string           // insertion-order schema refs

	// CorePkg is the Go package name for the core API output (e.g. "opensearchapi").
	CorePkg string
	// CoreImport is the full import path for CorePkg.
	CoreImport string
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
// registered, it returns the existing type. If the Go name collides with a
// different schema ref, it returns an error via the second return value.
func (r *typeRegistry) register(t *goType) (*goType, bool) {
	if existing, ok := r.byRef[t.SchemaRef]; ok {
		return existing, false
	}
	if _, ok := r.byName[t.Name]; ok {
		return nil, false
	}
	r.byRef[t.SchemaRef] = t
	r.byName[t.Name] = t
	r.order = append(r.order, t.SchemaRef)
	return t, true
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
