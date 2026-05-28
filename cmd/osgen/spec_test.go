// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestOperationGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *openapi3.Operation
		want string
	}{
		{name: "nil operation", op: nil, want: ""},
		{name: "nil extensions", op: &openapi3.Operation{}, want: ""},
		{name: "missing key", op: &openapi3.Operation{
			Extensions: map[string]any{"x-other": "val"},
		}, want: ""},
		{name: "string value", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: "indices.create"},
		}, want: "indices.create"},
		{name: "json.RawMessage value", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: json.RawMessage(`"cluster.health"`)},
		}, want: "cluster.health"},
		{name: "invalid json.RawMessage", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: json.RawMessage(`not-json`)},
		}, want: ""},
		{name: "unexpected type", op: &openapi3.Operation{
			Extensions: map[string]any{extOperationGroup: 42},
		}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, operationGroup(tt.op))
		})
	}
}

func TestDeprecationMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *openapi3.Operation
		want string
	}{
		{name: "nil operation", op: nil, want: ""},
		{name: "nil extensions", op: &openapi3.Operation{}, want: ""},
		{name: "missing key", op: &openapi3.Operation{
			Extensions: map[string]any{},
		}, want: ""},
		{name: "string value", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: "Use v2 API instead."},
		}, want: "Use v2 API instead."},
		{name: "json.RawMessage value", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: json.RawMessage(`"Removed in 3.0."`)},
		}, want: "Removed in 3.0."},
		{name: "invalid json.RawMessage", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: json.RawMessage(`{bad}`)},
		}, want: ""},
		{name: "unexpected type", op: &openapi3.Operation{
			Extensions: map[string]any{extDeprecationMessage: true},
		}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, deprecationMessage(tt.op))
		})
	}
}

func TestExtensionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want string
	}{
		{name: "nil map", ext: nil, key: "x-foo", want: ""},
		{name: "missing key", ext: map[string]any{}, key: "x-foo", want: ""},
		{name: "string value", ext: map[string]any{"x-foo": "bar"}, key: "x-foo", want: "bar"},
		{name: "json.RawMessage", ext: map[string]any{"x-foo": json.RawMessage(`"baz"`)}, key: "x-foo", want: "baz"},
		{name: "invalid json.RawMessage", ext: map[string]any{"x-foo": json.RawMessage(`123`)}, key: "x-foo", want: ""},
		{name: "wrong type int", ext: map[string]any{"x-foo": 99}, key: "x-foo", want: ""},
		{name: "wrong type bool", ext: map[string]any{"x-foo": false}, key: "x-foo", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extensionString(tt.ext, tt.key))
		})
	}
}

func TestExtensionBool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want bool
	}{
		{name: "nil map", ext: nil, key: "x-flag", want: false},
		{name: "missing key", ext: map[string]any{}, key: "x-flag", want: false},
		{name: "bool true", ext: map[string]any{"x-flag": true}, key: "x-flag", want: true},
		{name: "bool false", ext: map[string]any{"x-flag": false}, key: "x-flag", want: false},
		{name: "json.RawMessage true", ext: map[string]any{"x-flag": json.RawMessage(`true`)}, key: "x-flag", want: true},
		{name: "json.RawMessage false", ext: map[string]any{"x-flag": json.RawMessage(`false`)}, key: "x-flag", want: false},
		{name: "invalid json.RawMessage", ext: map[string]any{"x-flag": json.RawMessage(`"yes"`)}, key: "x-flag", want: false},
		{name: "wrong type string", ext: map[string]any{"x-flag": "true"}, key: "x-flag", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extensionBool(tt.ext, tt.key))
		})
	}
}

func TestExtensionStringSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  map[string]any
		key  string
		want []string
	}{
		{name: "nil map", ext: nil, key: "x-list", want: nil},
		{name: "missing key", ext: map[string]any{}, key: "x-list", want: nil},
		{name: "json.RawMessage slice", ext: map[string]any{
			"x-list": json.RawMessage(`["a","b","c"]`),
		}, key: "x-list", want: []string{"a", "b", "c"}},
		{name: "json.RawMessage empty", ext: map[string]any{
			"x-list": json.RawMessage(`[]`),
		}, key: "x-list", want: []string{}},
		{name: "json.RawMessage invalid", ext: map[string]any{
			"x-list": json.RawMessage(`not-json`),
		}, key: "x-list", want: nil},
		{name: "[]any with strings", ext: map[string]any{
			"x-list": []any{"foo", "bar"},
		}, key: "x-list", want: []string{"foo", "bar"}},
		{name: "[]any with mixed types", ext: map[string]any{
			"x-list": []any{"foo", 42, "bar"},
		}, key: "x-list", want: []string{"foo", "bar"}},
		{name: "wrong type", ext: map[string]any{
			"x-list": "not-a-slice",
		}, key: "x-list", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extensionStringSlice(tt.ext, tt.key)
			if tt.want == nil {
				require.Nil(t, got)
			} else {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
