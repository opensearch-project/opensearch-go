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

	return flushSample{bytes: bytes, latency: service + testDistance, distance: testDistance, served: true}
}

func newTestSizer(cfg BulkSizing) *sizer {
	return newSizer(cfg.resolve(testCeiling))
}

func TestBulkSizingResolve(t *testing.T) {
	t.Parallel()

	// resolved is the part of a resolvedSizing the cases compare.
	type resolved struct {
		min, initial, max, step, headroom, backoff, kp, ki, kd float64
	}

	tests := []struct {
		name    string
		sizing  BulkSizing
		ceiling int
		want    resolved
	}{
		{
			name:    "the zero value selects the defaults",
			ceiling: testCeiling,
			want: resolved{
				min: testCeiling / 32, initial: testCeiling / 4, max: testCeiling, step: testCeiling / 16,
				headroom: 0.25, backoff: 0.5, kp: sizingDefaultKp, ki: sizingDefaultKi, kd: sizingDefaultKd,
			},
		},
		{
			name:    "explicit values win, and a gain set on its own leaves the others at what was asked",
			sizing:  BulkSizing{MinBytes: 100, InitialBytes: 1000, StepBytes: 50, Headroom: 0.5, Backoff: 0.7, Kp: 2},
			ceiling: testCeiling,
			want: resolved{
				min: 100, initial: 1000, max: testCeiling, step: 50,
				headroom: 0.5, backoff: 0.7, kp: 2, ki: 0, kd: 0,
			},
		},
		{
			name:    "values are kept inside the ceiling",
			sizing:  BulkSizing{MinBytes: testCeiling * 2, InitialBytes: testCeiling * 3},
			ceiling: testCeiling,
			want: resolved{
				min: testCeiling, initial: testCeiling, max: testCeiling, step: testCeiling / 16,
				headroom: 0.25, backoff: 0.5, kp: sizingDefaultKp, ki: sizingDefaultKi, kd: sizingDefaultKd,
			},
		},
		{
			name:    "a tiny ceiling still works",
			ceiling: 1,
			want: resolved{
				min: 1, initial: 1, max: 1, step: 1.0 / 16,
				headroom: 0.25, backoff: 0.5, kp: sizingDefaultKp, ki: sizingDefaultKi, kd: sizingDefaultKd,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.sizing.resolve(tt.ceiling)
			w := tt.want
			require.InDeltaSlice(t,
				[]float64{w.min, w.initial, w.max, w.step, w.headroom, w.backoff, w.kp, w.ki, w.kd},
				[]float64{r.min, r.initial, r.max, r.step, r.headroom, r.backoff, r.kp, r.ki, r.kd},
				1e-9)
		})
	}
}

func TestSizerFixed(t *testing.T) {
	t.Parallel()

	s := newTestSizer(BulkSizing{Fixed: true})
	require.EqualValues(t, testCeiling, s.bytes())

	s.observe(flushOf(testCeiling, 1e6))
	require.EqualValues(t, testCeiling, s.bytes(), "a fixed size ignores every signal")
}

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

// TestSizerReadsOnlyWhatMeasuresIndexing: a flush's duration is read only when the node
// indexed something and did not refuse it with a 429 or fail it without an answer. The
// others are answered quickly, or not at all, and would read as a fast node.
func TestSizerReadsOnlyWhatMeasuresIndexing(t *testing.T) {
	t.Parallel()

	healthy := func(f *flushSample) {}
	tests := []struct {
		name string
		// shape turns a healthy, fast flush into the kind under test.
		shape    func(f *flushSample)
		wantRead bool
	}{
		{name: "a healthy flush", shape: healthy, wantRead: true},
		{name: "a flush the node rejected with a 429", shape: func(f *flushSample) { f.rejected = true }},
		{name: "a request that failed without an answer", shape: func(f *flushSample) { f.failed = true }},
		{name: "a request in which no item was indexed", shape: func(f *flushSample) { f.served = false }},
		{name: "a request the node refused as too large", shape: func(f *flushSample) { f.failed, f.served, f.tooLarge = true, false, 1<<20 }},
		{name: "a flush too small to say anything", shape: func(f *flushSample) { f.bytes = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestSizer(BulkSizing{})
			start := s.bytes()

			f := flushOf(s.bytes(), 100e6)
			tt.shape(&f)
			s.observe(f)

			if tt.wantRead {
				require.Greater(t, s.bytes(), start, "a fast flush grows the buffer")

				return
			}
			require.Equal(t, start, s.bytes(), "the buffer is not moved by it")
			require.Zero(t, s.baseRate, "and no best rate is learned from it")
		})
	}
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

func TestSizerSeededFromTheNodeSeesASlowNodeOnItsFirstFlush(t *testing.T) {
	t.Parallel()

	node := rateTracker{floor: 1}
	node.learn(flushOf(testCeiling/4, 100e6))

	fresh := newTestSizer(BulkSizing{})
	start := fresh.bytes()
	fresh.observe(flushOf(fresh.bytes(), 20e6))
	require.Greater(t, fresh.bytes(), start, "unseeded, a first flush is the best the node has done")

	seeded := newTestSizer(BulkSizing{})
	seeded.seed(node.base)
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

// TestSizerHoldsThroughNoise: service times vary around a steady node, as they do on
// any real one. The variance says nothing about the node, so the size must hold near
// the ceiling instead of being walked to the floor, which is where a controller that
// believes every sample ends up: its cuts are multiplicative and its growth additive,
// so noise alone pulls it down.
func TestSizerHoldsThroughNoise(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 5; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := newTestSizer(BulkSizing{})

		var tail float64
		const flushes, window = 3000, 500
		for i := range flushes {
			f := flushOf(s.bytes(), 100e6)
			f.latency = time.Duration(float64(f.latency) * math.Exp(0.3*rng.NormFloat64()))
			s.observe(f)
			if i >= flushes-window {
				tail += float64(s.bytes())
			}
		}

		require.Greater(t, tail/window, float64(testCeiling)*0.7,
			"seed %d: a steady node's noise must not walk the size down", seed)
	}
}

// TestSizerClimbsOutOfAFixedCost: every request costs the node a fixed time before
// its bytes count, so a small request moves fewer bytes per second than a large one
// on the same healthy node. After a cut, the controller must read that for what it
// is, the price of smallness, and climb back out, not take it for the node slowing
// and pin itself to the floor.
func TestSizerClimbsOutOfAFixedCost(t *testing.T) {
	t.Parallel()

	const (
		bandwidth = 100e6 // bytes per second the node moves once a request is parsed.
		overhead  = 5 * time.Millisecond
	)
	affine := func(bytes int64) flushSample {
		service := overhead + time.Duration(float64(bytes)/bandwidth*float64(time.Second))

		return flushSample{bytes: bytes, latency: service + testDistance, distance: testDistance, served: true}
	}

	s := newTestSizer(BulkSizing{})
	for range 200 {
		s.observe(affine(s.bytes()))
	}
	require.EqualValues(t, testCeiling, s.bytes(), "the fixed cost alone is no reason to stay small")

	// The node has a bad spell: an eighth of its rate for a stretch.
	for range 20 {
		f := affine(s.bytes())
		f.latency = (f.latency-testDistance)*8 + testDistance
		s.observe(f)
	}
	require.Less(t, s.bytes(), int64(testCeiling)/4, "the bad spell must have cut the size")

	// The node is itself again. The small requests it now gets carry the fixed
	// cost as most of their time, which is not the node slowing.
	recovered := -1
	for i := range 150 {
		s.observe(affine(s.bytes()))
		if s.bytes() >= testCeiling/2 {
			recovered = i

			break
		}
	}

	require.GreaterOrEqual(t, recovered, 0, "the size must climb back once the node recovers")
	require.LessOrEqual(t, recovered, 100, "and within a bounded number of flushes, not a decay's half-life")
}
