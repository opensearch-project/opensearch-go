// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import "math"

// rateTracker keeps the best service rate a node has shown, which a shard that starts
// on the node is measured against. The rate is smoothed before the best is taken
// from it, so that one lucky flush does not become a best no honest flush can match.
type rateTracker struct {
	// base is the best recent smoothed rate, in bytes per nanosecond, fading slowly.
	base float64
	// rateS is the smoothed rate; 0 until the first flush.
	rateS float64
	// floor is the smallest request whose rate means anything.
	floor float64
}

// learn folds a flush into the best rate. A flush that was rejected, failed or indexed
// nothing, or is too small to tell anything, says nothing about how fast the node serves.
func (r *rateTracker) learn(f flushResult) {
	if !f.hasValidLatency() || float64(f.bytes) < r.floor {
		return
	}

	if rate, ok := f.throughput(); ok {
		r.rateS = smoothInto(r.rateS, rate)
		r.base = math.Max(r.rateS, r.base*sizingBaseDecay)
	}
}
