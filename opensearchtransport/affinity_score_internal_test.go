// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testClock is a deterministic, frozen clock for tests. Now() always
// returns the same time unless Advance() is called between reads.
//
// Safe for concurrent use (atomic counter).
type testClock struct {
	current atomic.Int64
}

// newTestClock creates a test clock frozen at 1s (arbitrary non-zero epoch).
// Call Advance() for explicit time jumps between operations.
func newTestClock() *testClock {
	c := &testClock{}
	c.current.Store(int64(1 * time.Second)) // 1s epoch
	return c
}

// Now returns the current frozen time.
func (c *testClock) Now() time.Time {
	return time.Unix(0, c.current.Load())
}

// Advance moves the clock forward by d.
func (c *testClock) Advance(d time.Duration) {
	c.current.Add(int64(d))
}

func TestDecayCounterAdd(t *testing.T) {
	t.Parallel()

	t.Run("add with variable value", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		// 0.0 * 1.0 + 5.0 = 5.0
		got := c.add(1.0, 5.0)
		require.InDelta(t, 5.0, got, 1e-9)
	})

	t.Run("accumulates without decay", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		// With decay=1.0 (no decay), pure accumulation.
		c.add(1.0, 10.0)
		c.add(1.0, 20.0)
		got := c.load()
		require.InDelta(t, 30.0, got, 1e-9)
	})

	t.Run("add with decay reduces prior value", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		c.store(100.0)
		// 100.0 * 0.5 + 25.0 = 75.0
		got := c.add(0.5, 25.0)
		require.InDelta(t, 75.0, got, 1e-9)
	})
}

func TestDecayCounterIncrement(t *testing.T) {
	t.Parallel()

	t.Run("first increment from zero", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		got := c.increment(0.999)
		// 0.0 * 0.999 + 1.0 = 1.0
		require.InDelta(t, 1.0, got, 1e-9)
	})

	t.Run("successive increments converge", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		decay := 0.999

		// Steady state = 1/(1-decay) = 1000
		for range 10000 {
			c.increment(decay)
		}

		// After enough iterations, should be near steady state.
		got := c.load()
		steadyState := 1.0 / (1.0 - decay)
		require.InDelta(t, steadyState, got, steadyState*0.02, "should converge to ~1000")
	})

	t.Run("different decay factors", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		decay := 0.99 // steady state = 100

		for range 2000 {
			c.increment(decay)
		}

		got := c.load()
		require.InDelta(t, 100.0, got, 5.0, "decay=0.99 should converge to ~100")
	})
}

func TestDecayCounterDecay(t *testing.T) {
	t.Parallel()

	t.Run("decay without increment reduces value", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		c.store(1000.0)

		got := c.decay(0.5)
		require.InDelta(t, 500.0, got, 1e-9)

		got = c.decay(0.5)
		require.InDelta(t, 250.0, got, 1e-9)
	})

	t.Run("repeated decay converges to zero", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		c.store(1000.0)

		for range 200 {
			c.decay(0.9)
		}

		got := c.load()
		require.Less(t, got, 0.01, "should converge toward zero")
	})
}

func TestDecayCounterLoadStore(t *testing.T) {
	t.Parallel()

	t.Run("initial value is zero", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		require.InDelta(t, 0.0, c.load(), 0)
	})

	t.Run("store and load roundtrip", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		c.store(42.5)
		require.InDelta(t, 42.5, c.load(), 0)
	})

	t.Run("store special values", func(t *testing.T) {
		t.Parallel()
		var c decayCounter
		c.store(math.MaxFloat64)
		require.InDelta(t, math.MaxFloat64, c.load(), 0)

		c.store(math.SmallestNonzeroFloat64)
		require.InDelta(t, math.SmallestNonzeroFloat64, c.load(), 0)
	})
}

func TestDecayCounterConcurrency(t *testing.T) {
	t.Parallel()

	// Verify that concurrent increments don't lose updates (CAS loop is correct).
	var c decayCounter
	decay := 0.999

	const goroutines = 10
	const iterations = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				c.increment(decay)
			}
		}()
	}
	wg.Wait()

	// The counter should be positive and reasonable.
	// Exact value depends on interleaving, but must be > 0.
	got := c.load()
	require.Greater(t, got, 0.0, "counter should be positive after concurrent increments")
}

// --- timeWeightedCounter tests ---

func TestTimeWeightedCounter_HalfLife(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		advance  time.Duration
		expected float64
	}{
		{"no time elapsed", 0, 1000.0},
		{"one half-life (5s)", 5 * time.Second, 500.0},
		{"two half-lives (10s)", 10 * time.Second, 250.0},
		{"three half-lives (15s)", 15 * time.Second, 125.0},
		{"99% decay (~33s)", 33 * time.Second, 1000.0 * math.Exp(-affinityDecayLambda*33)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := newTestClock()
			c := timeWeightedCounter{clock: clk}
			c.store(1000.0)

			clk.Advance(tt.advance)
			got := c.load()
			require.InDelta(t, tt.expected, got, 0.01, "load after %v", tt.advance)
		})
	}
}

func TestTimeWeightedCounter_AddWithDecay(t *testing.T) {
	t.Parallel()
	clk := newTestClock()
	c := timeWeightedCounter{clock: clk}

	// Step 1: first add on fresh counter (nanoTime == 0 → no decay).
	got := c.add(100.0)
	require.InDelta(t, 100.0, got, 0.01, "first add should return value without decay")

	// Step 2: advance one half-life, then add 100.
	// Prior 100 decays to 50, plus new 100 = 150.
	clk.Advance(5 * time.Second)
	got = c.add(100.0)
	require.InDelta(t, 150.0, got, 0.01, "add after one half-life: 100*0.5 + 100")

	// Step 3: advance another half-life, read with load().
	// 150 decays to 75.
	clk.Advance(5 * time.Second)
	got = c.load()
	require.InDelta(t, 75.0, got, 0.01, "load after one half-life of 150")
}

func TestTimeWeightedCounter_StoreResetsClock(t *testing.T) {
	t.Parallel()
	clk := newTestClock()
	c := timeWeightedCounter{clock: clk}

	// First store at t=0.
	c.store(1000.0)

	// Advance 5s, then store a new value. The second store resets
	// the timestamp, so subsequent decay is measured from the second store.
	clk.Advance(5 * time.Second)
	c.store(500.0)

	// Advance 5s from the second store (one half-life).
	clk.Advance(5 * time.Second)
	got := c.load()
	require.InDelta(t, 250.0, got, 0.01, "should decay from second store (500), not first (1000)")
}

func TestTimeWeightedCounter_FirstAddNoDecay(t *testing.T) {
	t.Parallel()
	clk := newTestClock()
	c := timeWeightedCounter{clock: clk}

	// Even after a large time advance, the first add should not decay
	// because nanoTime == 0 forces dt = 0.
	clk.Advance(100 * time.Second)
	got := c.add(42.0)
	require.InDelta(t, 42.0, got, 0.01, "first add should ignore elapsed time (nanoTime guard)")
}

func TestTimeWeightedCounter_LargeTimeJump(t *testing.T) {
	t.Parallel()

	t.Run("60s decay reduces by 12 half-lives", func(t *testing.T) {
		t.Parallel()
		clk := newTestClock()
		c := timeWeightedCounter{clock: clk}
		c.store(1e6)

		clk.Advance(60 * time.Second)
		got := c.load()
		// 60s / 5s = 12 half-lives → 1e6 / 2^12 ≈ 244.14
		expected := 1e6 * math.Exp(-affinityDecayLambda*60)
		require.InDelta(t, expected, got, 0.01, "60s decay of 1e6")
		require.Less(t, got, 250.0, "12 half-lives should reduce 1e6 to ~244")
		require.Greater(t, got, 240.0, "12 half-lives should reduce 1e6 to ~244")
	})

	t.Run("300s decay effectively zero", func(t *testing.T) {
		t.Parallel()
		clk := newTestClock()
		c := timeWeightedCounter{clock: clk}
		c.store(1e6)

		clk.Advance(300 * time.Second)
		got := c.load()
		require.Less(t, got, 1e-12, "should be effectively zero after 300s")
	})
}

func TestTimeWeightedCounter_SteadyState(t *testing.T) {
	t.Parallel()
	clk := newTestClock()
	c := timeWeightedCounter{clock: clk}

	// Simulate a constant request rate: add cost v every interval.
	// At steady state: counter → v / (1 - exp(-lambda * interval)).
	v := 60.0
	interval := 100 * time.Millisecond
	intervalSec := interval.Seconds()
	expectedSteadyState := v / (1 - math.Exp(-affinityDecayLambda*intervalSec))

	var got float64
	for range 500 {
		got = c.add(v)
		clk.Advance(interval)
	}

	// After 500 iterations at 100ms intervals (50s = 10 half-lives),
	// the counter should have converged.
	require.InDelta(t, expectedSteadyState, got, expectedSteadyState*0.01,
		"should converge to v/(1-exp(-λ*interval)) ≈ %.1f", expectedSteadyState)
}

func TestTimeWeightedCounter_ConcurrentAddFrozen(t *testing.T) {
	t.Parallel()

	// With a frozen clock, dt=0 on every add after the first, so no
	// decay occurs — pure accumulation. Final value must equal N*M.
	clk := newTestClock()
	c := timeWeightedCounter{clock: clk}

	const goroutines = 10
	const iterations = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				c.add(1.0)
			}
		}()
	}
	wg.Wait()

	got := c.load()
	expected := float64(goroutines * iterations)
	require.InDelta(t, expected, got, 0.01,
		"frozen clock: concurrent adds should accumulate to %v", expected)
}
