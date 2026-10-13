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
		shardID     string // shard_id member, verbatim; empty omits the key
		extra       string // further members, verbatim
		wantShardID *string
	}{
		{name: "shard_id as a number, which is what the server sends", shardID: `"shard_id": 0,`, wantShardID: new("0")},
		{name: "shard_id as a string, which the spec declares", shardID: `"shard_id": "0",`, wantShardID: new("0")},
		{name: "non-zero shard_id as a number", shardID: `"shard_id": 3,`, wantShardID: new("3")},
		{name: "non-zero shard_id as a string", shardID: `"shard_id": "3",`, wantShardID: new("3")},
		{name: "shard_id absent"},
		{name: "shard_id null", shardID: `"shard_id": null,`},
		{
			// The spec fix adds index_uuid, which the Go type does not declare
			// yet. It must be ignored without disturbing any other field.
			name:        "undeclared index_uuid alongside a numeric shard_id",
			shardID:     `"shard_id": 2,`,
			extra:       `"index_uuid": "A5RlvdBZTGujWjSjEVzHlA",`,
			wantShardID: new("2"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := `{
				"index": "test-index",
				"node_id": "lL6ZrGWGRHWiPXhAyNrPaw",
				"reason": "IndexShardSnapshotFailedException[Aborted]",
				` + tt.shardID + tt.extra + `
				"status": "INTERNAL_SERVER_ERROR"
			}`

			var failure opensearchapi.SnapshotShardFailure
			require.NoError(t, json.Unmarshal([]byte(input), &failure))
			require.Equal(t, tt.wantShardID, failure.ShardID)

			// The embedded alias decodes every other field, so none of them
			// may be lost to the shard_id handling.
			require.Equal(t, opensearchapi.SnapshotShardFailure{
				Index:   new("test-index"),
				NodeID:  new("lL6ZrGWGRHWiPXhAyNrPaw"),
				Reason:  new("IndexShardSnapshotFailedException[Aborted]"),
				ShardID: tt.wantShardID,
				Status:  new("INTERNAL_SERVER_ERROR"),
			}, failure)
		})
	}
}

// TestSnapshotShardFailure_UnmarshalInvalid covers shard_id values that are
// not an integer or a string. These fail, so a malformed response is reported
// instead of being read as a nonsense shard ID.
func TestSnapshotShardFailure_UnmarshalInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "shard_id as an object", input: `{"index": "test-index", "shard_id": {"id": 0}}`},
		{name: "shard_id as an array", input: `{"index": "test-index", "shard_id": [0]}`},
		{name: "shard_id as a bool", input: `{"index": "test-index", "shard_id": true}`},
		{name: "shard_id as a fraction", input: `{"index": "test-index", "shard_id": 1.5}`},
		{name: "shard_id with an exponent", input: `{"index": "test-index", "shard_id": 1e2}`},
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

// TestSnapshotShardFailure_Marshal pins the encoding side, which follows the
// generated struct tags; see [opensearchapi.SnapshotShardFailure.UnmarshalJSON].
func TestSnapshotShardFailure_Marshal(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(opensearchapi.SnapshotShardFailure{Index: new("test-index"), ShardID: new("0")})
	require.NoError(t, err)
	require.JSONEq(t, `{"index": "test-index", "shard_id": "0"}`, string(data))

	// Omitted fields stay omitted.
	data, err = json.Marshal(opensearchapi.SnapshotShardFailure{})
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
}

// snapshotWithFailures is a trimmed snapshot whose failures carry numeric
// shard_id values, as the server writes them.
const snapshotWithFailures = `{
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
}`

func requireSnapshotWithFailures(t *testing.T, got opensearchapi.SnapshotInfo) {
	t.Helper()

	require.Equal(t, new("snapshot-with-failure"), got.Snapshot)
	require.Equal(t, new("PARTIAL"), got.State)
	require.Len(t, got.Failures, 2)

	for i, want := range []string{"0", "1"} {
		require.Equal(t, new(want), got.Failures[i].ShardID)
		require.Equal(t, new("test-index"), got.Failures[i].Index)
		require.Equal(t, new("INTERNAL_SERVER_ERROR"), got.Failures[i].Status)
	}
}

// TestSnapshotGetResp_UnmarshalShardFailure decodes a Get Snapshot response
// whose first snapshot carries numeric shard_id failures, and checks that the
// healthy snapshot after it still decodes.
func TestSnapshotGetResp_UnmarshalShardFailure(t *testing.T) {
	t.Parallel()

	input := `{
		"snapshots": [
			` + snapshotWithFailures + `,
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

	requireSnapshotWithFailures(t, resp.Snapshots[0])

	healthy := resp.Snapshots[1]
	require.Equal(t, new("healthy-snapshot"), healthy.Snapshot)
	require.Empty(t, healthy.Failures)
}

// TestSnapshotCreateResp_UnmarshalShardFailure decodes a Create Snapshot
// response, as returned with wait_for_completion=true, whose snapshot carries
// numeric shard_id failures.
func TestSnapshotCreateResp_UnmarshalShardFailure(t *testing.T) {
	t.Parallel()

	input := `{"snapshot": ` + snapshotWithFailures + `}`

	var resp opensearchapi.SnapshotCreateResp
	require.NoError(t, json.Unmarshal([]byte(input), &resp))
	require.NotNil(t, resp.Snapshot)

	requireSnapshotWithFailures(t, *resp.Snapshot)
}
