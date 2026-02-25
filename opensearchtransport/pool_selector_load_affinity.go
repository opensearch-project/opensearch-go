// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

const (
	// affinityScaleUpThreshold is the busy fraction that triggers a grow.
	// Aggressive (0.6) to promote from standby before SLO violations.
	affinityScaleUpThreshold = 0.6

	// affinityScaleDownThreshold is the busy fraction below which the pool
	// sheds capacity. Conservative (0.3) to let the system reach equilibrium.
	// The gap between 0.6 and 0.3 is the hysteresis band.
	affinityScaleDownThreshold = 0.3
)

// poolLoadAffinity implements [poolSelector] with CPU-time-based scoring.
//
// Instead of round-robin, it picks the active connection with the lowest
// affinity score (RTT bucket * CPU-time accumulator). Cap scaling uses
// asymmetric thresholds to reach equilibrium without oscillation:
//
//   - Grow: when >= [affinityScaleUpThreshold] of connections are busy,
//     promote from standby immediately to avoid saturating the cluster.
//   - Shrink: when < [affinityScaleDownThreshold] of connections are busy,
//     shed capacity back to standby conservatively.
//   - Hold: between the two thresholds, the pool size is stable.
//
// A connection is "busy" when its CPU-time accumulator is at or above the
// mean load across active connections. At uniform equilibrium ~50% of
// connections are above mean, which falls in the hold band (0.3-0.6).
//
// The CPU-time accumulator on each connection is populated by
// [Connection.recordCPUTime] after each successful request, and decays
// automatically based on wall clock time via [timeWeightedCounter].
type poolLoadAffinity struct{}

func newPoolLoadAffinity() *poolLoadAffinity {
	return &poolLoadAffinity{}
}

func (s *poolLoadAffinity) selectNext(ready []*Connection, activeCount int) (*Connection, int, int, error) {
	var (
		bestConn  *Connection
		bestScore float64
		totalLoad float64
	)

	for i := range activeCount {
		conn := ready[i] //nolint:gosec // activeCount <= len(ready) enforced by caller.
		counter := conn.affinityCounter.load()
		totalLoad += counter

		bucket := float64(conn.rttRing.medianBucket())
		score := bucket * max(counter, 1.0)

		if bestConn == nil || score < bestScore {
			bestScore = score
			bestConn = conn
		}
	}

	// A connection is "busy" when its load >= mean. Grow/shrink decisions
	// use only the busy fraction with asymmetric thresholds.
	activeCap := capRemain
	standbyCap := capRemain

	if activeCount > 0 {
		meanLoad := max(totalLoad/float64(activeCount), 1.0)

		var busyCount int
		for i := range activeCount {
			if ready[i].affinityCounter.load() >= meanLoad { //nolint:gosec // activeCount <= len(ready) enforced by caller.
				busyCount++
			}
		}

		busyRatio := float64(busyCount) / float64(activeCount)

		switch {
		case busyRatio >= affinityScaleUpThreshold:
			// Load is concentrating -- promote from standby.
			activeCap = capGrow
		case busyRatio < affinityScaleDownThreshold && activeCount > 1:
			// Excess capacity -- shed back to standby.
			activeCap = capShrink
			standbyCap = capGrow
		}
	}

	return bestConn, activeCap, standbyCap, nil
}
