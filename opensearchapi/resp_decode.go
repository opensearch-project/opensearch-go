// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"
	stdjson "encoding/json"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// decodeOptional decodes the next value into a pointer field as the reflective
// decoder does: null clears field; otherwise the value decodes into what field
// points to, and a nil field is first pointed at dst, even when the value turns
// out to be of the wrong type.
func decodeOptional[T any](dec *json.TextDecoder, dst *T, field **T) error {
	if json.PeekKind(dec) == 'n' {
		*field = nil
		_, err := dec.ReadToken()

		return err
	}
	if *field == nil {
		*field = dst
	}

	return json.UnmarshalDecode(dec, *field)
}

// memberName reads an object member's name and returns it unquoted. The name is
// the decoder's own bytes, good until its next read, unless it holds an escape,
// which is decoded into a copy.
func memberName(dec *json.TextDecoder) ([]byte, error) {
	v, err := dec.ReadValue()
	if err != nil {
		return nil, err
	}
	name := v[1 : len(v)-1]
	if bytes.IndexByte(name, '\\') < 0 {
		return name, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, err
	}

	return []byte(s), nil
}

// typeErrors keeps the first type error of a decode that goes on past it, as the
// reflective decoder does, leaving the value of the wrong type unset.
type typeErrors struct {
	first *stdjson.UnmarshalTypeError
}

// keep records err if it is the first type error, and returns nil for any type
// error. It returns any other error, such as a syntax error, which ends the
// decode.
func (e *typeErrors) keep(err error) error {
	if err == nil {
		return nil
	}
	te := json.AsTypeError(err)
	if te == nil {
		return err
	}
	if e.first == nil {
		e.first = te
	}

	return nil
}

// err returns the first type error kept, or nil. The error names root as the type
// decoded, as the reflective decoder names the type it was called with.
func (e *typeErrors) err(root string) error {
	if e.first == nil {
		return nil
	}
	e.first.Struct = root

	return e.first
}

// chunkStore hands out values of T from chunks, so that a response costs a few
// allocations however many values it has. The first chunk holds first values,
// the number the response has room for, or chunkFirst when that is 0; each later
// chunk is twice the last. No chunk is longer than chunkMax, so a small response
// does not pay for a large chunk, and a value kept past the response keeps at
// most chunkMax-1 others alive. A chunk is never grown, so a pointer into it
// stays valid.
type chunkStore[T any] struct {
	first int
	chunk []T
}

const (
	chunkFirst = 8
	chunkMax   = 256
)

func (st *chunkStore[T]) next() *T {
	if len(st.chunk) == cap(st.chunk) {
		size := chunkFirst
		switch {
		case st.chunk != nil:
			size = min(2*cap(st.chunk), chunkMax)
		case st.first > 0:
			size = min(st.first, chunkMax)
		}
		st.chunk = make([]T, 0, size)
	}
	st.chunk = st.chunk[:len(st.chunk)+1]

	return &st.chunk[len(st.chunk)-1]
}
