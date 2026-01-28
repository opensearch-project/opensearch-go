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

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
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

func TestPollUntil_WithJitter(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	start := time.Now()
	err := testutil.PollUntil(t, ctx, 10*time.Millisecond, 3, 0.5, func() (bool, error) {
		attempts++
		if attempts == 3 {
			return true, nil
		}
		return false, nil
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("Expected 3 attempts, got %d", attempts)
	}
	// With exponential backoff and jitter, timing should be reasonable
	// Attempt 0: ~10ms ± 5ms, Attempt 1: ~20ms ± 10ms = ~15-45ms baseline + overhead
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Expected completion within 100ms with backoff and jitter, took %v", elapsed)
	}
}
