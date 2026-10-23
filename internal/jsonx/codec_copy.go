// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Where the toolchain does not build encoding/json/v2, encoding/json is the
// original engine, so jsonx runs the copy of encoding/json/v2 in
// internal/third_party/jsonv2.

//go:build !goexperiment.jsonv2

package jsonx

import (
	"encoding/json"
	"errors"

	jsonv2 "github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/json"
	"github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/jsontext"
	jsonv1 "github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/v1"
)

// Engine names the JSON engine jsonx runs on: the copy of the standard
// library's encoding/json/v2 in internal/third_party/jsonv2.
const Engine = "internal/third_party/jsonv2"

// options are encoding/json's semantics on this engine: jsonv1's
// DefaultOptionsV1, the options encoding/json passes when it runs on the
// encoding/json/v2 engine, with one addition. The copy's jsonv1 is a package of
// its own, so the engine knows jsonv1.Number, not encoding/json.Number, and
// would write and read the latter as a plain string; encoding/json.Number goes
// through jsonv1.Number's methods.
var options = jsonv2.JoinOptions(
	jsonv1.DefaultOptionsV1(),
	jsonv2.WithMarshalers(jsonv2.MarshalToFunc(func(enc *jsontext.Encoder, n json.Number) error {
		return jsonv1.Number(n).MarshalJSONTo(enc)
	})),
	jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, n *json.Number) error {
		// Start from the current value, which a null leaves in place under
		// encoding/json's merge semantics.
		m := jsonv1.Number(*n)
		if err := m.UnmarshalJSONFrom(dec); err != nil {
			return err
		}
		*n = json.Number(m)

		return nil
	})),
)

// Marshal is encoding/json.Marshal, run on the encoding/json/v2 engine.
func Marshal(v any) ([]byte, error) {
	b, err := jsonv2.Marshal(v, options)
	if err != nil {
		return nil, stdError(err, nil)
	}

	return b, nil
}

// Unmarshal is encoding/json.Unmarshal, run on the encoding/json/v2 engine.
func Unmarshal(data []byte, v any) error {
	if err := jsonv2.Unmarshal(data, v, options); err != nil {
		return stdError(err, data)
	}

	return nil
}

// stdError returns err as the encoding/json error it stands for, so that a
// caller's errors.As finds the type encoding/json would have returned. The
// engine reports errors in jsonv1's types, which have the fields of
// encoding/json's but are other types. A syntax error cannot be built from
// outside encoding/json, whose SyntaxError keeps its message unexported, so
// encoding/json reports it, from data. err is returned as it is when it is
// none of these, such as an error a caller's method returned. Where
// encoding/json returns an untyped error, such as for a ",string" field holding
// an unquoted value, this engine's typed error is returned: a caller can match
// it no worse than encoding/json's.
func stdError(err error, data []byte) error {
	var (
		typeErr        *jsonv1.UnmarshalTypeError
		invalidErr     *jsonv1.InvalidUnmarshalError
		syntaxErr      *jsonv1.SyntaxError
		marshalerErr   *jsonv1.MarshalerError
		unsupportedTyp *jsonv1.UnsupportedTypeError
		unsupportedVal *jsonv1.UnsupportedValueError
	)
	switch {
	case errors.As(err, &typeErr):
		return &json.UnmarshalTypeError{
			Value: typeErr.Value, Type: typeErr.Type, Offset: typeErr.Offset, Struct: typeErr.Struct, Field: typeErr.Field,
		}
	case errors.As(err, &invalidErr):
		return &json.InvalidUnmarshalError{Type: invalidErr.Type}
	case errors.As(err, &syntaxErr):
		// encoding/json.Unmarshal checks the whole input's syntax before it
		// decodes anything, so for input this engine found invalid it returns
		// its SyntaxError, at the offset it reports, having decoded nothing.
		if data != nil {
			var discard struct{}
			var serr *json.SyntaxError
			if uerr := json.Unmarshal(data, &discard); errors.As(uerr, &serr) {
				return serr
			}
		}
	case errors.As(err, &marshalerErr):
		return &json.MarshalerError{Type: marshalerErr.Type, Err: marshalerErr.Err}
	case errors.As(err, &unsupportedTyp):
		return &json.UnsupportedTypeError{Type: unsupportedTyp.Type}
	case errors.As(err, &unsupportedVal):
		return &json.UnsupportedValueError{Value: unsupportedVal.Value, Str: unsupportedVal.Str}
	}

	return err
}
