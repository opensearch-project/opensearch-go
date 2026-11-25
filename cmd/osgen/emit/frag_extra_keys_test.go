// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"fmt"
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

			require.Contains(t, body, "type TermsQueryFixed struct {")
			require.Contains(t, body, "MarshalJSON   struct{}")
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
	require.Equal(t, []emit.Import{{Path: "fmt"}, {Path: emit.JSONImportPath, Alias: "json"}, {Path: "strings"}}, frag.Imports())
	// Marshal rejects a key that names a fixed property rather than dropping it.
	require.Contains(t, body, `return nil, fmt.Errorf("TermsQuery: key %q in Fields names a fixed property", k)`)
	require.Equal(t, 1, strings.Count(body, "if v.isFixedKey(k) {\n\t\t\treturn nil, fmt.Errorf"))
}

func TestSharedTypesFragment_ExtraKeysEmbedders(t *testing.T) {
	t.Parallel()

	base := func() *ir.Type {
		return &ir.Type{
			Name: "Base", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
			Fields:    []ir.Field{{GoName: "DocCount", JSONName: "doc_count", GoType: "int64"}},
			ExtraKeys: &ir.ExtraKeys{GoName: "Aggregations", ValueType: "Agg", Known: []string{"doc_count"}, RawValue: true},
		}
	}
	mid := &ir.Type{
		Name: "Mid", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
		Fields: []ir.Field{{GoType: "Base", IsEmbed: true}, {GoName: "Err", JSONName: "err", GoType: "int64"}},
	}
	leaf := &ir.Type{
		Name: "Leaf", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
		Fields: []ir.Field{{GoType: "Mid", IsEmbed: true}, {GoName: "Key", JSONName: "key", GoType: "string"}},
	}
	other := &ir.Type{
		Name: "Other", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
		Fields: []ir.Field{{GoName: "Name", JSONName: "name", GoType: "string"}},
	}

	body, err := (&emit.SharedTypesFragment{Types: []*ir.Type{base(), mid, leaf, other}}).Body()
	require.NoError(t, err)

	for _, name := range []string{"Base", "Mid", "Leaf"} {
		require.Contains(t, body, "func (v "+name+") MarshalJSON() ([]byte, error)")
		require.Contains(t, body, "func (v *"+name+") UnmarshalJSON(data []byte) error")
	}
	require.Contains(t, body, `[...]string{"doc_count"}`)
	require.Contains(t, body, `[...]string{"doc_count", "err"}`)
	require.Contains(t, body, `[...]string{"doc_count", "err", "key"}`)
	require.NotContains(t, body, "func (v Other) MarshalJSON")
	require.NotContains(t, body, "func (v *Other) UnmarshalJSON")
}

func TestSharedTypesFragment_ExtraKeysEmbedCyclePanics(t *testing.T) {
	t.Parallel()

	root := &ir.Type{Name: "RootEmbedder", Kind: ir.TypeStruct, Scope: ir.ScopeShared, Fields: []ir.Field{{GoType: "LoopA", IsEmbed: true}}}
	a := &ir.Type{Name: "LoopA", Kind: ir.TypeStruct, Scope: ir.ScopeShared, Fields: []ir.Field{{GoType: "LoopB", IsEmbed: true}}}
	b := &ir.Type{Name: "LoopB", Kind: ir.TypeStruct, Scope: ir.ScopeShared, Fields: []ir.Field{{GoType: "LoopA", IsEmbed: true}}}

	msg := panicMessage(t, func() {
		_, _ = (&emit.SharedTypesFragment{Types: []*ir.Type{root, a, b}}).Body()
	})
	require.Contains(t, msg, "RootEmbedder")
	require.Contains(t, msg, "LoopA")
}

func TestSharedTypesFragment_ExtraKeysUnresolvedEmbedPanics(t *testing.T) {
	t.Parallel()

	root := &ir.Type{Name: "RootEmbedder", Kind: ir.TypeStruct, Scope: ir.ScopeShared, Fields: []ir.Field{{GoType: "Mid", IsEmbed: true}}}
	mid := &ir.Type{
		Name: "Mid", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
		Fields: []ir.Field{{GoType: "Base", IsEmbed: true}, {GoType: "Missing", IsEmbed: true}},
	}
	base := &ir.Type{
		Name: "Base", Kind: ir.TypeStruct, Scope: ir.ScopeShared,
		ExtraKeys: &ir.ExtraKeys{GoName: "Aggregations", ValueType: "Agg"},
	}

	msg := panicMessage(t, func() {
		_, _ = (&emit.SharedTypesFragment{Types: []*ir.Type{root, mid, base}}).Body()
	})
	require.Contains(t, msg, "RootEmbedder")
	require.Contains(t, msg, "Mid")
	require.Contains(t, msg, "Missing")
}

func panicMessage(t *testing.T, fn func()) string {
	t.Helper()

	var msg string
	func() {
		defer func() {
			r := recover()
			require.NotNil(t, r, "expected a panic")
			msg = fmt.Sprint(r)
		}()
		fn()
	}()
	return msg
}
