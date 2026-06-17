// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir_test

import (
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestTypeRegistryRegisterAndLookup(t *testing.T) {
	t.Parallel()

	reg := ir.NewTypeRegistry(ir.DefaultCorePkgName, ir.DefaultCoreImportPath)

	typ := &ir.Type{
		Name:      "ClusterHealthResp",
		SchemaRef: "cluster.health___HealthResponseBody",
		Kind:      ir.TypeStruct,
		Scope:     ir.ScopeResponse,
	}

	got, ok := reg.Register(typ)
	if !ok || got != typ {
		t.Fatalf("Register returned (%v, %v), want (%v, true)", got, ok, typ)
	}

	// Duplicate ref returns existing.
	dup, ok := reg.Register(typ)
	if ok {
		t.Fatal("duplicate Register should return false")
	}
	if dup != typ {
		t.Fatal("duplicate Register should return existing type")
	}

	// Lookup by ref.
	found, ok := reg.Lookup("cluster.health___HealthResponseBody")
	if !ok || found != typ {
		t.Fatal("Lookup by ref failed")
	}

	// Lookup by name.
	found, ok = reg.LookupByName("ClusterHealthResp")
	if !ok || found != typ {
		t.Fatal("LookupByName failed")
	}

	// Unknown ref.
	_, ok = reg.Lookup("nonexistent")
	if ok {
		t.Fatal("Lookup of unknown ref should return false")
	}
}

func TestTypeRegistryShared(t *testing.T) {
	t.Parallel()

	reg := ir.NewTypeRegistry(ir.DefaultCorePkgName, ir.DefaultCoreImportPath)

	shared := &ir.Type{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics", Scope: ir.ScopeShared}
	local := &ir.Type{Name: "IndexHealthStats", SchemaRef: "cluster.health___IndexHealthStats", Scope: ir.ScopeLocal}
	resp := &ir.Type{Name: "ClusterHealthResp", SchemaRef: "cluster.health___HealthResponseBody", Scope: ir.ScopeResponse}

	reg.Register(shared)
	reg.Register(local)
	reg.Register(resp)

	sharedTypes := reg.Shared()
	if len(sharedTypes) != 1 {
		t.Fatalf("Shared() returned %d types, want 1", len(sharedTypes))
	}
	if sharedTypes[0] != shared {
		t.Fatal("Shared() did not return the shared type")
	}
}

func TestTypeRegistryPromoteSharedDeps(t *testing.T) {
	t.Parallel()

	reg := ir.NewTypeRegistry(ir.DefaultCorePkgName, ir.DefaultCoreImportPath)

	parent := &ir.Type{
		Name:      "SharedParent",
		SchemaRef: "shared___Parent",
		Scope:     ir.ScopeShared,
		Package:   ir.DefaultCorePkgName,
		Fields:    []ir.Field{{GoName: "Child", GoType: "LocalChild"}},
	}
	child := &ir.Type{
		Name:      "LocalChild",
		SchemaRef: "op___LocalChild",
		Scope:     ir.ScopeLocal,
	}

	reg.Register(parent)
	reg.Register(child)

	reg.PromoteSharedDeps()

	if child.Scope != ir.ScopeShared {
		t.Fatal("PromoteSharedDeps did not promote child to shared")
	}
}

func TestUnwrapTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "ShardStatistics", want: "ShardStatistics"},
		{name: "pointer", input: "*ShardStatistics", want: "ShardStatistics"},
		{name: "slice", input: "[]ShardStatistics", want: "ShardStatistics"},
		{name: "pointer slice", input: "[]*ShardStatistics", want: "ShardStatistics"},
		{name: "map", input: "map[string]ShardStatistics", want: "ShardStatistics"},
		{name: "map pointer", input: "map[string]*ShardStatistics", want: "ShardStatistics"},
		{name: "builtin", input: "string", want: "string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ir.UnwrapTypeName(tt.input)
			if got != tt.want {
				t.Errorf("unwrapTypeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParamKindConstants(t *testing.T) {
	t.Parallel()

	// Verify the enum values are distinct and ordered.
	kinds := []ir.ParamKind{ir.ParamString, ir.ParamBool, ir.ParamInt, ir.ParamDuration, ir.ParamList}
	for i := range kinds {
		if int(kinds[i]) != i {
			t.Errorf("ParamKind %d has value %d, want %d", i, int(kinds[i]), i)
		}
	}
}

func TestTypeKindConstants(t *testing.T) {
	t.Parallel()

	kinds := []ir.TypeKind{ir.TypeStruct, ir.TypeUnion, ir.TypeLazyUnion}
	for i := range kinds {
		if int(kinds[i]) != i {
			t.Errorf("TypeKind %d has value %d, want %d", i, int(kinds[i]), i)
		}
	}
}
