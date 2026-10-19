// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"math"
	"slices"
	"sync/atomic"
)

// NodeSpread describes one reading across the nodes the indexer writes to, so that a
// caller can tell a cluster that is slow from one slow node. A single number would hide
// the difference: the lowest or highest node alone lets one outlier speak for the
// whole cluster, and an average alone hides the outlier.
type NodeSpread struct {
	// Min and Max are the nodes at either end of the reading.
	Min, Max float64
	// Median is the middle node: half the nodes are at or below it. One outlier
	// leaves it where it was, so it is the reading to steer by.
	Median float64
	// Mean is the average over the nodes, each counted in proportion to the bytes the
	// indexer holds for it: a node with little work waiting moves it little.
	Mean float64
}

// summarize works out the spread of values, whose weights are weights, using scratch as
// workspace. It returns the zero spread for no values. When every weight is zero, or a
// weight is not a usable number, each node counts the same.
func summarize(values, weights, scratch []float64) NodeSpread {
	if len(values) == 0 {
		return NodeSpread{}
	}

	scratch = append(scratch[:0], values...)
	slices.Sort(scratch)

	spread := NodeSpread{Min: scratch[0], Max: scratch[len(scratch)-1]}
	if mid := len(scratch) / 2; len(scratch)%2 == 1 {
		spread.Median = scratch[mid]
	} else {
		spread.Median = (scratch[mid-1] + scratch[mid]) / 2
	}

	var total, weighted float64
	for i, v := range values {
		w := 0.0
		if i < len(weights) && weights[i] > 0 && !math.IsInf(weights[i], 0) {
			w = weights[i]
		}
		total += w
		weighted += w * v
	}
	if total > 0 {
		spread.Mean = weighted / total

		return spread
	}

	var sum float64
	for _, v := range values {
		sum += v
	}
	spread.Mean = sum / float64(len(values))

	return spread
}

// spreadBits is a NodeSpread held in atomics, as the bits of its floats, so that it
// can be read without a lock while the scheduler writes it.
type spreadBits struct {
	min, median, mean, max atomic.Uint64
}

func (b *spreadBits) store(s NodeSpread) {
	b.min.Store(math.Float64bits(s.Min))
	b.median.Store(math.Float64bits(s.Median))
	b.mean.Store(math.Float64bits(s.Mean))
	b.max.Store(math.Float64bits(s.Max))
}

func (b *spreadBits) load() NodeSpread {
	return NodeSpread{
		Min:    math.Float64frombits(b.min.Load()),
		Median: math.Float64frombits(b.median.Load()),
		Mean:   math.Float64frombits(b.mean.Load()),
		Max:    math.Float64frombits(b.max.Load()),
	}
}

// smoothing is how much of a new reading an exponentially weighted average takes:
// 0.2 averages over about the last five requests a node completed.
const smoothing = 0.2

// smooth moves old towards reading.
func smooth(old, reading float64) float64 {
	return ewma(old, reading, smoothing)
}

// ewma moves old towards v by the fraction alpha.
func ewma(old, v, alpha float64) float64 {
	return old + alpha*(v-old)
}
