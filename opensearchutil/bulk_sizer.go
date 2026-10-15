// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"math"
	"time"
)

// BulkSizing tunes how the size of the batches sent to a node follows what the
// node can take. Every field is optional; the zero value selects the defaults
// below, which are all derived from BulkIndexerConfig.FlushBytes.
//
// The size is steered by two signals from the flushes the indexer sends to a node.
//
//   - A rejection, a 429 from the node's write thread pool, cuts the size by
//     Backoff at once and holds it there for a few flushes.
//   - The node's service rate, bytes sent per second of time on the node
//     (the round trip less the network distance), is compared with the best rate
//     seen recently. While the rate stays within Headroom of that best, the size
//     grows by StepBytes per flush. When the node slows by more than that, the
//     size is cut in proportion to how far past Headroom it is, long before the
//     node starts rejecting, so that bulk traffic gives way to the other requests
//     a node is serving. A PID controller (Kp, Ki, Kd) on the normalized slowdown
//     sets how hard each step is.
type BulkSizing struct {
	// Fixed turns the controller off: every batch is FlushBytes.
	Fixed bool

	// MinBytes is the smallest batch the controller will ask for. Defaults to
	// FlushBytes/32, and at least 1.
	MinBytes int
	// InitialBytes is the size a node's batches start at. Defaults to
	// FlushBytes/4.
	InitialBytes int
	// StepBytes is the additive increase per healthy flush at full gain.
	// Defaults to FlushBytes/16.
	StepBytes int

	// Headroom is how much slower than its best recent rate a node may get before
	// the size is cut: 0.25 allows the rate to fall to 80% of the best. Defaults
	// to 0.25.
	Headroom float64
	// Backoff is the factor the size is multiplied by when the node rejects a
	// request. Defaults to 0.5.
	Backoff float64

	// Kp, Ki and Kd are the PID gains on the normalized slowdown. When all three
	// are zero they default to 1, 0.1 and 0.25.
	Kp, Ki, Kd float64
}

const (
	// sizingDefaultHeadroom is the slowdown a node may show before backing off.
	sizingDefaultHeadroom = 0.25
	// sizingDefaultBackoff halves the size on a rejection.
	sizingDefaultBackoff = 0.5
	// sizingMinDivisor, sizingInitialDivisor and sizingStepDivisor derive the
	// defaults from FlushBytes.
	sizingMinDivisor     = 32
	sizingInitialDivisor = 4
	sizingStepDivisor    = 16
	// Default PID gains.
	sizingDefaultKp = 1.0
	sizingDefaultKi = 0.1
	sizingDefaultKd = 0.25
	// sizingIntegralLimit bounds the integral term, which stops a long healthy
	// stretch from muting the response to the first sign of trouble.
	sizingIntegralLimit = 2.0
	// sizingMaxCut is the largest fraction a single non-rejecting flush removes.
	sizingMaxCut = 0.5
	// sizingCrowdedExponent sets how far a crowded node's limit is cut: the backoff
	// to this power, so a quarter of the way in the log from 1 to the backoff.
	sizingCrowdedExponent = 0.25
	// sizingHoldFlushes is how many healthy flushes after a rejection pass before
	// the size may grow again.
	sizingHoldFlushes = 3
	// sizingBaseDecay is how fast the memory of the best rate fades per sample while
	// the size is above the floor. It is slow because a node slows gradually as the
	// batches grow, and a memory that faded as fast as that would never notice.
	sizingBaseDecay = 0.9999
	// sizingFloorDecay is how fast it fades while the size is at the floor. A node
	// that is slow even for the smallest batches has not been made slow by them, so
	// it is how the node is now, and the size is allowed to climb back after a few
	// hundred flushes instead of staying cut forever.
	sizingFloorDecay = 0.995
	// sizingMinSampleFraction is the part of the current target a flush must
	// carry for its rate to say anything: the last, partial batch of a burst is
	// dominated by fixed overhead.
	sizingMinSampleFraction = 0.25
	// sizingMinServiceFraction floors the service time at this part of the round
	// trip, so a round trip shorter than the measured distance cannot give an
	// absurd rate.
	sizingMinServiceFraction = 0.25
)

// resolvedSizing is BulkSizing with its defaults applied.
type resolvedSizing struct {
	fixed    bool
	min, max float64
	initial  float64
	step     float64
	// sampleFloor, when set, is the smallest request that tells the controller
	// anything. Unset, a request must carry a quarter of the current size, which
	// suits a size that follows the requests themselves; a node's limit is a
	// multiple of any one request, so it is given a floor of its own.
	sampleFloor float64
	headroom    float64
	backoff     float64
	kp, ki, kd  float64
}

// resolve applies the defaults for a ceiling of flushBytes.
func (c BulkSizing) resolve(flushBytes int) resolvedSizing {
	ceiling := float64(max(flushBytes, 1))

	r := resolvedSizing{
		fixed:    c.Fixed,
		max:      ceiling,
		min:      pick(float64(c.MinBytes), ceiling/sizingMinDivisor),
		initial:  pick(float64(c.InitialBytes), ceiling/sizingInitialDivisor),
		step:     pick(float64(c.StepBytes), ceiling/sizingStepDivisor),
		headroom: pick(c.Headroom, sizingDefaultHeadroom),
		backoff:  pick(c.Backoff, sizingDefaultBackoff),
		kp:       c.Kp,
		ki:       c.Ki,
		kd:       c.Kd,
	}

	if c.Kp == 0 && c.Ki == 0 && c.Kd == 0 {
		r.kp, r.ki, r.kd = sizingDefaultKp, sizingDefaultKi, sizingDefaultKd
	}

	r.min = max(math.Min(r.min, ceiling), 1)
	r.initial = math.Min(math.Max(r.initial, r.min), ceiling)
	r.backoff = math.Min(math.Max(r.backoff, 0.05), 0.95)

	if r.fixed {
		r.min, r.initial = ceiling, ceiling
	}

	return r
}

// pick returns v, or def when v is not set.
func pick(v, def float64) float64 {
	if v > 0 {
		return v
	}

	return def
}

// flushSample is what the indexer learned from one flush to a node.
type flushSample struct {
	// bytes is the size of the request body.
	bytes int64
	// latency is the time from sending the request to the end of the response.
	latency time.Duration
	// distance is the node's round trip time on an idle connection, which is not
	// the node's doing; 0 when unknown.
	distance time.Duration
	// rejected is set when the node answered 429 to the request or to any item.
	rejected bool
	// failed is set when the request did not complete for another reason.
	failed bool
	// overloaded is set when the transport sees the node's write pool rejecting.
	overloaded bool
	// crowded is set when what was buffered for the node outgrew its limit, so the
	// node is not taking what it is given as fast as it is given it.
	crowded bool
}

// sizer decides how large a batch for one node should be. It is not safe for
// concurrent use; the scheduler's lock serializes it.
type sizer struct {
	cfg resolvedSizing

	target   float64
	baseRate float64 // best recent bytes per nanosecond
	integral float64
	prevErr  float64
	hold     int
}

func newSizer(cfg resolvedSizing) *sizer {
	return &sizer{cfg: cfg, target: cfg.initial}
}

// sampleFloor is the smallest request, in bytes, whose rate means anything.
func (s *sizer) sampleFloor() float64 {
	if s.cfg.sampleFloor > 0 {
		return s.cfg.sampleFloor
	}

	return s.target * sizingMinSampleFraction
}

// ceiling returns the largest size the controller ever asks for.
func (s *sizer) ceiling() int64 {
	return int64(s.cfg.max)
}

// bytes returns the batch size to aim for now.
func (s *sizer) bytes() int64 {
	return int64(s.target)
}

// observe feeds the outcome of a flush to the controller.
func (s *sizer) observe(f flushSample) {
	if s.cfg.fixed {
		return
	}

	switch {
	case f.rejected:
		s.target *= s.cfg.backoff
		s.integral = 0
		s.hold = sizingHoldFlushes
	case f.failed:
		s.target *= math.Sqrt(s.cfg.backoff)
		s.integral = 0
		s.hold = sizingHoldFlushes
	case f.crowded:
		// A quarter of the way, in the log, to a rejection's cut: a node that is
		// being given more than it takes is told so gently and often.
		s.target *= math.Pow(s.cfg.backoff, sizingCrowdedExponent)
	case float64(f.bytes) < s.sampleFloor():
		// Too small to tell anything about the node.
		return
	default:
		s.steer(f)
	}

	s.target = math.Min(math.Max(s.target, s.cfg.min), s.cfg.max)
}

// steer applies the PID-modulated step for a flush that completed.
func (s *sizer) steer(f flushSample) {
	service := f.latency - f.distance
	if floor := time.Duration(float64(f.latency) * sizingMinServiceFraction); service < floor {
		service = floor
	}
	if service <= 0 {
		return
	}

	rate := float64(f.bytes) / float64(service)

	decay := sizingBaseDecay
	if s.target <= s.cfg.min {
		decay = sizingFloorDecay
	}
	s.baseRate = math.Max(rate, s.baseRate*decay)

	// slowdown is 0 at the best rate seen and 1 when the node takes twice as long
	// for the same bytes.
	slowdown := s.baseRate/rate - 1

	// e is +1 for a node as quick as ever, 0 at the headroom, and -1 or below
	// (clamped) when it has slowed by twice the headroom or more.
	e := math.Min(math.Max((s.cfg.headroom-slowdown)/s.cfg.headroom, -1), 1)
	if f.overloaded {
		e = math.Min(e, 0)
	}

	s.integral = math.Min(math.Max(s.integral+e, -sizingIntegralLimit), sizingIntegralLimit)
	derivative := e - s.prevErr
	s.prevErr = e

	u := s.cfg.kp*e + s.cfg.ki*s.integral + s.cfg.kd*derivative

	switch {
	case u >= 0 && s.hold > 0:
		s.hold--
	case u >= 0:
		s.target += s.cfg.step * math.Min(u, 1)
	default:
		s.target *= 1 + math.Max(u, -sizingMaxCut)
	}
}
