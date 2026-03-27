// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"errors"
	"fmt"
)

// PartialFailureError is implemented by error types that represent partial
// operation success. When a caller receives a PartialFailureError, the
// accompanying response is fully populated — use errors.As to inspect
// the failure details while still processing the partial results.
//
// Use [IsPartialFailure] to test, [ToleratePartialFailures] to suppress,
// or [RequireSuccessRate] for threshold-based tolerance. For exact counts,
// use errors.As with the concrete type ([PartialBulkError],
// [PartialSearchError], or [ShardFailureError]).
type PartialFailureError interface {
	error
	IsPartial() bool
}

// ---------------------------------------------------------------------------
// PartialBulkError
// ---------------------------------------------------------------------------

// PartialBulkError indicates that a bulk operation completed with HTTP 200
// but some items failed. The accompanying BulkResp is fully populated.
type PartialBulkError struct {
	FailedItems    []BulkRespItem
	SucceededCount int
}

//nolint:errcheck // false positive: check-blank flags error-embedding interface assertions
var _ PartialFailureError = (*PartialBulkError)(nil)

func (e *PartialBulkError) Error() string {
	total := e.SucceededCount + len(e.FailedItems)
	return fmt.Sprintf("bulk operation partially failed: %d/%d items failed", len(e.FailedItems), total)
}

// IsPartial implements [PartialFailureError].
func (e *PartialBulkError) IsPartial() bool { return true }

// ---------------------------------------------------------------------------
// PartialSearchError
// ---------------------------------------------------------------------------

// PartialSearchError indicates that a search-family operation returned
// results but some shards failed. The accompanying response is fully
// populated with whatever hits came back from the successful shards.
type PartialSearchError struct {
	FailedShards int
	TotalShards  int
	Failures     []ResponseShardsFailure
}

//nolint:errcheck // false positive: check-blank flags error-embedding interface assertions
var _ PartialFailureError = (*PartialSearchError)(nil)

func (e *PartialSearchError) Error() string {
	return fmt.Sprintf("search partially failed: %d/%d shards failed", e.FailedShards, e.TotalShards)
}

// IsPartial implements [PartialFailureError].
func (e *PartialSearchError) IsPartial() bool { return true }

// ---------------------------------------------------------------------------
// ShardFailureError
// ---------------------------------------------------------------------------

// Write operation names used in [ShardFailureError.Operation].
const (
	OperationIndex  = "index"
	OperationCreate = "create"
	OperationUpdate = "update"
	OperationDelete = "delete"
)

// ShardFailureError indicates that a single-document write operation
// succeeded on the primary shard but one or more replica shards failed.
// The accompanying response is fully populated.
type ShardFailureError struct {
	Operation    string // one of OperationIndex, OperationCreate, OperationUpdate, OperationDelete
	FailedShards int
	TotalShards  int
}

//nolint:errcheck // false positive: check-blank flags error-embedding interface assertions
var _ PartialFailureError = (*ShardFailureError)(nil)

func (e *ShardFailureError) Error() string {
	return fmt.Sprintf("%s had shard failures: %d/%d shards failed", e.Operation, e.FailedShards, e.TotalShards)
}

// IsPartial implements [PartialFailureError].
func (e *ShardFailureError) IsPartial() bool { return true }

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// IsPartialFailure reports whether err is a partial failure.
func IsPartialFailure(err error) bool {
	var partial PartialFailureError
	return errors.As(err, &partial)
}

// ToleratePartialFailures returns nil for partial failure errors,
// passes through all other errors unchanged.
func ToleratePartialFailures(err error) error {
	if IsPartialFailure(err) {
		return nil
	}
	return err
}

// RequireSuccessRate returns nil if err is a partial failure with a success
// rate at or above threshold (0.0 to 1.0). Non-partial errors pass through
// unchanged. A nil err returns nil.
//
// The success rate is computed from the integer counts on the concrete error
// type: succeeded/total for [PartialBulkError], (total-failed)/total for
// [PartialSearchError] and [ShardFailureError].
func RequireSuccessRate(err error, threshold float64) error {
	if err == nil {
		return nil
	}

	var succeeded, total int
	switch e := err.(type) { //nolint:errorlint // unwrapped switch is intentional; falls through to errors.As below
	case *PartialBulkError:
		succeeded = e.SucceededCount
		total = e.SucceededCount + len(e.FailedItems)
	case *PartialSearchError:
		succeeded = e.TotalShards - e.FailedShards
		total = e.TotalShards
	case *ShardFailureError:
		succeeded = e.TotalShards - e.FailedShards
		total = e.TotalShards
	default:
		// Try unwrapping — the partial error may be wrapped.
		var bulkErr *PartialBulkError
		var searchErr *PartialSearchError
		var shardErr *ShardFailureError
		switch {
		case errors.As(err, &bulkErr):
			succeeded = bulkErr.SucceededCount
			total = bulkErr.SucceededCount + len(bulkErr.FailedItems)
		case errors.As(err, &searchErr):
			succeeded = searchErr.TotalShards - searchErr.FailedShards
			total = searchErr.TotalShards
		case errors.As(err, &shardErr):
			succeeded = shardErr.TotalShards - shardErr.FailedShards
			total = shardErr.TotalShards
		default:
			return err
		}
	}

	if total == 0 {
		return err
	}

	rate := float64(succeeded) / float64(total)
	if rate >= threshold {
		return nil
	}
	return fmt.Errorf("%d/%d succeeded (%.1f%%) below threshold %.1f%%: %w",
		succeeded, total, rate*100, threshold*100, err)
}
