// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package jsonx

import "bytes"

// PeekKind returns the kind of dec's next value: '{', '[', '"', '0' for a
// number, 't', 'f' or 'n', or 0 when there is none or it is invalid, in which
// case the next read reports why.
func PeekKind(dec *TextDecoder) byte {
	return byte(dec.PeekKind())
}

// Mark is a position in a decoder's input, taken just before a value, from
// which Consumed returns the value's bytes once it has been decoded.
//
// An UnmarshalJSONFrom method takes a Mark, decodes its value in one pass, and
// keeps Consumed as the value's raw bytes, where an UnmarshalJSON method would
// keep the bytes it is given: the bytes are a slice of the decoder's buffer, not
// a copy. Decoding a []byte, the buffer is that slice, so they stay valid as
// long as it does, as the bytes an UnmarshalJSON method keeps do. A decoder
// reading an io.Reader reuses its buffer, so bytes kept from it, by either
// method, become invalid.
type Mark struct {
	before []byte
	start  int64
}

// MarkOf returns the position of dec's next value.
func MarkOf(dec *TextDecoder) Mark {
	return Mark{before: dec.UnreadBuffer(), start: dec.InputOffset()}
}

// Consumed returns the bytes of the value dec decoded since m was taken, without
// the separator and whitespace before it. It returns nil when they are no longer
// in the buffer m saw, which happens only to a decoder reading an io.Reader that
// refilled its buffer mid-value.
func (m Mark) Consumed(dec *TextDecoder) RawMessage {
	n := dec.InputOffset() - m.start
	after := dec.UnreadBuffer()
	if n <= 0 || n > int64(len(m.before)) || len(after) != len(m.before)-int(n) {
		return nil
	}
	if len(after) > 0 && &after[0] != &m.before[n] {
		return nil
	}

	return bytes.TrimLeft(m.before[:n], " \t\r\n,:")
}
