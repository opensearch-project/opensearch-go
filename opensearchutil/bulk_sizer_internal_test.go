// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testCeiling = 4 << 20 // 4 MiB.
	testIdleRTT = time.Millisecond
)

// flushAtThroughput is a flush of bytes to a node that indexes bytesPerSec: its
// latency is the time that takes, plus the node's idle round trip time.
func flushAtThroughput(bytes int64, bytesPerSec float64) flushResult {
	service := time.Duration(float64(bytes) / bytesPerSec * float64(time.Second))

	return flushResult{bytes: bytes, latency: service + testIdleRTT, idleRTT: testIdleRTT, served: true}
}

func newTestShardController(cfg BulkSizing) *shardController {
	return newShardController(cfg.resolve(testCeiling))
}

// TestShardControllerFraction: the fraction is the target as a part of the ceiling, and is 0
// for a shard controller that has no ceiling to measure against.
func TestShardControllerFraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		target, max float64
		want        float64
	}{
		{name: "at the ceiling", target: 4 << 20, max: 4 << 20, want: 1},
		{name: "half way", target: 2 << 20, max: 4 << 20, want: 0.5},
		{name: "empty", target: 0, max: 4 << 20, want: 0},
		{name: "no ceiling", target: 1 << 20, max: 0, want: 0},
		{name: "a negative ceiling", target: 1 << 20, max: -1, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newShardController(resolvedSizing{max: tt.max, initial: tt.target})

			require.InDelta(t, tt.want, s.fraction(), 1e-9)
		})
	}

	t.Run("follows the target as it moves", func(t *testing.T) {
		t.Parallel()

		s := newTestShardController(BulkSizing{})
		before := s.fraction()
		require.Positive(t, before)
		require.LessOrEqual(t, before, 1.0)

		s.target = float64(s.ceiling())
		require.InDelta(t, 1.0, s.fraction(), 1e-9)
	})
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
			sizing:  BulkSizing{MinBytes: 100, InitialBytes: 1000, StepBytes: 50, Headroom: 0.5, Backoff: 0.7, PID: PIDGains{Kp: 2}},
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
			name:    "fixed pins the start to the ceiling and keeps the adaptive minimum",
			sizing:  BulkSizing{Shard: ShardFixed, Target: TargetFixed},
			ceiling: testCeiling,
			want: resolved{
				min: testCeiling / 32, initial: testCeiling, max: testCeiling, step: testCeiling / 16,
				headroom: 0.25, backoff: 0.5, kp: sizingDefaultKp, ki: sizingDefaultKi, kd: sizingDefaultKd,
			},
		},
		{
			name:    "fixed keeps the minimum at least 1 on a tiny ceiling",
			sizing:  BulkSizing{Shard: ShardFixed, Target: TargetFixed},
			ceiling: 1,
			want: resolved{
				min: 1, initial: 1, max: 1, step: 1.0 / 16,
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

func TestShardControllerFixed(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{Shard: ShardFixed, Target: TargetFixed})
	require.EqualValues(t, testCeiling, s.bytes())

	s.observe(flushAtThroughput(testCeiling, 1e6))
	require.EqualValues(t, testCeiling, s.bytes(), "a fixed size ignores every signal")
}

// TestShardControllerFixedReportsASustainedSlowdown: a fixed size reads the node for the slowdown
// it reports, and a node that stays 5x slower is still reported as slow after many
// flushes. The fixed size is not the smallest batch the controller would ask for: if it
// were, every request would teach the fixed cost of a request and the memory of the
// best rate would fade at the rate for small requests, and the slowdown would be
// absorbed as that cost.
func TestShardControllerFixedReportsASustainedSlowdown(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{Shard: ShardFixed, Target: TargetFixed})
	for range 200 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
	}

	var slowdown float64
	for range 51 {
		n, ok := s.observe(flushAtThroughput(s.bytes(), 20e6))
		require.True(t, ok)
		slowdown = n - 1
	}

	// The node is 5x slower than at its best, which is 4 over the expected time.
	require.Greater(t, slowdown, 3.0, "the slowdown must survive 51 flushes of a node that stays slow")
	require.EqualValues(t, testCeiling, s.bytes())
}

func TestShardControllerGrowsWhileTheNodeKeepsUp(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	start := s.bytes()

	var last int64
	for range 200 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
		require.GreaterOrEqual(t, s.bytes(), last, "a healthy node never makes the size shrink")
		last = s.bytes()
	}

	require.Greater(t, last, start)
	require.EqualValues(t, testCeiling, last, "and it reaches the ceiling")
}

// TestShardControllerReadsOnlyWhatMeasuresIndexing: a flush's duration is read only when the node
// indexed something and did not refuse it with a 429 or fail it without an answer. The
// others are answered quickly, or not at all, and would read as a fast node.
func TestShardControllerReadsOnlyWhatMeasuresIndexing(t *testing.T) {
	t.Parallel()

	healthy := func(f *flushResult) {}
	tests := []struct {
		name string
		// shape turns a healthy, fast flush into the kind under test.
		shape    func(f *flushResult)
		wantRead bool
	}{
		{name: "a healthy flush", shape: healthy, wantRead: true},
		{name: "a flush the node rejected with a 429", shape: func(f *flushResult) { f.rejected = true }},
		{name: "a request that failed without an answer", shape: func(f *flushResult) { f.failed = true }},
		{name: "a request in which no item was indexed", shape: func(f *flushResult) { f.served = false }},
		{name: "a request the node refused as too large", shape: func(f *flushResult) { f.failed, f.served, f.tooLarge = true, false, 1<<20 }},
		{name: "a flush too small to say anything", shape: func(f *flushResult) { f.bytes = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestShardController(BulkSizing{})
			start := s.bytes()

			f := flushAtThroughput(s.bytes(), 100e6)
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

func TestShardControllerBacksOffBeforeTheNodeRejects(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	for range 200 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
	}
	healthy := s.bytes()

	// The node slows to a quarter of its rate, as when it is busy with other
	// requests. It rejects nothing.
	for range 5 {
		s.observe(flushAtThroughput(s.bytes(), 25e6))
	}

	require.Less(t, s.bytes(), healthy/2, "a node four times slower must see far smaller batches")
}

func TestShardControllerRecoversWhenTheNodeDoes(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	for range 200 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
	}
	for range 10 {
		s.observe(flushAtThroughput(s.bytes(), 25e6))
	}
	slowed := s.bytes()
	require.Less(t, slowed, int64(testCeiling))

	for range 200 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
	}
	require.Greater(t, s.bytes(), slowed)
	require.EqualValues(t, testCeiling, s.bytes())
}

func TestShardControllerAcceptsAPermanentlySlowerNode(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	for range 100 {
		s.observe(flushAtThroughput(s.bytes(), 100e6))
	}

	// The node settles at a quarter of its old rate and stays there. The memory of
	// the old rate fades, and the size comes back instead of staying shrunk forever.
	for range 3000 {
		s.observe(flushAtThroughput(s.bytes(), 25e6))
	}

	require.Greater(t, s.bytes(), int64(testCeiling/2))
}

func TestShardControllerPinsGrowthWhenTheNodeBecomesWriteSaturated(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	before := s.bytes()

	// The node becomes write-saturated and stays so. Its first pinFlushes
	// flushes may not grow the buffer.
	saturated := func() flushResult {
		f := flushAtThroughput(s.bytes(), 100e6)
		f.nodeWriteSaturated = true

		return f
	}
	for range pinFlushes {
		s.observe(saturated())
		require.LessOrEqual(t, s.bytes(), before, "no growth while the pin lasts")
	}

	// After that the buffer grows as usual, although the node is still reported
	// write-saturated.
	for range 20 {
		s.observe(saturated())
	}
	require.Greater(t, s.bytes(), before, "growth resumes while the node is still reported write-saturated")
}

func TestShardControllerIgnoresTinyFlushes(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	before := s.bytes()

	// The last, partial batch of a burst: slow per byte because of fixed costs.
	s.observe(flushAtThroughput(before/100, 1e3))
	require.Equal(t, before, s.bytes())
}

func TestShardControllerIdleRTTIsNotTheNodesFault(t *testing.T) {
	t.Parallel()

	// A node with a long idle round trip time takes the same time on the node as one
	// with a short one; only the round trip differs. Both must be treated alike.
	run := func(idleRTT time.Duration) int64 {
		s := newTestShardController(BulkSizing{})
		for range 200 {
			f := flushAtThroughput(s.bytes(), 100e6)
			f.latency += idleRTT - testIdleRTT
			f.idleRTT = idleRTT
			s.observe(f)
		}

		return s.bytes()
	}

	require.Equal(t, run(testIdleRTT), run(80*time.Millisecond))
}

// TestShardControllerSettlesWhereLoadSlowsTheNode closes the loop: the node's rate falls
// as the batches it is sent grow, as a node whose write queue fills does. The
// controller must find a size and stay near it, rather than ride the ceiling or
// oscillate across the range.
func TestShardControllerSettlesWhereLoadSlowsTheNode(t *testing.T) {
	t.Parallel()

	const (
		capacity = 200e6
		// knee is the batch size at which the node's rate has halved.
		knee    = 1 << 20
		samples = 600
		window  = 100
	)

	rate := func(bytes int64) float64 { return capacity / (1 + float64(bytes)/knee) }

	s := newTestShardController(BulkSizing{})
	sizes := make([]float64, 0, samples)
	for range samples {
		s.observe(flushAtThroughput(s.bytes(), rate(s.bytes())))
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

func TestShardControllerStaysInBounds(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := newTestShardController(BulkSizing{})

		for range 2000 {
			f := flushAtThroughput(int64(rng.Intn(testCeiling)+1), 1e6+rng.Float64()*1e9)
			f.rejected = rng.Intn(20) == 0
			f.failed = rng.Intn(30) == 0
			f.nodeWriteSaturated = rng.Intn(10) == 0
			s.observe(f)

			require.GreaterOrEqual(t, s.bytes(), int64(testCeiling/32))
			require.LessOrEqual(t, s.bytes(), int64(testCeiling))
		}
	}
}

func TestShardControllerSeededFromTheNodeSeesASlowNodeOnItsFirstFlush(t *testing.T) {
	t.Parallel()

	node := rateTracker{floor: 1}
	node.learn(flushAtThroughput(testCeiling/4, 100e6))

	fresh := newTestShardController(BulkSizing{})
	start := fresh.bytes()
	fresh.observe(flushAtThroughput(fresh.bytes(), 20e6))
	require.Greater(t, fresh.bytes(), start, "unseeded, a first flush is the best the node has done")

	seeded := newTestShardController(BulkSizing{})
	seeded.seed(node.base)
	seeded.observe(flushAtThroughput(seeded.bytes(), 20e6))
	require.Less(t, seeded.bytes(), start, "seeded, the same flush is a node that has slowed down")
}

func TestShardControllerSeedOnlyFillsAnEmptyBaseline(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	s.seed(0)
	require.Zero(t, s.baseRate, "nothing to seed from")

	s.seed(3)
	s.seed(7)
	require.InDelta(t, 3, s.baseRate, 0, "a baseline the sizer has is not replaced")
}

// TestShardControllerHoldsThroughNoise: service times vary around a steady node, as they do on
// any real one. The variance says nothing about the node, so the size must hold near
// the ceiling instead of being walked to the floor (see sizingSignalAlpha).
func TestShardControllerHoldsThroughNoise(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewSource(seed))
			s := newTestShardController(BulkSizing{})

			var tail float64
			const flushes, window = 3000, 500
			for i := range flushes {
				f := flushAtThroughput(s.bytes(), 100e6)
				f.latency = time.Duration(float64(f.latency) * math.Exp(0.3*rng.NormFloat64()))
				s.observe(f)
				if i >= flushes-window {
					tail += float64(s.bytes())
				}
			}

			require.Greater(t, tail/window, float64(testCeiling)*0.7,
				"a steady node's noise must not walk the size down")
		})
	}
}

// TestShardControllerClimbsOutOfAFixedCost: every request costs the node a fixed time before
// its bytes count, so a small request moves fewer bytes per second than a large one
// on the same healthy node. After a reduction, the controller must read that for what it
// is, the price of smallness, and climb back out, not take it for the node slowing
// and pin itself to the floor.
func TestShardControllerClimbsOutOfAFixedCost(t *testing.T) {
	t.Parallel()

	const (
		bandwidth = 100e6 // bytes per second the node moves once a request is parsed.
		overhead  = 5 * time.Millisecond
	)
	affine := func(bytes int64) flushResult {
		service := overhead + time.Duration(float64(bytes)/bandwidth*float64(time.Second))

		return flushResult{bytes: bytes, latency: service + testIdleRTT, idleRTT: testIdleRTT, served: true}
	}

	s := newTestShardController(BulkSizing{})
	for range 200 {
		s.observe(affine(s.bytes()))
	}
	require.EqualValues(t, testCeiling, s.bytes(), "the fixed cost alone is no reason to stay small")

	// The node has a bad spell: an eighth of its rate for a stretch.
	for range 20 {
		f := affine(s.bytes())
		f.latency = (f.latency-testIdleRTT)*8 + testIdleRTT
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

// peerNode stands for a node that serves one shard: with no other shard to measure
// it against, the shard is measured against its own level, as the scheduler does.
type peerNode struct{}

func (peerNode) observe(s *shardController, f flushResult) {
	f.peer = s.normS
	s.observe(f)
}

// TestShardControllerHoldsThroughASlowdownItsPeersShare: the whole node slows, as it does when
// it is busy, and every shard on it slows alike. That is the node's load, which its
// window answers when the node rejects; a shard that is no slower than the rest of
// the node keeps its size, so that the node is sent as much as it takes until it
// says it is full.
func TestShardControllerHoldsThroughASlowdownItsPeersShare(t *testing.T) {
	t.Parallel()

	node := peerNode{}
	s := newTestShardController(BulkSizing{})
	for range 200 {
		node.observe(s, flushAtThroughput(s.bytes(), 100e6))
	}
	require.EqualValues(t, testCeiling, s.bytes())

	for range 200 {
		node.observe(s, flushAtThroughput(s.bytes(), 20e6)) // Five times slower, all of it.
	}
	require.EqualValues(t, testCeiling, s.bytes(), "a slowdown the shard shares with its node is not the shard's to answer")
}

// TestShardControllerBacksOffAShardSlowerThanItsPeers: the rest of the node is as quick as
// ever and this shard alone slows, as a hot shard or one with costly documents does.
// It must shrink, so that it does not take the node's time from the others.
func TestShardControllerBacksOffAShardSlowerThanItsPeers(t *testing.T) {
	t.Parallel()

	s := newTestShardController(BulkSizing{})
	healthy := func(bytes int64, rate float64) flushResult {
		f := flushAtThroughput(bytes, rate)
		f.peer = 1 // The node's other shards are at their best.

		return f
	}
	for range 200 {
		s.observe(healthy(s.bytes(), 100e6))
	}
	before := s.bytes()

	for range 10 {
		s.observe(healthy(s.bytes(), 20e6))
	}
	require.Less(t, s.bytes(), before/2, "a shard five times slower than its node's others must shrink")
}
