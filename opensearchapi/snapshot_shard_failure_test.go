// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func TestSnapshotShardFailure_Unmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantShardID string
		wantNilID   bool
	}{
		{
			name: "shard_id as a number, which is what the server sends",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"shard_id": 0,
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantShardID: "0",
		},
		{
			name: "shard_id as a string, which the spec declares",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"shard_id": "0",
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantShardID: "0",
		},
		{
			name: "non-zero shard_id as a number",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"shard_id": 3,
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantShardID: "3",
		},
		{
			name: "non-zero shard_id as a string",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"shard_id": "3",
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantShardID: "3",
		},
		{
			name: "shard_id absent",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantNilID: true,
		},
		{
			name: "shard_id null",
			input: `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				"shard_id": null,
				"status": "INTERNAL_SERVER_ERROR"
			}`,
			wantNilID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var failure opensearchapi.SnapshotShardFailure
			require.NoError(t, json.Unmarshal([]byte(tt.input), &failure))

			if tt.wantNilID {
				require.Nil(t, failure.ShardID)
			} else {
				require.NotNil(t, failure.ShardID, "expected shard_id to be decoded")
				require.Equal(t, tt.wantShardID, *failure.ShardID)
			}

			// The embedded alias decodes every other field, so none of them
			// may be lost to the shard_id handling.
			require.NotNil(t, failure.Index)
			require.Equal(t, "test-index", *failure.Index)
			require.NotNil(t, failure.NodeID)
			require.Equal(t, "lL6ZrGWGRHWiPXhAyNrPaw", *failure.NodeID)
			require.NotNil(t, failure.Reason)
			require.Equal(t, "IndexShardSnapshotFailedException[Aborted]", *failure.Reason)
			require.NotNil(t, failure.Status)
			require.Equal(t, "INTERNAL_SERVER_ERROR", *failure.Status)
		})
	}
}

// TestSnapshotShardFailure_UnmarshalInvalid covers shard_id values that are
// neither a number nor a string. Lenient decoding does not mean coercing
// anything at all into the field: these still fail, so a genuinely malformed
// response is reported instead of being read as a nonsense shard ID.
func TestSnapshotShardFailure_UnmarshalInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "shard_id as an object", input: `{"index": "test-index", "shard_id": {"id": 0}}`},
		{name: "shard_id as an array", input: `{"index": "test-index", "shard_id": [0]}`},
		{name: "shard_id as a bool", input: `{"index": "test-index", "shard_id": true}`},
		{name: "shard_id as invalid JSON", input: `{"index": "test-index", "shard_id": }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var failure opensearchapi.SnapshotShardFailure
			require.Error(t, json.Unmarshal([]byte(tt.input), &failure))
		})
	}
}

// TestSnapshotShardFailure_UnmarshalUnknownField pins tolerance of a key the
// Go type does not declare. index_uuid is the case that matters: the spec fix
// adds it to this object, so the client will gain the field on its next
// regeneration. Until it does, this test can only assert that the key is
// ignored exactly as encoding/json ignores any unknown key and that it
// disturbs nothing else; it does not and cannot yet prove the field decodes.
// What will decode it is the embedded alias, which defers every field but
// shard_id to the normal rules, where a hand-written field list would have to
// be extended by hand.
func TestSnapshotShardFailure_UnmarshalUnknownField(t *testing.T) {
	t.Parallel()

	input := `{
		"index": "test-index",
		"index_uuid": "A5RlvdBZTGujWjSjEVzHlA",
		"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
		"reason": "IndexShardSnapshotFailedException[Aborted]",
		"shard_id": 2,
		"status": "INTERNAL_SERVER_ERROR"
	}`

	var failure opensearchapi.SnapshotShardFailure
	require.NoError(t, json.Unmarshal([]byte(input), &failure))

	require.NotNil(t, failure.ShardID)
	require.Equal(t, "2", *failure.ShardID)
	require.NotNil(t, failure.Index)
	require.Equal(t, "test-index", *failure.Index)
	require.NotNil(t, failure.NodeID)
	require.Equal(t, "lL6ZrGWGRHWiPXhAyNrPaw", *failure.NodeID)
	require.NotNil(t, failure.Reason)
	require.Equal(t, "IndexShardSnapshotFailedException[Aborted]", *failure.Reason)
	require.NotNil(t, failure.Status)
	require.Equal(t, "INTERNAL_SERVER_ERROR", *failure.Status)
}

// TestSnapshotShardFailure_Marshal pins the encoding side, which is left as
// the generated struct tags define it. SnapshotShardFailure is response-only,
// so shard_id going back out as a string is never seen by the server.
func TestSnapshotShardFailure_Marshal(t *testing.T) {
	t.Parallel()

	index, shardID := "test-index", "0"
	data, err := json.Marshal(opensearchapi.SnapshotShardFailure{Index: &index, ShardID: &shardID})
	require.NoError(t, err)
	require.JSONEq(t, `{"index": "test-index", "shard_id": "0"}`, string(data))

	// Omitted fields stay omitted.
	data, err = json.Marshal(opensearchapi.SnapshotShardFailure{})
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
}

// TestSnapshotGetResp_UnmarshalShardFailure is the regression test for the
// reported bug. A shard failure anywhere in the repository used to fail the
// whole Get Snapshot response, because the number in shard_id could not go
// into a *string, and the caller saw "failed to json unmarshal body" on an
// HTTP 200. The payload is a trimmed real response, with the failing snapshot
// first and a healthy one after it to show the rest of the list still decodes.
func TestSnapshotGetResp_UnmarshalShardFailure(t *testing.T) {
	t.Parallel()

	input := `{
		"snapshots": [
			{
				"snapshot": "snapshot-with-failure",
				"uuid": "mB4XJNZESGm3pQ9Vn5sWtQ",
				"state": "PARTIAL",
				"indices": ["test-index"],
				"failures": [
					{
						"index": "test-index",
						"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
						"reason": "IndexShardSnapshotFailedException[Aborted]",
						"shard_id": 0,
						"status": "INTERNAL_SERVER_ERROR"
					},
					{
						"index": "test-index",
						"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
						"reason": "IndexShardSnapshotFailedException[Aborted]",
						"shard_id": 1,
						"status": "INTERNAL_SERVER_ERROR"
					}
				],
				"shards": {"total": 2, "failed": 2, "successful": 0}
			},
			{
				"snapshot": "healthy-snapshot",
				"uuid": "rT8kLpQwSQ2vXc7Hd3mYbA",
				"state": "SUCCESS",
				"indices": ["test-index"],
				"failures": [],
				"shards": {"total": 2, "failed": 0, "successful": 2}
			}
		]
	}`

	var resp opensearchapi.SnapshotGetResp
	require.NoError(t, json.Unmarshal([]byte(input), &resp))

	require.Len(t, resp.Snapshots, 2)

	failed := resp.Snapshots[0]
	require.NotNil(t, failed.Snapshot)
	require.Equal(t, "snapshot-with-failure", *failed.Snapshot)
	require.NotNil(t, failed.State)
	require.Equal(t, "PARTIAL", *failed.State)
	require.Len(t, failed.Failures, 2)

	for i, want := range []string{"0", "1"} {
		require.NotNil(t, failed.Failures[i].ShardID)
		require.Equal(t, want, *failed.Failures[i].ShardID)
		require.NotNil(t, failed.Failures[i].Index)
		require.Equal(t, "test-index", *failed.Failures[i].Index)
		require.NotNil(t, failed.Failures[i].Status)
		require.Equal(t, "INTERNAL_SERVER_ERROR", *failed.Failures[i].Status)
	}

	healthy := resp.Snapshots[1]
	require.NotNil(t, healthy.Snapshot)
	require.Equal(t, "healthy-snapshot", *healthy.Snapshot)
	require.Empty(t, healthy.Failures)
}
