// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// defaultNodeWindow is how many bulk requests the indexer keeps in flight to a
// node whose write pool window the transport has not reported.
const defaultNodeWindow = 2

// idleShardIntervals is how many flush intervals a shard may sit with nothing
// buffered and nothing in flight before the indexer forgets what it learned about
// it. It bounds the memory the indexer spends on shards it has stopped writing to.
const idleShardIntervals = 5

// shardKey identifies a shard of an index, the unit whose documents are written
// in order and whose requests are timed.
type shardKey struct {
	index string
	shard int
}

// buffer is the documents bound for one bulk request, which carries the
// documents of one shard (or, for documents that belong to no shard, of one node)
// in the order they were added. It is open while documents are still being added
// to it, sealed when it is waiting to be sent, and complete when its request has
// finished.
type buffer struct {
	shard *shardState
	// node is where the request goes: the node hosting the shard when the buffer
	// was started, or the node chosen for documents that belong to no shard.
	node *nodeState
	// items are the documents, kept for their callbacks and serialized into the
	// request body when it is sent.
	items []BulkIndexerItem
	// bytes is the estimated size of the request body, which sizes the buffer.
	bytes int64
	// held is the memory the buffer accounts for: the body, and the item structs
	// that carry the documents to their callbacks.
	held int64

	// dest is the node the request goes to, captured when the buffer is dispatched
	// so that sending it does not read state the scheduler goes on changing.
	dest Node

	sealed     bool
	dispatched bool
	// ctx is the context the bulk request runs on.
	ctx context.Context //nolint:containedctx // A buffer carries the context of the flush that sends it.

	done chan struct{}
	err  error
	// waiters counts the Flush calls waiting for the buffer; they receive its
	// error, and OnError does not.
	waiters int
}

// shardState is the indexer's view of one shard: the buffer being filled, how many
// buffers are waiting or out, and how large its buffers should be.
type shardState struct {
	key shardKey
	// ordered is false for the buffers of documents that belong to no shard, which
	// have no order to keep and may have several requests out at once.
	ordered bool
	open    *buffer
	// pending counts the shard's buffers that are sealed and not complete, and
	// inflight those whose request is out. An ordered shard has at most one out.
	pending, inflight int
	// sizer decides how large the shard's buffers are. It is fed the time the
	// shard's own requests took, which is why a request carries one shard.
	sizer *sizer
	// idleSince is when the shard last had something buffered or in flight.
	idleSince time.Time
	// skipped marks the shard as passed over in the current dispatch pass.
	skipped uint64
}

// blocked reports whether a buffer sealed now would have to wait for an earlier
// one of the same shard.
func (ss *shardState) blocked() bool {
	return ss.ordered && ss.pending > 0
}

// nodeState is the indexer's view of one node: what is buffered for it and what is
// out to it, and how much it may have buffered.
type nodeState struct {
	name string
	node Node
	// opens are the node's open buffers.
	opens map[*buffer]struct{}
	// held is everything held for the node, in open buffers, queued, and in
	// requests out, and openHeld the part in open buffers. They are atomic so that
	// the share of the job's memory the other nodes leave a node can be worked out
	// without visiting them.
	held, openHeld atomic.Int64
	// crowded is set when the node's open buffers outgrew its limit since its last
	// request completed.
	crowded bool
	// queued counts the node's sealed buffers not yet sent, and inflight its
	// requests out.
	queued, inflight int
	// limit is the controller behind how much the node may have open. It starts at
	// the node's even share of the job, follows the node as a whole (what it
	// rejects, how its service rate moves, whether anything asks it for more) and
	// is read through nodeLimit, which bounds it by the rest of the job.
	limit *sizer
	// sink serves documents that belong to no shard.
	sink *shardState
}

// window is how many requests may be outstanding to the node: the window the
// transport allows its write pool, and a single request while the pool rejects.
func (ns *nodeState) window() int {
	p := ns.node.pool()
	n := p.Window
	if n <= 0 {
		n = defaultNodeWindow
	}
	if p.Overloaded {
		n = 1
	}

	return n
}

// hasRoom reports whether another request to the node would go out now.
func (ns *nodeState) hasRoom() bool {
	return ns.inflight+ns.queued < ns.window()
}

// scheduler owns every buffer from the moment its first document is added to the
// moment its request completes. One lock guards it; the work done under the lock is
// bookkeeping, and serializing and sending a buffer happen outside it.
//
// Three limits keep memory bounded, each acting on a different level:
//
//   - the job's memory, a hard limit: when it is spent Add waits, and the fullest
//     buffer is sent at once so that waiting does not last;
//   - a node's buffer, which follows the node: when the open buffers bound for it
//     outgrow it, the fullest is sent;
//   - a shard's buffer, which follows the shard: a buffer that reaches it is sent.
//
// Ordering. The server applies the documents of one request to a shard in order,
// but applies two requests in whatever order they arrive, and nothing else orders
// them: not the node they were sent to, and not the order they were sent in. So a
// shard has one request outstanding at a time. The documents that arrive meanwhile
// gather in its next buffer, which grows past its usual size to the ceiling instead
// of queueing small ones, and a shard that moves to another node changes only where
// its next buffer is sent. The same rule keeps a hot shard from taking over a node.
type scheduler struct {
	sync.Mutex

	bi *bulkIndexer

	shards map[shardKey]*shardState
	nodes  map[string]*nodeState
	// sealed are the buffers waiting to be sent, in the order they were sealed.
	sealed []*buffer
	// live holds every buffer that is not complete.
	live map[*buffer]struct{}
	// sealedPending counts buffers that are sealed and not complete.
	sealedPending int
	// sink is the node unrouted documents are being filled into.
	sink *nodeState
	pass uint64
	// heldTotal is everything the job holds, and activeNodes the number of nodes
	// something is held for.
	heldTotal   atomic.Int64
	activeNodes int

	closed, stopped bool
}

func newScheduler(bi *bulkIndexer) *scheduler {
	return &scheduler{
		bi:     bi,
		shards: make(map[shardKey]*shardState),
		nodes:  make(map[string]*nodeState),
		live:   make(map[*buffer]struct{}),
	}
}

// nodeFor returns the state for the node, creating it on first use.
func (s *scheduler) nodeFor(node Node) *nodeState {
	if ns, ok := s.nodes[node.Name]; ok {
		// The transport may hand out a fresh connection object for a node that came
		// back; keep the view of it current for the buffers started from now on.
		ns.node = node

		return ns
	}

	ns := &nodeState{
		name:  node.Name,
		node:  node,
		opens: make(map[*buffer]struct{}),
		limit: newSizer(s.bi.nodeSizing),
	}
	ns.sink = &shardState{key: shardKey{shard: -1}, sizer: newSizer(s.bi.sizing), idleSince: s.bi.now()}
	s.nodes[node.Name] = ns

	return ns
}

// shardFor returns the state for the shard, creating it on first use.
func (s *scheduler) shardFor(key shardKey) *shardState {
	if ss, ok := s.shards[key]; ok {
		return ss
	}

	ss := &shardState{key: key, ordered: true, sizer: newSizer(s.bi.sizing), idleSince: s.bi.now()}
	s.shards[key] = ss

	return ss
}

// sinkNode picks the node for documents that belong to no shard: it keeps filling
// the buffer it has, and starts the next on the nearest node with room, moving on
// to the next nearest only when the nearest has all the requests out it is
// allowed. That sends a stream of unrouted documents in large buffers to as few
// nodes as will take them, which leaves the server the most work to do per request.
func (s *scheduler) sinkNode() *nodeState {
	if s.sink != nil && s.sink.sink.open != nil {
		return s.sink
	}

	nodes := s.bi.router.WriteNodes()
	if len(nodes) == 0 {
		s.sink = s.nodeFor(Node{})

		return s.sink
	}

	best := s.nodeFor(nodes[0])
	for _, n := range nodes {
		ns := s.nodeFor(n)
		if ns.hasRoom() {
			best = ns

			break
		}
		if ns.inflight+ns.queued < best.inflight+best.queued {
			best = ns
		}
	}

	s.sink = best

	return best
}

// add places an item. key is nil for an item that belongs to no shard. reserved is
// what the item holds of the job's memory, and size the estimated size of its
// part of the request body.
//
// It must be called with the lock held.
func (s *scheduler) add(item BulkIndexerItem, size, reserved int64, key *shardKey, node Node) {
	var ss *shardState
	var ns *nodeState

	if key != nil {
		ss = s.shardFor(*key)
		if node.Name != "" {
			ns = s.nodeFor(node)
		}
	}

	if ss != nil && ss.open != nil {
		ns = ss.open.node
	} else {
		if ns == nil {
			ns = s.sinkNode()
		}
		if ss == nil {
			ss = ns.sink
		}
	}

	b := ss.open
	if b == nil {
		b = &buffer{shard: ss, node: ns, items: s.bi.getItems(), done: make(chan struct{})}
		ss.open = b
		ns.opens[b] = struct{}{}
		s.live[b] = struct{}{}
	}

	b.items = append(b.items, item)
	b.bytes += size
	b.held += reserved
	s.hold(b.node, reserved)
	ss.idleSince = s.bi.now()

	s.sealIfFull(b)

	// The node's limit is soft: the item that crossed it has been accepted. What
	// follows is that the node's fullest buffers are sent until it is back under,
	// and that the node is told it was crowded, so its limit comes down.
	limit := s.nodeLimit(ns)
	for ns.openHeld.Load() > limit {
		if !s.sealFullest(ns, flushPressure) {
			break
		}
		ns.crowded = true
	}
}

// hold records that n more bytes are held for the node, in an open buffer.
func (s *scheduler) hold(ns *nodeState, n int64) {
	if ns.held.Add(n) == n {
		s.activeNodes++
	}
	ns.openHeld.Add(n)
	s.heldTotal.Add(n)
}

// release records that a buffer's n bytes are no longer held for the node.
func (s *scheduler) release(ns *nodeState, n int64) {
	if ns.held.Add(-n) == 0 {
		s.activeNodes--
	}
	s.heldTotal.Add(-n)
}

// nodeLimit is how much the node may have open now. The node's controller starts
// at its even share of the job and moves with the node, and the other nodes bound
// the result; see [nodeLimitFor].
func (s *scheduler) nodeLimit(ns *nodeState) int64 {
	job := int64(s.bi.config.MaxMemoryBytes)
	fair := job / int64(max(s.activeNodes, 1))
	others := s.heldTotal.Load() - ns.held.Load()

	return nodeLimitFor(job, int64(s.bi.config.FlushBytes), fair, ns.limit.bytes(), others)
}

// nodeLimitFor works out a node's limit from what the job may hold (job), the size
// of a full shard buffer (shard), the node's even share of the nodes in use (fair),
// the node's own controller (control, which is cut when the node rejects, slows or
// is crowded and grows when it keeps up and is asked for more), and what the other
// nodes hold (others).
//
// The controller is the limit, except that the other nodes can only take it down to
// the node's even share: a node that has the traffic to itself is given up to the
// whole job, and is cut back as others start to hold some, but is never squeezed
// below its share by them. Only the node's own signals can take it below its share,
// and nothing takes it below one full shard buffer.
func nodeLimitFor(job, shard, fair, control, others int64) int64 {
	ceiling := max(job-others, fair)

	return max(min(control, ceiling), shard)
}

// sealThreshold is the size at which an open buffer is sent: the size its shard
// asks for, and no more than the shard's slice of what the node may have open. As
// more shards of a node are being filled each is given less, so the node's
// buffers together stay within its limit instead of each filling alone.
func (s *scheduler) sealThreshold(b *buffer) (int64, int64) {
	ns := b.node
	slice := s.nodeLimit(ns) / int64(max(len(ns.opens), 1))

	return b.shard.sizer.bytes(), max(slice, int64(b.shard.sizer.cfg.min))
}

// sealIfFull seals an open buffer that has reached the size chosen for its shard.
// A buffer that has to wait for an earlier buffer of its shard keeps collecting
// documents, up to the largest size a shard is ever sent, since sealing it would
// only queue it: this is how a hot shard, which has one request outstanding at a
// time, ends up sending fewer, larger requests instead of a queue of small ones.
func (s *scheduler) sealIfFull(b *buffer) {
	if b.sealed {
		return
	}

	sz := b.shard.sizer
	target, slice := s.sealThreshold(b)

	switch {
	case b.bytes >= sz.ceiling():
		s.seal(b, flushSize, s.bi.config.Context)
	case b.held >= slice && !b.shard.blocked():
		s.seal(b, flushPressure, s.bi.config.Context)
	case b.bytes >= target && !b.shard.blocked():
		s.seal(b, flushSize, s.bi.config.Context)
	}
}

// seal closes a buffer to further documents and queues it to be sent, to run its
// request on ctx.
func (s *scheduler) seal(b *buffer, trigger flushTrigger, ctx context.Context) {
	if b.sealed {
		return
	}

	b.sealed = true
	b.ctx = ctx

	ss, ns := b.shard, b.node
	if ss.open == b {
		ss.open = nil
	}
	delete(ns.opens, b)
	ns.openHeld.Add(-b.held)
	ns.queued++
	ss.pending++
	s.sealed = append(s.sealed, b)
	s.sealedPending++

	switch trigger {
	case flushSize:
		s.bi.stats.numByteFlushes.Add(1)
	case flushInterval:
		s.bi.stats.numTimerFlushes.Add(1)
	case flushRequested, flushPressure:
	}

	s.pump()
}

// sealFullest seals the fullest open buffer bound for the node, or the fullest of
// all when ns is nil, and reports whether there was one.
func (s *scheduler) sealFullest(ns *nodeState, trigger flushTrigger) bool {
	var fullest *buffer

	consider := func(n *nodeState) {
		for b := range n.opens {
			if fullest == nil || b.bytes > fullest.bytes {
				fullest = b
			}
		}
	}

	if ns != nil {
		consider(ns)
	} else {
		for _, n := range s.nodes {
			consider(n)
		}
	}

	if fullest == nil {
		return false
	}

	s.seal(fullest, trigger, s.bi.config.Context)

	return true
}

// sealAllOpen seals every open buffer.
func (s *scheduler) sealAllOpen(trigger flushTrigger, ctx context.Context) {
	for _, ns := range s.nodes {
		for b := range ns.opens {
			s.seal(b, trigger, ctx)
		}
	}
}

// pump sends every sealed buffer that may go: those whose node has room and whose
// shard has no request out. A buffer that has to wait does not hold up the ones
// behind it, except for later buffers of its own shard, which must not overtake it.
func (s *scheduler) pump() {
	if s.stopped || len(s.sealed) == 0 {
		return
	}

	s.pass++
	remaining := s.sealed[:0]

	for _, b := range s.sealed {
		ss, ns := b.shard, b.node

		if ss.skipped == s.pass || (ss.ordered && ss.inflight > 0) || ns.inflight >= ns.window() {
			ss.skipped = s.pass
			remaining = append(remaining, b)

			continue
		}

		ns.queued--
		ns.inflight++
		ss.inflight++
		b.dispatched = true
		b.dest = ns.node
		s.bi.flushingBytes.Add(b.held)

		s.bi.wg.Add(1)
		go s.bi.run(b)
	}

	clear(s.sealed[len(remaining):])
	s.sealed = remaining
}

// complete records that a buffer's request has finished, gives back its memory,
// teaches the shard and the node what the request showed, and sends whatever that
// unblocks. The error is delivered to the Flush calls waiting for the buffer, or to
// OnError when there are none.
func (s *scheduler) complete(b *buffer, sample flushSample, err error) {
	s.Lock()

	ss, ns := b.shard, b.node
	ss.inflight--
	ss.pending--
	ns.inflight--

	// A node's rate is the sum of its shards', and what a node rejects is the node's.
	// A shard is told how its own requests fared, but not about a rejection, which
	// says nothing about the shard in particular.
	//
	// The node's limit is also told whether it was crowded since the last request
	// finished: whether more was buffered for it than it was allowed.
	node := sample
	shard := sample
	shard.rejected = false
	node.crowded, ns.crowded = ns.crowded, false
	ns.limit.observe(node)
	ss.sizer.observe(shard)
	ss.idleSince = s.bi.now()

	delete(s.live, b)
	s.sealedPending--
	b.err = err
	reportToOnError := err != nil && b.waiters == 0

	s.bi.flushingBytes.Add(-b.held)
	s.bi.budget.release(b.held)
	s.release(ns, b.held)
	close(b.done)

	// The shard's next buffer, collected while this request was out, may be ready.
	if ss.open != nil {
		s.sealIfFull(ss.open)
	}

	// If Add is still waiting for memory this request did not free enough, send the
	// next fullest buffer.
	if s.bi.budget.waiting() > 0 {
		s.sealFullest(nil, flushPressure)
	}

	s.pump()
	s.Unlock()

	s.bi.putItems(b.items)
	b.items = nil

	if reportToOnError && s.bi.config.OnError != nil {
		s.bi.config.OnError(b.ctx, err)
	}
}

// relieve runs when an Add starts waiting for memory. The job's memory comes back
// as requests finish, so the fullest buffer is sent now to start one, in place of
// waiting for a size or an interval that may be far off.
func (s *scheduler) relieve() {
	s.Lock()
	defer s.Unlock()

	s.sealFullest(nil, flushPressure)
}

// drain seals every open buffer and waits for every buffer that exists to
// complete, running those not yet sent on ctx.
//
// With collect set it returns the errors of those buffers, joined, and they are
// not reported to OnError: that is how Flush gives its caller the outcome. Without
// it drain only waits, and each buffer reports its own error to OnError as it would
// had nobody waited.
func (s *scheduler) drain(ctx context.Context, collect bool) error {
	s.Lock()
	s.sealAllOpen(flushRequested, ctx)

	buffers := make([]*buffer, 0, len(s.live))
	for b := range s.live {
		if collect {
			b.waiters++
		}
		if !b.dispatched {
			b.ctx = ctx
		}
		buffers = append(buffers, b)
	}
	s.Unlock()

	workersDone := s.bi.workersDone()
	var errs []error

	for _, b := range buffers {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-workersDone:
			return s.bi.config.Context.Err()
		case <-b.done:
			if collect && b.err != nil {
				errs = append(errs, b.err)
			}
		}
	}

	return errors.Join(errs...)
}

// evictIdle forgets the shards that have had nothing buffered and nothing in
// flight for a good while, with what was learned about them, so that an indexer
// that moves from index to index does not keep a record of every shard it has
// ever written to.
func (s *scheduler) evictIdle() {
	s.Lock()
	defer s.Unlock()

	cutoff := s.bi.now().Add(-idleShardIntervals * s.bi.config.FlushInterval)
	for key, ss := range s.shards {
		if ss.open == nil && ss.pending == 0 && ss.idleSince.Before(cutoff) {
			delete(s.shards, key)
		}
	}
}

// stop stops sending, for when the construction context has ended.
func (s *scheduler) stop() {
	s.Lock()
	s.stopped = true
	s.Unlock()
}

// close ends the scheduler once the indexer has drained.
func (s *scheduler) close() {
	s.Lock()
	s.closed = true
	s.Unlock()
}

// observeSample builds the controllers' input for a finished flush.
func observeSample(node Node, bytes int64, latency time.Duration, throttled, failed bool) flushSample {
	f := flushSample{
		bytes:      bytes,
		latency:    latency,
		rejected:   throttled,
		failed:     failed,
		overloaded: node.pool().Overloaded,
	}

	if node.Conn != nil {
		if rtt := node.Conn.RTTMedian(); rtt > 0 {
			f.distance = rtt
		}
	}

	return f
}
