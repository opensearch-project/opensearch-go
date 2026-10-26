// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Where the toolchain is older than Go 1.27, or does not build
// encoding/json/v2, jsonx runs the copy of encoding/json/v2 in
// internal/third_party/jsonv2.

//go:build !goexperiment.jsonv2 || !go1.27

package jsonx

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"

	jsonv2 "github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/json"
	"github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/jsontext"
	jsonv1 "github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2/v1"
)

// Engine names the JSON engine jsonx runs on: the copy of the standard
// library's encoding/json/v2 in internal/third_party/jsonv2.
const Engine = "internal/third_party/jsonv2"

// options are encoding/json's semantics on this engine: jsonv1's
// DefaultOptionsV1, the options encoding/json passes when it runs on the
// encoding/json/v2 engine.
//
// The copy's jsonv1 is a package of its own, so the engine knows jsonv1.Number,
// not encoding/json.Number, and would write and read the latter as a plain
// string. numberOptions add marshal and unmarshal functions that send
// encoding/json.Number through jsonv1.Number's methods. They cost every value
// the engine visits a lookup, about 10% of a decode, so they are passed only for
// a value whose type can hold an encoding/json.Number; see needsNumber.
var (
	options       = jsonv1.DefaultOptionsV1()
	numberOptions = jsonv2.JoinOptions(
		options,
		jsonv2.WithMarshalers(jsonv2.MarshalToFunc(func(enc *jsontext.Encoder, n json.Number) error {
			return jsonv1.Number(n).MarshalJSONTo(enc)
		})),
		jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, n *json.Number) error {
			// Start from the current value, which a null leaves in place under
			// encoding/json's merge semantics.
			m := jsonv1.Number(*n)
			if err := m.UnmarshalJSONFrom(dec); err != nil {
				// Name the caller's type in a type error, as encoding/json does.
				var semErr *jsonv2.SemanticError
				if errors.As(err, &semErr) && semErr.GoType == copyNumberType {
					semErr.GoType = stdNumberType
				}

				return err
			}
			*n = json.Number(m)

			return nil
		})),
	)
	copyNumberType = reflect.TypeFor[jsonv1.Number]()
	stdNumberType  = reflect.TypeFor[json.Number]()
)

// optionsFor returns the options to marshal (or unmarshal, when marshal is
// false) a value of v's type with.
func optionsFor(v any, marshal bool) jsonv2.Options {
	if needsNumber(reflect.TypeOf(v), marshal) {
		return numberOptions
	}

	return options
}

// needsNumberCache holds needsNumber's answers, by direction and type.
var needsNumberCache [2]sync.Map

// needsNumber reports whether marshaling (or unmarshaling) a value of type t
// can meet an encoding/json.Number. Unmarshaling, an interface never does, as
// encoding/json decodes a number into one as a float64; marshaling, one may
// hold a Number. A type with its own JSON method is not searched: the method
// encodes or decodes through jsonx, which decides again for what it holds.
func needsNumber(t reflect.Type, marshal bool) bool {
	if t == nil {
		return false
	}
	i := 0
	if marshal {
		i = 1
	}
	if v, ok := needsNumberCache[i].Load(t); ok {
		return v.(bool) //nolint:forcetypeassert // The cache holds only bools.
	}
	v := searchNumber(t, marshal, make(map[reflect.Type]struct{}))
	needsNumberCache[i].Store(t, v)

	return v
}

var (
	marshalerTypes = []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[jsonv2.MarshalerTo](), reflect.TypeFor[encoding.TextMarshaler](),
	}
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[jsonv2.UnmarshalerFrom](), reflect.TypeFor[encoding.TextUnmarshaler](),
	}
)

func searchNumber(t reflect.Type, marshal bool, seen map[reflect.Type]struct{}) bool {
	if t == stdNumberType {
		return true
	}
	if _, ok := seen[t]; ok {
		return false
	}
	seen[t] = struct{}{}

	methods := unmarshalerTypes
	if marshal {
		methods = marshalerTypes
	}
	for _, m := range methods {
		if t.Implements(m) || reflect.PointerTo(t).Implements(m) {
			return false
		}
	}

	switch t.Kind() { //nolint:exhaustive // Every other kind holds no Number.
	case reflect.Interface:
		return marshal
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return searchNumber(t.Elem(), marshal, seen)
	case reflect.Map:
		return searchNumber(t.Key(), marshal, seen) || searchNumber(t.Elem(), marshal, seen)
	case reflect.Struct:
		for f := range t.Fields() {
			if (f.IsExported() || f.Anonymous) && searchNumber(f.Type, marshal, seen) {
				return true
			}
		}
	}

	return false
}

// TextDecoder is the copy's jsontext.Decoder, which the engine passes to an
// UnmarshalJSONFrom method.
type TextDecoder = jsontext.Decoder

// TextValue is the copy's jsontext.Value.
type TextValue = jsontext.Value

// AsTypeError returns the error err holds for a value of the wrong type as an
// encoding/json.UnmarshalTypeError, or nil. Unmarshal reports it in that type;
// UnmarshalDecode reports it in the copy's jsonv1 type, which this converts as
// Unmarshal does.
func AsTypeError(err error) *json.UnmarshalTypeError {
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return te
	}
	if _, ok := errors.AsType[*jsonv1.UnmarshalTypeError](err); !ok {
		return nil
	}
	te, _ := errors.AsType[*json.UnmarshalTypeError](stdError(err, nil))

	return te
}

// UnmarshalDecode decodes the next value of dec into v with dec's options. Call
// it from an UnmarshalJSONFrom method. When v's type can hold an
// encoding/json.Number that dec's options were not chosen for, it adds the
// options that decode one.
func UnmarshalDecode(dec *TextDecoder, v any) error {
	if needsNumber(reflect.TypeOf(v), false) {
		return jsonv2.UnmarshalDecode(dec, v, numberOptions)
	}

	return jsonv2.UnmarshalDecode(dec, v)
}

// NewTextDecoder returns a decoder that reads values from r with
// encoding/json's semantics.
func NewTextDecoder(r io.Reader) *TextDecoder {
	return jsontext.NewDecoder(r, numberOptions)
}

// TextEncoder is the copy's jsontext.Encoder, which the engine passes to a
// MarshalJSONTo method.
type TextEncoder = jsontext.Encoder

// MarshalEncode writes v to enc with enc's options. Call it from a
// MarshalJSONTo method. When v's type can hold an encoding/json.Number that
// enc's options were not chosen for, it adds the options that encode one.
func MarshalEncode(enc *TextEncoder, v any) error {
	if needsNumber(reflect.TypeOf(v), true) {
		return jsonv2.MarshalEncode(enc, v, numberOptions)
	}

	return jsonv2.MarshalEncode(enc, v)
}

// MarshalWrite writes v to w as encoding/json.Marshal encodes it, with no
// trailing newline, without holding the encoding in a buffer of its own.
func MarshalWrite(w io.Writer, v any) error {
	if err := jsonv2.MarshalWrite(w, v, optionsFor(v, true)); err != nil {
		return stdError(err, nil)
	}

	return nil
}

// WriteString writes s to enc as a JSON string.
func WriteString(enc *TextEncoder, s string) error {
	return enc.WriteToken(jsontext.String(s))
}

// Marshal is encoding/json.Marshal, run on the encoding/json/v2 engine.
func Marshal(v any) ([]byte, error) {
	b, err := jsonv2.Marshal(v, optionsFor(v, true))
	if err != nil {
		return nil, stdError(err, nil)
	}

	return b, nil
}

// Unmarshal is encoding/json.Unmarshal, run on the encoding/json/v2 engine.
func Unmarshal(data []byte, v any) error {
	if err := jsonv2.Unmarshal(data, v, optionsFor(v, false)); err != nil {
		return stdError(err, data)
	}

	return nil
}

// Encoder is encoding/json.Encoder's Encode and SetEscapeHTML, run on the
// encoding/json/v2 engine. Each Encode writes one value and a newline. The
// copy's jsonv1.Encoder is not used, since it does not take options and so
// would write encoding/json.Number as a string.
//
// As encoding/json's does, each Encode encodes into a buffer of the Encoder's
// own and gives the writer the whole value in one Write. A jsontext.Encoder on
// the caller's writer would not do: on a *bytes.Buffer it appends into the
// buffer's spare capacity and keeps its own length, so bytes the caller writes
// to the buffer between values, such as the commas of an array, are
// overwritten.
type Encoder struct {
	w        io.Writer
	buf      bytes.Buffer
	enc      *jsontext.Encoder
	keepHTML bool
}

// NewEncoder is encoding/json.NewEncoder.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// SetEscapeHTML is encoding/json.Encoder.SetEscapeHTML: whether &, < and > in
// strings are escaped. They are by default.
func (e *Encoder) SetEscapeHTML(on bool) {
	if e.keepHTML == !on {
		return
	}
	e.keepHTML = !on
	e.enc = nil
}

// Encode is encoding/json.Encoder.Encode.
func (e *Encoder) Encode(v any) error {
	e.buf.Reset()
	opts := optionsFor(v, true)
	if e.enc == nil {
		e.enc = jsontext.NewEncoder(&e.buf, opts, jsontext.EscapeForHTML(!e.keepHTML))
	} else {
		e.enc.Reset(&e.buf, opts, jsontext.EscapeForHTML(!e.keepHTML))
	}
	if err := jsonv2.MarshalEncode(e.enc, v); err != nil {
		return stdError(err, nil)
	}
	_, err := e.w.Write(e.buf.Bytes())

	return err
}

// Decoder is encoding/json.Decoder's Decode, run on the encoding/json/v2
// engine: each Decode reads the next value in the stream into v.
type Decoder struct {
	dec *jsontext.Decoder
}

// NewDecoder is encoding/json.NewDecoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: jsontext.NewDecoder(r, options)}
}

// Decode is encoding/json.Decoder.Decode. It returns io.EOF at the end of the
// stream, and io.ErrUnexpectedEOF for a stream that ends inside a value, as
// encoding/json's does.
func (d *Decoder) Decode(v any) error {
	b, err := d.dec.ReadValue()
	switch {
	case err == nil:
		return Unmarshal(b, v)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return io.ErrUnexpectedEOF
	default:
		return err
	}
}

// Indent is encoding/json.Indent.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	return stdError(jsonv1.Indent(dst, src, prefix, indent), src)
}

// Valid is encoding/json.Valid, run on the encoding/json/v2 engine.
func Valid(data []byte) bool {
	return jsonv1.Valid(data)
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
