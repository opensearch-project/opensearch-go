// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateTracker(t *testing.T) {
	t.Parallel()

	const floor = 1 << 20

	fast := flushAtThroughput(floor, 100e6)
	slow := flushAtThroughput(floor, 50e6)

	var best rateTracker
	best.floor = floor
	best.learn(fast)
	require.Positive(t, best.base)

	tests := []struct {
		name  string
		learn []flushResult
		// learned is set when the flushes leave a best rate, which is then the fast one.
		learned bool
	}{
		{name: "a timed flush sets the best", learn: []flushResult{fast}, learned: true},
		{name: "the best is kept when a slower flush follows", learn: []flushResult{fast, slow}, learned: true},
		{name: "a rejected flush is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond, served: true, rejected: true}}},
		{name: "a failed flush is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond, served: true, failed: true}}},
		{name: "a flush that indexed nothing is not learned", learn: []flushResult{{bytes: floor, latency: time.Microsecond}}},
		{name: "a flush too small to tell is not learned", learn: []flushResult{flushAtThroughput(floor/4, 1e12)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := rateTracker{floor: floor}
			for _, f := range tt.learn {
				r.learn(f)
			}

			if !tt.learned {
				require.Zero(t, r.base)

				return
			}
			require.InEpsilon(t, best.base, r.base, 0.01)
		})
	}
}
