// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
	kinds := []ir.ParamKind{ir.ParamString, ir.ParamBool, ir.ParamInt, ir.ParamDuration, ir.ParamList, ir.ParamFloat}
	for i := range kinds {
		if int(kinds[i]) != i {
			t.Errorf("ParamKind %d has value %d, want %d", i, int(kinds[i]), i)
		}
	}
}

func TestTypeKindConstants(t *testing.T) {
	t.Parallel()

	kinds := []ir.TypeKind{ir.TypeStruct, ir.TypeUnion, ir.TypeAmbiguousWire}
	for i := range kinds {
		if int(kinds[i]) != i {
			t.Errorf("TypeKind %d has value %d, want %d", i, int(kinds[i]), i)
		}
	}
}

func TestTokenClassConstants(t *testing.T) {
	t.Parallel()

	// The emitter selects a union branch's decode arm by comparing TokenClass
	// values, so the constants are the contract; nothing depends on a string
	// spelling. Pin the iota order so a reordering that would silently remap
	// existing branches fails here.
	classes := []ir.TokenClass{
		ir.TokenObject,
		ir.TokenArray,
		ir.TokenString,
		ir.TokenNumber,
		ir.TokenBool,
	}
	for i := range classes {
		require.Equal(t, i, int(classes[i]), "TokenClass constants must keep their iota order")
	}
}

// TestRegistryAccessors covers the query methods the emit phase uses to decide
// what goes in which file.
func TestRegistryAccessors(t *testing.T) {
	t.Parallel()

	reg := ir.NewTypeRegistry(ir.DefaultCorePkgName, ir.DefaultCoreImportPath)

	shared := &ir.Type{
		Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics",
		Kind: ir.TypeStruct, Scope: ir.ScopeShared, ImportPath: ir.DefaultCoreImportPath,
	}
	union := &ir.Type{
		Name: "StringOrStringArray", SchemaRef: "_common___StringOrStringArray",
		Kind: ir.TypeUnion, Scope: ir.ScopeShared, ImportPath: ir.DefaultCoreImportPath,
	}
	ambiguous := &ir.Type{
		Name: "CommonAggregationsAggregate", SchemaRef: "_common.aggregations___Aggregate",
		Kind: ir.TypeAmbiguousWire, Scope: ir.ScopeShared, ImportPath: ir.DefaultCoreImportPath,
	}
	localCat := &ir.Type{
		Name: "CatCountRecord", SchemaRef: "cat.count___Record",
		Kind: ir.TypeStruct, Scope: ir.ScopeLocal, OwnerGroup: "cat.count",
		ImportPath: "example.com/plugins/cat",
	}
	localSearch := &ir.Type{
		Name: "SearchProfile", SchemaRef: "_core.search___Profile",
		Kind: ir.TypeStruct, Scope: ir.ScopeLocal, OwnerGroup: "search",
	}

	for _, t2 := range []*ir.Type{shared, union, ambiguous, localCat, localSearch} {
		if _, ok := reg.Register(t2); !ok {
			t.Fatalf("Register(%s) failed", t2.Name)
		}
	}

	t.Run("All preserves insertion order", func(t *testing.T) {
		t.Parallel()
		all := reg.All()
		got := make([]string, 0, len(all))
		for _, tt := range all {
			got = append(got, tt.Name)
		}
		want := []string{
			"ShardStatistics", "StringOrStringArray", "CommonAggregationsAggregate",
			"CatCountRecord", "SearchProfile",
		}
		if len(got) != len(want) {
			t.Fatalf("All() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("All()[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	// Both union kinds are returned: the decode strategy differs but both need
	// a union fragment emitted.
	t.Run("Unions covers both decode strategies, sorted", func(t *testing.T) {
		t.Parallel()
		unions := reg.Unions()
		got := make([]string, 0, len(unions))
		for _, tt := range unions {
			got = append(got, tt.Name)
		}
		want := []string{"CommonAggregationsAggregate", "StringOrStringArray"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("Unions() = %v, want %v", got, want)
		}
	})

	t.Run("ForOperation selects local types by owner", func(t *testing.T) {
		t.Parallel()
		got := reg.ForOperation("cat.count")
		if len(got) != 1 || got[0].Name != "CatCountRecord" {
			t.Errorf("ForOperation(cat.count) = %v, want [CatCountRecord]", got)
		}
		if got := reg.ForOperation("nope"); len(got) != 0 {
			t.Errorf("ForOperation(nope) = %v, want empty", got)
		}
	})

	t.Run("PackageFor", func(t *testing.T) {
		t.Parallel()
		if got := reg.PackageFor("CatCountRecord"); got != "example.com/plugins/cat" {
			t.Errorf("PackageFor(CatCountRecord) = %q", got)
		}
		if got := reg.PackageFor("ShardStatistics"); got != ir.DefaultCoreImportPath {
			t.Errorf("PackageFor(ShardStatistics) = %q", got)
		}
		if got := reg.PackageFor("string"); got != "" {
			t.Errorf("PackageFor(string) = %q, want empty for an unknown name", got)
		}
	})
}
