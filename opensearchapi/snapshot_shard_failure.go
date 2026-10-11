// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import "encoding/json"

// UnmarshalJSON decodes a [SnapshotShardFailure], accepting shard_id as either
// a JSON number or a JSON string and storing both as the decimal string form.
//
// The server writes shard_id as an integer: SnapshotShardFailure.toXContent
// passes ShardId.id(), an int, to builder.field, and the server's own parser
// reads it back with declareInt. The spec types it as a string, so the
// generated ShardID field is a *string, and encoding/json refuses to decode a
// JSON number into one. Snapshot responses therefore failed to decode in full
// as soon as any snapshot carried a shard failure, which surfaced to callers
// as "failed to json unmarshal body" on an HTTP 200.
//
// Decoding leniently keeps ShardID a *string, so the fix ships without the
// breaking change a retype to *int32 would force. This method is a stopgap:
// delete it once the fix for opensearch-api-specification#1263 reaches the
// generator and ShardID is regenerated as an integer.
//
// MarshalJSON is deliberately left alone. SnapshotShardFailure only ever
// arrives in a response, so the asymmetry is never observable on the wire.
func (s *SnapshotShardFailure) UnmarshalJSON(data []byte) error {
	// alias is a defined type, so it carries none of SnapshotShardFailure's
	// methods and decoding it does not recurse back into this one. Embedding
	// it decodes every other field by the normal rules, including fields the
	// generator has yet to emit, instead of a hand-written field list that
	// would silently drop them. The outer ShardID is the shallower of the two,
	// so encoding/json routes shard_id to it and never to the embedded field.
	type alias SnapshotShardFailure
	tmp := struct {
		*alias
		ShardID json.RawMessage `json:"shard_id"`
	}{alias: (*alias)(s)}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	raw := tmp.ShardID
	switch {
	case len(raw) == 0:
		// shard_id absent. Leave ShardID as it was, which is what
		// encoding/json does for any key a document omits.
		return nil
	case string(raw) == "null":
		s.ShardID = nil
		return nil
	case raw[0] == '"':
		var shardID string
		if err := json.Unmarshal(raw, &shardID); err != nil {
			return err
		}

		s.ShardID = &shardID

		return nil
	default:
		// json.Number accepts any JSON number and keeps its literal text, so
		// an integer round-trips exactly. Anything else, an object, an array,
		// or a bool, fails here rather than being coerced.
		var shardID json.Number
		if err := json.Unmarshal(raw, &shardID); err != nil {
			return err
		}

		str := shardID.String()
		s.ShardID = &str

		return nil
	}
}
