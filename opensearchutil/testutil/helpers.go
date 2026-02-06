// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides utilities for testing OpenSearch Go client functionality.
// This package contains test helpers that are shared across integration tests
// and can be used by external test packages.
package testutil

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// MustUniqueString returns a unique string with the given prefix.
// This is useful for creating unique resource names in tests to avoid conflicts.
func MustUniqueString(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, rand.Int64()) // #nosec G404 -- Using math/rand for test resource names, not cryptographic purposes
}

// PollUntil repeatedly calls checkFn until it returns true or the context times out.
// It uses exponential backoff with jitter between attempts, based on the retry logic
// from opensearchtransport.backoffRetry().
//
// This is useful for waiting for eventual consistency in integration tests, such as
// waiting for ISM policies to be applied, indices to be ready, or cluster state changes.
//
// Parameters:
//   - t: testing.T for helper marking and logging
//   - ctx: context for timeout and cancellation control
//   - baseDelay: initial delay between attempts (e.g., 500ms)
//   - maxAttempts: maximum number of check attempts
//   - jitter: randomization factor (0.0-1.0) to avoid thundering herd
//   - checkFn: function that returns (ready bool, error). Returns true when condition is met.
//
// Example usage:
//
//	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
//	defer cancel()
//	err := testutil.PollUntil(t, ctx, 500*time.Millisecond, 10, 0.1, func() (bool, error) {
//	    resp, err := client.Explain(ctx, &ism.ExplainReq{Indices: indices})
//	    if err != nil {
//	        return false, err
//	    }
//	    return resp.Indices[index].Info != nil && resp.Indices[index].Info.Message != "", nil
//	})
func PollUntil(
	t *testing.T, ctx context.Context, baseDelay time.Duration,
	maxAttempts int, jitter float64, checkFn func() (bool, error),
) error {
	t.Helper()

	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := range maxAttempts {
		// Check if context is already cancelled before attempting
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Call the check function
		ready, err := checkFn()
		if err != nil {
			return fmt.Errorf("check failed on attempt %d: %w", attempt+1, err)
		}
		if ready {
			return nil // Success
		}

		// If this is not the last attempt, wait before retrying
		if attempt < maxAttempts-1 && baseDelay > 0 {
			// Exponential backoff: base delay * 2^attempt
			// Cap attempt to prevent overflow (2^30 is ~1 billion, more than enough)
			cappedAttempt := min(attempt, 30)
			delay := time.Duration(int64(baseDelay) * (1 << cappedAttempt))

			// Apply jitter to avoid thundering herd
			// #nosec G404 -- Using math/rand for test retry jitter, not cryptographic purposes
			if jitter > 0.0 {
				jitterRange := float64(delay) * jitter
				jitterOffset := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
				delay = time.Duration(float64(delay) + jitterOffset)
			}

			// Wait with context cancellation support
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}

	return fmt.Errorf("condition not met after %d attempts", maxAttempts)
}
