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

func TestSharedTypesFragmentAdditionalProperties(t *testing.T) {
	t.Parallel()

	f := &emit.SharedTypesFragment{Types: []*ir.Type{{
		Name:  "TermsQuery",
		Kind:  ir.TypeStruct,
		Scope: ir.ScopeShared,
		Fields: []ir.Field{
			{GoName: "Boost", JSONName: "boost", GoType: "*float32", OmitEmpty: true},
			{GoName: "Terms", JSONName: "-", GoType: "map[string][]string"},
		},
		Additional: &ir.AdditionalProperties{GoName: "Terms", ValueType: "[]string", DeclaredKeys: []string{"_name", "boost"}},
	}}}

	body, err := f.Body()
	require.NoError(t, err)
	for _, want := range []string{
		"Terms map[string][]string `json:\"-\"`",
		"func (t TermsQuery) MarshalJSON() ([]byte, error) {",
		`case "_name", "boost":`,
		`return nil, fmt.Errorf("TermsQuery: Terms key %q is a declared field", k)`,
		"func (t *TermsQuery) UnmarshalJSON(data []byte) error {",
		"var val []string",
	} {
		require.Contains(t, body, want)
	}
	require.Equal(t, []emit.Import{{Path: "fmt"}, {Path: emit.JSONImportPath, Alias: "json"}}, f.Imports())

	_, err = parser.ParseFile(token.NewFileSet(), "types_gen.go", "package p\n"+body, 0)
	require.NoError(t, err)
}
