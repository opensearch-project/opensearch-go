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

// The format default applies only to an operation that takes `format`: cat
// help is in the cat family but has no format parameter.
func TestFormatOverride(t *testing.T) {
	t.Parallel()

	format := ir.QueryParam{GoName: "Format", WireName: "format", GoType: "CatRespFormat", Kind: ir.ParamString, IsStringEnum: true}
	tests := []struct {
		name     string
		op       ir.Operation
		expected string
	}{
		{name: "cat op with format", op: ir.Operation{Group: "cat.indices", QueryParams: []ir.QueryParam{format}}, expected: "json"},
		{name: "explain group override", op: ir.Operation{Group: "sql.explain", QueryParams: []ir.QueryParam{format}}, expected: "json"},
		{name: "cat op without format", op: ir.Operation{Group: "cat.help"}, expected: ""},
		{name: "op outside the families", op: ir.Operation{Group: "search", QueryParams: []ir.QueryParam{format}}, expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, emit.FormatOverride(&tt.op))
		})
	}
}

// The server default the spec gives `format` is not what the client sends when
// it overrides the param, so the field doc drops it there and keeps it elsewhere.
func TestParamsFragment_FormatDefaultDoc(t *testing.T) {
	t.Parallel()

	format := ir.QueryParam{
		GoName: "Format", WireName: "format", GoType: "string", Kind: ir.ParamString,
		Description: "Response format.", Default: "text",
	}
	tests := []struct {
		name        string
		group       string
		wantDefault bool
	}{
		{name: "client overrides format", group: "cat.indices", wantDefault: false},
		{name: "no client override", group: "search", wantDefault: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := &ir.Operation{TypePrefix: "X", Group: tt.group, QueryParams: []ir.QueryParam{format}}
			body, err := (&emit.ParamsFragment{Op: op}).Body()
			require.NoError(t, err)
			require.Equal(t, tt.wantDefault, strings.Contains(body, "// Default: text."))
			require.Contains(t, body, "// Response format.\n")
			require.NotContains(t, body, "//\n\tFormat string", "no dangling separator before the field")
		})
	}
}
