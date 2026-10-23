// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package jsonx_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

type inner struct {
	A string `json:"a"`
}

type embedded struct {
	E int `json:"e,omitempty"`
}

type sample struct {
	embedded

	Name     string            `json:"name"`
	Opt      *int              `json:"opt,omitempty"`
	Empty    string            `json:"empty,omitempty"`
	Slice    []int             `json:"slice"`
	NilSlice []int             `json:"nil_slice"`
	Map      map[string]int    `json:"map,omitempty"`
	Bytes    []byte            `json:"bytes"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	Any      any               `json:"any"`
	Time     time.Time         `json:"time"`
	Duration time.Duration     `json:"duration"`
	Inner    *inner            `json:"inner,omitempty"`
	Str      int               `json:"str,string"`
	Nested   map[string]*inner `json:"nested,omitempty"`
	Skipped  int               `json:"-"`
}

// method implements both method interfaces, the way generated unions do.
type method struct {
	raw json.RawMessage
}

//nolint:unparam // The signature is json.Unmarshaler's.
func (m *method) UnmarshalJSON(b []byte) error {
	m.raw = append(m.raw[:0], b...)

	return nil
}

//nolint:unparam // The signature is json.Marshaler's.
func (m method) MarshalJSON() ([]byte, error) {
	if m.raw == nil {
		return []byte(`null`), nil
	}

	return m.raw, nil
}

type withMethod struct {
	M  method   `json:"m"`
	MP *method  `json:"mp,omitempty"`
	Ms []method `json:"ms"`
}

// TestMarshalMatchesEncodingJSON checks that jsonx writes the bytes
// encoding/json writes. Without GOEXPERIMENT=jsonv2 the two are different
// engines; with it they are one, and the test checks only jsonx's wiring.
func TestMarshalMatchesEncodingJSON(t *testing.T) {
	t.Parallel()

	one := 1
	tests := []struct {
		name string
		in   any
		// sameMeaning is set where the engines write different bytes for the same
		// JSON value, so the test compares what the bytes decode to.
		sameMeaning bool
	}{
		{name: "struct", in: sample{
			embedded: embedded{E: 3}, Name: "n", Opt: &one, Slice: []int{}, Map: map[string]int{"b": 2, "a": 1},
			Bytes: []byte("hi"), Raw: json.RawMessage(` { "x" : [1, 2] } `), Any: map[string]any{"k": []any{1.5, "s", nil}},
			Time: time.Date(2026, 10, 23, 1, 2, 3, 4, time.UTC), Duration: 90 * time.Second, Inner: &inner{A: "<&>"},
			Str: 7, Nested: map[string]*inner{"z": nil, "y": {A: "y"}},
		}},
		{name: "zero struct", in: sample{}},
		{name: "html in strings is escaped", in: map[string]string{"k": "<a href=\"x\">&amp;</a>"}},
		{name: "raw message html and whitespace", in: map[string]json.RawMessage{"r": json.RawMessage(`{"a": "<b>" }`)}},
		// The original engine writes U+FFFD for an invalid byte as a six-byte
		// \u escape, the v2 engine as the character itself.
		{name: "invalid utf-8 is replaced", in: map[string]string{"k": "a\xffb"}, sameMeaning: true},
		{name: "methods", in: withMethod{M: method{raw: json.RawMessage(`{"a":1}`)}, Ms: []method{{}, {raw: json.RawMessage(`[1]`)}}}},
		{name: "floats", in: []float64{0, 1e21, 1e-7, 123456789.125, -0.5}},
		{name: "nil map and slice", in: struct {
			M map[string]int `json:"m"`
			S []string       `json:"s"`
		}{}},
		{name: "encoding/json.Number", in: struct {
			N json.Number `json:"n"`
			E json.Number `json:"e"`
		}{N: "12345678901234567890.5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want, wantErr := json.Marshal(tt.in)
			got, err := jsonx.Marshal(tt.in)
			require.Equal(t, wantErr == nil, err == nil, "encoding/json: %v, jsonx: %v", wantErr, err)
			if tt.sameMeaning {
				var wantValue, gotValue any
				require.NoError(t, json.Unmarshal(want, &wantValue))
				require.NoError(t, json.Unmarshal(got, &gotValue))
				require.Equal(t, wantValue, gotValue, "engine %s", jsonx.Engine)

				return
			}
			require.Equal(t, string(want), string(got), "engine %s", jsonx.Engine)
		})
	}
}

// TestUnmarshalMatchesEncodingJSON checks that jsonx reads into a value what
// encoding/json reads, and fails where it fails, with an error of the type
// encoding/json returns.
func TestUnmarshalMatchesEncodingJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		into func() any
	}{
		{name: "struct", into: func() any { return &sample{} }, in: `{"e":3,"name":"n","opt":1,"slice":[],"map":{"a":1},` +
			`"bytes":"aGk=","raw":{ "x" : [1, 2] },"any":{"k":[1.5,"s",null]},"time":"2026-10-23T01:02:03.000000004Z",` +
			`"duration":90000000000,"inner":{"a":"<&>"},"str":"7","nested":{"z":null,"y":{"a":"y"}}}`},
		{name: "names match without regard to case", into: func() any { return &sample{} }, in: `{"NAME":"n","Inner":{"A":"x"}}`},
		{name: "a duplicate key keeps the last value", into: func() any { return &sample{} }, in: `{"name":"a","name":"b"}`},
		{name: "unknown fields are ignored", into: func() any { return &sample{} }, in: `{"nope":1,"name":"n"}`},
		{name: "null leaves a value alone", into: func() any { return &sample{Name: "kept"} }, in: `{"name":null}`},
		{name: "invalid utf-8 is replaced", into: func() any { return &sample{} }, in: "{\"name\":\"a\xffb\"}"},
		{name: "numbers into any are float64", into: func() any { var v any; return &v }, in: `[1, 2.5, 1e300, -0]`},
		{name: "methods get the raw value", into: func() any { return &withMethod{} }, in: `{"m": {"a" : 1}, "mp":[1], "ms":[null,{"b":2}]}`},
		{name: "a type mismatch fails", into: func() any { return &sample{} }, in: `{"name":1}`},
		{name: "a syntax error fails", into: func() any { return &sample{} }, in: `{"name":`},
		{name: "decoding into nil fails", into: func() any { return nil }, in: `{}`},
		{name: "decoding into a non-pointer fails", into: func() any { return sample{} }, in: `{}`},
		{name: "trailing data fails", into: func() any { return &sample{} }, in: `{} {}`},
		{name: "a string tag on a non-string fails", into: func() any { return &sample{} }, in: `{"str":7}`},
		// A caller's own type can use encoding/json.Number, which encoding/json
		// decodes a number into as its literal text.
		{name: "encoding/json.Number", into: func() any {
			return &struct {
				N json.Number   `json:"n"`
				A []json.Number `json:"a"`
			}{}
		}, in: `{"n":12345678901234567890.5,"a":[1,-2e3]}`},
		{name: "encoding/json.Number from a string fails", into: func() any {
			return &struct {
				N json.Number `json:"n"`
			}{}
		}, in: `{"n":"x"}`},
		{name: "null leaves an encoding/json.Number alone", into: func() any {
			return &struct {
				N json.Number `json:"n"`
			}{N: "5"}
		}, in: `{"n":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want, got := tt.into(), tt.into()
			wantErr := json.Unmarshal([]byte(tt.in), want)
			err := jsonx.Unmarshal([]byte(tt.in), got)
			require.Equal(t, wantErr == nil, err == nil, "encoding/json: %v, jsonx: %v", wantErr, err)
			if wantErr != nil {
				requireSameErrorType(t, wantErr, err)

				return
			}
			require.True(t, reflect.DeepEqual(want, got), "engine %s:\nencoding/json: %#v\njsonx:         %#v", jsonx.Engine, want, got)
		})
	}
}

// failingMarshaler is a Marshaler whose MarshalJSON fails.
type failingMarshaler struct{}

func (failingMarshaler) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

// TestMarshalErrorsAreEncodingJSONs checks that jsonx fails to write what
// encoding/json fails to write, with an error of the type encoding/json
// returns.
func TestMarshalErrorsAreEncodingJSONs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
	}{
		{name: "an unsupported type", in: map[string]any{"c": make(chan int)}},
		{name: "an unsupported value", in: []float64{math.NaN()}},
		{name: "a failing MarshalJSON", in: []any{failingMarshaler{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, wantErr := json.Marshal(tt.in)
			_, err := jsonx.Marshal(tt.in)
			require.Error(t, wantErr)
			require.Error(t, err)
			requireSameErrorType(t, wantErr, err)
		})
	}
}

// requireSameErrorType checks that, where encoding/json fails with one of its
// exported error types, jsonx fails with the same type and the fields a caller
// reads. Where encoding/json's error is untyped there is nothing for a caller
// to match, and any error will do. Offsets are left out of a type error: the
// engines stop at different points of the same value.
func requireSameErrorType(t *testing.T, want, got error) {
	t.Helper()

	switch w := want.(type) { //nolint:errorlint // want is encoding/json's own, unwrapped error.
	case *json.UnmarshalTypeError:
		var g *json.UnmarshalTypeError
		require.ErrorAs(t, got, &g)
		require.Equal(t, w.Type, g.Type)
		require.Equal(t, w.Field, g.Field)
	case *json.SyntaxError:
		var g *json.SyntaxError
		require.ErrorAs(t, got, &g)
		require.Equal(t, w.Error(), g.Error())
		require.Equal(t, w.Offset, g.Offset)
	case *json.MarshalerError:
		var g *json.MarshalerError
		require.ErrorAs(t, got, &g)
		// The copied engine calls the method through a pointer, and names the
		// pointer type where encoding/json may name the value's.
		gotType := g.Type
		if gotType.Kind() == reflect.Pointer && w.Type.Kind() != reflect.Pointer {
			gotType = gotType.Elem()
		}
		require.Equal(t, w.Type, gotType)
		require.Equal(t, w.Err.Error(), g.Err.Error())
	case *json.InvalidUnmarshalError, *json.UnsupportedTypeError, *json.UnsupportedValueError:
		require.Equal(t, reflect.TypeOf(want), reflect.TypeOf(got), "jsonx: %v", got)
		require.Equal(t, want.Error(), got.Error())
	}
}
