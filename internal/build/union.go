// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package build

import (
	"encoding/json"
	"sync"
)

// maxPooledKeySetSize bounds the maps [HasJSONKeys] returns to keySetPool.
// A pathological sub-response with thousands of distinct top-level keys would
// grow the probe map to match; returning that map to the pool would pin its
// backing storage for the process lifetime. Maps that exceed this many keys
// are dropped (left for GC) instead of recycled, so the pool's steady-state
// footprint stays bounded while still covering the common small-object case.
const maxPooledKeySetSize = 32

// keySetPool recycles the maps used by [HasJSONKeys] so the discriminator
// probe does not allocate a fresh map on every union sub-response decode.
// The value type is json.RawMessage so the probe accepts objects whose
// fields hold any JSON value -- a map[string]struct{} would reject scalar
// values, since a number, string, or bool cannot decode into struct{}.
var keySetPool = sync.Pool{
	New: func() any { return make(map[string]json.RawMessage, 16) },
}

// HasJSONKeys reports whether data is a JSON object containing every key in
// keys. Generated discriminated-union UnmarshalJSON methods call it to gate a
// branch on the presence of its required (discriminator) fields before
// decoding into it: encoding/json does not enforce a schema's "required" set,
// so a structurally permissive branch would otherwise absorb JSON that belongs
// to a more specific branch (e.g. an error sub-response decoding into the
// success branch). Non-object JSON (arrays, primitives, null) yields false.
//
// The probe decodes into a pooled map[string]json.RawMessage so only the
// top-level key set is needed; values are captured as raw byte slices and
// discarded.
func HasJSONKeys(data []byte, keys ...string) bool {
	if len(keys) == 0 {
		return true
	}

	fields, _ := keySetPool.Get().(map[string]json.RawMessage)
	defer func() {
		// Drop maps that ballooned past the cap: returning them would pin
		// oversized backing storage in the pool. len is read before clear,
		// which resets it to 0.
		if len(fields) > maxPooledKeySetSize {
			return
		}
		clear(fields)
		keySetPool.Put(fields)
	}()

	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	return true
}

// JSONDiscriminator reads the top-level string property named key from a JSON
// object. Generated UnmarshalJSON methods for unions that declare an OpenAPI
// discriminator call it to learn which branch the payload is before decoding,
// so exactly one branch is ever attempted.
//
// present is false when data is a JSON object that simply lacks the key, which
// the caller distinguishes from a decode failure so it can fall back to the
// discriminator's x-default branch. A non-object payload, or a key whose value
// is not a JSON string, yields an error: neither can name a branch.
func JSONDiscriminator(data []byte, key string) (string, bool, error) {
	fields, _ := keySetPool.Get().(map[string]json.RawMessage)
	defer func() {
		if len(fields) > maxPooledKeySetSize {
			return
		}
		clear(fields)
		keySetPool.Put(fields)
	}()

	if err := json.Unmarshal(data, &fields); err != nil {
		return "", false, err
	}
	raw, ok := fields[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}
