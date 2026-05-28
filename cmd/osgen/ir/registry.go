// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir

import "sort"

// TypeRegistry tracks generated types, deduplicates by schema ref, and
// provides stable iteration order.
type TypeRegistry struct {
	byRef  map[string]*Type
	byName map[string]*Type
	order  []string

	CorePkg    string
	CoreImport string
}

// NewTypeRegistry creates an empty registry.
func NewTypeRegistry(corePkg, coreImport string) *TypeRegistry {
	return &TypeRegistry{
		byRef:      make(map[string]*Type),
		byName:     make(map[string]*Type),
		CorePkg:    corePkg,
		CoreImport: coreImport,
	}
}

// Register adds a type to the registry. If the schema ref is already
// registered, it returns the existing type and false. If the Go name collides
// with a different schema ref, it returns nil and false.
func (r *TypeRegistry) Register(t *Type) (*Type, bool) {
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

// Lookup returns a previously registered type by its schema ref.
func (r *TypeRegistry) Lookup(ref string) (*Type, bool) {
	t, ok := r.byRef[ref]
	return t, ok
}

// LookupByName returns a previously registered type by its Go name.
func (r *TypeRegistry) LookupByName(name string) (*Type, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All returns all registered types in insertion order.
func (r *TypeRegistry) All() []*Type {
	result := make([]*Type, 0, len(r.order))
	for _, ref := range r.order {
		result = append(result, r.byRef[ref])
	}
	return result
}

// Shared returns all shared types sorted by name for stable output.
func (r *TypeRegistry) Shared() []*Type {
	var result []*Type
	for _, ref := range r.order {
		t := r.byRef[ref]
		if t.Scope == ScopeShared {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Unions returns all union types (both strict and lazy) sorted by name.
func (r *TypeRegistry) Unions() []*Type {
	var result []*Type
	for _, ref := range r.order {
		t := r.byRef[ref]
		if t.Kind == TypeUnion || t.Kind == TypeLazyUnion {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ForOperation returns types associated with a specific operation (not shared,
// not response). These are the sibling types emitted alongside the Resp struct.
func (r *TypeRegistry) ForOperation(group string) []*Type {
	var result []*Type
	for _, ref := range r.order {
		t := r.byRef[ref]
		if t.Scope == ScopeLocal && t.OwnerGroup == group {
			result = append(result, t)
		}
	}
	return result
}

// PackageFor returns the import path of the package that owns the given type
// name. Returns empty string if the type is unknown or a builtin.
func (r *TypeRegistry) PackageFor(typeName string) string {
	t, ok := r.byName[typeName]
	if !ok {
		return ""
	}
	return t.ImportPath
}

// PromoteSharedDeps walks all shared types and promotes any non-shared type
// they reference to shared scope. This ensures types used by shared types are
// emitted to types_gen.go.
func (r *TypeRegistry) PromoteSharedDeps() {
	changed := true
	for changed {
		changed = false
		for _, ref := range r.order {
			t := r.byRef[ref]
			if t.Scope != ScopeShared {
				continue
			}
			for _, f := range t.Fields {
				typeName := unwrapTypeName(f.GoType)
				child, ok := r.byName[typeName]
				if !ok || child.Scope == ScopeShared || child.Scope == ScopeResponse {
					continue
				}
				child.Scope = ScopeShared
				child.Package = t.Package
				child.ImportPath = t.ImportPath
				changed = true
			}
			for _, b := range t.Branches {
				typeName := unwrapTypeName(b.GoType)
				child, ok := r.byName[typeName]
				if !ok || child.Scope == ScopeShared || child.Scope == ScopeResponse {
					continue
				}
				child.Scope = ScopeShared
				child.Package = t.Package
				child.ImportPath = t.ImportPath
				changed = true
			}
		}
	}
}

// unwrapTypeName strips pointer, slice, and map prefixes to get the base type name.
func unwrapTypeName(goType string) string {
	for len(goType) > 0 {
		switch {
		case goType[0] == '*':
			goType = goType[1:]
		case len(goType) > 2 && goType[:2] == "[]":
			goType = goType[2:]
		case len(goType) > 4 && goType[:4] == "map[":
			idx := len(goType) - 1
			for i := 4; i < len(goType); i++ {
				if goType[i] == ']' {
					idx = i
					break
				}
			}
			goType = goType[idx+1:]
		default:
			return goType
		}
	}
	return goType
}
