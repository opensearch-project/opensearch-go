// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
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
}
