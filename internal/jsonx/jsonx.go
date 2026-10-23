// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package jsonx is the client's JSON codec: the encoding/json API the client
// and its generated code use, run on the encoding/json/v2 engine whatever the
// toolchain.
//
// Where the toolchain builds encoding/json/v2, which Go 1.27 does by default
// and Go 1.26 does with GOEXPERIMENT=jsonv2, encoding/json already runs on that
// engine, and jsonx is encoding/json (codec_std.go). Elsewhere jsonx runs a
// copy of the standard library's encoding/json/v2 from internal/third_party/
// jsonv2 (codec_copy.go). On Go 1.26 without the experiment, the original
// encoding/json engine makes about 2.7 times the allocations decoding an mget
// response that the v2 engine does. Once the module requires a toolchain that
// builds encoding/json/v2, codec_copy.go and the copy can be deleted.
//
// Every function has encoding/json's semantics, so what the client sends and
// how it reads a response do not depend on the toolchain. Errors are
// encoding/json's types, so a caller's errors.As matches them under every
// engine. Under codec_copy.go three details differ from Go 1.26's
// encoding/json: a Go string holding invalid UTF-8 is written with U+FFFD as
// the character rather than as a \u escape, as encoding/json does from Go
// 1.27; where encoding/json fails with an untyped error, such as for a
// ",string" field holding an unquoted value, jsonx fails with a typed one; and
// a MarshalerError may name the pointer type of a value whose method failed.
//
// RawMessage, Marshaler and Unmarshaler are aliases of encoding/json's, so a
// type that uses them is the same type under every engine.
package jsonx

import "encoding/json"

// RawMessage is encoding/json.RawMessage.
type RawMessage = json.RawMessage

// Marshaler is encoding/json.Marshaler.
type Marshaler = json.Marshaler

// Unmarshaler is encoding/json.Unmarshaler.
type Unmarshaler = json.Unmarshaler
