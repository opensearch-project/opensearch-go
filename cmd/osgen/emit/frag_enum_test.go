// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestEnumFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.EnumFragment{
		Types: []*ir.Type{
			{
				Name:    "RestStatus",
				Kind:    ir.TypeEnum,
				Scope:   ir.ScopeShared,
				Comment: "RestStatus is the HTTP status name.",
				EnumMembers: []ir.EnumMember{
					{ConstName: "RestStatusOk", Value: "OK"},
					{ConstName: "RestStatusNotFound", Value: "NOT_FOUND"},
					{ConstName: "RestStatusHTTPVersionNotSupported", Value: "HTTP_VERSION_NOT_SUPPORTED"},
				},
			},
		},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	wantSnippets := []struct {
		name    string
		snippet string
	}{
		// int-backed (const iota) with an Unknown zero-value sentinel.
		{"int type", "type RestStatus int"},
		{"unknown sentinel", "RestStatusUnknown RestStatus = iota"},
		{"const member", "RestStatusOk"},
		{"acronym const", "RestStatusHTTPVersionNotSupported"},
		{"doc comment", "// RestStatus is the HTTP status name."},
		// lookup tables (both directions).
		{"names map", "restStatusNames = map[RestStatus]string{"},
		{"names entry", `RestStatusOk: "OK",`},
		{"values map", "restStatusValues = map[string]RestStatus{"},
		{"values entry", `"NOT_FOUND": RestStatusNotFound,`},
		// methods.
		{"String method", "func (s RestStatus) String() string"},
		{"MarshalJSON", "func (s RestStatus) MarshalJSON() ([]byte, error)"},
		{"UnmarshalJSON", "func (s *RestStatus) UnmarshalJSON(data []byte) error"},
		// unknown value sets sentinel + returns the typed error carrying the raw value.
		{"sentinel on unknown", "*s = RestStatusUnknown"},
		{"typed error returned", "return &UnknownRestStatusError{Value: v}"},
		{"error type", "type UnknownRestStatusError struct {"},
		{"error message", `fmt.Sprintf("unknown RestStatus %q", e.Value)`},
	}
	for _, tt := range wantSnippets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tt.snippet)
		})
	}
}

// TestEnumFragment_Imports asserts the fragment pulls in encoding/json (for
// Marshal/UnmarshalJSON) and fmt (for the error type), as the int-backed enum
// needs custom marshaling.
func TestEnumFragment_Imports(t *testing.T) {
	t.Parallel()

	frag := &emit.EnumFragment{
		Types: []*ir.Type{{
			Name:        "RestStatus",
			Kind:        ir.TypeEnum,
			Scope:       ir.ScopeShared,
			EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}},
		}},
	}

	paths := make([]string, 0, 2)
	for _, imp := range frag.Imports() {
		paths = append(paths, imp.Path)
	}
	require.ElementsMatch(t, []string{"encoding/json", "fmt"}, paths)
}

func TestEnumFragment_Empty(t *testing.T) {
	t.Parallel()

	frag := &emit.EnumFragment{}
	body, err := frag.Body()
	require.NoError(t, err)
	require.Empty(t, body)
	// An empty fragment also requests no imports.
	require.Empty(t, frag.Imports())
}

// TestNewEnumTypesFile_Filters covers which types NewEnumTypesFile selects:
// only shared enums are included; non-enum kinds and non-shared scopes are
// excluded, and an input with no qualifying enum yields a nil Target.
func TestNewEnumTypesFile_Filters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		types     []*ir.Type
		wantNil   bool
		wantIn    []string // enum type names expected in the rendered output
		wantNotIn []string // names that must not appear
	}{
		{
			name: "selects shared enum only",
			types: []*ir.Type{
				{
					Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared,
					EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}},
				},
			},
			wantIn: []string{"RestStatus"},
		},
		{
			name: "excludes non-enum kinds and non-shared scope",
			types: []*ir.Type{
				{
					Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared,
					EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}},
				},
				{Name: "LocalEnum", Kind: ir.TypeEnum, Scope: ir.ScopeLocal, EnumMembers: []ir.EnumMember{{ConstName: "LocalEnumA", Value: "A"}}},
				{Name: "SomeStruct", Kind: ir.TypeStruct, Scope: ir.ScopeShared},
				{Name: "SomeUnion", Kind: ir.TypeUnion, Scope: ir.ScopeShared},
			},
			wantIn:    []string{"RestStatus"},
			wantNotIn: []string{"LocalEnum", "SomeStruct", "SomeUnion"},
		},
		{
			name:    "no enums yields nil target",
			types:   []*ir.Type{{Name: "SomeStruct", Kind: ir.TypeStruct, Scope: ir.ScopeShared}},
			wantNil: true,
		},
		{
			name:    "empty input yields nil target",
			types:   nil,
			wantNil: true,
		},
		{
			name: "enum present but local scope yields nil target",
			types: []*ir.Type{{
				Name: "LocalEnum", Kind: ir.TypeEnum, Scope: ir.ScopeLocal,
				EnumMembers: []ir.EnumMember{{ConstName: "LocalEnumA", Value: "A"}},
			}},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, tt.types)

			if tt.wantNil {
				require.Nil(t, target)
				return
			}
			require.NotNil(t, target)

			src, err := target.Render()
			require.NoError(t, err)
			output := string(src)

			require.Contains(t, output, "package "+ir.DefaultCorePkgName)
			for _, name := range tt.wantIn {
				require.Contains(t, output, "type "+name+" int")
			}
			for _, name := range tt.wantNotIn {
				require.NotContains(t, output, name)
			}
		})
	}
}

// TestNewEnumTypesFile_PathAndPackage pins the output file path and package.
func TestNewEnumTypesFile_PathAndPackage(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared,
			EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}},
		},
	}

	target := emit.NewEnumTypesFile("/tmp/out", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)
	require.Equal(t, "/tmp/out/enums_gen.go", target.Path())

	src, err := target.Render()
	require.NoError(t, err)
	require.Contains(t, string(src), "package "+ir.DefaultCorePkgName)
}

// TestNewEnumTypesFile_RenderedEnum asserts the assembled file carries the full
// enum surface (type, consts, marshaling, error type) for a shared enum.
func TestNewEnumTypesFile_RenderedEnum(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared,
			EnumMembers: []ir.EnumMember{
				{ConstName: "RestStatusOk", Value: "OK"},
				{ConstName: "RestStatusNotFound", Value: "NOT_FOUND"},
			},
		},
	}

	target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)
	output := string(src)

	for _, want := range []string{
		"type RestStatus int",
		"RestStatusUnknown RestStatus = iota",
		`RestStatusOk:`,
		`"OK",`,
		"func (s RestStatus) MarshalJSON()",
		"func (s *RestStatus) UnmarshalJSON(",
		"type UnknownRestStatusError struct {",
	} {
		require.Contains(t, output, want)
	}
	// Custom marshaling pulls in encoding/json and fmt.
	require.Contains(t, output, `"encoding/json"`)
	require.Contains(t, output, `"fmt"`)
}

// TestNewEnumTypesFile_MultipleEnums confirms several shared enums all land in
// the single file in registry order.
func TestNewEnumTypesFile_MultipleEnums(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared, EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}}},
		{Name: "SortOrder", Kind: ir.TypeEnum, Scope: ir.ScopeShared, EnumMembers: []ir.EnumMember{{ConstName: "SortOrderAsc", Value: "asc"}}},
	}

	target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)
	output := string(src)

	require.Contains(t, output, "type RestStatus int")
	require.Contains(t, output, "type SortOrder int")
	// Declaration order is preserved (RestStatus before SortOrder).
	require.Less(t, strings.Index(output, "type RestStatus int"), strings.Index(output, "type SortOrder int"))
}

// TestStringEnumFragment_Body asserts the string-backed enum renders a named
// string type with one exported const per value, per-member doc comments, and
// NO custom marshaling (the type is permissive: unknown values round-trip).
func TestStringEnumFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.StringEnumFragment{
		Types: []*ir.Type{
			{
				Name:    "NodeRole",
				Kind:    ir.TypeStringEnum,
				Scope:   ir.ScopeShared,
				Comment: "The role assigned to the node.",
				EnumMembers: []ir.EnumMember{
					{ConstName: "NodeRoleDataHot", Value: "data_hot", Comment: "The node can store hot data."},
					{ConstName: "NodeRoleML", Value: "ml"},
				},
			},
		},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	for _, want := range []string{
		"// The role assigned to the node.",
		"type NodeRole string",
		"// The node can store hot data.",
		`NodeRoleDataHot NodeRole = "data_hot"`,
		`NodeRoleML NodeRole = "ml"`,
	} {
		require.Contains(t, body, want)
	}

	// Permissive: no closed-set marshaling or error type is generated.
	require.NotContains(t, body, "MarshalJSON")
	require.NotContains(t, body, "UnmarshalJSON")
	require.NotContains(t, body, "Unknown")
	// String-backed enums need no imports.
	require.Empty(t, frag.Imports())
}

// TestStringEnumFragment_Empty renders nothing and needs no imports.
func TestStringEnumFragment_Empty(t *testing.T) {
	t.Parallel()

	frag := &emit.StringEnumFragment{}
	body, err := frag.Body()
	require.NoError(t, err)
	require.Empty(t, body)
	require.Empty(t, frag.Imports())
}

// TestNewEnumTypesFile_IntAndStringEnums confirms both enum kinds land in the
// single enums_gen.go file.
func TestNewEnumTypesFile_IntAndStringEnums(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{Name: "RestStatus", Kind: ir.TypeEnum, Scope: ir.ScopeShared, EnumMembers: []ir.EnumMember{{ConstName: "RestStatusOk", Value: "OK"}}},
		{
			Name: "NodeRole", Kind: ir.TypeStringEnum, Scope: ir.ScopeShared,
			EnumMembers: []ir.EnumMember{{ConstName: "NodeRoleData", Value: "data"}},
		},
	}

	target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)
	output := string(src)

	require.Contains(t, output, "type RestStatus int")
	require.Contains(t, output, "type NodeRole string")
	require.Contains(t, output, `NodeRoleData NodeRole = "data"`)
}

// TestNewEnumTypesFile_StringEnumOnly confirms a file of only string enums is
// still produced (no int enum required).
func TestNewEnumTypesFile_StringEnumOnly(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{
		{
			Name: "NodeRole", Kind: ir.TypeStringEnum, Scope: ir.ScopeShared,
			EnumMembers: []ir.EnumMember{{ConstName: "NodeRoleData", Value: "data"}},
		},
	}

	target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, types)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)
	require.Contains(t, string(src), "type NodeRole string")
}
