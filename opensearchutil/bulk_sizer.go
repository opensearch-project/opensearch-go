// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// BulkSizing tunes how the indexer follows what a node can take. Every field is
// optional; the zero value selects the defaults below, which are all derived from
// BulkIndexerConfig.FlushBytes.
//
// Two controllers work, each on one thing and each on one signal, and each can be
// turned off on its own: Shard sets how a shard's buffer is sized, and Node sets
// how a node's window answers 429s.
//
// The shard-buffer controller is a PID controller on latency (ShardPID, the
// default). The size of a shard's buffer, the bytes it collects before it is sent,
// follows how fast the node serves that shard's requests next to the node's other
// shards. Each flush's time on the node (the round trip less the network distance)
// is compared with what the shard's best recent self would need for a request of
// that size: a fixed cost per request, learned from small requests where it is a
// visible part of the time, plus the bytes at the best service rate seen recently.
// That level is smoothed over the last several flushes (see sizingSignalAlpha) and
// read against the mean level of the node's other shards, so a slowdown the whole
// node shares, which is the node's load, cuts no shard. A shard alone on its node,
// such as the sink of documents that belong to no shard, is read against itself and
// keeps its size. While a shard stays within Headroom of the rest of its node, its
// size grows by up to StepBytes per flush. When it is slower than that, as a hot
// shard or one with costly documents is, its size is cut in proportion to how far
// past Headroom it is, so that it does not take the node's time from the others.
// The PID gains (see [PIDGains]) set the size of each step. A shard's buffer reads
// nothing else: not a request the node rejected with a 429, which was answered
// quickly because no work was done, not a request that failed without an answer,
// and not one in which no item was indexed. A shard that starts on a node is seeded
// with the node's best rate, so that its first flush is not taken for the best the
// node can do. A 413, a request refused for being too large, is the one failure that
// changes a buffer: no buffer is made larger than Backoff times the size that was
// refused. ShardFixed turns this controller off: every buffer is FlushBytes.
//
// The node-window controller is additive increase, multiplicative decrease on 429s
// (NodeAIMD, the default). How many bytes may be out to a node at once is the
// node's window, and a 429 is the only thing that moves it. When a request is
// answered with a 429, for the request or for any item in it, the window becomes
// Backoff times the smaller of itself and the bytes that were out to the node when
// the answer arrived, so that the first rejection counts even though the window
// starts wide open. Rejections of requests sent before that cut are the same event
// and do not cut again. The window then grows by FlushBytes for each window's worth
// of bytes the node answers, held for a few answers after a rejection and while the
// node's write pool is overloaded. A sealed buffer that would take the node past its
// window waits until the requests out are answered; when nothing is out, one is
// sent whatever its size. The window does not change the size of any buffer. It
// starts at NodeBytes, or at MaxMemoryBytes when that is not set. NodeFixed turns
// this controller off: the window stays where it started.
//
// An operator can override Shard, Node and the PID gains without a code change
// through OPENSEARCH_GO_BULK_SHARD, OPENSEARCH_GO_BULK_NODE and
// OPENSEARCH_GO_BULK_PID_KP, _KI and _KD, read when the indexer is built. A variable
// that is set and valid wins over the field it names; see guides/config-envvars.md.
type BulkSizing struct {
	// Shard chooses how a shard's buffer is sized: ShardPID, the PID controller on
	// latency, or ShardFixed, every buffer at FlushBytes. ShardUnset, the zero
	// value, is ShardPID. OPENSEARCH_GO_BULK_SHARD=pid|fixed overrides it.
	Shard ShardSizing
	// Node chooses how a node's window follows the node: NodeAIMD, cut on a 429 and
	// grown back slowly, or NodeFixed, held where it started. NodeUnset, the zero
	// value, is NodeAIMD. OPENSEARCH_GO_BULK_NODE=aimd|fixed overrides it.
	Node NodeSizing

	// MinBytes is the smallest batch the shard-buffer controller will ask for.
	// Defaults to FlushBytes/32, and at least 1.
	MinBytes int
	// InitialBytes is where a shard's buffer starts when it starts on a node that
	// shows load already: its write pool rejecting, or its window cut by a 429 and
	// not grown back. From there it climbs to full size as the node keeps up. On a
	// node that shows no load, a shard starts at FlushBytes, the most the node
	// will be sent, and backs off only when it is slower than the node's other
	// shards. Set, it is where every shard starts. Defaults to FlushBytes/4. To
	// send FlushBytes always, with neither the climb nor the backoff, set Shard to
	// ShardFixed.
	InitialBytes int
	// StepBytes is the most a shard's buffer grows in one healthy flush.
	// Defaults to FlushBytes/16.
	StepBytes int

	// Headroom is how much slower than the rest of its node a shard may be before
	// its size is cut: 0.25 allows its time to rise to 125% of the others'. Defaults
	// to 0.25.
	Headroom float64
	// Backoff is the factor a node's window is multiplied by when the node answers
	// a request with a 429. After a 413 it also caps every request at Backoff times
	// the size that was refused, unless Shard is ShardFixed, which cuts no buffer.
	// Defaults to 0.5.
	Backoff float64

	// PID holds the gains of the shard-buffer PID controller; they have no effect
	// with ShardFixed.
	PID PIDGains
}

// ShardSizing is how a [BulkSizing] sizes each shard's buffer.
type ShardSizing uint8

const (
	// ShardUnset is the zero value, and means ShardPID.
	ShardUnset ShardSizing = iota
	// ShardPID sizes each shard's buffer with a PID controller on how much slower
	// the shard is than the rest of its node; see [PIDGains].
	ShardPID
	// ShardFixed sends every buffer at FlushBytes.
	ShardFixed

	numShardSizings = iota
)

// String returns the setting as OPENSEARCH_GO_BULK_SHARD spells it, "pid" or
// "fixed", or "unset" for ShardUnset.
func (m ShardSizing) String() string {
	switch m {
	case ShardUnset:
		return "unset"
	case ShardPID:
		return "pid"
	case ShardFixed:
		return "fixed"
	default:
		return "ShardSizing(" + strconv.Itoa(int(m)) + ")"
	}
}

// NodeSizing is how a [BulkSizing] moves each node's window.
type NodeSizing uint8

const (
	// NodeUnset is the zero value, and means NodeAIMD.
	NodeUnset NodeSizing = iota
	// NodeAIMD cuts a node's window when the node answers with a 429 and grows it
	// back slowly while the node keeps accepting writes.
	NodeAIMD
	// NodeFixed holds every node's window where it started, NodeBytes or
	// MaxMemoryBytes.
	NodeFixed

	numNodeSizings = iota
)

// String returns the setting as OPENSEARCH_GO_BULK_NODE spells it, "aimd" or
// "fixed", or "unset" for NodeUnset.
func (m NodeSizing) String() string {
	switch m {
	case NodeUnset:
		return "unset"
	case NodeAIMD:
		return "aimd"
	case NodeFixed:
		return "fixed"
	default:
		return "NodeSizing(" + strconv.Itoa(int(m)) + ")"
	}
}

// PIDGains are the gains of the shard-buffer PID controller on a shard's
// normalized slowdown. When all three are zero they default to 1, 0.1 and 0.25.
// OPENSEARCH_GO_BULK_PID_KP, _KI and _KD override them one by one, and a gain set to
// 0 there turns that term off.
type PIDGains struct {
	// Kp scales the part of each step that is in proportion to how far the shard is
	// from Headroom on this flush.
	Kp float64
	// Ki scales the part of each step that is in proportion to the sum of that
	// distance over recent flushes.
	Ki float64
	// Kd scales the part of each step that is in proportion to the change in that
	// distance since the previous flush.
	Kd float64
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
	// sizingHoldFlushes is how many healthy flushes after a rejection pass before
	// the size may grow again.
	sizingHoldFlushes = 3
	// sizingBaseDecay is how fast the memory of the best rate fades per sample while
	// the batches are large. It is slow because a node slows gradually as the
	// batches grow, and a memory that faded as fast as that would never notice; it
	// is not slower, because the best is a running maximum of a noisy signal, and
	// the longer it is held the further above an honest flush it creeps.
	sizingBaseDecay = 0.999
	// sizingFloorDecay is how fast it fades when the requests are small, at or near
	// the smallest batch. A node that is slow even for the smallest batches has not
	// been made slow by them, so it is how the node is now, and the size is allowed
	// to climb back after a few hundred flushes instead of staying cut forever.
	sizingFloorDecay = 0.995
	// sizingMinSampleFraction is the part of the current target a flush must
	// carry for its rate to say anything: the last, partial batch of a burst is
	// dominated by fixed overhead.
	sizingMinSampleFraction = 0.25
	// sizingMinServiceFraction floors the service time at this part of the round
	// trip, so a round trip shorter than the measured distance cannot give an
	// absurd rate.
	sizingMinServiceFraction = 0.25
	// sizingSignalAlpha is how much one flush moves the smoothed service rate and
	// the smoothed slowdown. One flush's time is mostly noise: queueing, a GC, a
	// merge. The cuts are multiplicative and the growth additive, so a controller
	// that believed every sample would be walked to the floor by variance alone.
	sizingSignalAlpha = 0.08
	// sizingOverheadAlpha is how fast the fixed per-request cost is believed as
	// small requests show it. It is slower than the signal, so that a node's bad
	// spell is read as a slowdown first and as a cost of smallness only when it
	// keeps not going away.
	sizingOverheadAlpha = 0.05
	// sizingOverheadDecay fades the learned cost, so a cost the node has stopped
	// charging is forgotten within a few hundred flushes.
	sizingOverheadDecay = 0.999
	// sizingSlackSmoothed widens the headroom by this many of the signal's own
	// smoothed absolute deviations before a slowdown counts. The smoothed signal the
	// PID reads has most of the noise averaged out of it, so it needs little slack.
	// A steady node has no deviation and gets no slack.
	sizingSlackSmoothed = 0.5
	// sizingOverheadZone is the largest request, in multiples of the smallest
	// batch, that teaches the fixed cost. In larger requests the cost is lost in
	// the byte time, and a controller that read the residue of large requests as
	// fixed cost would absorb a node slowing under its own batches and ride the
	// ceiling.
	sizingOverheadZone = 2.0
)

// resolvedSizing is BulkSizing with its defaults applied.
type resolvedSizing struct {
	shard ShardSizing
	node  NodeSizing
	// fixed is whether shard buffers are fixed, and nodeFixed whether node windows
	// are: shard and node spelled out for the controllers' tests.
	fixed     bool
	nodeFixed bool
	min, max  float64
	initial   float64
	// initialSet is whether the caller chose initial, which then holds for every
	// shard, loaded node or not.
	initialSet bool
	step       float64
	headroom   float64
	backoff    float64
	kp, ki, kd float64
}

// resolve applies the defaults for a ceiling of flushBytes.
func (c BulkSizing) resolve(flushBytes int) resolvedSizing {
	ceiling := float64(max(flushBytes, 1))

	r := resolvedSizing{
		shard:      c.Shard,
		node:       c.Node,
		max:        ceiling,
		min:        pick(float64(c.MinBytes), ceiling/sizingMinDivisor),
		initial:    pick(float64(c.InitialBytes), ceiling/sizingInitialDivisor),
		initialSet: c.InitialBytes > 0,
		step:       pick(float64(c.StepBytes), ceiling/sizingStepDivisor),
		headroom:   pick(c.Headroom, sizingDefaultHeadroom),
		backoff:    pick(c.Backoff, sizingDefaultBackoff),
		kp:         c.PID.Kp,
		ki:         c.PID.Ki,
		kd:         c.PID.Kd,
	}

	if c.PID == (PIDGains{}) {
		r.kp, r.ki, r.kd = sizingDefaultKp, sizingDefaultKi, sizingDefaultKd
	}

	r.applyEnv()
	r.fixed, r.nodeFixed = r.shard == ShardFixed, r.node == NodeFixed

	r.min = max(math.Min(r.min, ceiling), 1)
	r.initial = math.Min(math.Max(r.initial, r.min), ceiling)
	r.backoff = math.Min(math.Max(r.backoff, 0.05), 0.95)

	// A fixed size starts, and stays, at the ceiling. It keeps the minimum the
	// controller would use, because the minimum is also how the sizer tells a small
	// request, which teaches the fixed cost of a request and fades the memory of the
	// best rate faster, from a large one. Pinned to the ceiling it would make every
	// request small, and the slowdown a fixed size reports would be absorbed as that
	// cost.
	if r.fixed {
		r.initial = ceiling
	}

	return r
}

// applyEnv applies the operator's overrides from the environment. It runs once,
// as the indexer is built, so that the controller tests plain fields afterwards
// and never looks at the environment again.
func (r *resolvedSizing) applyEnv() {
	if v, ok := lookupEnv(envvars.BulkShard); ok {
		if m, ok := parseSetting(v, ShardPID, ShardFixed); ok {
			r.shard = m
		}
	}

	if v, ok := lookupEnv(envvars.BulkNode); ok {
		if m, ok := parseSetting(v, NodeAIMD, NodeFixed); ok {
			r.node = m
		}
	}

	for _, g := range []struct {
		name string
		gain *float64
	}{{envvars.BulkPIDKp, &r.kp}, {envvars.BulkPIDKi, &r.ki}, {envvars.BulkPIDKd, &r.kd}} {
		if v, ok := lookupEnv(g.name); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && !math.IsInf(f, 0) {
				*g.gain = f
			}
		}
	}
}

// parseSetting returns the one of settings whose String is v, in any case, and
// whether there is one. The Unset values are not among settings, so "unset" is not
// accepted.
func parseSetting[T fmt.Stringer](v string, settings ...T) (T, bool) {
	for _, m := range settings {
		if strings.EqualFold(v, m.String()) {
			return m, true
		}
	}

	var zero T

	return zero, false
}

// lookupEnv returns the trimmed value of name, and whether it is set and not empty.
func lookupEnv(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	v = strings.TrimSpace(v)

	return v, ok && v != ""
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
	// served is set when the node indexed at least one item of the request. A request
	// in which every item failed did no indexing, so how long it took says nothing
	// about how fast the node indexes.
	served bool
	// rejected is set when the node answered 429 to the request or to any item.
	rejected bool
	// failed is set when the request did not complete for another reason.
	failed bool
	// tooLarge is the size of a request the node refused for being too large (413),
	// or 0.
	tooLarge int64
	// overloaded is set when the transport sees the node's write pool rejecting.
	overloaded bool
	// peer is what the shard is measured against: the mean level of the other shards
	// on its node, or the shard's own level when it is alone there, and 0 when there
	// is nothing to measure against yet. Measuring a shard against its node means a
	// slowdown the whole node shares shrinks no shard; that is the node's load, which
	// its window answers when it rejects. Only a shard slower than the rest of its
	// node gives way.
	peer float64
}

// timed reports whether the flush's duration measures how fast the node indexes:
// it indexed something, and the node did not reject it with a 429, which is
// answered at once because no work was done, nor fail it without an answer.
func (f flushSample) timed() bool {
	return f.served && !f.rejected && !f.failed
}

// sizer decides how large a batch for one node should be. It is not safe for
// concurrent use; the scheduler's lock serializes it.
type sizer struct {
	cfg resolvedSizing

	target   float64
	begun    bool    // set once begin has chosen where the shard starts
	baseRate float64 // best recent smoothed bytes per nanosecond
	rateS    float64 // smoothed service rate; 0 until the first flush
	overhead float64 // fixed cost a request pays before its bytes count, ns
	normS    float64 // smoothed service time over the envelope's; 0 until the first flush
	devS     float64 // smoothed absolute deviation of the signal around normS
	integral float64
	prevErr  float64
}

func newSizer(cfg resolvedSizing) *sizer {
	return &sizer{cfg: cfg, target: cfg.initial}
}

// start is where a shard on a node that shows no load starts: the ceiling, unless
// the caller chose where shards start.
func (r resolvedSizing) start() float64 {
	if r.initialSet {
		return r.initial
	}

	return r.max
}

// begin sets where a shard's buffer starts, the first time it is given one: full
// size on a node that shows no load, so that a node able to take full requests is
// sent them from the first, and initial on one that does, so that a shard joining
// a loaded node climbs to full size instead of adding to the load at once.
func (s *sizer) begin(loaded bool) {
	if s.begun {
		return
	}
	s.begun = true

	if !loaded {
		s.target = s.cfg.start()
	}
}

// sampleFloor is the smallest request, in bytes, whose rate means anything.
func (s *sizer) sampleFloor() float64 {
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
//
// The only input is the duration of a flush that indexed something, as a rate. A
// flush the node rejected with a 429, one that failed without an answer, one in which
// every item failed, and one too small to say anything are not read: none of them
// measures how fast the node indexes this shard's documents, and a controller that
// took a quick refusal for a fast flush would grow the buffer while the node says it
// is full. With fixed sizing the flush is still read, for the slowdown it reports,
// but the buffer's size does not change.
//
// It returns how many times the expected time the flush took, and false when the flush
// was not read.
func (s *sizer) observe(f flushSample) (float64, bool) {
	if !f.timed() || float64(f.bytes) < s.sampleFloor() {
		return 0, false
	}

	n, ok := s.steer(f)

	if !s.cfg.fixed {
		s.target = math.Min(math.Max(s.target, s.cfg.min), s.cfg.max)
	}

	return n, ok
}

// seed gives a sizer that has seen nothing the best rate its node has shown, so
// that its first flush is measured against what the node can do and not against
// itself.
func (s *sizer) seed(rate float64) {
	if s.baseRate == 0 && rate > 0 {
		s.baseRate = rate
	}
}

// serviceTime is the part of the round trip spent on the node: the latency less
// the distance, but never less than a part of the whole, so that a trip shorter
// than the measured distance cannot read as instant.
func (f flushSample) serviceTime() time.Duration {
	service := f.latency - f.distance
	if floor := time.Duration(float64(f.latency) * sizingMinServiceFraction); service < floor {
		service = floor
	}

	return service
}

// rate is the node's service rate for the flush, in bytes per nanosecond of time
// on the node, and false when the flush says nothing about it.
func (f flushSample) rate() (float64, bool) {
	service := f.serviceTime()
	if service <= 0 {
		return 0, false
	}

	return float64(f.bytes) / float64(service), true
}

// steer applies the PID-modulated step for a flush that completed, and returns the
// flush's service time over the expected.
func (s *sizer) steer(f flushSample) (float64, bool) {
	rate, ok := f.rate()
	if !ok {
		return 0, false
	}

	s.rateS = smoothInto(s.rateS, rate)

	decay := sizingBaseDecay
	if s.target <= s.cfg.min || float64(f.bytes) <= s.cfg.min*sizingOverheadZone {
		// Small requests fade the memory of the best rate faster: see sizingFloorDecay.
		decay = sizingFloorDecay
	}
	s.baseRate = math.Max(s.rateS, s.baseRate*decay)

	s.learnOverhead(f)

	// expected is what the node's best recent self would need for this request:
	// its fixed cost, then the bytes at the best rate. Without the fixed cost a
	// small request would be judged against the bytes-per-second of large ones,
	// which it cannot reach on any healthy node, and a controller that read that
	// as the node slowing would answer every cut with another cut.
	expected := s.overhead + float64(f.bytes)/s.baseRate
	n := float64(f.serviceTime()) / expected
	if s.normS != 0 {
		s.devS = ewma(s.devS, math.Abs(n-s.normS), sizingSignalAlpha)
	}
	s.normS = smoothInto(s.normS, n)

	// Fixed sizing reads the node but never moves the buffer.
	if s.cfg.fixed {
		return n, true
	}

	// slowdown is 0 at the node's best and 1 when requests take twice what the
	// best self would need, read smoothed. It is allowed the slack its signal's
	// noise calls for: a jittery node must slow by more than its jitter before the
	// slowing means anything, while a steady node gets no slack at all, and is
	// held to the headroom alone.
	level := s.normS
	if f.peer > 0 {
		// Against the rest of the node rather than the shard's own best: a node
		// that slows for every shard alike is loaded, not slowed by this shard.
		level /= f.peer
	}
	slowdown := level - 1
	slack := sizingSlackSmoothed * s.devS

	// e is +1 for a node as quick as ever, 0 at the headroom plus slack, and -1 or
	// below (clamped) when it has slowed by twice the headroom plus the slack or more.
	e := math.Min(math.Max((s.cfg.headroom+slack-slowdown)/s.cfg.headroom, -1), 1)
	if f.overloaded {
		e = math.Min(e, 0)
	}

	s.integral = math.Min(math.Max(s.integral+e, -sizingIntegralLimit), sizingIntegralLimit)
	derivative := e - s.prevErr
	s.prevErr = e

	u := s.cfg.kp*e + s.cfg.ki*s.integral + s.cfg.kd*derivative

	switch {
	case u >= 0:
		s.target += s.cfg.step * math.Min(u, 1)
	default:
		s.target *= 1 + math.Max(u, -sizingMaxCut)
	}

	return n, true
}

// learnOverhead reads the fixed per-request cost out of small requests, where it
// is a visible part of the service time: whatever is left once the bytes at the
// best rate are paid for. It is this cost that makes a small request carry fewer
// bytes per second than a large one on the same healthy node, so a controller that
// did not account for it would take the price of smallness for the node slowing.
// Only requests close to the smallest batch teach it; in larger ones the residue
// is dominated by load, and reading load as fixed cost would absorb a node slowing
// under its own batches.
func (s *sizer) learnOverhead(f flushSample) {
	s.overhead *= sizingOverheadDecay
	if float64(f.bytes) > s.cfg.min*sizingOverheadZone {
		return
	}

	resid := float64(f.serviceTime()) - float64(f.bytes)/s.baseRate
	if resid < 0 {
		resid = 0
	}
	s.overhead = ewma(s.overhead, resid, sizingOverheadAlpha)
}

// smoothInto folds one sample into a smoothed signal, seeding an empty one.
func smoothInto(old, v float64) float64 {
	if old == 0 {
		return v
	}

	return ewma(old, v, sizingSignalAlpha)
}

// fraction is the target as a part of the most the sizer would ask for.
func (s *sizer) fraction() float64 {
	if s.cfg.max <= 0 {
		return 0
	}

	return s.target / s.cfg.max
}
