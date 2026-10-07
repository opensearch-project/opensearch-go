// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/emit"
	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

func TestOpaqueStringFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		types       []*ir.Type
		wantBody    []string
		wantImports []emit.Import
	}{
		{
			name: "one type",
			types: []*ir.Type{{
				Name:    "PITID",
				Kind:    ir.TypeOpaqueString,
				Scope:   ir.ScopeShared,
				Comment: "Identifies a point in time.",
				Package: "github.com/opensearch-project/opensearch-go/v5/opensearchapi",
			}},
			wantBody: []string{
				"// Identifies a point in time.\n//\n// Build one with ParsePITID",
				"type PITID struct {\n\ts string\n}",
				"func ParsePITID(s string) (PITID, error) {",
				`var errEmptyPITID = errors.New("opensearchapi: empty PITID")`,
				"return PITID{}, errEmptyPITID",
				"func (v PITID) String() string",
				"func (v PITID) IsSet() bool",
				"func (v PITID) MarshalText() ([]byte, error) {\n\tif v.s == \"\" {\n\t\treturn nil, errEmptyPITID",
				"func (v *PITID) UnmarshalText(text []byte) error",
			},
			wantImports: []emit.Import{{Path: "errors"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			frag := &emit.OpaqueStringFragment{Types: tt.types}
			body, err := frag.Body()
			require.NoError(t, err)
			require.Equal(t, tt.wantImports, frag.Imports())
			for _, want := range tt.wantBody {
				require.Contains(t, body, want)
			}
			_, err = parser.ParseFile(token.NewFileSet(), "enums_gen.go", "package opensearchapi\nimport \"errors\"\n"+body, 0)
			require.NoError(t, err, "the fragment is valid Go")
		})
	}
}

// TestOpaqueStringFragment_Empty renders nothing and needs no imports.
func TestOpaqueStringFragment_Empty(t *testing.T) {
	t.Parallel()

	frag := &emit.OpaqueStringFragment{}
	body, err := frag.Body()
	require.NoError(t, err)
	require.Empty(t, body)
	require.Empty(t, frag.Imports())
}

// TestNewEnumTypesFile_OpaqueString confirms an opaque type lands in
// enums_gen.go, alone or beside a string enum, and the file renders.
func TestNewEnumTypesFile_OpaqueString(t *testing.T) {
	t.Parallel()

	pitID := &ir.Type{Name: "PITID", Kind: ir.TypeOpaqueString, Scope: ir.ScopeShared, Package: ir.DefaultCoreImportPath}
	nodeRole := &ir.Type{
		Name: "NodeRole", Kind: ir.TypeStringEnum, Scope: ir.ScopeShared,
		EnumMembers: []ir.EnumMember{{ConstName: "NodeRoleData", Value: "data"}},
	}
	tests := []struct {
		name  string
		types []*ir.Type
		want  []string
	}{
		{name: "opaque only", types: []*ir.Type{pitID}, want: []string{"type PITID struct", "func ParsePITID("}},
		{
			name:  "beside a string enum",
			types: []*ir.Type{nodeRole, pitID},
			want:  []string{"type PITID struct", "type NodeRole string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := emit.NewEnumTypesFile("/tmp/test", ir.DefaultCorePkgName, tt.types)
			require.NotNil(t, target)
			src, err := target.Render()
			require.NoError(t, err)
			for _, want := range tt.want {
				require.Contains(t, string(src), want)
			}
		})
	}
}
