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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestParseBulkByScrollTaskStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		check func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error)
	}{
		{
			name:  "nil status",
			input: nil,
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.ErrorIs(t, err, opensearchapi.ErrNilTaskStatus)
				require.Nil(t, got)
			},
		},
		{
			name: "all required fields",
			input: json.RawMessage(`{
				"total": 100, "created": 50, "updated": 30, "deleted": 20,
				"batches": 5, "version_conflicts": 2, "noops": 3,
				"retries": {"bulk": 1, "search": 0},
				"throttled_millis": 500, "requests_per_second": -1,
				"throttled_until_millis": 0
			}`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, int64(100), got.Total)
				require.Equal(t, int64(50), got.Created)
				require.Equal(t, int64(30), got.Updated)
				require.Equal(t, int64(20), got.Deleted)
				require.Equal(t, int32(5), got.Batches)
				require.Equal(t, int64(2), got.VersionConflicts)
				require.Equal(t, int64(3), got.Noops)
				require.Equal(t, int64(1), got.Retries.Bulk)
				require.Equal(t, int64(0), got.Retries.Search)
				require.Equal(t, int64(500), got.ThrottledMillis)
				require.InDelta(t, float32(-1), got.RequestsPerSecond, 0.001)
				require.Equal(t, int64(0), got.ThrottledUntilMillis)
				require.Nil(t, got.SliceID)
				require.Empty(t, got.Canceled)
				require.Nil(t, got.Slices)
			},
		},
		{
			name: "conditional fields present",
			input: json.RawMessage(`{
				"slice_id": 3, "total": 50, "updated": 0, "created": 50,
				"deleted": 0, "batches": 2, "version_conflicts": 0, "noops": 0,
				"retries": {"bulk": 0, "search": 0},
				"throttled_millis": 0, "throttled": "0s",
				"requests_per_second": -1,
				"canceled": "by user request",
				"throttled_until_millis": 0, "throttled_until": "0s"
			}`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, got.SliceID)
				require.Equal(t, int32(3), *got.SliceID)
				require.Equal(t, "by user request", got.Canceled)
				require.Equal(t, "0s", got.Throttled)
				require.Equal(t, "0s", got.ThrottledUntil)
			},
		},
		{
			name: "slices with mixed status and exception",
			input: json.RawMessage(`{
				"total": 200, "updated": 0, "created": 200, "deleted": 0,
				"batches": 4, "version_conflicts": 0, "noops": 0,
				"retries": {"bulk": 0, "search": 0},
				"throttled_millis": 0, "requests_per_second": -1,
				"throttled_until_millis": 0,
				"slices": [
					{
						"slice_id": 0, "total": 100, "updated": 0, "created": 100,
						"deleted": 0, "batches": 2, "version_conflicts": 0, "noops": 0,
						"retries": {"bulk": 0, "search": 0},
						"throttled_millis": 0, "requests_per_second": -1,
						"throttled_until_millis": 0
					},
					{"type": "search_phase_execution_exception", "reason": "all shards failed"}
				]
			}`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Len(t, got.Slices, 2)

				require.NotNil(t, got.Slices[0].Status)
				require.Nil(t, got.Slices[0].Exception)
				require.NotNil(t, got.Slices[0].Status.SliceID)
				require.Equal(t, int32(0), *got.Slices[0].Status.SliceID)
				require.Equal(t, int64(100), got.Slices[0].Status.Total)

				require.Nil(t, got.Slices[1].Status)
				require.NotNil(t, got.Slices[1].Exception)
				require.Equal(t, "search_phase_execution_exception", got.Slices[1].Exception.Type)
				require.Equal(t, "all shards failed", got.Slices[1].Exception.Reason)
			},
		},
		{
			name: "null slice elements",
			input: json.RawMessage(`{
				"total": 100, "updated": 0, "created": 100, "deleted": 0,
				"batches": 2, "version_conflicts": 0, "noops": 0,
				"retries": {"bulk": 0, "search": 0},
				"throttled_millis": 0, "requests_per_second": -1,
				"throttled_until_millis": 0,
				"slices": [null, null]
			}`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Len(t, got.Slices, 2)
				require.Nil(t, got.Slices[0].Status)
				require.Nil(t, got.Slices[0].Exception)
			},
		},
		{
			name:  "partial JSON uses zero values",
			input: json.RawMessage(`{"total": 42, "deleted": 10}`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, int64(42), got.Total)
				require.Equal(t, int64(10), got.Deleted)
				require.Equal(t, int64(0), got.Created)
				require.Equal(t, int32(0), got.Batches)
			},
		},
		{
			name:  "invalid JSON",
			input: json.RawMessage(`not valid json`),
			check: func(t *testing.T, got *opensearchapi.BulkByScrollTaskStatus, err error) {
				t.Helper()
				require.Error(t, err)
				require.Nil(t, got)
				require.ErrorContains(t, err, "unmarshaling opensearchapi.BulkByScrollTaskStatus")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := opensearchapi.ParseBulkByScrollTaskStatus(tt.input)
			tt.check(t, got, err)
		})
	}
}

func TestParseReplicationTaskStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		check func(t *testing.T, got *opensearchapi.ReplicationTaskStatus, err error)
	}{
		{
			name:  "nil status",
			input: nil,
			check: func(t *testing.T, got *opensearchapi.ReplicationTaskStatus, err error) {
				t.Helper()
				require.ErrorIs(t, err, opensearchapi.ErrNilTaskStatus)
				require.Nil(t, got)
			},
		},
		{
			name:  "valid JSON",
			input: json.RawMessage(`{"phase": "indexing"}`),
			check: func(t *testing.T, got *opensearchapi.ReplicationTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "indexing", got.Phase)
			},
		},
		{
			name:  "invalid JSON",
			input: json.RawMessage(`not valid`),
			check: func(t *testing.T, got *opensearchapi.ReplicationTaskStatus, err error) {
				t.Helper()
				require.Error(t, err)
				require.Nil(t, got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := opensearchapi.ParseReplicationTaskStatus(tt.input)
			tt.check(t, got, err)
		})
	}
}

func TestParseResyncTaskStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		check func(t *testing.T, got *opensearchapi.ResyncTaskStatus, err error)
	}{
		{
			name:  "nil status",
			input: nil,
			check: func(t *testing.T, got *opensearchapi.ResyncTaskStatus, err error) {
				t.Helper()
				require.ErrorIs(t, err, opensearchapi.ErrNilTaskStatus)
				require.Nil(t, got)
			},
		},
		{
			name: "valid JSON",
			input: json.RawMessage(`{
				"phase": "translog",
				"totalOperations": 500,
				"resyncedOperations": 350,
				"skippedOperations": 10
			}`),
			check: func(t *testing.T, got *opensearchapi.ResyncTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "translog", got.Phase)
				require.Equal(t, 500, got.TotalOperations)
				require.Equal(t, 350, got.ResyncedOperations)
				require.Equal(t, 10, got.SkippedOperations)
			},
		},
		{
			name:  "invalid JSON",
			input: json.RawMessage(`not valid`),
			check: func(t *testing.T, got *opensearchapi.ResyncTaskStatus, err error) {
				t.Helper()
				require.Error(t, err)
				require.Nil(t, got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := opensearchapi.ParseResyncTaskStatus(tt.input)
			tt.check(t, got, err)
		})
	}
}

func TestParsePersistentTaskStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		check func(t *testing.T, got *opensearchapi.PersistentTaskStatus, err error)
	}{
		{
			name:  "nil status",
			input: nil,
			check: func(t *testing.T, got *opensearchapi.PersistentTaskStatus, err error) {
				t.Helper()
				require.ErrorIs(t, err, opensearchapi.ErrNilTaskStatus)
				require.Nil(t, got)
			},
		},
		{
			name:  "valid JSON",
			input: json.RawMessage(`{"state": "STARTED"}`),
			check: func(t *testing.T, got *opensearchapi.PersistentTaskStatus, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "STARTED", got.State)
			},
		},
		{
			name:  "invalid JSON",
			input: json.RawMessage(`not valid`),
			check: func(t *testing.T, got *opensearchapi.PersistentTaskStatus, err error) {
				t.Helper()
				require.Error(t, err)
				require.Nil(t, got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := opensearchapi.ParsePersistentTaskStatus(tt.input)
			tt.check(t, got, err)
		})
	}
}

func TestBulkByScrollTaskStatusOrException_MarshalJSON(t *testing.T) {
	t.Parallel()

	sliceID := int32(0)
	roundtripSliceID := int32(2)

	tests := []struct {
		name  string
		input opensearchapi.BulkByScrollTaskStatusOrException
		check func(t *testing.T, data []byte, err error)
	}{
		{
			name: "status",
			input: opensearchapi.BulkByScrollTaskStatusOrException{
				Status: &opensearchapi.BulkByScrollTaskStatus{
					SliceID: &sliceID,
					Total:   100,
					Created: 100,
					Retries: opensearchapi.BulkByScrollTaskStatusRetries{},
				},
			},
			check: func(t *testing.T, data []byte, err error) {
				t.Helper()
				require.NoError(t, err)
				var got map[string]any
				require.NoError(t, json.Unmarshal(data, &got))
				require.InDelta(t, float64(100), got["total"], 0)
				require.InDelta(t, float64(0), got["slice_id"], 0)
			},
		},
		{
			name: "exception",
			input: opensearchapi.BulkByScrollTaskStatusOrException{
				Exception: &opensearchapi.BulkByScrollTaskException{
					Type:   "search_phase_execution_exception",
					Reason: "all shards failed",
				},
			},
			check: func(t *testing.T, data []byte, err error) {
				t.Helper()
				require.NoError(t, err)
				var got map[string]any
				require.NoError(t, json.Unmarshal(data, &got))
				require.Equal(t, "search_phase_execution_exception", got["type"])
				require.Equal(t, "all shards failed", got["reason"])
			},
		},
		{
			name:  "empty produces null",
			input: opensearchapi.BulkByScrollTaskStatusOrException{},
			check: func(t *testing.T, data []byte, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "null", string(data))
			},
		},
		{
			name: "roundtrip preserves fields",
			input: opensearchapi.BulkByScrollTaskStatusOrException{
				Status: &opensearchapi.BulkByScrollTaskStatus{
					SliceID:           &roundtripSliceID,
					Total:             50,
					Created:           30,
					Updated:           20,
					Batches:           3,
					VersionConflicts:  1,
					Retries:           opensearchapi.BulkByScrollTaskStatusRetries{Bulk: 2, Search: 1},
					RequestsPerSecond: -1,
				},
			},
			check: func(t *testing.T, data []byte, err error) {
				t.Helper()
				require.NoError(t, err)

				var got opensearchapi.BulkByScrollTaskStatusOrException
				require.NoError(t, json.Unmarshal(data, &got))

				require.NotNil(t, got.Status)
				require.Nil(t, got.Exception)
				require.Equal(t, int64(50), got.Status.Total)
				require.Equal(t, int64(30), got.Status.Created)
				require.NotNil(t, got.Status.SliceID)
				require.Equal(t, int32(2), *got.Status.SliceID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tt.input)
			tt.check(t, data, err)
		})
	}
}

func TestBulkByScrollTaskStatusOrException_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input json.RawMessage
		check func(t *testing.T, got opensearchapi.BulkByScrollTaskStatusOrException, err error)
	}{
		{
			name:  "probe unmarshal error",
			input: json.RawMessage(`42`),
			check: func(t *testing.T, got opensearchapi.BulkByScrollTaskStatusOrException, err error) {
				t.Helper()
				require.ErrorContains(t, err, "failed to probe")
			},
		},
		{
			name:  "exception unmarshal error",
			input: json.RawMessage(`{"type": "some_error", "reason": 123}`),
			check: func(t *testing.T, got opensearchapi.BulkByScrollTaskStatusOrException, err error) {
				t.Helper()
				require.ErrorContains(t, err, "failed to unmarshal BulkByScrollTaskException")
			},
		},
		{
			name:  "status unmarshal error",
			input: json.RawMessage(`{"total": 5, "retries": "bad"}`),
			check: func(t *testing.T, got opensearchapi.BulkByScrollTaskStatusOrException, err error) {
				t.Helper()
				require.ErrorContains(t, err, "failed to unmarshal BulkByScrollTaskStatus")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got opensearchapi.BulkByScrollTaskStatusOrException
			err := json.Unmarshal(tt.input, &got)
			tt.check(t, got, err)
		})
	}
}
