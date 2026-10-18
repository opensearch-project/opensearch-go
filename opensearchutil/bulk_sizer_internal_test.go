// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testCeiling  = 4 << 20 // 4 MiB.
	testDistance = time.Millisecond
)

// flushOf is a flush of bytes that took the time a node serving rate bytes per
// second needs, plus the distance.
func flushOf(bytes int64, rate float64) flushSample {
	service := time.Duration(float64(bytes) / rate * float64(time.Second))

	return flushSample{bytes: bytes, latency: service + testDistance, distance: testDistance}
}

func newTestSizer(cfg BulkSizing) *sizer {
	return newSizer(cfg.resolve(testCeiling))
}

func TestBulkSizingDefaults(t *testing.T) {
	t.Parallel()

	r := BulkSizing{}.resolve(testCeiling)

	require.InDelta(t, testCeiling, r.max, 0)
	require.InDelta(t, testCeiling/32, r.min, 0)
	require.InDelta(t, testCeiling/4, r.initial, 0)
	require.InDelta(t, testCeiling/16, r.step, 0)
	require.InDelta(t, 0.25, r.headroom, 1e-9)
	require.InDelta(t, 0.5, r.backoff, 1e-9)
	require.InDelta(t, 1.0, r.kp, 1e-9)

	t.Run("explicit values win", func(t *testing.T) {
		t.Parallel()

		r := BulkSizing{MinBytes: 100, InitialBytes: 1000, StepBytes: 50, Headroom: 0.5, Backoff: 0.7, Kp: 2}.resolve(testCeiling)
		require.InDelta(t, 100, r.min, 0)
		require.InDelta(t, 1000, r.initial, 0)
		require.InDelta(t, 50, r.step, 0)
		require.InDelta(t, 0.5, r.headroom, 1e-9)
		require.InDelta(t, 0.7, r.backoff, 1e-9)
		require.InDelta(t, 2.0, r.kp, 1e-9)
		require.Zero(t, r.ki, "a gain set on its own leaves the others at what was asked")
	})

	t.Run("values are kept inside the ceiling", func(t *testing.T) {
		t.Parallel()

		r := BulkSizing{MinBytes: testCeiling * 2, InitialBytes: testCeiling * 3}.resolve(testCeiling)
		require.InDelta(t, testCeiling, r.min, 0)
		require.InDelta(t, testCeiling, r.initial, 0)
	})

	t.Run("a tiny ceiling still works", func(t *testing.T) {
		t.Parallel()

		r := BulkSizing{}.resolve(1)
		require.InDelta(t, 1, r.min, 0)
		require.InDelta(t, 1, r.initial, 0)
		require.InDelta(t, 1, r.max, 0)
	})
}

func TestSizerFixed(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{Fixed: true})
	require.EqualValues(t, testCeiling, s.bytes())

	s.observe(rejectedFlush())
	s.observe(flushOf(testCeiling, 1e6))
	require.EqualValues(t, testCeiling, s.bytes(), "a fixed size ignores every signal")
}

func rejectedFlush() flushSample { return flushSample{bytes: 1 << 20, rejected: true} }

func TestSizerGrowsWhileTheNodeKeepsUp(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	start := s.bytes()

	var last int64
	for range 200 {
		s.observe(flushOf(s.bytes(), 100e6))
		require.GreaterOrEqual(t, s.bytes(), last, "a healthy node never makes the size shrink")
		last = s.bytes()
	}

	require.Greater(t, last, start)
	require.EqualValues(t, testCeiling, last, "and it reaches the ceiling")
}

func TestSizerBacksOffOnRejection(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	for range 200 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	require.EqualValues(t, testCeiling, s.bytes())

	s.observe(flushSample{bytes: s.bytes(), rejected: true})
	require.EqualValues(t, testCeiling/2, s.bytes(), "a 429 halves the size at once")

	// The size is held for a few healthy flushes, then grows again.
	held := s.bytes()
	for range sizingHoldFlushes {
		s.observe(flushOf(s.bytes(), 100e6))
		require.Equal(t, held, s.bytes(), "no growth while the hold lasts")
	}
	s.observe(flushOf(s.bytes(), 100e6))
	require.Greater(t, s.bytes(), held, "growth resumes after the hold")
}

func TestSizerRejectionNeverGoesBelowTheFloor(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	for range 50 {
		s.observe(flushSample{bytes: s.bytes(), rejected: true})
	}

	require.EqualValues(t, testCeiling/32, s.bytes())
}

func TestSizerFailureBacksOffLessThanRejection(t *testing.T) {
	t.Parallel()

	rejected, failed := newTestSizer(BulkSizing{}), newTestSizer(BulkSizing{})
	rejected.observe(flushSample{bytes: 1 << 20, rejected: true})
	failed.observe(flushSample{bytes: 1 << 20, failed: true})

	require.Less(t, rejected.bytes(), failed.bytes())
	require.Less(t, failed.bytes(), int64(testCeiling/4))
}

func TestSizerBacksOffBeforeTheNodeRejects(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	for range 200 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	healthy := s.bytes()

	// The node slows to a quarter of its rate, as when it is busy with other
	// requests. It rejects nothing.
	for range 5 {
		s.observe(flushOf(s.bytes(), 25e6))
	}

	require.Less(t, s.bytes(), healthy/2, "a node four times slower must see far smaller batches")
}

func TestSizerRecoversWhenTheNodeDoes(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	for range 200 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	for range 10 {
		s.observe(flushOf(s.bytes(), 25e6))
	}
	slowed := s.bytes()
	require.Less(t, slowed, int64(testCeiling))

	for range 200 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	require.Greater(t, s.bytes(), slowed)
	require.EqualValues(t, testCeiling, s.bytes())
}

func TestSizerAcceptsAPermanentlySlowerNode(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	for range 100 {
		s.observe(flushOf(s.bytes(), 100e6))
	}

	// The node settles at a quarter of its old rate and stays there. The memory of
	// the old rate fades, and the size comes back instead of staying cut forever.
	for range 3000 {
		s.observe(flushOf(s.bytes(), 25e6))
	}

	require.Greater(t, s.bytes(), int64(testCeiling/2))
}

func TestSizerHoldsOnAnOverloadedPool(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	before := s.bytes()

	for range 20 {
		f := flushOf(s.bytes(), 100e6)
		f.overloaded = true
		s.observe(f)
	}

	require.LessOrEqual(t, s.bytes(), before, "a pool that is rejecting is never a reason to grow")
}

func TestSizerIgnoresTinyFlushes(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	before := s.bytes()

	// The last, partial batch of a burst: slow per byte because of fixed costs.
	s.observe(flushOf(before/100, 1e3))
	require.Equal(t, before, s.bytes())
}

func TestSizerDistanceIsNotTheNodesFault(t *testing.T) {
	t.Parallel()

	// A far node takes the same time on the node as a near one; only the distance
	// differs. Both must be treated alike.
	run := func(distance time.Duration) int64 {
		s := newTestSizer(BulkSizing{})
		for range 200 {
			f := flushOf(s.bytes(), 100e6)
			f.latency += distance - testDistance
			f.distance = distance
			s.observe(f)
		}

		return s.bytes()
	}

	require.Equal(t, run(testDistance), run(80*time.Millisecond))
}

// TestSizerSettlesWhereLoadSlowsTheNode closes the loop: the node's rate falls
// as the batches it is sent grow, as a node whose write queue fills does. The
// controller must find a size and stay near it, rather than ride the ceiling or
// oscillate across the range.
func TestSizerSettlesWhereLoadSlowsTheNode(t *testing.T) {
	t.Parallel()

	const (
		capacity = 200e6
		// knee is the batch size at which the node's rate has halved.
		knee    = 1 << 20
		samples = 600
		window  = 100
	)

	rate := func(bytes int64) float64 { return capacity / (1 + float64(bytes)/knee) }

	s := newTestSizer(BulkSizing{})
	sizes := make([]float64, 0, samples)
	for range samples {
		s.observe(flushOf(s.bytes(), rate(s.bytes())))
		sizes = append(sizes, float64(s.bytes()))
	}

	tail := sizes[samples-window:]
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range tail {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}

	require.Less(t, hi, float64(testCeiling), "the controller must come off the ceiling when load slows the node")
	require.Greater(t, lo, float64(testCeiling/32), "and must not collapse to the floor")
	require.Less(t, hi/lo, 3.0, "and must stay within a band instead of swinging across the range")
}

func TestSizerStaysInBounds(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := newTestSizer(BulkSizing{})

		for range 2000 {
			f := flushOf(int64(rng.Intn(testCeiling)+1), 1e6+rng.Float64()*1e9)
			f.rejected = rng.Intn(20) == 0
			f.failed = rng.Intn(30) == 0
			f.overloaded = rng.Intn(10) == 0
			s.observe(f)

			require.GreaterOrEqual(t, s.bytes(), int64(testCeiling/32))
			require.LessOrEqual(t, s.bytes(), int64(testCeiling))
		}
	}
}

// TestSizerCrowdedCutsGently: a node that is given more than it takes is told so
// with a cut well short of a rejection's, and keeps being told while it stays so.
func TestSizerCrowdedCutsGently(t *testing.T) {
	t.Parallel()

	crowded, rejected := newTestSizer(BulkSizing{}), newTestSizer(BulkSizing{})
	for range 40 {
		crowded.observe(flushOf(crowded.bytes(), 100e6))
		rejected.observe(flushOf(rejected.bytes(), 100e6))
	}
	require.EqualValues(t, testCeiling, crowded.bytes())

	crowded.observe(flushSample{bytes: 1 << 20, crowded: true})
	rejected.observe(flushSample{bytes: 1 << 20, rejected: true})

	want := float64(testCeiling) * math.Pow(0.5, sizingCrowdedExponent)
	require.InDelta(t, want, float64(crowded.bytes()), 2)
	require.Greater(t, crowded.bytes(), rejected.bytes(), "milder than a rejection")
	require.Less(t, crowded.bytes(), int64(testCeiling))

	before := crowded.bytes()
	crowded.observe(flushSample{bytes: 1 << 20, crowded: true})
	require.Less(t, crowded.bytes(), before, "and again while it stays crowded")

	for range 200 {
		crowded.observe(flushSample{bytes: 1 << 20, crowded: true})
	}
	require.EqualValues(t, testCeiling/32, crowded.bytes(), "but never below the floor")
}

// newTestLimit is the sizer of a node's limit: it starts at the initial size and
// is steered by rejections alone.
func newTestLimit() *sizer {
	r := BulkSizing{}.resolve(testCeiling)
	r.limit = true

	return newSizer(r)
}

func TestLimitIgnoresHowSlowTheNodeIs(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	step := int64(s.cfg.step)

	// A node five times slower than its best would cut a shard buffer. It does not
	// cut the limit, which grows by a step per flush the node did not reject.
	s.observe(flushOf(s.bytes(), 100e6))
	for range 5 {
		before := s.bytes()
		s.observe(flushOf(s.bytes(), 20e6))
		require.Equal(t, before+step, s.bytes())
	}
}

func TestLimitGrowsByAFixedStepUpToTheCeiling(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	step := int64(s.cfg.step)

	before := s.bytes()
	s.observe(flushOf(s.bytes(), 100e6))
	require.Equal(t, before+step, s.bytes())

	for range 100 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	require.EqualValues(t, testCeiling, s.bytes())
}

func TestLimitHalvesOnARejectionAndHolds(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	for range 100 {
		s.observe(flushOf(s.bytes(), 100e6))
	}

	s.observe(rejectedFlush())
	cut := s.bytes()
	require.EqualValues(t, testCeiling/2, cut)

	for range sizingHoldFlushes {
		s.observe(flushOf(s.bytes(), 100e6))
		require.Equal(t, cut, s.bytes(), "no growth while the hold lasts")
	}
	s.observe(flushOf(s.bytes(), 100e6))
	require.Greater(t, s.bytes(), cut, "growth resumes after the hold")
}

func TestLimitHoldsOnAnOverloadedPool(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	before := s.bytes()

	f := flushOf(s.bytes(), 100e6)
	f.overloaded = true
	s.observe(f)
	require.Equal(t, before, s.bytes())
}

func TestLimitStillGivesWayWhenCrowdedOrFailed(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	for range 100 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	full := s.bytes()

	f := flushOf(s.bytes(), 100e6)
	f.crowded = true
	s.observe(f)
	require.Less(t, s.bytes(), full, "a crowded node is told so")

	s.observe(flushSample{bytes: 1 << 20, failed: true})
	require.Less(t, s.bytes(), full)
}

func TestLimitLearnsTheNodesBestRate(t *testing.T) {
	t.Parallel()

	s := newTestLimit()
	require.Zero(t, s.baseRate)

	s.observe(flushOf(s.bytes(), 100e6))
	best := s.baseRate
	require.Positive(t, best)

	s.observe(flushOf(s.bytes(), 20e6))
	require.Greater(t, s.baseRate, best*0.99, "a slow flush does not lower the best rate, which only fades slowly")

	s.observe(flushSample{bytes: 1 << 20, rejected: true})
	require.Greater(t, s.baseRate, best*0.99, "a rejection says nothing about the rate")
}

func TestSizerSeededFromTheNodeSeesASlowNodeOnItsFirstFlush(t *testing.T) {
	t.Parallel()

	node := newTestLimit()
	node.observe(flushOf(node.bytes(), 100e6))

	fresh := newTestSizer(BulkSizing{})
	start := fresh.bytes()
	fresh.observe(flushOf(fresh.bytes(), 20e6))
	require.Greater(t, fresh.bytes(), start, "unseeded, a first flush is the best the node has done")

	seeded := newTestSizer(BulkSizing{})
	seeded.seed(node.baseRate)
	seeded.observe(flushOf(seeded.bytes(), 20e6))
	require.Less(t, seeded.bytes(), start, "seeded, the same flush is a node that has slowed down")
}

func TestSizerSeedOnlyFillsAnEmptyBaseline(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{})
	s.seed(0)
	require.Zero(t, s.baseRate, "nothing to seed from")

	s.seed(3)
	s.seed(7)
	require.InDelta(t, 3, s.baseRate, 0, "a baseline the sizer has is not replaced")
}
