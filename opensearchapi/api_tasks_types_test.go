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

	t.Run("nil status returns error", func(t *testing.T) {
		t.Parallel()
		result, err := opensearchapi.ParseBulkByScrollTaskStatus(nil)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "status is nil")
	})

	t.Run("valid JSON with all required fields", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{
			"total": 100,
			"created": 50,
			"updated": 30,
			"deleted": 20,
			"batches": 5,
			"version_conflicts": 2,
			"noops": 3,
			"retries": {"bulk": 1, "search": 0},
			"throttled_millis": 500,
			"requests_per_second": -1,
			"throttled_until_millis": 0
		}`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)

		require.Equal(t, int64(100), result.Total)
		require.Equal(t, int64(50), result.Created)
		require.Equal(t, int64(30), result.Updated)
		require.Equal(t, int64(20), result.Deleted)
		require.Equal(t, int32(5), result.Batches)
		require.Equal(t, int64(2), result.VersionConflicts)
		require.Equal(t, int64(3), result.Noops)
		require.Equal(t, int64(1), result.Retries.Bulk)
		require.Equal(t, int64(0), result.Retries.Search)
		require.Equal(t, int64(500), result.ThrottledMillis)
		require.InDelta(t, float32(-1), result.RequestsPerSecond, 0.001)
		require.Equal(t, int64(0), result.ThrottledUntilMillis)
		require.Nil(t, result.SliceID)
		require.Empty(t, result.Canceled)
		require.Nil(t, result.Slices)
	})

	t.Run("conditional fields present", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{
			"slice_id": 3,
			"total": 50,
			"updated": 0,
			"created": 50,
			"deleted": 0,
			"batches": 2,
			"version_conflicts": 0,
			"noops": 0,
			"retries": {"bulk": 0, "search": 0},
			"throttled_millis": 0,
			"throttled": "0s",
			"requests_per_second": -1,
			"canceled": "by user request",
			"throttled_until_millis": 0,
			"throttled_until": "0s"
		}`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)

		require.NotNil(t, result.SliceID)
		require.Equal(t, int32(3), *result.SliceID)
		require.Equal(t, "by user request", result.Canceled)
		require.Equal(t, "0s", result.Throttled)
		require.Equal(t, "0s", result.ThrottledUntil)
	})

	t.Run("slices with mixed status and exception", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{
			"total": 200,
			"updated": 0,
			"created": 200,
			"deleted": 0,
			"batches": 4,
			"version_conflicts": 0,
			"noops": 0,
			"retries": {"bulk": 0, "search": 0},
			"throttled_millis": 0,
			"requests_per_second": -1,
			"throttled_until_millis": 0,
			"slices": [
				{
					"slice_id": 0,
					"total": 100,
					"updated": 0,
					"created": 100,
					"deleted": 0,
					"batches": 2,
					"version_conflicts": 0,
					"noops": 0,
					"retries": {"bulk": 0, "search": 0},
					"throttled_millis": 0,
					"requests_per_second": -1,
					"throttled_until_millis": 0
				},
				{
					"type": "search_phase_execution_exception",
					"reason": "all shards failed"
				}
			]
		}`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Slices, 2)

		// First slice: successful status
		require.NotNil(t, result.Slices[0].Status)
		require.Nil(t, result.Slices[0].Exception)
		require.NotNil(t, result.Slices[0].Status.SliceID)
		require.Equal(t, int32(0), *result.Slices[0].Status.SliceID)
		require.Equal(t, int64(100), result.Slices[0].Status.Total)

		// Second slice: exception
		require.Nil(t, result.Slices[1].Status)
		require.NotNil(t, result.Slices[1].Exception)
		require.Equal(t, "search_phase_execution_exception", result.Slices[1].Exception.Type)
		require.Equal(t, "all shards failed", result.Slices[1].Exception.Reason)
	})

	t.Run("null slice element", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{
			"total": 100,
			"updated": 0,
			"created": 100,
			"deleted": 0,
			"batches": 2,
			"version_conflicts": 0,
			"noops": 0,
			"retries": {"bulk": 0, "search": 0},
			"throttled_millis": 0,
			"requests_per_second": -1,
			"throttled_until_millis": 0,
			"slices": [null, null]
		}`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Slices, 2)
		require.Nil(t, result.Slices[0].Status)
		require.Nil(t, result.Slices[0].Exception)
	})

	t.Run("partial JSON uses zero values", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{"total": 42, "deleted": 10}`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)

		require.Equal(t, int64(42), result.Total)
		require.Equal(t, int64(10), result.Deleted)
		require.Equal(t, int64(0), result.Created)
		require.Equal(t, int32(0), result.Batches)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`not valid json`)

		result, err := opensearchapi.ParseBulkByScrollTaskStatus(raw)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "failed to unmarshal BulkByScrollTaskStatus")
	})
}

func TestParseReplicationTaskStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil status returns error", func(t *testing.T) {
		t.Parallel()
		result, err := opensearchapi.ParseReplicationTaskStatus(nil)
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("valid JSON", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{"phase": "indexing"}`)

		result, err := opensearchapi.ParseReplicationTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, "indexing", result.Phase)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`not valid`)

		result, err := opensearchapi.ParseReplicationTaskStatus(raw)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func TestParseResyncTaskStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil status returns error", func(t *testing.T) {
		t.Parallel()
		result, err := opensearchapi.ParseResyncTaskStatus(nil)
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("valid JSON", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{
			"phase": "translog",
			"totalOperations": 500,
			"resyncedOperations": 350,
			"skippedOperations": 10
		}`)

		result, err := opensearchapi.ParseResyncTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, "translog", result.Phase)
		require.Equal(t, 500, result.TotalOperations)
		require.Equal(t, 350, result.ResyncedOperations)
		require.Equal(t, 10, result.SkippedOperations)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`not valid`)

		result, err := opensearchapi.ParseResyncTaskStatus(raw)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func TestParsePersistentTaskStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil status returns error", func(t *testing.T) {
		t.Parallel()
		result, err := opensearchapi.ParsePersistentTaskStatus(nil)
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("valid JSON", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`{"state": "STARTED"}`)

		result, err := opensearchapi.ParsePersistentTaskStatus(raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, "STARTED", result.State)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`not valid`)

		result, err := opensearchapi.ParsePersistentTaskStatus(raw)
		require.Error(t, err)
		require.Nil(t, result)
	})
}
