// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Where the toolchain builds encoding/json/v2, encoding/json runs on it, and
// jsonx is encoding/json. The go1.27 constraint, besides naming the release
// that builds it by default, lets this file use the encoding/json/v2 API that
// Go 1.27 publishes; Go 1.26 with GOEXPERIMENT=jsonv2 runs the copy instead.

//go:build goexperiment.jsonv2 && go1.27

package jsonx

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
)

// TextDecoder is the encoding/json/jsontext.Decoder the engine passes to an
// UnmarshalJSONFrom method.
type TextDecoder = jsontext.Decoder

// TextValue is encoding/json/jsontext.Value.
type TextValue = jsontext.Value

// UnmarshalDecode decodes the next value of dec into v with dec's options. Call
// it from an UnmarshalJSONFrom method.
func UnmarshalDecode(dec *TextDecoder, v any) error {
	return jsonv2.UnmarshalDecode(dec, v)
}

// NewTextDecoder returns a decoder that reads values from r with
// encoding/json's semantics.
func NewTextDecoder(r io.Reader) *TextDecoder {
	return jsontext.NewDecoder(r, json.DefaultOptionsV1())
}

// Engine names the JSON engine jsonx runs on: the standard library's
// encoding/json/v2.
const Engine = "encoding/json/v2"

// Marshal is encoding/json.Marshal.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal is encoding/json.Unmarshal.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Encoder is encoding/json.Encoder.
type Encoder = json.Encoder

// NewEncoder is encoding/json.NewEncoder.
func NewEncoder(w io.Writer) *Encoder {
	return json.NewEncoder(w)
}

// Decoder is encoding/json.Decoder.
type Decoder = json.Decoder

// NewDecoder is encoding/json.NewDecoder.
func NewDecoder(r io.Reader) *Decoder {
	return json.NewDecoder(r)
}

// Indent is encoding/json.Indent.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	return json.Indent(dst, src, prefix, indent)
}

// Valid is encoding/json.Valid.
func Valid(data []byte) bool {
	return json.Valid(data)
}
