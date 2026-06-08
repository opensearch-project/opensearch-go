// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/errmask"
	"github.com/opensearch-project/opensearch-go/v4/internal/envvars"
)

func ptrMask(m errmask.ErrorMask) *errmask.ErrorMask { return &m }

func TestResolveErrorMask(t *testing.T) {
	tests := []struct {
		name string
		env  *string // nil = unset, non-nil = set to this value
		cfg  Config
		want errmask.ErrorMask
	}{
		{
			name: "v4 default nil config masks everything",
			env:  nil,
			cfg:  Config{},
			want: errmask.All,
		},
		{
			name: "explicit Empty pointer reports everything",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.Empty)},
			want: errmask.Empty,
		},
		{
			name: "explicit All pointer masks everything",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.All,
		},
		{
			name: "explicit single-bit mask honored",
			env:  nil,
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems,
		},
		{
			name: "env adds bit on top of cfg base",
			env:  strPtr("+search_shards"),
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems | errmask.SearchShards,
		},
		{
			name: "env clears bit from cfg base",
			env:  strPtr("-bulk_items"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.All &^ errmask.BulkItems,
		},
		{
			name: "env empty token unmasks everything",
			env:  strPtr("empty"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.Empty,
		},
		{
			name: "env none alias unmasks everything",
			env:  strPtr("none"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.Empty,
		},
		{
			name: "env composite resets and sets",
			env:  strPtr("empty,+write_shards"),
			cfg:  Config{Errors: ptrMask(errmask.All)},
			want: errmask.WriteShards,
		},
		{
			name: "env unknown tokens silently dropped",
			env:  strPtr("garbage"),
			cfg:  Config{Errors: ptrMask(errmask.BulkItems)},
			want: errmask.BulkItems,
		},
		{
			name: "env empty string falls through to base",
			env:  strPtr(""),
			cfg:  Config{Errors: ptrMask(errmask.SearchShards)},
			want: errmask.SearchShards,
		},
		{
			name: "pascal case rejected as unknown tokens",
			env:  strPtr("+BulkItems,+SearchShards"),
			cfg:  Config{Errors: ptrMask(errmask.Empty)},
			want: errmask.Empty, // both tokens fall through to unknown; mask unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != nil {
				t.Setenv(envvars.ErrorMask, *tt.env)
			}

			got := resolveErrorMask(tt.cfg)
			require.Equal(t, tt.want, got)
		})
	}
}

func strPtr(s string) *string { return &s }

// TestPerOpErrorWrappers locks the surface of the unexported per-op
// multi-error containers (MSearchErrors, MSearchTemplateErrors).
// They're constructed by generated dispatch code via collapsePerOpErrors,
// so callers never instantiate them directly. The integration tests
// reach Unwrap() (via errors.As walking) but never call Error() or
// IsPartial() -- so those methods sit at 0% on Codecov even though
// the types are otherwise heavily exercised. This internal test
// reaches the unexported errs field directly to drive the formatter
// path and the IsPartial bool, which is the contract the
// PartialFailureError interface relies on.
func TestPerOpErrorWrappers(t *testing.T) {
	t.Parallel()

	subErrs := []error{
		&PartialSearchError{FailedShards: 1, TotalShards: 5},
		&MultiSearchItemError{
			Items:          []MultiSearchItemFailure{{Index: 0, Status: 400}},
			SucceededCount: 2,
		},
	}

	t.Run("MSearchErrors_Error_format", func(t *testing.T) {
		t.Parallel()
		e := &MSearchErrors{errs: subErrs}

		got := e.Error()
		require.True(t, strings.HasPrefix(got, "msearch partial failures: "),
			"Error() must lead with operation prefix, got: %q", got)
		require.Contains(t, got, "search partially failed: 1/5 shards failed")
		require.Contains(t, got, "multi-search partially failed: 1/3 sub-queries failed")
		require.Contains(t, got, "; ", "sub-errors must be joined with '; '")

		require.Equal(t, subErrs, e.Unwrap())
		require.True(t, e.IsPartial())
	})

	t.Run("MSearchTemplateErrors_Error_format", func(t *testing.T) {
		t.Parallel()
		e := &MSearchTemplateErrors{errs: subErrs}

		got := e.Error()
		require.True(t, strings.HasPrefix(got, "msearch_template partial failures: "),
			"Error() must lead with operation prefix, got: %q", got)
		require.Contains(t, got, "search partially failed: 1/5 shards failed")
		require.Contains(t, got, "multi-search partially failed: 1/3 sub-queries failed")

		require.Equal(t, subErrs, e.Unwrap())
		require.True(t, e.IsPartial())
	})

	t.Run("MultiSearchItemError_Error_format", func(t *testing.T) {
		t.Parallel()
		e := &MultiSearchItemError{
			Items:          []MultiSearchItemFailure{{Index: 2, Status: 400}},
			SucceededCount: 3,
		}
		require.Equal(t, "multi-search partially failed: 1/4 sub-queries failed", e.Error())
		require.True(t, e.IsPartial())
	})
}

// TestFormatPerOpErrors covers the unexported formatter directly.
// The wrappers (MSearchErrors / MSearchTemplateErrors) delegate to it,
// so locking its contract here means a future refactor that changes
// the join character or operation-name placement will fail loudly,
// rather than silently shifting downstream log parsers.
func TestFormatPerOpErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   string
		errs []error
		want string
	}{
		{
			name: "single sub-error",
			op:   "msearch",
			errs: []error{errors.New("alpha")},
			want: "msearch partial failures: alpha",
		},
		{
			name: "two sub-errors joined with semicolon-space",
			op:   "msearch",
			errs: []error{errors.New("alpha"), errors.New("beta")},
			want: "msearch partial failures: alpha; beta",
		},
		{
			name: "operation name appears verbatim",
			op:   "custom_op",
			errs: []error{errors.New("err1")},
			want: "custom_op partial failures: err1",
		},
		{
			name: "empty slice still produces stable prefix",
			op:   "msearch",
			errs: nil,
			want: "msearch partial failures: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, formatPerOpErrors(tt.op, tt.errs))
		})
	}
}

// TestCollapsePerOpErrors covers the runtime-collapse rule directly.
// The 0/1 cases are reached by every integration test, but the 2+
// case requires a wrap closure -- exercise it here explicitly so a
// regression in the closure dispatch shows up as a unit failure
// rather than a behavior-change in MSearch's caller experience.
func TestCollapsePerOpErrors(t *testing.T) {
	t.Parallel()

	mkWrap := func(errs []error) error { return &MSearchErrors{errs: errs} }

	t.Run("zero returns nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, collapsePerOpErrors(nil, mkWrap))
		require.NoError(t, collapsePerOpErrors([]error{}, mkWrap))
	})

	t.Run("one returns the bare sub-error", func(t *testing.T) {
		t.Parallel()
		sub := &PartialSearchError{FailedShards: 1, TotalShards: 5}
		got := collapsePerOpErrors([]error{sub}, mkWrap)
		require.Same(t, sub, got, "single sub-error must not be wrapped")
	})

	t.Run("two or more invokes wrap closure", func(t *testing.T) {
		t.Parallel()
		subs := []error{
			&PartialSearchError{FailedShards: 1, TotalShards: 5},
			&MultiSearchItemError{Items: []MultiSearchItemFailure{{Index: 0}}, SucceededCount: 1},
		}
		got := collapsePerOpErrors(subs, mkWrap)
		require.Error(t, got)

		var wrapper *MSearchErrors
		require.ErrorAs(t, got, &wrapper)
		require.Equal(t, subs, wrapper.Unwrap())
	})
}

// TestRequireSuccessRate_MSearchItemError covers the
// MultiSearchItemError direct + wrapped branches in
// RequireSuccessRate's switch. The exported test in errors_test.go
// covers Bulk/Search/Shard but not MultiSearchItem, leaving the
// case *MultiSearchItemError and the errors.As(err, &msearchErr)
// branches uncovered.
func TestRequireSuccessRate_MSearchItemError(t *testing.T) {
	t.Parallel()

	mkErr := func(failed, succeeded int) *MultiSearchItemError {
		items := make([]MultiSearchItemFailure, failed)
		for i := range items {
			items[i].Index = i
		}
		return &MultiSearchItemError{
			Items:          items,
			SucceededCount: succeeded,
		}
	}

	tests := []struct {
		name        string
		err         error
		threshold   float64
		wantNil     bool
		wantContain string
	}{
		{
			name:      "direct above threshold",
			err:       mkErr(1, 9),
			threshold: 0.80,
			wantNil:   true,
		},
		{
			name:      "direct at threshold",
			err:       mkErr(2, 8),
			threshold: 0.80,
			wantNil:   true,
		},
		{
			name:        "direct below threshold",
			err:         mkErr(3, 7),
			threshold:   0.80,
			wantContain: "7/10",
		},
		{
			name:      "wrapped above threshold",
			err:       fmt.Errorf("ctx: %w", mkErr(1, 19)),
			threshold: 0.95,
			wantNil:   true,
		},
		{
			name:        "wrapped below threshold",
			err:         fmt.Errorf("ctx: %w", mkErr(5, 5)),
			threshold:   0.80,
			wantContain: "5/10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := RequireSuccessRate(tt.err, tt.threshold)
			if tt.wantNil {
				require.NoError(t, got)
				return
			}
			require.Error(t, got)
			require.Contains(t, got.Error(), tt.wantContain)

			var target *MultiSearchItemError
			require.ErrorAs(t, got, &target,
				"wrapped MultiSearchItemError must be recoverable via errors.As")
		})
	}
}

// TestRequireSuccessRate_MSearchErrorsBothFired guards the F3 regression:
// when an *MSearchErrors wraps both a PartialSearchError and a
// MultiSearchItemError, every category must be evaluated against the
// threshold. A first-match check matched PartialSearchError (listed first)
// and returned, so a healthy shard rate masked a failed-sub-query rate --
// 9/10 sub-queries failing entirely would still pass RequireSuccessRate(0.9)
// as long as shards were >=90% healthy.
func TestRequireSuccessRate_MSearchErrorsBothFired(t *testing.T) {
	t.Parallel()

	shardsHealthy := &PartialSearchError{FailedShards: 5, TotalShards: 100} // 0.95
	itemsHealthy := &MultiSearchItemError{                                  // 0.95
		Items: make([]MultiSearchItemFailure, 5), SucceededCount: 95,
	}
	itemsBad := &MultiSearchItemError{ // 0.10
		Items: make([]MultiSearchItemFailure, 9), SucceededCount: 1,
	}
	shardsBad := &PartialSearchError{FailedShards: 90, TotalShards: 100} // 0.10

	tests := []struct {
		name        string
		errs        []error
		threshold   float64
		wantNil     bool
		wantContain string
	}{
		{
			// The reviewer's exact scenario: shards healthy first, items failed.
			name:        "shards healthy, items failed -> below threshold",
			errs:        []error{shardsHealthy, itemsBad},
			threshold:   0.90,
			wantContain: "1/10",
		},
		{
			// Reverse order: item category healthy first, shards failed second.
			name:        "items healthy, shards failed -> below threshold",
			errs:        []error{itemsHealthy, shardsBad},
			threshold:   0.90,
			wantContain: "10/100",
		},
		{
			name:      "both categories above threshold -> nil",
			errs:      []error{shardsHealthy, itemsHealthy},
			threshold: 0.90,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := RequireSuccessRate(&MSearchErrors{errs: tt.errs}, tt.threshold)
			if tt.wantNil {
				require.NoError(t, got)
				return
			}
			require.Error(t, got)
			require.Contains(t, got.Error(), tt.wantContain)
		})
	}
}

// TestErrors guards the F8 regression: Errors() must only flatten recognized
// partial-failure wrappers. A joined (or third-party) multi-error that is not
// itself a partial failure has to be returned as a single element so its
// top-level identity survives and its sub-errors are not silently exploded.
func TestErrors(t *testing.T) {
	t.Parallel()

	searchSub := &PartialSearchError{FailedShards: 1, TotalShards: 5}
	itemSub := &MultiSearchItemError{Items: []MultiSearchItemFailure{{Index: 0}}, SucceededCount: 1}
	joinedNonPartial := errors.Join(errors.New("a"), errors.New("b"))

	tests := []struct {
		name string
		err  error
		want []error
	}{
		{
			name: "nil returns nil",
			err:  nil,
			want: nil,
		},
		{
			name: "non-partial joined error is not exploded",
			err:  joinedNonPartial,
			want: []error{joinedNonPartial},
		},
		{
			name: "partial-failure wrapper is flattened",
			err:  &MSearchErrors{errs: []error{searchSub, itemSub}},
			want: []error{searchSub, itemSub},
		},
		{
			name: "bare partial failure is a single element",
			err:  searchSub,
			want: []error{searchSub},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, Errors(tt.err))
		})
	}
}
