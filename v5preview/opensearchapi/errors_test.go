// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestPartialFailureError_InterfaceCompliance(t *testing.T) {
	t.Parallel()

	var _ opensearchapi.PartialFailureError = (*opensearchapi.PartialBulkError)(nil)
	var _ opensearchapi.PartialFailureError = (*opensearchapi.PartialSearchError)(nil)
	var _ opensearchapi.PartialFailureError = (*opensearchapi.ShardFailureError)(nil)
}

// ---------------------------------------------------------------------------
// opensearchapi.PartialBulkError
// ---------------------------------------------------------------------------

func TestPartialBulkError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failed    int
		succeeded int
		wantMsg   string
	}{
		{
			name:      "some failures",
			failed:    3,
			succeeded: 7,
			wantMsg:   "bulk operation partially failed: 3/10 items failed",
		},
		{
			name:      "all failures",
			failed:    5,
			succeeded: 0,
			wantMsg:   "bulk operation partially failed: 5/5 items failed",
		},
		{
			name:      "no items",
			failed:    0,
			succeeded: 0,
			wantMsg:   "bulk operation partially failed: 0/0 items failed",
		},
		{
			name:      "single failure",
			failed:    1,
			succeeded: 99,
			wantMsg:   "bulk operation partially failed: 1/100 items failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := make([]opensearchapi.BulkRespItem, tt.failed)
			e := &opensearchapi.PartialBulkError{
				FailedItems:    items,
				SucceededCount: tt.succeeded,
			}

			assert.True(t, e.IsPartial())
			assert.Equal(t, tt.wantMsg, e.Error())
			assert.Len(t, e.FailedItems, tt.failed)
			assert.Equal(t, tt.succeeded, e.SucceededCount)
		})
	}
}

// ---------------------------------------------------------------------------
// opensearchapi.PartialSearchError
// ---------------------------------------------------------------------------

func TestPartialSearchError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		failed  int
		total   int
		wantMsg string
	}{
		{
			name:    "some shards failed",
			failed:  2,
			total:   5,
			wantMsg: "search partially failed: 2/5 shards failed",
		},
		{
			name:    "all shards failed",
			failed:  5,
			total:   5,
			wantMsg: "search partially failed: 5/5 shards failed",
		},
		{
			name:    "zero total",
			failed:  0,
			total:   0,
			wantMsg: "search partially failed: 0/0 shards failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := &opensearchapi.PartialSearchError{
				FailedShards: tt.failed,
				TotalShards:  tt.total,
			}

			assert.True(t, e.IsPartial())
			assert.Equal(t, tt.wantMsg, e.Error())
			assert.Equal(t, tt.failed, e.FailedShards)
			assert.Equal(t, tt.total, e.TotalShards)
		})
	}
}

// ---------------------------------------------------------------------------
// opensearchapi.ShardFailureError
// ---------------------------------------------------------------------------

func TestShardFailureError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		op      string
		failed  int
		total   int
		wantMsg string
	}{
		{
			name:    "index replica failure",
			op:      opensearchapi.OperationIndex,
			failed:  1,
			total:   3,
			wantMsg: "index had shard failures: 1/3 shards failed",
		},
		{
			name:    "delete replica failure",
			op:      opensearchapi.OperationDelete,
			failed:  2,
			total:   5,
			wantMsg: "delete had shard failures: 2/5 shards failed",
		},
		{
			name:    "create replica failure",
			op:      opensearchapi.OperationCreate,
			failed:  1,
			total:   2,
			wantMsg: "create had shard failures: 1/2 shards failed",
		},
		{
			name:    "update replica failure",
			op:      opensearchapi.OperationUpdate,
			failed:  3,
			total:   5,
			wantMsg: "update had shard failures: 3/5 shards failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := &opensearchapi.ShardFailureError{
				Operation:    tt.op,
				FailedShards: tt.failed,
				TotalShards:  tt.total,
			}

			assert.True(t, e.IsPartial())
			assert.Equal(t, tt.wantMsg, e.Error())
			assert.Equal(t, tt.failed, e.FailedShards)
			assert.Equal(t, tt.total, e.TotalShards)
		})
	}
}

// ---------------------------------------------------------------------------
// errors.As
// ---------------------------------------------------------------------------

func TestErrorsAs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		targetNew func() any // returns pointer to target for errors.As
		wantMatch bool
		check     func(t *testing.T, target any)
	}{
		{
			name: "opensearchapi.PartialBulkError unwrapped",
			err:  &opensearchapi.PartialBulkError{SucceededCount: 5, FailedItems: make([]opensearchapi.BulkRespItem, 2)},
			targetNew: func() any {
				var t *opensearchapi.PartialBulkError
				return &t
			},
			wantMatch: true,
			check: func(t *testing.T, target any) {
				t.Helper()
				e := *(target.(**opensearchapi.PartialBulkError))
				assert.Equal(t, 5, e.SucceededCount)
				assert.Len(t, e.FailedItems, 2)
			},
		},
		{
			name: "opensearchapi.PartialBulkError wrapped",
			err: fmt.Errorf("wrapped: %w", &opensearchapi.PartialBulkError{
				SucceededCount: 5,
				FailedItems:    make([]opensearchapi.BulkRespItem, 2),
			}),
			targetNew: func() any {
				var t *opensearchapi.PartialBulkError
				return &t
			},
			wantMatch: true,
			check: func(t *testing.T, target any) {
				t.Helper()
				e := *(target.(**opensearchapi.PartialBulkError))
				assert.Equal(t, 5, e.SucceededCount)
			},
		},
		{
			name: "opensearchapi.PartialSearchError",
			err:  fmt.Errorf("wrapped: %w", &opensearchapi.PartialSearchError{FailedShards: 2, TotalShards: 5}),
			targetNew: func() any {
				var t *opensearchapi.PartialSearchError
				return &t
			},
			wantMatch: true,
			check: func(t *testing.T, target any) {
				t.Helper()
				e := *(target.(**opensearchapi.PartialSearchError))
				assert.Equal(t, 2, e.FailedShards)
			},
		},
		{
			name: "opensearchapi.ShardFailureError",
			err: fmt.Errorf("wrapped: %w", &opensearchapi.ShardFailureError{
				Operation: opensearchapi.OperationDelete, FailedShards: 1, TotalShards: 3,
			}),
			targetNew: func() any {
				var t *opensearchapi.ShardFailureError
				return &t
			},
			wantMatch: true,
			check: func(t *testing.T, target any) {
				t.Helper()
				e := *(target.(**opensearchapi.ShardFailureError))
				assert.Equal(t, opensearchapi.OperationDelete, e.Operation)
			},
		},
		{
			name: "opensearchapi.PartialFailureError interface",
			err:  &opensearchapi.PartialBulkError{SucceededCount: 10, FailedItems: make([]opensearchapi.BulkRespItem, 1)},
			targetNew: func() any {
				var t opensearchapi.PartialFailureError
				return &t
			},
			wantMatch: true,
			check: func(t *testing.T, target any) {
				t.Helper()
				e := *(target.(*opensearchapi.PartialFailureError))
				assert.True(t, e.IsPartial())
			},
		},
		{
			name: "non-partial error",
			err:  fmt.Errorf("transport error"),
			targetNew: func() any {
				var t opensearchapi.PartialFailureError
				return &t
			},
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := tt.targetNew()
			matched := errors.As(tt.err, target)
			assert.Equal(t, tt.wantMatch, matched)
			if matched && tt.check != nil {
				tt.check(t, target)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// opensearchapi.IsPartialFailure
// ---------------------------------------------------------------------------

func TestIsPartialFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"opensearchapi.PartialBulkError", &opensearchapi.PartialBulkError{}, true},
		{"opensearchapi.PartialSearchError", &opensearchapi.PartialSearchError{}, true},
		{"opensearchapi.ShardFailureError", &opensearchapi.ShardFailureError{}, true},
		{"wrapped partial", fmt.Errorf("wrap: %w", &opensearchapi.PartialBulkError{}), true},
		{"non-partial", fmt.Errorf("not partial"), false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, opensearchapi.IsPartialFailure(tt.err))
		})
	}
}

// ---------------------------------------------------------------------------
// opensearchapi.ToleratePartialFailures
// ---------------------------------------------------------------------------

func TestToleratePartialFailures(t *testing.T) {
	t.Parallel()

	nonPartial := fmt.Errorf("transport error")

	tests := []struct {
		name    string
		err     error
		wantNil bool
		wantErr error
	}{
		{"nil input", nil, true, nil},
		{"partial bulk", &opensearchapi.PartialBulkError{SucceededCount: 5}, true, nil},
		{"wrapped partial search", fmt.Errorf("wrap: %w", &opensearchapi.PartialSearchError{FailedShards: 1, TotalShards: 5}), true, nil},
		{"non-partial passes through", nonPartial, false, nonPartial},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := opensearchapi.ToleratePartialFailures(tt.err)
			if tt.wantNil {
				assert.NoError(t, result)
			} else {
				assert.Equal(t, tt.wantErr, result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// opensearchapi.RequireSuccessRate
// ---------------------------------------------------------------------------

func TestRequireSuccessRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		threshold   float64
		wantNil     bool
		wantContain string // substring expected in error message, if non-nil
		wantWrapped bool   // whether original error should be recoverable via errors.As
	}{
		{
			name:      "nil err",
			err:       nil,
			threshold: 0.99,
			wantNil:   true,
		},
		{
			name:      "bulk above threshold",
			err:       &opensearchapi.PartialBulkError{SucceededCount: 99, FailedItems: make([]opensearchapi.BulkRespItem, 1)},
			threshold: 0.99,
			wantNil:   true,
		},
		{
			name:      "bulk at threshold",
			err:       &opensearchapi.PartialBulkError{SucceededCount: 95, FailedItems: make([]opensearchapi.BulkRespItem, 5)},
			threshold: 0.95,
			wantNil:   true,
		},
		{
			name:        "bulk below threshold",
			err:         &opensearchapi.PartialBulkError{SucceededCount: 90, FailedItems: make([]opensearchapi.BulkRespItem, 10)},
			threshold:   0.95,
			wantContain: "90/100",
			wantWrapped: true,
		},
		{
			name:      "search above threshold",
			err:       &opensearchapi.PartialSearchError{FailedShards: 1, TotalShards: 5},
			threshold: 0.80,
			wantNil:   true,
		},
		{
			name:        "search below threshold",
			err:         &opensearchapi.PartialSearchError{FailedShards: 1, TotalShards: 5},
			threshold:   0.90,
			wantContain: "4/5",
		},
		{
			name:      "shard failure above threshold",
			err:       &opensearchapi.ShardFailureError{Operation: opensearchapi.OperationIndex, FailedShards: 1, TotalShards: 3},
			threshold: 0.50,
			wantNil:   true,
		},
		{
			name:        "shard failure below threshold",
			err:         &opensearchapi.ShardFailureError{Operation: opensearchapi.OperationIndex, FailedShards: 1, TotalShards: 3},
			threshold:   0.90,
			wantContain: "2/3",
		},
		{
			name: "wrapped bulk above threshold",
			err: fmt.Errorf("context: %w", &opensearchapi.PartialBulkError{
				SucceededCount: 95, FailedItems: make([]opensearchapi.BulkRespItem, 5),
			}),
			threshold: 0.95,
			wantNil:   true,
		},
		{
			name: "wrapped bulk below threshold",
			err: fmt.Errorf("context: %w", &opensearchapi.PartialBulkError{
				SucceededCount: 90, FailedItems: make([]opensearchapi.BulkRespItem, 10),
			}),
			threshold:   0.99,
			wantContain: "90/100",
			wantWrapped: true,
		},
		{
			name:        "zero total returns error",
			err:         &opensearchapi.PartialBulkError{SucceededCount: 0, FailedItems: nil},
			threshold:   0.99,
			wantContain: "0/0",
		},
		{
			name:        "non-partial passes through",
			err:         fmt.Errorf("transport error"),
			threshold:   0.99,
			wantContain: "transport error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := opensearchapi.RequireSuccessRate(tt.err, tt.threshold)
			if tt.wantNil {
				assert.NoError(t, result)
				return
			}

			require.Error(t, result)
			if tt.wantContain != "" {
				assert.Contains(t, result.Error(), tt.wantContain)
			}
			if tt.wantWrapped {
				var target *opensearchapi.PartialBulkError
				assert.ErrorAs(t, result, &target)
			}
		})
	}
}
