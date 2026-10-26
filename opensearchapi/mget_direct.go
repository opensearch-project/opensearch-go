// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
	"github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
	"github.com/opensearch-project/opensearch-go/v5/internal/slicepool"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

const (
	// mgetPreferenceLocal makes the receiving node answer from its own copy of
	// each shard, and fall back to another copy when it holds none, so a request
	// sent to the wrong node still gets the right answer.
	mgetPreferenceLocal = "_local"

	// mgetDirectFailureType is the error type of a document the driver answers
	// itself, because no request for it, direct or through a coordinating node,
	// completed. The cluster answers a document whose shard request failed the
	// same way, with the failure's exception (TransportMultiGetAction).
	mgetDirectFailureType = "mget_direct_request_exception"

	mgetRetryBase = 50 * time.Millisecond
	mgetRetryMax  = time.Second
)

var (
	// errMGetNotSplittable is wrapped by every reason the direct driver leaves an
	// mget to the cluster as one request.
	errMGetNotSplittable = errors.New("mget cannot be split the way the cluster would split it")

	errMGetNoDocs        = fmt.Errorf("%w: it has no docs, a raw BodyReader, or ids in place of docs", errMGetNotSplittable)
	errMGetPreference    = fmt.Errorf("%w: it has a preference", errMGetNotSplittable)
	errMGetReshaped      = fmt.Errorf("%w: filter_path, format or source reshapes it", errMGetNotSplittable)
	errMGetNoneRouted    = fmt.Errorf("%w: no document routes to a node", errMGetNotSplittable)
	errNoUsableShardNode = errors.New("no copy of the shard is on a usable node")
)

// mgetRetryableTypes are the per-document error types that say the document's
// shard copy could not answer, rather than anything about the document, so
// asking again, perhaps another copy, can succeed.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var mgetRetryableTypes = map[string]struct{}{
	"no_shard_available_action_exception": {},
	"shard_not_found_exception":           {},
	"unavailable_shards_exception":        {},
	"illegal_index_shard_state_exception": {},
	"node_not_connected_exception":        {},
	"node_disconnected_exception":         {},
	"node_closed_exception":               {},
	"connect_transport_exception":         {},
	"receive_timeout_transport_exception": {},
}

// mgetRejectedTypes are the per-document error types that say the node was
// overloaded. They are retried, and they shrink the node's window as a 429
// does.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var mgetRejectedTypes = map[string]struct{}{
	"opensearch_rejected_execution_exception": {},
	"circuit_breaking_exception":              {},
}

// mgetSender sends one request to a node.
type mgetSender func(ctx context.Context, c *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error)

// sendMGetToNode sends req to node, answered from the node's own shard copies,
// as an ordinary mget so the generated dispatch decodes it.
func sendMGetToNode(ctx context.Context, c *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
	ctx = opensearchtransport.WithReadNode(ctx, node)

	return c.Doc.MGet(mgetCoordinated(ctx), req)
}

// mgetCoordinated returns ctx with [MGetDriverCoordinator] selected, so that a
// sub-request goes to the cluster as one request rather than being split again.
// flush selects it once for every request of a call; a ctx that already selects
// it is returned as it is.
func mgetCoordinated(ctx context.Context) context.Context {
	if v, ok := ctx.Value(mgetDriverKey{}).(MGetDriver); ok && v == MGetDriverCoordinator {
		return ctx
	}

	return WithMGetDriver(ctx, MGetDriverCoordinator)
}

// mgetDriver fetches an mget's documents from the nodes that hold them. One
// driver serves every call a client makes, so its node windows learn from all
// of them.
type mgetDriver struct {
	driver      MGetDriver
	fanOut      MGetFanOut
	lookup      shardLookup
	send        mgetSender
	saturated   func(*opensearchtransport.Connection) bool
	windows     *scheduler.NodeWindows
	observer    MGetObserver
	maxDocs     int
	maxAttempts int
}

//nolint:gochecknoglobals // A pool is process-wide by design.
var mgetTargetPool slicepool.Pool[opensearchtransport.ShardTarget]

// mgetJob is one request to one node: the documents it asks the node for.
type mgetJob struct {
	node *opensearchtransport.Connection
	// local sends preference=_local, so the node answers from its own shard
	// copies. Without it the node routes each document as a coordinator would,
	// which sends a realtime read of a segment-replicated index to the primary.
	local bool
	// slots are the positions of the job's documents in the caller's request.
	slots []int
	// attempt is 1 for a document's first request, and counts up on each retry.
	attempt int
}

// shardNode is the node findShardNode picked for a document, among the nodes
// that hold a copy of its shard, and whether the request to it carries
// preference=_local.
type shardNode struct {
	node  *opensearchtransport.Connection
	local bool
}

// mgetCall is one mget the direct driver is answering.
type mgetCall struct {
	d   *mgetDriver
	c   *Client
	req MGetReq
	// fanOut is how the call picks the shard copies it reads from.
	fanOut MGetFanOut
	// realtime is the request's realtime, true unless it says otherwise.
	realtime bool
	// routing is the request's default routing, applied to documents with none.
	routing string
	// params holds the caller's parameters for every sub-request: [0] without a
	// preference and [1] with preference=_local. They are set once per call and
	// shared, read-only, by its sub-requests.
	params [2]MGetParams

	jobs []mgetJob
	// unrouted are the positions route could not send to a node. They are
	// fetched through a coordinating node.
	unrouted []int

	// docs are the answers, one per position. A position is written by the one
	// goroutine that holds it at that moment, so it needs no lock.
	docs []MGetRespItem

	// cancel stops every request of the call, to resend it whole.
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu struct {
		sync.Mutex
		header http.Header
		resend bool
		// err is the failure returned should no request succeed, and errResp
		// the response that came with it, if any; see recordErr.
		err      error
		errResp  *MGetResp
		answered int
		// nodes names each node the call sent a request to, once.
		nodes []string
		stats MGetDoneEvent
	}
}

// write takes req's documents and routes each to the node to send it to, picking
// shard copies by fanOut, ready for flush. When req is not one the client can
// split the way the cluster would, or no document routes to a node, it returns
// an error that wraps errMGetNotSplittable and says why, and the mget goes to
// the cluster as one request.
func (d *mgetDriver) write(c *Client, req MGetReq, fanOut MGetFanOut) (*mgetCall, error) {
	switch {
	case req.BodyReader != nil || req.Body == nil || req.Body.IDs != nil || len(req.Body.Docs) == 0:
		return nil, errMGetNoDocs
	case req.Params != nil && req.Params.Preference != "":
		return nil, errMGetPreference
	case req.Params != nil && mgetReshapesResponse(req.Params.DebugParams):
		return nil, errMGetReshaped
	}

	call := &mgetCall{
		d:        d,
		c:        c,
		req:      req,
		fanOut:   fanOut,
		realtime: req.Params == nil || req.Params.Realtime == nil || *req.Params.Realtime,
		docs:     make([]MGetRespItem, len(req.Body.Docs)),
	}
	if req.Params != nil {
		call.routing = strings.Join(req.Params.Routing, ",")
		call.params[0] = *req.Params
	}
	call.params[0].Preference = ""
	call.params[1] = call.params[0]
	call.params[1].Preference = mgetPreferenceLocal

	call.jobs, call.unrouted = call.split(nil, 1, "")
	if len(call.jobs) == 0 {
		return nil, errMGetNoneRouted
	}

	return call, nil
}

// mgetReshapesResponse reports whether p changes the response's shape or the
// request's body, so that a node's answer can no longer be matched to the
// documents asked for: filter_path can drop each document's _id, format changes
// the encoding, and source replaces the body.
func mgetReshapesResponse(p DebugParams) bool {
	return len(p.FilterPath) > 0 || p.Format != "" || p.Source != ""
}

// split routes the documents at positions to nodes for the given attempt, and
// cuts each node's share into jobs of at most maxDocs documents. A nil
// positions splits every document of the request. avoid names a node to skip
// when a document's shard has a copy on another node. split returns the jobs,
// and the positions it could not route to a node, which are fetched through a
// coordinating node instead.
func (call *mgetCall) split(positions []int, attempt int, avoid string) ([]mgetJob, []int) {
	count := len(positions)
	if positions == nil {
		count = len(call.req.Body.Docs)
	}

	s := getMGetRouteScratch(count)
	defer putMGetRouteScratch(s)

	unrouted := call.route(positions, avoid, s)

	// assigned[i] is the group of the i-th position, or -1 when it is not routed.
	// The groups' sizes are known only once every position is assigned.
	for i, sn := range s.picks {
		s.assigned[i] = -1
		if sn.node == nil {
			continue
		}

		gk := mgetGroupKey{node: sn.node.Name, local: sn.local}
		g, found := s.index[gk]
		if !found {
			g = len(s.groups)
			s.index[gk] = g
			s.groups = append(s.groups, mgetGroup{node: sn.node, local: sn.local})
		}
		s.groups[g].n++
		s.assigned[i] = int32(g) //nolint:gosec // There are no more groups than nodes.
	}

	// Lay every group's positions out in one array, and cut each group into jobs
	// of at most maxDocs. The array and the jobs outlive the call to split, so
	// they are not scratch space.
	routed, numJobs := 0, 0
	for g := range s.groups {
		s.groups[g].start = routed
		routed += s.groups[g].n
		numJobs += (s.groups[g].n + call.d.maxDocs - 1) / call.d.maxDocs
	}
	slots := make([]int, routed)
	s.filled = slices.Grow(s.filled[:0], len(s.groups))[:len(s.groups)]
	clear(s.filled)
	for i, g := range s.assigned {
		if g < 0 {
			continue
		}
		slots[s.groups[g].start+s.filled[g]] = mgetPosition(positions, i)
		s.filled[g]++
	}

	jobs := make([]mgetJob, 0, numJobs)
	for _, grp := range s.groups {
		mine := slots[grp.start : grp.start+grp.n]
		for start := 0; start < len(mine); start += call.d.maxDocs {
			end := min(start+call.d.maxDocs, len(mine))
			jobs = append(jobs, mgetJob{node: grp.node, local: grp.local, attempt: attempt, slots: mine[start:end:end]})
		}
	}

	return jobs, unrouted
}

// mgetPosition returns the i-th position of positions, where a nil positions
// stands for every document of the request, in order.
func mgetPosition(positions []int, i int) int {
	if positions == nil {
		return i
	}

	return positions[i]
}

// route chooses the node each document at positions is read from, by the
// call's fan-out, and records the choice for the i-th position in s.picks[i]. A
// position it cannot route to a node keeps no node in s.picks; route returns
// those positions, in order.
func (call *mgetCall) route(positions []int, avoid string, s *mgetRouteScratch) []int {
	var (
		unrouted []int
		planned  map[string]int
		cover    *mgetCover
	)
	if call.fanOut == MGetFanOutLeastLoaded {
		planned = s.planned
	} else {
		cover = getMGetCover(len(s.picks))
		defer putMGetCover(cover)
	}

	targets := mgetTargetPool.Get()
	defer mgetTargetPool.Put(targets)

	for i := range s.picks {
		pos := mgetPosition(positions, i)

		op := call.req.Body.Docs[pos]
		key := op.ID
		switch {
		case op.Routing != nil && *op.Routing != "":
			key = *op.Routing
		case call.routing != "":
			key = call.routing
		}

		idx := call.indexOf(pos)
		if idx == "" || op.ID == "" || call.d.lookup.LookupShard(idx, key, targets) != nil {
			unrouted = append(unrouted, pos)

			continue
		}

		// A read that must go to the primary has no copy to choose, so the cover
		// leaves it to findShardNode.
		if cover != nil && !call.primaryOnly(*targets) {
			if !cover.add(i, idx, *targets, avoid, call.d.saturated) {
				unrouted = append(unrouted, pos)
			}

			continue
		}

		sn, err := call.findShardNode(*targets, avoid, planned)
		if err != nil {
			unrouted = append(unrouted, pos)

			continue
		}
		if planned != nil {
			planned[sn.node.Name]++
		}
		if cover != nil {
			cover.contacted(sn.node.Name)
		}
		s.picks[i] = sn
	}

	if cover != nil {
		cover.assign(call.d.windows, s.picks)
	}

	return unrouted
}

// mgetGroupKey identifies the documents split sends to one node with one
// preference.
type mgetGroupKey struct {
	node  string
	local bool
}

// mgetGroup is the documents split sends to one node with one preference. start
// is where their positions begin in the slots array that split lays out, and n
// is how many there are.
type mgetGroup struct {
	node     *opensearchtransport.Connection
	local    bool
	start, n int
}

// mgetRouteScratch is the space split and route use only while they run. It is
// pooled, and keeps what it grew from one mget to the next, up to the cover's
// bounds.
type mgetRouteScratch struct {
	picks    []shardNode
	assigned []int32
	filled   []int
	groups   []mgetGroup
	index    map[mgetGroupKey]int
	// planned counts, for MGetFanOutLeastLoaded, the documents already routed to
	// each node.
	planned map[string]int
}

//nolint:gochecknoglobals // A pool is process-wide by design.
var mgetRouteScratchPool = sync.Pool{New: func() any {
	return &mgetRouteScratch{index: make(map[mgetGroupKey]int), planned: make(map[string]int)}
}}

// getMGetRouteScratch returns pooled scratch space for routing count positions.
func getMGetRouteScratch(count int) *mgetRouteScratch {
	s, _ := mgetRouteScratchPool.Get().(*mgetRouteScratch)
	s.picks = slices.Grow(s.picks[:0], count)[:count]
	s.assigned = slices.Grow(s.assigned[:0], count)[:count]

	return s
}

// putMGetRouteScratch empties s and returns it to the pool. It drops every
// reference to a connection, so that pooled scratch keeps no removed node
// alive, and drops what grew past the cover's bounds.
func putMGetRouteScratch(s *mgetRouteScratch) {
	clear(s.picks)
	clear(s.groups)
	s.groups = s.groups[:0]
	if cap(s.picks) > mgetCoverMaxPositions {
		s.picks, s.assigned = nil, nil
	}
	if cap(s.groups) > mgetCoverMaxNodes {
		s.groups, s.filled = nil, nil
	}
	if len(s.index) > mgetCoverMaxNodes || len(s.planned) > mgetCoverMaxNodes {
		s.index, s.planned = make(map[mgetGroupKey]int), make(map[string]int)
	} else {
		clear(s.index)
		clear(s.planned)
	}
	mgetRouteScratchPool.Put(s)
}

// primaryOnly reports whether a document whose shard has the copies targets is
// read from the primary only: a realtime read of an index not known to use
// document replication. A replication type not known to be document
// replication is treated as segment replication, whose replicas can trail the
// primary.
func (call *mgetCall) primaryOnly(targets []*opensearchtransport.ShardTarget) bool {
	return call.realtime && len(targets) > 0 && targets[0].Replication != opensearchtransport.ReplicationDocument
}

// mgetCover picks, for MGetFanOutFewestNodes, one node for each shard an mget
// reads so that the mget reaches as few nodes as it can. Finding the fewest is
// the set cover problem, so it takes the greedy answer: the node holding a copy
// of the most shards not yet covered, until every shard is covered. A node the
// mget already sends to takes every shard it holds before any other node is
// chosen: one more document to a node already reached costs less than a request
// to another node, however idle, and every node an mget waits on is one more
// that can time out or fail it.
//
// A cover is pooled, and keeps its maps and slices from one mget to the next;
// recycle bounds what it keeps.
type mgetCover struct {
	shards []mgetCoverShard
	index  map[mgetCoverKey]int32
	// of is the shard of the i-th position, or -1 for a position the cover does
	// not place.
	of []int32
	// nodes holds, for each node with a usable copy of a shard, what the cover
	// knows of it.
	nodes map[string]*mgetCoverNode
	// spare are the mgetCoverNodes of earlier mgets, ready for reuse.
	spare []*mgetCoverNode
	// reached names the nodes the mget sends to whatever the cover chooses:
	// those of reads that must go to the primary. A name may repeat.
	reached []string
	// usable and idle are add's scratch space.
	usable, idle []*opensearchtransport.Connection
}

type mgetCoverKey struct {
	index string
	shard int
}

// mgetCoverShard is one shard: the nodes with a usable copy of it, and the node
// the cover chose.
type mgetCoverShard struct {
	nodes  []string
	chosen *opensearchtransport.Connection
}

// mgetCoverNode is one node: the shards it holds a usable copy of, how many of
// them no node has been chosen for yet, and its documents in flight when assign
// began.
type mgetCoverNode struct {
	conn      *opensearchtransport.Connection
	holds     []int32
	uncovered int
	load      int64
}

const (
	// mgetCoverMaxNodes, mgetCoverMaxShards and mgetCoverMaxPositions bound what a
	// pooled cover keeps, so that one large mget does not hold its memory for every
	// small one after it. A map or slice that grew past its bound is dropped when
	// the cover is recycled, and the next mget grows a new one.
	mgetCoverMaxNodes     = 1024
	mgetCoverMaxShards    = 8192
	mgetCoverMaxPositions = 16384
)

//nolint:gochecknoglobals // A pool is process-wide by design.
var mgetCoverPool = sync.Pool{New: func() any { return newMGetCover() }}

func newMGetCover() *mgetCover {
	return &mgetCover{
		index: make(map[mgetCoverKey]int32),
		nodes: make(map[string]*mgetCoverNode),
	}
}

// getMGetCover returns a pooled cover ready for count positions.
func getMGetCover(count int) *mgetCover {
	c, _ := mgetCoverPool.Get().(*mgetCover)
	c.prepare(count)

	return c
}

// prepare readies a recycled cover for count positions.
func (c *mgetCover) prepare(count int) {
	c.of = slices.Grow(c.of[:0], count)[:count]
	for i := range c.of {
		c.of[i] = -1
	}
}

// recycle empties the cover for the next mget, dropping what grew past its
// bounds and every reference to a connection, so that a pooled cover keeps no
// removed node alive.
func (c *mgetCover) recycle() {
	if len(c.nodes) > mgetCoverMaxNodes || len(c.index) > mgetCoverMaxShards {
		c.index = make(map[mgetCoverKey]int32)
		c.nodes = make(map[string]*mgetCoverNode)
	} else {
		for _, n := range c.nodes {
			if len(c.spare) >= mgetCoverMaxNodes || cap(n.holds) > mgetCoverMaxShards {
				continue
			}
			n.conn, n.holds = nil, n.holds[:0]
			c.spare = append(c.spare, n)
		}
		clear(c.index)
		clear(c.nodes)
	}

	if cap(c.shards) > mgetCoverMaxShards {
		c.shards = nil
	} else {
		for i := range c.shards {
			c.shards[i].chosen = nil
		}
		c.shards = c.shards[:0]
	}
	if cap(c.of) > mgetCoverMaxPositions {
		c.of = nil
	}
	if cap(c.reached) > mgetCoverMaxNodes {
		c.reached = nil
	}
	c.reached = c.reached[:0]
	clear(c.usable[:cap(c.usable)])
	clear(c.idle[:cap(c.idle)])
}

// putMGetCover recycles c and returns it to the pool.
func putMGetCover(c *mgetCover) {
	c.recycle()
	mgetCoverPool.Put(c)
}

// contacted records that the mget sends to node whatever the cover chooses.
func (c *mgetCover) contacted(node string) {
	if n := len(c.reached); n > 0 && c.reached[n-1] == node {
		return
	}
	c.reached = append(c.reached, node)
}

// add records that the i-th position is in index's shard with the copies
// targets. It skips the avoid node when the shard has another copy, and a
// saturated node when the shard has a copy on one that is not. It reports
// false when no copy is on a usable node.
func (c *mgetCover) add(
	i int, index string, targets []*opensearchtransport.ShardTarget, avoid string,
	saturated func(*opensearchtransport.Connection) bool,
) bool {
	key := mgetCoverKey{index: index, shard: targets[0].Shard}
	if s, ok := c.index[key]; ok {
		c.of[i] = s

		return true
	}

	c.usable, c.idle = c.usable[:0], c.idle[:0]
	for _, target := range targets {
		if target.Node == nil || (target.Node.Name == avoid && len(targets) > 1) {
			continue
		}
		c.usable = append(c.usable, target.Node)
		if !saturated(target.Node) {
			c.idle = append(c.idle, target.Node)
		}
	}
	usable := c.usable
	if len(c.idle) > 0 {
		usable = c.idle
	}
	if len(usable) == 0 {
		return false
	}

	s := int32(len(c.shards)) //nolint:gosec // There are no more shards than positions.
	if len(c.shards) < cap(c.shards) {
		c.shards = c.shards[:s+1]
	} else {
		c.shards = append(c.shards, mgetCoverShard{})
	}
	shard := &c.shards[s]
	shard.nodes, shard.chosen = shard.nodes[:0], nil
	for _, conn := range usable {
		shard.nodes = append(shard.nodes, conn.Name)
		n := c.node(conn)
		n.holds = append(n.holds, s)
	}
	c.index[key] = s
	c.of[i] = s

	return true
}

// node returns what the cover knows of conn's node, reusing a spare entry for a
// node it has not seen in this mget.
func (c *mgetCover) node(conn *opensearchtransport.Connection) *mgetCoverNode {
	if n, ok := c.nodes[conn.Name]; ok {
		return n
	}

	var n *mgetCoverNode
	if last := len(c.spare) - 1; last >= 0 {
		n, c.spare[last] = c.spare[last], nil
		c.spare = c.spare[:last]
	} else {
		n = &mgetCoverNode{}
	}
	n.conn = conn
	c.nodes[conn.Name] = n

	return n
}

// assign chooses a node for every shard and writes each placed position's node
// into picks. The nodes the mget already reaches take the shards they hold
// first, in name order. Then the node with the most shards still uncovered
// goes next, and on a tie the one with the fewest documents in flight in
// windows, then the first by name. The greedy choice approximates the fewest
// nodes; it does not promise them.
func (c *mgetCover) assign(windows *scheduler.NodeWindows, picks []shardNode) {
	for name, n := range c.nodes {
		n.uncovered = len(n.holds)
		n.load = 0
		if st, ok := windows.Stats(scheduler.NodeName(name)); ok {
			n.load = st.InFlight
		}
	}
	left := len(c.shards)
	take := func(n *mgetCoverNode) {
		for _, s := range n.holds {
			if c.shards[s].chosen != nil {
				continue
			}
			c.shards[s].chosen = n.conn
			left--
			for _, other := range c.shards[s].nodes {
				c.nodes[other].uncovered--
			}
		}
	}

	slices.Sort(c.reached)
	c.reached = slices.Compact(c.reached)
	for _, name := range c.reached {
		if n, ok := c.nodes[name]; ok {
			take(n)
		}
	}

	for left > 0 {
		var (
			best     *mgetCoverNode
			bestName string
		)
		for name, n := range c.nodes {
			if n.uncovered == 0 {
				continue
			}
			switch {
			case best == nil, n.uncovered > best.uncovered:
			case n.uncovered == best.uncovered && n.load < best.load:
			case n.uncovered == best.uncovered && n.load == best.load && name < bestName:
			default:
				continue
			}
			best, bestName = n, name
		}
		take(best)
	}

	for i, s := range c.of {
		if s >= 0 {
			picks[i] = shardNode{node: c.shards[s].chosen, local: true}
		}
	}
}

// findShardNode picks, among the nodes that hold a copy of a document's shard,
// the node to read the document from.
//
// A realtime read of an index not known to use document replication goes to the
// primary's node with no preference, so the node itself sends the read to the
// primary, as a coordinator would. Any other read goes to the node with the
// fewest documents in flight, counting the documents in planned, the ones
// already routed in this pass, and a replica wins a tie. It returns
// errNoUsableShardNode when no copy is on a usable node.
func (call *mgetCall) findShardNode(
	targets []*opensearchtransport.ShardTarget, avoid string, planned map[string]int,
) (shardNode, error) {
	if len(targets) == 0 {
		return shardNode{}, errNoUsableShardNode
	}

	if call.primaryOnly(targets) {
		primary := targets[0]
		if !primary.Primary || primary.Node == nil {
			return shardNode{}, errNoUsableShardNode
		}

		return shardNode{node: primary.Node}, nil
	}

	var (
		best     *opensearchtransport.ShardTarget
		bestLoad int64
	)
	for _, target := range targets {
		if target.Node == nil {
			continue
		}
		if target.Node.Name == avoid && len(targets) > 1 {
			continue
		}

		load := int64(planned[target.Node.Name])
		if st, ok := call.d.windows.Stats(scheduler.NodeName(target.Node.Name)); ok {
			load += st.InFlight
		}

		switch {
		case best == nil, load < bestLoad:
		case load == bestLoad && best.Primary && !target.Primary:
		case load == bestLoad && best.Primary == target.Primary && target.Node.Name < best.Node.Name:
		default:
			continue
		}
		best, bestLoad = target, load
	}
	if best == nil {
		return shardNode{}, errNoUsableShardNode
	}

	return shardNode{node: best.Node, local: true}, nil
}

// flush sends the written requests, waits for them, and returns the answer.
func (call *mgetCall) flush(parent context.Context) (*MGetResp, error) {
	start := time.Now()
	// Every request of the call goes to the cluster as one request; selecting
	// that once here saves each request a context of its own.
	ctx, cancel := context.WithCancel(mgetCoordinated(parent))
	call.cancel = cancel
	defer cancel()

	// A job per node is the usual case, and a retry that reaches another node
	// grows the slice.
	call.mu.nodes = make([]string, 0, len(call.jobs))
	call.mu.stats.Docs = len(call.docs)

	if len(call.unrouted) > 0 {
		call.coordinate(ctx, call.unrouted)
	}
	// The last job runs on this goroutine, which would otherwise only wait, so
	// an mget that goes to one node starts no goroutine at all.
	for i, j := range call.jobs {
		if i == len(call.jobs)-1 {
			call.sendJob(ctx, j)

			break
		}
		call.dispatch(ctx, j)
	}
	call.wg.Wait()

	resp, err := call.finish(parent)

	call.mu.Lock()
	stats := call.mu.stats
	stats.Nodes = len(call.mu.nodes)
	call.mu.Unlock()
	stats.Duration = time.Since(start)
	stats.Err = err
	if call.d.observer != nil {
		call.d.observer.OnMGetDone(parent, stats)
	}

	return resp, err
}

// finish turns the answers into the call's result.
func (call *mgetCall) finish(parent context.Context) (*MGetResp, error) {
	if err := parent.Err(); err != nil {
		return &MGetResp{}, err
	}

	call.mu.Lock()
	resend, answered, err, errResp, header := call.mu.resend, call.mu.answered, call.mu.err, call.mu.errResp, call.mu.header
	call.mu.Unlock()

	if resend {
		call.mu.Lock()
		call.mu.stats.Resent = true
		call.mu.stats.Requests++
		call.mu.Unlock()

		return call.c.Doc.MGet(WithMGetDriver(parent, MGetDriverCoordinator), call.req)
	}
	if answered == 0 && err != nil {
		// No node answered at all, as the coordinator path would have failed.
		if errResp == nil {
			errResp = &MGetResp{}
		}

		return errResp, err
	}

	return call.merge(header), nil
}

// dispatch sends a job on its own goroutine.
func (call *mgetCall) dispatch(ctx context.Context, j mgetJob) {
	call.wg.Go(func() { call.sendJob(ctx, j) })
}

// coordinate fetches the documents at slots through a coordinating node, on its
// own goroutine.
func (call *mgetCall) coordinate(ctx context.Context, slots []int) {
	call.mu.Lock()
	call.mu.stats.CoordinatorDocs += len(slots)
	call.mu.Unlock()

	call.wg.Go(func() { call.sendCoordinated(ctx, slots) })
}

// subRequest builds the request for the documents at slots, with the caller's
// parameters; local adds preference=_local. Its body is encoded into a pooled
// buffer, and the caller passes the returned body to bufbody.Release once the
// request has returned. An error is from encoding a document, which the
// generated request would have hit as well.
func (call *mgetCall) subRequest(slots []int, local bool) (MGetReq, *bufbody.Pooled, error) {
	body, err := encodeMGetBody(call.req.Body.Docs, slots)
	if err != nil {
		return MGetReq{}, nil, err
	}

	params := &call.params[0]
	if local {
		params = &call.params[1]
	}

	return MGetReq{Index: call.req.Index, BodyReader: body, Header: call.req.Header, Params: params}, body, nil
}

// sendJob sends a job to its node once the node's window has room, injects the
// node's response, and sends again any documents that need another request.
func (call *mgetCall) sendJob(ctx context.Context, j mgetJob) {
	reservation, err := call.d.windows.Acquire(ctx, scheduler.NodeName(j.node.Name), int64(len(j.slots)))
	if err != nil {
		return
	}

	// Encoded once admitted, so that a job waiting for room holds no buffer.
	sub, body, err := call.subRequest(j.slots, j.local)
	if err != nil {
		call.d.windows.Release(reservation, scheduler.OutcomeUnknown, false)
		// The cluster's answer to the whole mget, encoded as the generated
		// request encodes it, is what the caller would have got.
		call.resendWhole()

		return
	}

	call.mu.Lock()
	if !slices.Contains(call.mu.nodes, j.node.Name) {
		call.mu.nodes = append(call.mu.nodes, j.node.Name)
	}
	call.mu.stats.Requests++
	if j.attempt > 1 {
		call.mu.stats.Retries++
	}
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.d.send(withMGetDocs(ctx, call.req, j.slots), call.c, j.node, sub)
	bufbody.Release(body)
	event := MGetRequestEvent{
		Node:       j.node.Name,
		Attempt:    j.attempt,
		Docs:       len(j.slots),
		Duration:   time.Since(sent),
		StatusCode: mgetStatus(resp),
	}

	outcome, retry, fallback := call.injectResponse(ctx, j, resp, err, &event)
	call.d.windows.Release(reservation, outcome, call.d.saturated(j.node))

	event.Err = err
	event.RetryDocs = len(retry) + len(fallback)
	if call.d.observer != nil {
		call.d.observer.OnMGetRequest(ctx, event)
	}

	if len(fallback) > 0 {
		call.coordinate(ctx, fallback)
	}
	if len(retry) == 0 {
		return
	}
	if j.attempt >= call.d.maxAttempts {
		call.coordinate(ctx, retry)

		return
	}
	if !mgetWait(ctx, j.attempt) {
		return
	}

	jobs, unrouted := call.split(retry, j.attempt+1, j.node.Name)
	if len(unrouted) > 0 {
		call.coordinate(ctx, unrouted)
	}
	for _, next := range jobs {
		call.dispatch(ctx, next)
	}
}

// injectResponse copies each document's answer from a node's response into
// that document's slot in the merged mget response. It returns the outcome to
// record in the node's window, the documents to send to a node again, and the
// documents to send to a coordinating node instead.
//
// A document whose answer says its shard copy could not respond is left out and
// returned for another request. A response that cannot be matched to the job's
// documents injects nothing and sends them all to a coordinating node.
func (call *mgetCall) injectResponse(
	ctx context.Context, j mgetJob, resp *MGetResp, err error, event *MGetRequestEvent,
) (scheduler.Outcome, []int, []int) {
	if err != nil {
		switch status := mgetStatus(resp); {
		case ctx.Err() != nil:
			return scheduler.OutcomeFailed, nil, nil
		case status == http.StatusTooManyRequests:
			event.Rejected = true

			return scheduler.OutcomeRejected, j.slots, nil
		case status == 0 || status >= http.StatusInternalServerError:
			call.recordErr(resp, err)

			return scheduler.OutcomeFailed, j.slots, nil
		default:
			// The cluster would refuse the whole mget the same way. Ask it for the
			// whole mget, and return what it says.
			call.resendWhole()

			return scheduler.OutcomeFailed, nil, nil
		}
	}

	if !mgetAnswerMatches(resp, call.req.Body.Docs, j.slots) {
		return scheduler.OutcomeFailed, nil, j.slots
	}

	outcome := scheduler.OutcomeAccepted
	var retry []int
	for i, pos := range j.slots {
		item := resp.Docs[i]
		if failure, ferr := item.MultiGetError(); ferr == nil {
			_, rejected := mgetRejectedTypes[failure.Error.Type]
			_, retryable := mgetRetryableTypes[failure.Error.Type]
			switch {
			case rejected:
				outcome = scheduler.OutcomeRejected
				event.Rejected = true
				retry = append(retry, pos)

				continue
			case retryable:
				retry = append(retry, pos)

				continue
			}
		}
		call.docs[pos] = item
	}
	call.answered(resp, len(j.slots)-len(retry))

	return outcome, retry, nil
}

// sendCoordinated asks a coordinating node for the documents at slots, and
// places its answer.
func (call *mgetCall) sendCoordinated(ctx context.Context, slots []int) {
	sub, body, err := call.subRequest(slots, false)
	if err != nil {
		call.resendWhole()

		return
	}

	call.mu.Lock()
	call.mu.stats.Requests++
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.c.Doc.MGet(withMGetDocs(mgetCoordinated(ctx), call.req, slots), sub)
	bufbody.Release(body)
	event := MGetRequestEvent{Docs: len(slots), Duration: time.Since(sent), StatusCode: mgetStatus(resp), Err: err}
	if call.d.observer != nil {
		defer call.d.observer.OnMGetRequest(ctx, event)
	}

	switch status := mgetStatus(resp); {
	case ctx.Err() != nil:
		return
	case err != nil && status != 0 && status != http.StatusTooManyRequests && status < http.StatusInternalServerError:
		call.resendWhole()

		return
	case err != nil:
		call.recordErr(resp, err)
		call.fail(slots, err)

		return
	case !mgetAnswerMatches(resp, call.req.Body.Docs, slots):
		// The cluster answered, but not in a shape the documents can be placed
		// from. Ask it for the whole mget, and return what it says.
		call.resendWhole()

		return
	}

	for i, pos := range slots {
		call.docs[pos] = resp.Docs[i]
	}
	call.answered(resp, len(slots))
}

// mgetWait waits out the backoff before attempt+1, and reports false when ctx
// ends first.
func mgetWait(ctx context.Context, attempt int) bool {
	ceiling := min(mgetRetryBase<<(attempt-1), mgetRetryMax)
	delay := time.Duration(rand.Int64N(int64(ceiling) + 1)) //nolint:gosec // Jitter, not security.

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// answered records that a node's answer settled n documents.
func (call *mgetCall) answered(resp *MGetResp, n int) {
	call.mu.Lock()
	defer call.mu.Unlock()

	call.mu.answered += n
	if call.mu.header == nil && resp != nil && resp.response != nil {
		// The sub-response is not used again, so its header can be shared.
		call.mu.header = resp.response.Header
	}
}

// recordErr records a request's failure, to be the call's result should no
// request succeed. A failure with a response replaces one without, so the
// caller can inspect what the cluster said, as a failed coordinator request
// lets it.
func (call *mgetCall) recordErr(resp *MGetResp, err error) {
	call.mu.Lock()
	defer call.mu.Unlock()

	if call.mu.err == nil || (mgetStatus(call.mu.errResp) == 0 && mgetStatus(resp) != 0) {
		call.mu.err, call.mu.errResp = err, resp
	}
}

// resendWhole stops the call, to be answered by the cluster as one request.
func (call *mgetCall) resendWhole() {
	call.mu.Lock()
	call.mu.resend = true
	call.mu.Unlock()

	call.cancel()
}

// fail answers the documents at slots with err, as the cluster answers a
// document whose shard request failed.
func (call *mgetCall) fail(slots []int, err error) {
	call.mu.Lock()
	call.mu.stats.FailedDocs += len(slots)
	call.mu.Unlock()

	for _, pos := range slots {
		call.docs[pos] = mgetFailureItem(call.indexOf(pos), call.req.Body.Docs[pos].ID, err)
	}
}

// indexOf returns the index the document at pos names: its own _index, or else
// the request's.
func (call *mgetCall) indexOf(pos int) string {
	if op := call.req.Body.Docs[pos]; op.Index != nil && *op.Index != "" {
		return *op.Index
	}

	return call.req.Index
}

// mgetFailureItem is the answer for a document whose every request failed, in
// the shape the cluster gives a failed document.
func mgetFailureItem(index, id string, err error) MGetRespItem {
	reason := err.Error()
	cause := ErrorCause{Type: mgetDirectFailureType, Reason: &reason}
	failure := MGetMultiGetError{
		ID:    id,
		Index: index,
		Error: ErrorCause{Type: mgetDirectFailureType, Reason: &reason, RootCause: []ErrorCause{cause}},
	}

	item := NewMGetRespItemFromMultiGetError(failure)
	if raw, merr := json.Marshal(failure); merr == nil {
		item.raw = raw
	}

	return item
}

// merge builds the response from the answers, already decoded from each node's
// response: the documents in the order they were asked for, and one raw body
// holding each document's answer as its node sent it, which each document's
// RawJSON then points into.
//
// merge joins bytes rather than encoding them, and that is safe only because
// every byte it appends is one of three things:
//
//   - the constants {"docs":[ , and ]};
//   - a document's raw JSON from a node's response. The generated decoder runs
//     json.Unmarshal over the whole response, which validates all of it before
//     decoding, and hands each document exactly one complete JSON value from it;
//   - a failure item that mgetFailureItem built with json.Marshal.
//
// A complete JSON value cannot end the array early, so the result is valid
// JSON. Keep it so: anything else appended here must come from json.Marshal or
// from a buffer json.Unmarshal has validated. encoding/json's own Marshal is not
// used because it compacts each json.RawMessage and escapes <, > and & in it,
// which would change the bytes the cluster sent. TestMGetDirectMergeKeepsNodeBytes
// checks the invariant with hostile document content.
func (call *mgetCall) merge(header http.Header) *MGetResp {
	const open, closing = `{"docs":[`, `]}`

	size := len(open) + len(closing)
	for i := range call.docs {
		if len(call.docs[i].raw) == 0 {
			// A position nothing answered: every path places or fails each one,
			// so this is a defect, answered as a failed document rather than a
			// malformed body.
			call.docs[i] = mgetFailureItem(call.indexOf(i), call.req.Body.Docs[i].ID, errors.New("mget: document was not answered"))
		}
		size += len(call.docs[i].raw) + 1
	}

	body := make([]byte, 0, size)
	body = append(body, open...)
	for i := range call.docs {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, call.docs[i].raw...)
	}
	body = append(body, closing...)

	// Point each document's raw JSON into body, from the lengths just written.
	off := len(open)
	for i := range call.docs {
		end := off + len(call.docs[i].raw)
		call.docs[i].raw = body[off:end:end]
		off = end + 1
	}

	return &MGetResp{Docs: call.docs, response: opensearch.NewBufferedResponse(http.StatusOK, body, header)}
}

// mgetStatus returns the HTTP status of the response behind resp, or 0 when the
// request produced none.
func mgetStatus(resp *MGetResp) int {
	if resp == nil || resp.response == nil {
		return 0
	}

	return resp.response.StatusCode
}

// mgetAnswerMatches reports whether resp answers the documents at slots, one
// for one and in order. A node answers each mget in the order it was asked, so
// a mismatch means the answer cannot be placed.
func mgetAnswerMatches(resp *MGetResp, ops []MGetOperation, slots []int) bool {
	if resp == nil || len(resp.Docs) != len(slots) {
		return false
	}
	for i, pos := range slots {
		if mgetItemID(&resp.Docs[i]) != ops[pos].ID {
			return false
		}
	}

	return true
}

func mgetItemID(item *MGetRespItem) string {
	if doc, err := item.GetResult(); err == nil {
		return doc.ID
	}
	if failure, err := item.MultiGetError(); err == nil {
		return failure.ID
	}

	return ""
}
