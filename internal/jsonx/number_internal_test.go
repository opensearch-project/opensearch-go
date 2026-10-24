// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !goexperiment.jsonv2 || !go1.27

package jsonx

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type numberLeaf struct {
	N json.Number `json:"n"`
}

type numberNested struct {
	Items map[string][]*numberLeaf `json:"items"`
}

type numberUnexported struct {
	n json.Number //nolint:unused // Present to show an unexported field is not searched.
	S string
}

type numberEmbedded struct {
	numberLeaf
}

type numberCycle struct {
	Next *numberCycle `json:"next"`
	S    string       `json:"s"`
}

type numberAny struct {
	V any `json:"v"`
}

// numberMethods decodes itself, so its fields are its own method's business.
type numberMethods struct {
	N json.Number
}

func (*numberMethods) UnmarshalJSON([]byte) error { return nil }

//nolint:unparam // The signature is json.Marshaler's.
func (numberMethods) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

func TestNeedsNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		v         any
		unmarshal bool
		marshal   bool
	}{
		{name: "a Number", v: json.Number("1"), unmarshal: true, marshal: true},
		{name: "a struct holding one", v: numberLeaf{}, unmarshal: true, marshal: true},
		{name: "nested in a map of slices of pointers", v: numberNested{}, unmarshal: true, marshal: true},
		{name: "an unexported field is not searched", v: numberUnexported{}},
		{name: "an embedded struct is searched", v: numberEmbedded{}, unmarshal: true, marshal: true},
		{name: "a cycle with none", v: numberCycle{}},
		{name: "an interface holds one only when marshaling", v: numberAny{}, marshal: true},
		{name: "a type with its own methods is not searched", v: numberMethods{}},
		{name: "plain types", v: map[string][]int{}},
		{name: "nil", v: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			typ := reflect.TypeOf(tt.v)
			require.Equal(t, tt.unmarshal, needsNumber(typ, false), "unmarshal")
			require.Equal(t, tt.marshal, needsNumber(typ, true), "marshal")
			if typ != nil {
				require.Equal(t, tt.unmarshal, needsNumber(reflect.PointerTo(typ), false), "unmarshal through a pointer")
			}
		})
	}
}

// TestNumberInsideAUnionDecodes decodes a json.Number held by a type that a
// one-pass method decodes through UnmarshalDecode, whose decoder's options were
// chosen for a type without one.
func TestNumberInsideAUnionDecodes(t *testing.T) {
	t.Parallel()

	var v struct {
		Items []numberFromHolder `json:"items"`
	}
	require.NoError(t, Unmarshal([]byte(`{"items":[{"n":12345678901234567890.5}]}`), &v))
	require.Equal(t, json.Number("12345678901234567890.5"), v.Items[0].leaf.N)
}

type numberFromHolder struct {
	leaf numberLeaf
}

func (h *numberFromHolder) UnmarshalJSONFrom(dec *TextDecoder) error {
	return UnmarshalDecode(dec, &h.leaf)
}

func (h *numberFromHolder) UnmarshalJSON(b []byte) error {
	return Unmarshal(b, &h.leaf)
}
