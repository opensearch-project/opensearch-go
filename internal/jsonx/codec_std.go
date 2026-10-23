// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Where the toolchain builds encoding/json/v2, encoding/json runs on it, and
// jsonx is encoding/json.

//go:build goexperiment.jsonv2

package jsonx

import (
	"bytes"
	"encoding/json"
	"io"
)

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
