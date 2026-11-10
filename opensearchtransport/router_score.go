// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"math"
	"sync/atomic"
	"time"
)

// clock provides timestamps for time-weighted counters.
// Production code uses realClock; tests inject a testClock for
// deterministic behavior.
type clock interface {
	Now() time.Time
}

// realClock reads the system clock.
type realClock struct{}

// Now returns the current wall-clock time.
func (realClock) Now() time.Time { return time.Now() }

const (
	// defaultDecayFactor is the per-request multiplicative decay for [decayCounter].
	// At constant request rate r, the counter converges to r/(1-decay).
	// With decay=0.999 and r=1 req/call: steady state ~ 1000.
	defaultDecayFactor = 0.999

	// defaultLoadHalfLife controls how fast the time-weighted load
	// counter decays. A 5-second half-life means:
	//   - At steady state with rate R and cost v: counter ~= v*R / lambda ~= v*R*7.2
	//   - Counter halves in 5s with no new requests
	//   - 99% decay in ~33s
	//   - Responsive enough for sub-second routing adjustments
	defaultLoadHalfLife = 5 * time.Second
)

// loadDecayLambda is the precomputed decay rate for the time-weighted
// load counter: lambda = ln(2) / halfLife.
//
//nolint:gochecknoglobals // Precomputed constant derived from defaultLoadHalfLife.
var loadDecayLambda = math.Ln2 / defaultLoadHalfLife.Seconds()

// decayCounter is an exponentially decaying load accumulator.
//
// On each add:
//
//	counter = counter * decay + value
//
// The counter converges to a steady-state that reflects both request
// rate and per-request cost. When requests stop, the counter decays
// toward zero with each subsequent add call or explicit decay step.
//
// This provides self-stabilizing load tracking without periodic resets:
// old load naturally fades, no sawtooth patterns, no coordination.
//
// The value is stored as float64 bits in an atomic.Uint64 for lock-free
// CAS updates.
type decayCounter struct {
	bits atomic.Uint64
}

// increment applies one decay step and adds 1.0 to the counter.
// Returns the new value. Safe for concurrent use (CAS loop).
// Used by indexSlot.requestDecay for per-index request volume tracking.
func (dc *decayCounter) increment(decay float64) float64 {
	for {
		old := dc.bits.Load()
		oldVal := math.Float64frombits(old)
		newVal := oldVal*decay + 1.0
		if dc.bits.CompareAndSwap(old, math.Float64bits(newVal)) {
			return newVal
		}
	}
}

// add applies one decay step and adds a variable amount to the counter.
// Returns the new value. Safe for concurrent use (CAS loop).
// Used for CPU-load-based routing where each request contributes
// a cost proportional to its server-side processing time.
func (dc *decayCounter) add(decay float64, value float64) float64 {
	for {
		old := dc.bits.Load()
		oldVal := math.Float64frombits(old)
		newVal := oldVal*decay + value
		if dc.bits.CompareAndSwap(old, math.Float64bits(newVal)) {
			return newVal
		}
	}
}

// decay applies one decay step without adding load.
// Used for periodic idle-connection drain and fan-out contraction.
func (dc *decayCounter) decay(factor float64) float64 {
	for {
		old := dc.bits.Load()
		oldVal := math.Float64frombits(old)
		newVal := oldVal * factor
		if dc.bits.CompareAndSwap(old, math.Float64bits(newVal)) {
			return newVal
		}
	}
}

// load returns the current counter value.
func (dc *decayCounter) load() float64 {
	return math.Float64frombits(dc.bits.Load())
}

// store sets the counter to an exact value.
func (dc *decayCounter) store(v float64) {
	dc.bits.Store(math.Float64bits(v))
}

// timeWeightedCounter is a time-decaying load accumulator using an
// exponentially weighted moving average (EWMA) tied to wall clock time.
//
// Unlike [decayCounter] where the decay rate depends on request frequency,
// this counter decays continuously based on elapsed time:
//
//	counter = counter * e^(-lambda * dt) + value
//
// Where dt is wall clock seconds since the last update and lambda = ln(2)/halfLife.
// This decouples decay from request rate: idle nodes drain quickly regardless
// of how few requests arrive, and busy nodes reflect actual load per unit time.
//
// At steady state with request rate R and per-request cost v:
//
//	counter ~= v * R / lambda
//
// With halfLife=5s, lambda~=0.1386: counter ~= 7.2 * v * R.
//
// The counter and timestamp are stored in separate atomics. The small race
// window between the CAS on the value and the timestamp store is benign:
// one request may apply slightly too much or too little decay, corrected
// on the next update.
type timeWeightedCounter struct {
	bits     atomic.Uint64 // float64 stored as bits
	nanoTime atomic.Int64  // last update time (UnixNano); 0 = never updated
	clock    clock         // required; realClock{} in production, testClock in tests
}

// now returns the current time via the injected clock.
func (twc *timeWeightedCounter) now() time.Time {
	return twc.clock.Now()
}

// add applies time-based decay since the last update and adds value.
// Safe for concurrent use (CAS loop).
func (twc *timeWeightedCounter) add(value float64) float64 {
	now := twc.now().UnixNano()

	for {
		oldBits := twc.bits.Load()
		oldNano := twc.nanoTime.Load()

		oldVal := math.Float64frombits(oldBits)
		dt := float64(now-oldNano) / 1e9
		if dt < 0 || oldNano == 0 {
			dt = 0 // First update or clock skew: no decay.
		}

		newVal := oldVal*math.Exp(-loadDecayLambda*dt) + value
		if twc.bits.CompareAndSwap(oldBits, math.Float64bits(newVal)) {
			twc.nanoTime.Store(now)
			return newVal
		}
	}
}

// load returns the current value decayed to the present time.
// Safe for concurrent use (read-only; does not update stored state).
func (twc *timeWeightedCounter) load() float64 {
	bits := twc.bits.Load()
	nano := twc.nanoTime.Load()

	val := math.Float64frombits(bits)
	if nano == 0 {
		return val // Never updated.
	}

	dt := float64(twc.now().UnixNano()-nano) / 1e9
	if dt <= 0 {
		return val
	}
	return val * math.Exp(-loadDecayLambda*dt)
}

// store sets the counter to an exact value and records the current time.
// Used in tests.
func (twc *timeWeightedCounter) store(v float64) {
	twc.bits.Store(math.Float64bits(v))
	twc.nanoTime.Store(twc.now().UnixNano())
}

// peakEWMADecay is the decay window of [peakEWMA]: the time constant tau of
// the exponential blend. Ten seconds matches Finagle's Peak-EWMA load balancer:
// long enough that one fast response does not hide a slow node, short enough
// that a node which spiked and then received no traffic is retried within
// seconds rather than starved.
const peakEWMADecay = 10 * time.Second

// peakEWMA is a peak-sensitive, time-decayed moving average of request
// latency in microseconds (Finagle's Peak-EWMA).
//
// A sample at or above the current value, decayed to now, replaces it
// outright, so a slowdown is seen at once. A lower sample blends in with
// weight 1 - e^(-dt/tau), where dt is the time since the last sample and the
// stored value is decayed by e^(-dt/tau) once, so recovery is gradual. load decays the value to the present without storing
// it, so an idle node drifts back toward zero and is probed again.
//
// The value and timestamp are separate atomics. The small race window between
// them is benign: one read may apply slightly too much or too little decay,
// corrected by the next sample.
type peakEWMA struct {
	bits     atomic.Uint64 // float64 microseconds stored as bits
	nanoTime atomic.Int64  // last sample time (UnixNano); 0 = never sampled
	clock    clock         // nil reads time.Now
}

func (p *peakEWMA) nowNano() int64 {
	if p.clock == nil {
		return time.Now().UnixNano()
	}
	return p.clock.Now().UnixNano()
}

// peakDecay returns v decayed over the time between last and now.
func peakDecay(v float64, last, now int64) float64 {
	dt := float64(now-last) / float64(peakEWMADecay)
	if dt <= 0 {
		return v
	}
	return v * math.Exp(-dt)
}

// sample records one latency observation. Safe for concurrent use.
func (p *peakEWMA) sample(d time.Duration) {
	us := float64(d) / float64(time.Microsecond)
	now := p.nowNano()
	for {
		oldBits := p.bits.Load()
		last := p.nanoTime.Load()

		next := us
		if last != 0 {
			v := math.Float64frombits(oldBits)
			w := math.Exp(-max(float64(now-last), 0) / float64(peakEWMADecay))
			if us < v*w {
				next = v*w + us*(1-w)
			}
		}
		if p.bits.CompareAndSwap(oldBits, math.Float64bits(next)) {
			p.nanoTime.Store(now)
			return
		}
	}
}

// load returns the value in microseconds decayed to now, and false when no
// sample has been recorded.
func (p *peakEWMA) load() (float64, bool) {
	last := p.nanoTime.Load()
	if last == 0 {
		return 0, false
	}
	return peakDecay(math.Float64frombits(p.bits.Load()), last, p.nowNano()), true
}
