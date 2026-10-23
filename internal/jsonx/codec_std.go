// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Where the toolchain builds encoding/json/v2, encoding/json runs on it, and
// jsonx is encoding/json.

//go:build goexperiment.jsonv2

package jsonx

import "encoding/json"

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
