// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math/rand/v2"
	"time"
)

// maxRetryBackoffShift bounds the doubling in jitteredRetryBackoff, so that a large
// attempt number cannot overflow the ceiling. 100ms doubled 16 times is about 1.8h,
// far past any retry budget.
const maxRetryBackoffShift = 16

// jitteredRetryBackoff is the default RetryBackoff: exponential backoff with full
// jitter. Before retry attempt, counted from 1, it waits a duration drawn uniformly
// from zero to defaultRetryBackoffBase doubled attempt-1 times. Drawing from the
// whole range spreads the retries of clients a node refused at the same moment.
func jitteredRetryBackoff(attempt int) time.Duration {
	shift := min(max(attempt-1, 0), maxRetryBackoffShift)
	ceiling := defaultRetryBackoffBase << shift

	return rand.N(ceiling + 1) //nolint:gosec // jitter, not security
}
