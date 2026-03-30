// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

func TestPollUntil_Success(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, 5, 0.0, func() (bool, error) {
		attempts++
		if attempts == 3 {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("Expected 3 attempts, got %d", attempts)
	}
}

func TestPollUntil_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	attempts := 0
	err := testutil.PollUntil(t, ctx, 20*time.Millisecond, 10, 0.0, func() (bool, error) {
		attempts++
		return false, nil // Never succeeds
	})

	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestPollUntil_CheckError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("check failed")

	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, 5, 0.0, func() (bool, error) {
		return false, expectedErr
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Expected wrapped error to contain %v, got: %v", expectedErr, err)
	}
}

func TestPollUntil_ImmediateSuccess(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	start := time.Now()
	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, 5, 0.0, func() (bool, error) {
		attempts++
		return true, nil // Succeeds immediately
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("Expected 1 attempt, got %d", attempts)
	}
	// Should complete quickly without waiting
	if elapsed > 50*time.Millisecond {
		t.Fatalf("Expected immediate return, took %v", elapsed)
	}
}

func TestPollUntil_MaxAttempts(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	maxAttempts := 3

	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, maxAttempts, 0.0, func() (bool, error) {
		attempts++
		return false, nil // Never succeeds
	})

	if err == nil {
		t.Fatal("Expected error after max attempts, got nil")
	}
	if attempts != maxAttempts {
		t.Fatalf("Expected %d attempts, got %d", maxAttempts, attempts)
	}
}

func TestPollUntil_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0

	// Cancel after first attempt
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, 10, 0.0, func() (bool, error) {
		attempts++
		return false, nil
	})

	if err == nil {
		t.Fatal("Expected cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected context.Canceled, got: %v", err)
	}
}

func TestBackoffDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		baseDelay time.Duration
		attempt   int
		jitter    float64
		wantExact time.Duration // -1 means check bounds instead
		wantMin   time.Duration // only used when wantExact == -1
		wantMax   time.Duration // only used when wantExact == -1
	}{
		{
			name:      "attempt 0 no jitter",
			baseDelay: 10 * time.Millisecond, attempt: 0, jitter: 0.0,
			wantExact: 10 * time.Millisecond,
		},
		{
			name:      "attempt 1 no jitter",
			baseDelay: 10 * time.Millisecond, attempt: 1, jitter: 0.0,
			wantExact: 20 * time.Millisecond,
		},
		{
			name:      "attempt 2 no jitter",
			baseDelay: 10 * time.Millisecond, attempt: 2, jitter: 0.0,
			wantExact: 40 * time.Millisecond,
		},
		{
			name:      "attempt 5 no jitter",
			baseDelay: 10 * time.Millisecond, attempt: 5, jitter: 0.0,
			wantExact: 320 * time.Millisecond,
		},
		{
			name:      "attempt capped at 30",
			baseDelay: 1 * time.Nanosecond, attempt: 31, jitter: 0.0,
			wantExact: time.Duration(1 << 30), // same as attempt 30
		},
		{
			name:      "jitter 0.5 in bounds",
			baseDelay: 100 * time.Millisecond, attempt: 0, jitter: 0.5,
			wantExact: -1,
			wantMin:   50 * time.Millisecond,
			wantMax:   150 * time.Millisecond,
		},
		{
			name:      "jitter 1.0 in bounds",
			baseDelay: 100 * time.Millisecond, attempt: 0, jitter: 1.0,
			wantExact: -1,
			wantMin:   0,
			wantMax:   200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.wantExact >= 0 {
				got := testutil.BackoffDelay(tt.baseDelay, tt.attempt, tt.jitter)
				require.Equal(t, tt.wantExact, got, "exact delay mismatch")
				return
			}

			// For jittered cases, run multiple times and check bounds
			const iterations = 100
			for i := range iterations {
				got := testutil.BackoffDelay(tt.baseDelay, tt.attempt, tt.jitter)
				require.GreaterOrEqual(t, got, tt.wantMin, "iteration %d: delay below minimum", i)
				require.LessOrEqual(t, got, tt.wantMax, "iteration %d: delay above maximum", i)
			}
		})
	}
}

func TestBackoffDelay_JitterProducesVariance(t *testing.T) {
	t.Parallel()

	// Verify that jitter actually produces different values across calls.
	const iterations = 20
	seen := make(map[time.Duration]struct{}, iterations)
	for range iterations {
		d := testutil.BackoffDelay(100*time.Millisecond, 0, 0.5)
		seen[d] = struct{}{}
	}
	// With 50% jitter on 100ms, the chance of 20 identical values is vanishingly small.
	require.Greater(t, len(seen), 1, "jitter should produce different delay values")
}

func TestPollUntil_WithJitter(t *testing.T) {
	// Verify PollUntil works correctly with jitter enabled (no timing assertion).
	ctx := context.Background()
	attempts := 0
	err := testutil.PollUntil(t, ctx, 1*time.Millisecond, 3, 0.5, func() (bool, error) {
		attempts++
		return attempts == 3, nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)
}

// ---------------------------------------------------------------------------
// MustParseURL
// ---------------------------------------------------------------------------

func TestMustParseURL_ValidURL(t *testing.T) {
	t.Parallel()

	u := testutil.MustParseURL("https://localhost:9200")
	require.NotNil(t, u)
	require.Equal(t, "https", u.Scheme)
	require.Equal(t, "localhost:9200", u.Host)
}

func TestMustParseURL_EmptyString(t *testing.T) {
	t.Parallel()

	// url.Parse("") succeeds and returns an empty *url.URL; it does not panic.
	u := testutil.MustParseURL("")
	require.NotNil(t, u)
}

func TestMustParseURL_InvalidURL(t *testing.T) {
	t.Parallel()

	// A control character in the URL makes url.Parse return an error,
	// which causes MustParseURL to panic.
	require.Panics(t, func() {
		testutil.MustParseURL("ht\x00tp://bad")
	})
}

// ---------------------------------------------------------------------------
// MustUniqueString
// ---------------------------------------------------------------------------

func TestMustUniqueString_PrefixPresent(t *testing.T) {
	t.Parallel()

	result := testutil.MustUniqueString(t, "test-idx")
	require.Contains(t, result, "test-idx")
}

func TestMustUniqueString_Unique(t *testing.T) {
	t.Parallel()

	a := testutil.MustUniqueString(t, "u")
	b := testutil.MustUniqueString(t, "u")
	require.NotEqual(t, a, b)
}

// ---------------------------------------------------------------------------
// NormalizeVersion
// ---------------------------------------------------------------------------

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty string", input: "", want: ""},
		{name: "bare version", input: "1.2.3", want: "v1.2.3"},
		{name: "already has v prefix", input: "v1.2.3", want: "v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := testutil.NormalizeVersion(t, tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// EvaluateVersionCondition
// ---------------------------------------------------------------------------

func TestEvaluateVersionCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		serverVersion string
		condition     string
		want          bool
	}{
		// >= operator
		{name: ">= equal", serverVersion: "v2.0.0", condition: ">=v2.0.0", want: true},
		{name: ">= above", serverVersion: "v2.1.0", condition: ">=v2.0.0", want: true},
		{name: ">= below", serverVersion: "v1.9.0", condition: ">=v2.0.0", want: false},

		// < operator
		{name: "< below", serverVersion: "v1.9.0", condition: "<v2.0.0", want: true},
		{name: "< equal", serverVersion: "v2.0.0", condition: "<v2.0.0", want: false},
		{name: "< above", serverVersion: "v2.1.0", condition: "<v2.0.0", want: false},

		// = operator
		{name: "= match", serverVersion: "v2.0.0", condition: "=v2.0.0", want: true},
		{name: "= no match", serverVersion: "v2.0.1", condition: "=v2.0.0", want: false},

		// > operator
		{name: "> above", serverVersion: "v2.1.0", condition: ">v2.0.0", want: true},
		{name: "> equal", serverVersion: "v2.0.0", condition: ">v2.0.0", want: false},
		{name: "> below", serverVersion: "v1.9.0", condition: ">v2.0.0", want: false},

		// <= operator
		{name: "<= equal", serverVersion: "v2.0.0", condition: "<=v2.0.0", want: true},
		{name: "<= below", serverVersion: "v1.9.0", condition: "<=v2.0.0", want: true},
		{name: "<= above", serverVersion: "v2.1.0", condition: "<=v2.0.0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := testutil.EvaluateVersionCondition(t, tt.serverVersion, tt.condition)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluateVersionCondition_InvalidFormat(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		testutil.EvaluateVersionCondition(t, "v1.0.0", "v1.0.0")
	})
}

// ---------------------------------------------------------------------------
// EvaluateVersionExpression
// ---------------------------------------------------------------------------

func TestEvaluateVersionExpression_SingleCondition(t *testing.T) {
	t.Parallel()
	got := testutil.EvaluateVersionExpression(t, "v2.0.0", ">=v1.0.0")
	require.True(t, got)
}

func TestEvaluateVersionExpression_Range(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		serverVersion string
		expr          string
		want          bool
	}{
		{name: "in range", serverVersion: "v1.5.0", expr: ">=v1.0.0,<v2.0.0", want: true},
		{name: "at lower bound", serverVersion: "v1.0.0", expr: ">=v1.0.0,<v2.0.0", want: true},
		{name: "at upper bound", serverVersion: "v2.0.0", expr: ">=v1.0.0,<v2.0.0", want: false},
		{name: "below range", serverVersion: "v0.9.0", expr: ">=v1.0.0,<v2.0.0", want: false},
		{name: "above range", serverVersion: "v3.0.0", expr: ">=v1.0.0,<v2.0.0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := testutil.EvaluateVersionExpression(t, tt.serverVersion, tt.expr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluateVersionExpression_EmptyServerVersion(t *testing.T) {
	t.Parallel()
	// Empty server version returns true (conservative: ignore the field when
	// version cannot be determined).
	got := testutil.EvaluateVersionExpression(t, "", ">=v1.0.0")
	require.True(t, got)
}

func TestEvaluateVersionExpression_EmptyExprPanics(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		testutil.EvaluateVersionExpression(t, "v1.0.0", "")
	})
}

// ---------------------------------------------------------------------------
// ShouldIgnoreField
// ---------------------------------------------------------------------------

func TestShouldIgnoreField_MatchingPattern(t *testing.T) {
	t.Parallel()
	got := testutil.ShouldIgnoreField(t, "/nodes/abc/fs/io_stats/total", "v2.0.0")
	require.True(t, got)
}

func TestShouldIgnoreField_NonMatchingPath(t *testing.T) {
	t.Parallel()
	got := testutil.ShouldIgnoreField(t, "/nodes/abc/name", "v2.0.0")
	require.False(t, got)
}
