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

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/emit"
	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

func TestSharedTypesFragment_ExtraKeysRawFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawValue bool
	}{
		{"union value keeps undecodable values raw", true},
		{"non-union value fails the decode", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			frag := &emit.SharedTypesFragment{Types: []*ir.Type{{
				Name:  "TermsQuery",
				Kind:  ir.TypeStruct,
				Scope: ir.ScopeShared,
				Fields: []ir.Field{
					{GoName: "Boost", JSONName: "boost", GoType: "float32", IsPointer: true, OmitEmpty: true},
				},
				ExtraKeys: &ir.ExtraKeys{GoName: "Fields", ValueType: "TermsQueryField", Known: []string{"boost"}, RawValue: tt.rawValue},
			}}}
			body, err := frag.Body()
			require.NoError(t, err)

			require.Contains(t, body, "type TermsQueryFixed TermsQuery")
			require.NotContains(t, body, "type plain ")
			require.Equal(t, tt.rawValue, strings.Contains(body, "val.SetRaw("))
			require.Equal(t, tt.rawValue, strings.Contains(body, "val.RawJSON()"))
		})
	}
}

func TestSharedTypesFragment_ExtraKeys(t *testing.T) {
	t.Parallel()

	types := []*ir.Type{{
		Name:  "TermsQuery",
		Kind:  ir.TypeStruct,
		Scope: ir.ScopeShared,
		Fields: []ir.Field{
			{GoName: "Boost", JSONName: "boost", GoType: "float32", IsPointer: true, OmitEmpty: true},
		},
		ExtraKeys: &ir.ExtraKeys{GoName: "Fields", ValueType: "TermsQueryField", KeyTitle: "field", Known: []string{"boost"}},
	}}

	frag := &emit.SharedTypesFragment{Types: types}
	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "Fields map[string]TermsQueryField `json:\"-\"`")
	require.Contains(t, body, "func (v TermsQuery) MarshalJSON() ([]byte, error)")
	require.Contains(t, body, "func (v *TermsQuery) UnmarshalJSON(data []byte) error")
	// Both methods skip the fixed names, so a map key never shadows a fixed property.
	// Keys compare case-insensitively, as the codec matches the fixed fields.
	require.Contains(t, body, `[...]string{"boost"}`)
	require.Contains(t, body, "strings.EqualFold(k, fixed)")
	require.Equal(t, 2, strings.Count(body, "if v.isFixedKey(k) {"))
	require.Equal(t, []emit.Import{{Path: emit.JSONImportPath, Alias: "json"}, {Path: "strings"}}, frag.Imports())
}
