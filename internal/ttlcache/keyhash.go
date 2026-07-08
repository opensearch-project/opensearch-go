// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ttlcache

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
)

// KeyBuilder accumulates a stable cache Key from primitive field values. A
// caller that can key an item feeds its hashable fields in through the typed
// methods and reads the result with Key. It keeps the hashing primitive out of
// callers (which would otherwise inline an fnv hasher and re-derive the framing
// rules), while the field selection stays with the caller that owns the type.
//
// Every write is length-prefixed (fixed-width values are self-delimiting), so
// two items whose fields concatenate to the same bytes under different
// boundaries still hash differently: String("ab")+String("c") and
// String("a")+String("bc") do not collide. The methods chain.
type KeyBuilder struct {
	h hash.Hash64
}

// NewKeyBuilder returns a KeyBuilder backed by a fresh FNV-1a 64-bit hasher.
func NewKeyBuilder() *KeyBuilder {
	return &KeyBuilder{h: fnv.New64a()}
}

// String writes a length-prefixed string.
func (b *KeyBuilder) String(s string) *KeyBuilder {
	b.writeLen(len(s))
	_, _ = b.h.Write([]byte(s))
	return b
}

// Bytes writes a length-prefixed byte slice.
func (b *KeyBuilder) Bytes(p []byte) *KeyBuilder {
	b.writeLen(len(p))
	_, _ = b.h.Write(p)
	return b
}

// Int writes a fixed-width (8-byte) integer. Negative values are reinterpreted
// bit-for-bit, so the sign is preserved in the hash.
func (b *KeyBuilder) Int(i int64) *KeyBuilder {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(i)) //nolint:gosec // G115: bit reinterpretation for hashing; value semantics irrelevant
	_, _ = b.h.Write(n[:])
	return b
}

// Bool writes a single distinguishing byte for the boolean.
func (b *KeyBuilder) Bool(v bool) *KeyBuilder {
	if v {
		return b.Int(1)
	}
	return b.Int(0)
}

// Key returns the accumulated Key. Further writes continue from the same state.
func (b *KeyBuilder) Key() Key {
	return Key(b.h.Sum64()) //nolint:gosec // G115: key is a hash; the bit pattern is the identity, not a magnitude
}

// writeLen prefixes a variable-length write with its fixed-width length so
// field boundaries are unambiguous.
func (b *KeyBuilder) writeLen(n int) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(n)) //nolint:gosec // G115: length is non-negative
	_, _ = b.h.Write(buf[:])
}
