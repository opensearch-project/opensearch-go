// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"
	"errors"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// idleShardIntervals is how many flush intervals a shard may sit with nothing
// buffered and nothing in flight before the bulk client forgets what it learned about
// it. It bounds the memory the bulk client spends on shards it has stopped writing to.
const idleShardIntervals = 5

// shardKey identifies a shard of an index, the unit whose documents are written
// in order and whose requests are timed.
type shardKey struct {
	index string
	shard int
}

// unplacedKey is the key of the documents that belong to no shard: they share each
// node's sink, and the job's memory counts them as one shard.
func unplacedKey() shardKey {
	return shardKey{shard: -1}
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

	// after are the earlier buffers of documents that belong to no shard that hold
	// a document with the same id as one in this buffer. The buffer is not sent
	// before they have finished, which keeps the documents of one id in order
	// although such buffers are otherwise sent without regard to each other.
	after []*buffer
	// finished is set once the buffer's request has completed.
	finished bool

	sealed     bool
	dispatched bool
	// sentAt is how many times the node's byte window had been shrunk when the buffer
	// was sent, so that a rejection of a request sent before a shrink does not shrink
	// the window again.
	sentAt uint64
	// ctx is the context the bulk request runs on.
	ctx context.Context //nolint:containedctx // A buffer carries the context of the flush that sends it.

	done chan struct{}
	err  error
	// waiters counts the Flush calls waiting for the buffer; they receive its
	// error, and OnUnhandledError does not.
	waiters int
}

// shardState is the bulk client's view of one shard: the buffer being filled, how many
// buffers are waiting or out, and how large its buffers should be.
type shardState struct {
	key shardKey
	// levelNode is the node whose levels hold this shard's, nil until it has one.
	levelNode *nodeState
	// ordered is false for the buffers of documents that belong to no shard, which
	// have no order to keep and may have several requests out at once.
	ordered bool
	open    *buffer
	// pending counts the shard's buffers that are sealed and not complete, and
	// inflight those whose request is out. An ordered shard has at most one out.
	pending, inflight int
	// controller decides how large the shard's buffers are. It is fed the time the
	// shard's own requests took, which is why a request carries one shard.
	controller *shardController
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

// hasBlockedReqs reports whether the shard has a request in flight and more of its
// documents waiting behind it: an open buffer, or a sealed buffer not yet sent. A
// shard without ordering sends its buffers side by side, so none of them waits.
func (ss *shardState) hasBlockedReqs() bool {
	return ss.ordered && ss.inflight > 0 && (ss.open != nil || ss.pending > ss.inflight)
}

// trackBlocked keeps the BlockedShards gauge, the number of shards for which
// hasBlockedReqs holds. GetStats reads the gauge without the scheduler's lock, so it
// is adjusted as shards change rather than counted from them. Every change to a
// shard that can change hasBlockedReqs calls trackBlocked with the value
// hasBlockedReqs had before the change: when a shard opens a buffer, sends one, or
// has one answered. Sealing is not among them: it moves a waiting buffer from open
// to sealed, and hasBlockedReqs keeps its value. It must be called with the
// scheduler's lock held.
func (s *scheduler) trackBlocked(ss *shardState, before bool) {
	after := ss.hasBlockedReqs()
	switch {
	case after == before:
	case after:
		s.bi.stats.blockedShards.Add(1)
	default:
		s.bi.stats.blockedShards.Add(-1)
	}
}

// trackQueued keeps the QueuedBuffers statistic: the sealed buffers not yet sent.
// It must be called with the scheduler's lock held, after s.sealed changes.
func (s *scheduler) trackQueued() {
	s.bi.stats.queuedBuffers.Store(int64(len(s.sealed)))
}

// nodeState is the bulk client's view of one node: what is buffered for it and what is
// out to it, and how many bytes it may have out.
type nodeState struct {
	name string
	node Node
	// opens are the node's open buffers.
	opens map[*buffer]struct{}
	// held is everything held for the node, in open buffers, queued, and in
	// requests out.
	held atomic.Int64
	// queued counts the node's sealed buffers not yet sent, and inflight its
	// requests out. flightBytes is the size of those requests, which windowBytes
	// bounds.
	queued, inflight int
	flightBytes      int64
	// bufferFrac and slowdown are smoothed readings of the node for the statistics:
	// the size of its shards' buffers as a fraction of the most they get, and how much
	// longer than expected the node takes for its requests. Guarded by the
	// scheduler's lock.
	bufferFrac, slowdown float64
	// levels holds the smoothed level of each shard the node serves: its flushes'
	// time over what they would need. A shard is measured against the others'.
	// Guarded by the scheduler's lock.
	levels map[*shardState]float64
	// windowBytes is how many bytes may be out to the node at once. It starts wide
	// open and is shrunk by a 429; see byteWindow.
	windowBytes *byteWindow
	// nodeWriteSaturated is set when the target node is write-saturated as its
	// previous flush ends. complete pins windowBytes' size only when the node becomes
	// write-saturated (i.e. when the new flushResult's nodeWriteSaturated is set and
	// this field is not). Guarded by the scheduler's lock.
	nodeWriteSaturated bool
	// rate keeps the best service rate the node has shown, which seeds the shards that
	// start on it.
	rate rateTracker
	// sink serves documents that belong to no shard.
	sink *shardState
	// stats holds the node's statistics as atomics, for GetTargetStats to read.
	stats nodeAtomicStats
}

// windowReqs is how many requests the node's write pool has room for, as the
// transport tracks it: the pool's window, maxReqs when the transport reports none,
// and 1 while the node is reported refusing writes, never more than maxReqs. It
// ranks nodes and does not cap requests. The transport updates it once per node
// statistics poll, keeps it at or below the pool's thread count, and halves it when
// the pool's queue reaches half its capacity or grows by more than the pool's
// thread count between polls.
// Requests to a node are capped by nodeReqLimit, and a 429 shrinks windowBytes.
func (ns *nodeState) windowReqs(maxReqs int) int {
	p := ns.node.pool()
	n := p.Window
	if n <= 0 {
		n = maxReqs
	}
	if p.Overloaded {
		n = 1
	}

	return min(n, maxReqs)
}

// peerLevel is what the shard is measured against: the mean level of the node's
// other shards, so that a slowdown the whole node shares reads as none for any of
// them. A shard alone on its node, the sink of documents that belong to no shard
// among them, is measured against itself, and so keeps its size until the node
// rejects. It is 0 when there is nothing to measure against yet.
func (ns *nodeState) peerLevel(ss *shardState) float64 {
	var sum float64
	others := 0
	for other, level := range ns.levels {
		if other != ss {
			sum += level
			others++
		}
	}
	if others == 0 {
		return ss.controller.normS
	}

	return sum / float64(others)
}

// overloaded reports whether the node is overloaded or still recovering from it: the
// transport reports it refusing writes, or a 429 has shrunk windowBytes and it has not
// grown back. A shard that starts on an overloaded node climbs to full size instead of
// starting there.
func (ns *nodeState) overloaded() bool {
	return ns.node.writeSaturated() || ns.windowBytes.fraction() < 1
}

// windowHasReqCap reports whether the node's write pool has room for one more
// request, with the window capped at maxReqs; see windowReqs.
func (ns *nodeState) windowHasReqCap(maxReqs int) bool {
	return ns.inflight+ns.queued < ns.windowReqs(maxReqs)
}

// scheduler owns every buffer from the moment its first document is added to the
// moment its request completes. One lock guards it; the work done under the lock is
// bookkeeping, and serializing and sending a buffer happen outside it.
//
// Three limits keep memory and load bounded, each acting on a different level:
//
//   - the job's memory, a hard limit: when it is spent Add waits, and the fullest
//     buffer is sent at once so that waiting does not last;
//   - a node's window, which a 429 shrinks: the bytes that may be out to the node at
//     once. A sealed buffer that would take the node past it waits, and still counts
//     against the job's memory;
//   - a shard's buffer, which follows how fast the shard is served: a buffer that
//     reaches it is sealed and sent.
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

	bi *bulkClient

	shards map[shardKey]*shardState
	nodes  map[string]*nodeState
	// nodeList is every node in nodes, in the order they were first used, for
	// GetTargetStats to read without the lock.
	nodeList atomic.Pointer[[]*nodeState]

	// work is the workspace publishLoad reuses.
	work loadWork
	// maxRequest is the largest request the nodes have been found to accept, learned
	// from a 413, or 0 while none has refused one. Never raised: it is a setting of
	// the cluster and not a load.
	maxRequest int64
	// sealed are the buffers waiting to be sent, in the order they were sealed.
	sealed []*buffer
	// live holds every buffer that is not complete.
	live map[*buffer]struct{}
	// lastByID maps the id of a document that belongs to no shard to the buffer it
	// was last added to, for as long as that buffer has not finished.
	lastByID map[string]*buffer
	// draining holds the indexes whose buffers a placementChanged is waiting out,
	// from sealIndex until it has recorded the new placement. Each channel is closed
	// by endDrain, when the index's shards have quiesced.
	draining map[string]chan struct{}
	// sealedPending counts buffers that are sealed and not complete.
	sealedPending int
	// sink is the node unrouted documents are being filled into.
	sink *nodeState
	pass uint64

	closed, stopped bool
}

func newScheduler(bi *bulkClient) *scheduler {
	return &scheduler{
		bi:     bi,
		shards: make(map[shardKey]*shardState),
		nodes:  make(map[string]*nodeState),
		live:   make(map[*buffer]struct{}),

		lastByID: make(map[string]*buffer),
		draining: make(map[string]chan struct{}),
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
		name:        node.Name,
		node:        node,
		opens:       make(map[*buffer]struct{}),
		windowBytes: newByteWindow(s.bi.window),
		rate:        rateTracker{floor: float64(s.bi.config.FlushBytes) * sizingMinSampleFraction},

		bufferFrac: s.bi.sizing.start() / s.bi.sizing.max,
	}
	ns.sink = &shardState{key: unplacedKey(), controller: newShardController(s.bi.sizing), idleSince: s.bi.now()}
	s.nodes[node.Name] = ns
	ns.storeStats()
	s.addNode(ns)

	return ns
}

// shardFor returns the state for the shard, creating it on first use.
func (s *scheduler) shardFor(key shardKey) *shardState {
	if ss, ok := s.shards[key]; ok {
		return ss
	}

	ss := &shardState{key: key, ordered: true, controller: newShardController(s.bi.sizing), idleSince: s.bi.now()}
	s.shards[key] = ss

	return ss
}

// nodeReqLimit is the most requests the bulk client keeps in flight to one node:
// NumWorkers when the caller set it, and otherwise GOMAXPROCS. A smaller number
// would cap a cluster of many nodes at what one slow request at a time can carry.
//
// GOMAXPROCS and not runtime.NumCPU, because GOMAXPROCS follows a container's CPU
// limit and NumCPU does not: a pod held to two CPUs on a large host would keep
// dozens of requests out. It is read each time, so a bulk client follows a limit
// that changes.
func (s *scheduler) nodeReqLimit() int {
	if s.bi.config.NumWorkers > 0 {
		return s.bi.config.NumWorkers
	}

	return runtime.GOMAXPROCS(0)
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
		if ns.windowHasReqCap(s.nodeReqLimit()) {
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

// add places an item. key is nil for an item that belongs to no shard. order is
// the id or routing value whose documents are written in the order they were
// added, empty when there is no such order to keep. reserved is what the item
// holds of the job's memory, and size the estimated size of its part of the
// request body.
//
// It must be called with the lock held.
func (s *scheduler) add(item BulkIndexerItem, size, reserved int64, key *shardKey, node Node, order string) {
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

	blockedBefore := ss.hasBlockedReqs()
	b := ss.open
	if b == nil {
		ss.controller.begin(ns.overloaded())
		b = &buffer{shard: ss, node: ns, items: s.bi.getItems(), done: make(chan struct{})}
		ss.open = b
		ns.opens[b] = struct{}{}
		s.live[b] = struct{}{}
	}

	b.items = append(b.items, item)
	s.bi.stats.itemBytes.Add(uint64(max(item.bodyBytes, 0)))
	storeMax(&s.bi.stats.maxItemBytes, uint64(max(item.bodyBytes, 0)))
	s.trackBlocked(ss, blockedBefore)
	if !ss.ordered && order != "" {
		s.orderAfterEarlier(b, order)
	}
	b.bytes += size
	b.held += reserved
	s.hold(b.node, reserved)
	ss.idleSince = s.bi.now()

	s.sealIfFull(b)
}

// orderAfterEarlier makes buffer b of documents that belong to no shard wait for
// the earlier buffer that holds a document with the same order key, if that has
// not finished. A shard's buffers go one at a time, but these may be out together,
// and two requests that carry the same id could then reach the server in either
// order. Buffers are filled one at a time, so a buffer only ever waits for an
// earlier one and the waits cannot form a cycle. A stream whose ids do not repeat
// within a buffer's lifetime never waits.
func (s *scheduler) orderAfterEarlier(b *buffer, order string) {
	prev := s.lastByID[order]
	s.lastByID[order] = b

	if prev == nil || prev == b || slices.Contains(b.after, prev) {
		return
	}

	b.after = append(b.after, prev)
}

// ready reports whether every buffer b must follow has finished.
func (b *buffer) ready() bool {
	for _, prev := range b.after {
		if !prev.finished {
			return false
		}
	}

	return true
}

// hold records that n more bytes are held for the node, in an open buffer.
func (s *scheduler) hold(ns *nodeState, n int64) {
	ns.held.Add(n)
}

// release records that a buffer's n bytes are no longer held for the node.
func (s *scheduler) release(ns *nodeState, n int64) {
	ns.held.Add(-n)
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

	sz := b.shard.controller

	switch {
	case b.bytes >= s.capped(sz.ceiling()):
		s.seal(b, flushSize, s.bi.config.Context)
	case b.bytes >= s.capped(sz.bytes()) && !b.shard.blocked():
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
	ns.queued++
	ns.storeStats()
	ss.pending++
	s.sealed = append(s.sealed, b)
	s.sealedPending++
	s.trackQueued()

	switch trigger {
	case flushSize:
		s.bi.stats.numByteFlushes.Add(1)
	case flushInterval:
		s.bi.stats.numTimerFlushes.Add(1)
	case flushPressure:
		s.bi.stats.numPressureFlushes.Add(1)
	case flushClose:
		s.bi.stats.numCloseFlushes.Add(1)
	case flushRequested:
	}

	s.pump()
}

// sealFullest seals the fullest open buffer that can go out at once: its shard has
// no earlier buffer unfinished, and its node has fewer requests out than nodeReqLimit
// and room for its bytes. This is stricter than pump's test on the shard, which only
// waits for an earlier request to be out. Sealing a buffer that cannot go out frees
// nothing, because it only queues until the requests ahead of it are answered, and
// it splits the batch it was becoming into small requests. When one of those
// requests is answered, complete looks again. Buffers queued behind their own
// shard's request do not count against the node's room, since they are not going
// out either: counting them would let one stalled shard's queue keep every other
// shard's buffers open.
func (s *scheduler) sealFullest(trigger flushTrigger) {
	var fullest *buffer

	for _, n := range s.nodes {
		for b := range n.opens {
			if b.shard.blocked() || n.inflight >= s.nodeReqLimit() || !n.windowBytes.allows(n.flightBytes, b.bytes) {
				continue
			}
			if fullest == nil || b.bytes > fullest.bytes {
				fullest = b
			}
		}
	}

	if fullest != nil {
		s.seal(fullest, trigger, s.bi.config.Context)
	}
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

		if ss.skipped == s.pass || (ss.ordered && ss.inflight > 0) || ns.inflight >= s.nodeReqLimit() ||
			!ns.windowBytes.allows(ns.flightBytes, b.bytes) {
			ss.skipped = s.pass
			remaining = append(remaining, b)

			continue
		}

		// Waiting for an earlier buffer does not hold up the buffers behind it,
		// which do not share its documents unless they follow it in turn.
		if !b.ready() {
			remaining = append(remaining, b)

			continue
		}
		b.after = nil

		blockedBefore := ss.hasBlockedReqs()
		ns.queued--
		ns.inflight++
		ns.flightBytes += b.bytes
		ns.storeStats()
		b.sentAt = ns.windowBytes.shrinks
		ss.inflight++
		b.dispatched = true
		b.dest = ns.node
		s.bi.flushingBytes.Add(b.held)
		s.bi.stats.inFlightRequests.Add(1)
		s.trackBlocked(ss, blockedBefore)

		s.bi.wg.Add(1)
		go s.bi.run(b)
	}

	clear(s.sealed[len(remaining):])
	s.sealed = remaining
	s.trackQueued()
}

// complete records that a buffer's request has finished, gives back its memory,
// teaches the shard and the node what the request showed, and sends whatever that
// unblocks. The error is delivered to the Flush calls waiting for the buffer, or to
// OnUnhandledError when there are none.
func (s *scheduler) complete(b *buffer, result flushResult, err error) {
	s.Lock()

	ss, ns := b.shard, b.node
	blockedBefore := ss.hasBlockedReqs()
	ss.inflight--
	ss.pending--
	ns.inflight--
	s.bi.stats.inFlightRequests.Add(-1)
	s.trackBlocked(ss, blockedBefore)

	// Two controllers, each told what it can use. The node's byte window is told only
	// whether the node rejected the request, which is the one thing a 429 says: it is
	// shrink from the bytes that were out to the node when the rejection arrived, the
	// rejected request's among them. The shard's controller is told how long the
	// request took, but not for a request the node rejected, which was answered quickly
	// because no work was done and would read as a fast flush, and not for one that
	// carries no 429 but failed, which only shrinks the buffer.
	//
	// When nodeWriteSaturated changes from false to true, the byte window is pinned for
	// pinFlushes results, as after a shrink, but not shrunk. While nodeWriteSaturated
	// stays true the window grows as usual: the transport keeps it set until a node
	// statistics poll finds no new refusals, which can be the whole time other clients
	// are being refused, and a shrunken window would not grow back.
	if result.nodeWriteSaturated && !ns.nodeWriteSaturated {
		ns.windowBytes.pinWindowSize()
	}
	ns.nodeWriteSaturated = result.nodeWriteSaturated
	if result.rejected {
		ns.windowBytes.shrinkWindowSize(ns.flightBytes, b.sentAt)
		ns.stats.rejections.Add(1)
	} else {
		ns.windowBytes.growWindowSize(b.bytes)
	}
	ns.flightBytes -= b.bytes

	// A shard's first flush is measured against the best the node has shown, which
	// the node learns after this flush is folded in, so that a shard that starts while
	// the node is degraded does not take that for the best it can do.
	ss.controller.seed(ns.rate.base)
	ns.rate.learn(result)
	result.peer = ns.peerLevel(ss)
	if n, ok := ss.controller.observe(result); ok {
		// The node's slowdown is the one the shard controller measures: the flush's time on the
		// node over the time a request of that size should take, fixed cost included.
		ns.slowdown = smooth(ns.slowdown, math.Max(n-1, 0))
		s.recordLevel(ss, ns)
	}
	if result.tooLarge > 0 {
		s.capRequests(result.tooLarge)
	}
	ns.bufferFrac = smooth(ns.bufferFrac, ss.controller.fraction())
	ns.storeStats()
	s.publishLoad(result)
	ss.idleSince = s.bi.now()

	delete(s.live, b)
	s.sealedPending--
	b.finished = true
	if !ss.ordered {
		for _, item := range b.items {
			if order := s.bi.orderKey(item); s.lastByID[order] == b {
				delete(s.lastByID, order)
			}
		}
	}
	b.err = err
	reportToOnError := err != nil && b.waiters == 0

	s.bi.flushingBytes.Add(-b.held)
	s.bi.budget.release(ss.key, b.held)
	s.release(ns, b.held)
	close(b.done)

	// The shard's next buffer, collected while this request was out, may be ready.
	if ss.open != nil {
		s.sealIfFull(ss.open)
	}

	// If Add is still waiting for memory this request did not free enough, send the
	// next fullest buffer.
	if s.bi.budget.waiting() > 0 {
		s.sealFullest(flushPressure)
	}

	s.pump()
	s.Unlock()

	s.bi.putItems(b.items)
	b.items = nil

	if reportToOnError && s.bi.config.OnUnhandledError != nil {
		s.bi.config.OnUnhandledError(b.ctx, err)
	}
}

// sealIndex seals the open buffers of index's shards, so that every document added
// for them goes out under the shard it was placed with, and returns every buffer of
// those shards that is not complete, for the caller to wait on. Each is sent in its
// shard's order, after the shard's earlier request has been answered. Buffers of
// documents that belong to no shard are not touched.
//
// It marks the index as draining until endDrain. An Add placed before the change,
// but queued only after this, is then turned back and placed again, instead of
// opening a buffer the caller does not wait for; see
// bulkIndexer.validShardPlacement. The placement lock the caller holds keeps it to
// one drain at a time.
func (s *scheduler) sealIndex(index string) []*buffer {
	s.Lock()
	defer s.Unlock()

	s.draining[index] = make(chan struct{})

	var pending []*buffer
	for b := range s.live {
		if !b.shard.ordered || b.shard.key.index != index {
			continue
		}
		pending = append(pending, b)
	}

	for _, b := range pending {
		s.seal(b, flushRequested, s.bi.config.Context)
	}

	return pending
}

// endDrain ends what sealIndex began, once the new placement of index is recorded,
// and wakes the Adds that were turned back while the index drained.
func (s *scheduler) endDrain(index string) {
	s.Lock()
	defer s.Unlock()

	if quiesced, ok := s.draining[index]; ok {
		close(quiesced)
		delete(s.draining, index)
	}
}

// relieve runs when an Add starts waiting for memory. The job's memory comes back
// as requests finish, so the fullest buffer is sent now to start one, in place of
// waiting for a size or an interval that may be far off.
func (s *scheduler) relieve() {
	s.Lock()
	defer s.Unlock()

	s.sealFullest(flushPressure)
}

// drain seals every open buffer, as sent for trigger, and waits for every buffer
// that exists to complete, running those not yet sent on run and waiting under wait. They are the
// same context for a caller who asks to be waited on and may end the work early, as
// Flush does; a caller who waits for work that is not only its own passes the
// bulk client's context as run, so that giving up on the wait does not fail what others
// added.
//
// With collect set it returns the errors of those buffers, joined, and they are
// not reported to OnUnhandledError: that is how Flush gives its caller the outcome. Without
// it drain only waits, and each buffer reports its own error to OnUnhandledError as it would
// had nobody waited.
func (s *scheduler) drain(trigger flushTrigger, run, wait context.Context, collect bool) error {
	s.Lock()
	s.sealAllOpen(trigger, run)

	buffers := make([]*buffer, 0, len(s.live))
	for b := range s.live {
		if collect {
			b.waiters++
		}
		if !b.dispatched {
			b.ctx = run
		}
		buffers = append(buffers, b)
	}
	s.Unlock()

	workersDone := s.bi.workersDone()
	var errs []error

	for i, b := range buffers {
		select {
		case <-wait.Done():
			s.abandon(collect, buffers[i:])
			return wait.Err()
		case <-workersDone:
			s.abandon(collect, buffers[i:])
			return s.bi.config.Context.Err()
		case <-b.done:
			if collect && b.err != nil {
				errs = append(errs, b.err)
			}
		}
	}

	return errors.Join(errs...)
}

// abandon withdraws a drain from the buffers it stopped waiting for, because its
// context ended or the workers stopped. A buffer that no Flush waits for reports
// its own error to OnUnhandledError when it completes, and one that completed since the
// drain began, while the drain was still counted, would otherwise have reported
// to nobody: Flush returned the context's error, not the buffer's.
func (s *scheduler) abandon(collect bool, buffers []*buffer) {
	if !collect {
		return
	}

	var unreported []*buffer

	s.Lock()
	for _, b := range buffers {
		b.waiters--
		if b.waiters > 0 {
			continue
		}

		select {
		case <-b.done:
			if b.err != nil {
				unreported = append(unreported, b)
			}
		default:
		}
	}
	s.Unlock()

	if s.bi.config.OnUnhandledError == nil {
		return
	}
	for _, b := range unreported {
		s.bi.config.OnUnhandledError(b.ctx, b.err)
	}
}

// recordLevel notes the shard's level on the node that served it, and drops it
// from the node it was on before, so a shard that moved is not counted where it
// no longer is. It must be called with the lock held.
func (s *scheduler) recordLevel(ss *shardState, ns *nodeState) {
	if ss.levelNode != nil && ss.levelNode != ns {
		delete(ss.levelNode.levels, ss)
	}
	if ns.levels == nil {
		ns.levels = map[*shardState]float64{}
	}
	ns.levels[ss] = ss.controller.normS
	ss.levelNode = ns
}

// evictIdle forgets the shards that have had nothing buffered and nothing in
// flight for a good while, with what was learned about them, so that a bulk client
// that moves from index to index does not keep a record of every shard it has
// ever written to.
func (s *scheduler) evictIdle() {
	s.Lock()
	defer s.Unlock()

	cutoff := s.bi.now().Add(-idleShardIntervals * s.bi.config.FlushInterval)
	for key, ss := range s.shards {
		if ss.open == nil && ss.pending == 0 && ss.idleSince.Before(cutoff) {
			delete(s.shards, key)
			if ss.levelNode != nil {
				delete(ss.levelNode.levels, ss)
			}
		}
	}
}

// stop stops sending, for when the construction context has ended.
func (s *scheduler) stop() {
	s.Lock()
	s.stopped = true
	s.Unlock()
}

// close ends the scheduler once the bulk client has drained.
func (s *scheduler) close() {
	s.Lock()
	s.closed = true
	s.Unlock()
}

// makeFlushResult builds the result of a buffer's flush to node from what worker w
// recorded over its attempts, bytes and latency among them.
func makeFlushResult(node Node, bytes int64, latency time.Duration, w *worker) flushResult {
	f := flushResult{
		bytes:              bytes,
		latency:            latency,
		served:             w.served,
		rejected:           w.throttled,
		failed:             w.failed,
		tooLarge:           w.tooLarge,
		nodeWriteSaturated: node.writeSaturated(),
	}

	if node.Conn != nil {
		if rtt := node.Conn.RTTMedian(); rtt > 0 {
			f.idleRTT = rtt
		}
	}

	return f
}

// publishLoad stores what the controllers now know where a caller can read it
// without a lock: a rejection is counted, and the spread of each reading across the
// nodes is worked out again. It runs once per completed request, with the scheduler's
// lock held.
func (s *scheduler) publishLoad(result flushResult) {
	if result.rejected {
		s.bi.stats.numRejections.Add(1)
	}

	w := &s.work
	w.window, w.buffer, w.slow, w.weight = w.window[:0], w.buffer[:0], w.slow[:0], w.weight[:0]
	for _, ns := range s.nodes {
		w.window = append(w.window, ns.windowBytes.fraction())
		w.buffer = append(w.buffer, ns.bufferFrac)
		w.slow = append(w.slow, ns.slowdown)
		w.weight = append(w.weight, float64(ns.held.Load()))
	}

	// summarize sorts a copy of each reading in the scratch it is given, and cannot
	// hand a grown one back, so it is sized here, where it is kept.
	w.tmp = slices.Grow(w.tmp[:0], len(w.window))

	st := s.bi.stats
	st.numNodes.Store(uint64(len(w.window)))
	st.nodeWindow.store(summarize(w.window, w.weight, w.tmp))
	st.bufferSize.store(summarize(w.buffer, w.weight, w.tmp))
	st.slowdown.store(summarize(w.slow, w.weight, w.tmp))
}

// loadWork is the workspace publishLoad reuses, so that it allocates nothing once it
// has seen every node.
type loadWork struct {
	window, buffer, slow, weight, tmp []float64
}

// capRequests learns from a request of refused bytes that a node refused for its size
// that no request should be larger than half of it, and no smaller than the least a
// shard's buffer gets. Guarded by the scheduler's lock.
func (s *scheduler) capRequests(refused int64) {
	floor := s.bi.sizing.min
	if s.bi.sizing.fixed {
		// A fixed size is not shrunk.
		floor = s.bi.sizing.max
	}
	limit := max(int64(float64(refused)*s.bi.sizing.backoff), int64(floor))
	if s.maxRequest == 0 || limit < s.maxRequest {
		s.maxRequest = limit
	}
}

// capped returns size, held to the largest request the nodes accept.
func (s *scheduler) capped(size int64) int64 {
	if s.maxRequest > 0 {
		return min(size, s.maxRequest)
	}

	return size
}
