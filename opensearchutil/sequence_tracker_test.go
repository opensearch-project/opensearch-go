// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil_test

import (
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// lowestIncomplete returns the lowest sequence in [base, base+n) that is not in
// done, or base+n when all of them are.
func lowestIncomplete(base int64, n int, done map[int64]bool) int64 {
	for seq := base; seq < base+int64(n); seq++ {
		if !done[seq] {
			return seq
		}
	}

	return base + int64(n)
}

func TestSequenceTracker(t *testing.T) {
	t.Parallel()

	type step struct {
		complete []int64 // sequences completed on "p", in order
		want     int64   // Safe("p") after them
	}

	tests := []struct {
		name  string
		begin int64
		steps []step
	}{
		{
			name:  "in order advances with each completion",
			begin: 10,
			steps: []step{{[]int64{10}, 11}, {[]int64{11}, 12}, {[]int64{12, 13}, 14}},
		},
		{
			name:  "nothing completed reports where it began",
			begin: 10,
			steps: []step{{nil, 10}},
		},
		{
			name:  "a gap holds the line until it fills",
			begin: 0,
			steps: []step{{[]int64{1, 2, 3}, 0}, {[]int64{0}, 4}},
		},
		{
			name:  "a hole in the middle stops the line below it",
			begin: 0,
			steps: []step{{[]int64{0, 1, 3, 4}, 2}, {[]int64{2}, 5}},
		},
		{
			name:  "completing out of order across several gaps",
			begin: 5,
			steps: []step{{[]int64{9, 7, 5}, 6}, {[]int64{8}, 6}, {[]int64{6}, 10}},
		},
		{
			name:  "a repeated completion changes nothing",
			begin: 0,
			steps: []step{{[]int64{0, 0, 2, 2}, 1}, {[]int64{1, 1}, 3}},
		},
		{
			name:  "a sequence below where it began is ignored",
			begin: 10,
			steps: []step{{[]int64{3, 9}, 10}, {[]int64{10}, 11}},
		},
		{
			name:  "a sequence never completed stalls every later one",
			begin: 0,
			steps: []step{{[]int64{0, 2, 3, 4, 5, 6, 7, 8, 9}, 1}},
		},
		{
			name:  "negative sequences",
			begin: -3,
			steps: []step{{[]int64{-2, -1}, -3}, {[]int64{-3}, 0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tracker opensearchutil.SequenceTracker[string]
			tracker.Begin("p", tt.begin)

			for i, s := range tt.steps {
				for _, seq := range s.complete {
					tracker.Complete("p", seq)
				}

				got, ok := tracker.Safe("p")
				require.True(t, ok)
				require.Equal(t, s.want, got, "step %d", i)
			}
		})
	}
}

func TestSequenceTrackerStreams(t *testing.T) {
	t.Parallel()

	t.Run("streams are independent", func(t *testing.T) {
		t.Parallel()

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Begin("a", 0)
		tracker.Begin("b", 100)

		tracker.Complete("a", 0)
		tracker.Complete("a", 1)
		tracker.Complete("b", 101)

		require.Equal(t, map[string]int64{"a": 2, "b": 100}, tracker.SafeAll())
	})

	t.Run("SafeAll is a snapshot the caller owns", func(t *testing.T) {
		t.Parallel()

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Begin("a", 0)

		snapshot := tracker.SafeAll()
		snapshot["a"] = 99
		snapshot["b"] = 5
		tracker.Complete("a", 0)

		require.Equal(t, map[string]int64{"a": 1}, tracker.SafeAll())
	})

	t.Run("an untracked stream reports nothing", func(t *testing.T) {
		t.Parallel()

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Complete("a", 0)

		_, ok := tracker.Safe("a")
		require.False(t, ok)
		_, ok = tracker.Forget("a")
		require.False(t, ok)
		require.Empty(t, tracker.SafeAll())
	})

	t.Run("Begin on a tracked stream starts it over", func(t *testing.T) {
		t.Parallel()

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Begin("a", 0)
		tracker.Complete("a", 0)
		tracker.Complete("a", 5)

		tracker.Begin("a", 3)
		got, ok := tracker.Safe("a")
		require.True(t, ok)
		require.Equal(t, int64(3), got)

		tracker.Complete("a", 3)
		got, _ = tracker.Safe("a")
		require.Equal(t, int64(4), got, "the completion of 5 before the restart is forgotten")
	})

	t.Run("Forget returns the final position and stops tracking", func(t *testing.T) {
		t.Parallel()

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Begin("a", 0)
		tracker.Begin("b", 0)
		tracker.Complete("a", 0)
		tracker.Complete("a", 1)
		tracker.Complete("a", 3)

		got, ok := tracker.Forget("a")
		require.True(t, ok)
		require.Equal(t, int64(2), got, "stops below the hole at 2")

		tracker.Complete("a", 2)
		_, ok = tracker.Safe("a")
		require.False(t, ok, "a late completion does not bring the stream back")
		require.Equal(t, map[string]int64{"b": 0}, tracker.SafeAll())
	})
}

// TestSequenceTrackerCompletionOrder completes every sequence of a stream in
// many orders and checks, after each completion, that the reported position is
// exactly the lowest sequence still incomplete: never above it, which would
// commit past a gap, and never below it, which would stall for nothing.
func TestSequenceTrackerCompletionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		n     int
		seed  uint64
		order func(n int, rng *rand.Rand) []int
	}{
		{name: "ascending", n: 200, order: func(n int, _ *rand.Rand) []int {
			order := make([]int, n)
			for i := range order {
				order[i] = i
			}
			return order
		}},
		{name: "descending", n: 200, order: func(n int, _ *rand.Rand) []int {
			order := make([]int, n)
			for i := range order {
				order[i] = n - 1 - i
			}
			return order
		}},
		{name: "shuffled", n: 500, seed: 1, order: func(n int, rng *rand.Rand) []int { return rng.Perm(n) }},
		{name: "shuffled with repeats", n: 300, seed: 2, order: func(n int, rng *rand.Rand) []int {
			order := rng.Perm(n)
			for range n {
				order = append(order, rng.IntN(n))
			}
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			return order
		}},
	}

	const base = int64(1000)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A fixed seed per case keeps a failure reproducible.
			rng := rand.New(rand.NewPCG(tt.seed, 1))

			var tracker opensearchutil.SequenceTracker[int]
			tracker.Begin(0, base)

			done := make(map[int64]bool)
			for _, i := range tt.order(tt.n, rng) {
				seq := base + int64(i)
				tracker.Complete(0, seq)
				done[seq] = true

				got, ok := tracker.Safe(0)
				require.True(t, ok)
				require.Equal(t, lowestIncomplete(base, tt.n, done), got, "after completing %d", seq)
			}

			got, _ := tracker.Safe(0)
			require.Equal(t, base+int64(tt.n), got, "everything completed")
		})
	}
}

// FuzzSequenceTrackerNeverPassesAGap completes sequences in an order the fuzzer
// chooses, with repeats and sequences from before the stream began mixed in, and
// asserts after every call that the reported position is the lowest incomplete
// sequence. Sequences that the data never completes must hold the position.
func FuzzSequenceTrackerNeverPassesAGap(f *testing.F) {
	f.Add(int64(0), []byte{8, 3, 1, 0, 2, 7, 5, 4, 6})
	f.Add(int64(100), []byte{4, 3, 3, 2, 1, 0})
	f.Add(int64(-5), []byte{16, 15, 14, 13, 12, 0x81, 0x80, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	f.Add(int64(1<<40), []byte{1, 0, 0, 0})
	f.Add(int64(0), []byte{64, 1, 2, 3})

	f.Fuzz(func(t *testing.T, rawBase int64, data []byte) {
		if len(data) == 0 {
			t.Skip()
		}

		// Keep base + n clear of overflow.
		base := rawBase % (1 << 50)
		n := int(data[0])%64 + 1

		var tracker opensearchutil.SequenceTracker[string]
		tracker.Begin("p", base)

		done := make(map[int64]bool)
		for _, b := range data[1:] {
			seq := base + int64(int(b)%n)
			if b&0x80 != 0 {
				// A sequence from before the stream began.
				seq = base - 1 - int64(b&0x3)
			}

			tracker.Complete("p", seq)
			done[seq] = true

			got, ok := tracker.Safe("p")
			require.True(t, ok)
			want := lowestIncomplete(base, n, done)
			require.Equal(t, want, got, "after completing %d", seq)
			require.LessOrEqual(t, got, want, "the position must not pass the lowest incomplete sequence")
		}
	})
}

func TestSequenceTrackerConcurrent(t *testing.T) {
	t.Parallel()

	const (
		streams = 4
		perKey  = 2000
		workers = 8
	)

	var tracker opensearchutil.SequenceTracker[int]
	for k := range streams {
		tracker.Begin(k, 0)
	}

	// Every worker completes a disjoint share of each stream's sequences, in a
	// shuffled order, while another goroutine reads the positions.
	var wg sync.WaitGroup
	stopReading := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stopReading:
				return
			default:
				_ = tracker.SafeAll()
			}
		}
	}()

	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(7, 7))
			for _, i := range rng.Perm(perKey) {
				if i%workers != w {
					continue
				}
				for k := range streams {
					tracker.Complete(k, int64(i))
				}
			}
		})
	}

	wg.Wait()
	close(stopReading)
	<-readerDone

	for k := range streams {
		got, ok := tracker.Safe(k)
		require.True(t, ok)
		require.Equal(t, int64(perKey), got, "stream %d", k)
	}
}
